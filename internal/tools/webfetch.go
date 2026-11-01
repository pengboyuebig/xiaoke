// web_fetch：抓取网页正文给模型。对标主流 agent 的 WebFetch：
//   - 30s 超时、2MB 下载上限、16KB 正文截断
//   - 仅允许 text/html / text/plain / application/json
//   - 跨主机重定向不自动跟随——返回 REDIRECT 信息让模型显式重调（审计友好）
//   - 私网/本机地址拒绝（SSRF 防护：http.Client Dial 层校验）
//   - 15 分钟按原始 URL 缓存（进程内，≤32 条）
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	webFetchTimeout    = 30 * time.Second
	webFetchMaxBytes   = 8 << 20   // 8MB 下载上限（微信文章 HTML 常达 3~4MB）
	webFetchOutputMax  = 16 * 1024 // 返回给模型的正文截断
	webFetchCacheTTL   = 15 * time.Minute
	webFetchCacheLimit = 32
)

// 浏览器 UA：多数站点（尤其微信公众号）按 UA 做风控，非浏览器标识会被
// 返回验证页。桌面 UA 优先，命中反爬页时换移动端 UA 重试一次。
const (
	uaDesktopChrome = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
	uaMobileSafari  = "Mozilla/5.0 (iPhone; CPU iPhone OS 16_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/16.6 Mobile/15E148 Safari/604.1"
)

// webFetchClient 的 Dial 层拒绝私网/回环地址（SSRF 防护），
// CheckRedirect 禁止自动跟随（跨主机重定向交给模型决策）。
// webFetchCheckPrivate 是私网校验钩子（测试替换为恒 false 以便 httptest
// 的回环监听可用；生产代码不得修改）。
var webFetchCheckPrivate = isPrivateIP

var webFetchClient = &http.Client{
	Timeout: webFetchTimeout,
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var d net.Dialer
			conn, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			remote := conn.RemoteAddr()
			if ip := parseRemoteIP(remote); ip != nil && webFetchCheckPrivate(ip) {
				conn.Close()
				return nil, fmt.Errorf("拒绝访问私网/本机地址 %s（web_fetch 只能访问公网）", ip)
			}
			return conn, nil
		},
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse // 不自动跟随
	},
}

func parseRemoteIP(addr net.Addr) net.IP {
	s := addr.String()
	if i := strings.LastIndex(s, ":"); i > 0 && !strings.Contains(s, "]") {
		s = s[:i]
	} else if strings.HasSuffix(s, "]:0") {
		s = strings.TrimSuffix(s, "]:0")
	}
	// host:port 形态（IPv6 带方括号）
	if i := strings.LastIndex(s, "]"); i > 0 {
		s = strings.Trim(s[:i], "[")
	}
	return net.ParseIP(strings.Trim(s, "[]"))
}

// isPrivateIP 判定回环/私网/链路本地地址。
func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// webFetchCache 是进程内 15 分钟缓存（key = 原始 URL）。
type webFetchCache struct {
	mu      sync.Mutex
	entries map[string]webFetchEntry
}

type webFetchEntry struct {
	content string
	at      time.Time
}

var urlFetchCache = &webFetchCache{entries: map[string]webFetchEntry{}}

func (c *webFetchCache) get(url string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[url]
	if !ok || time.Since(e.at) > webFetchCacheTTL {
		return "", false
	}
	return e.content, true
}

func (c *webFetchCache) put(url, content string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// 超限清最旧
	if len(c.entries) >= webFetchCacheLimit {
		urls := make([]string, 0, len(c.entries))
		for u := range c.entries {
			urls = append(urls, u)
		}
		sort.Slice(urls, func(i, j int) bool {
			return c.entries[urls[i]].at.Before(c.entries[urls[j]].at)
		})
		for _, u := range urls[:len(urls)-webFetchCacheLimit+1] {
			delete(c.entries, u)
		}
	}
	c.entries[url] = webFetchEntry{content: content, at: time.Now()}
}

// del 删除指定 URL 的缓存项（反爬重试前清掉拦截结果用）。
func (c *webFetchCache) del(url string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, url)
}

// normalizeWebURL 基本校验：仅 http/https，拒绝无点主机名（本地名用 run_command）。
func normalizeWebURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("需要 url 参数")
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "https://" + raw
	}
	host := raw
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	if host == "" || !strings.Contains(host, ".") {
		return "", fmt.Errorf("url 主机名 %q 不合法（web_fetch 只访问公网 http/https 地址）", host)
	}
	return raw, nil
}

// execWebFetch 是 web_fetch 工具的执行入口。
// 先用桌面 Chrome UA 抓取；若命中反爬验证页，换移动端 UA 重试一次
// （微信等站点对两类 UA 的风控判定不同，移动端常能拿到正文）。
func execWebFetch(ctx context.Context, env *Env, args json.RawMessage) (string, error) {
	var in struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return "", err
	}
	u, err := normalizeWebURL(in.URL)
	if err != nil {
		return "", err
	}
	if hit, ok := urlFetchCache.get(u); ok {
		return hit, nil
	}

	out, blocked, err := fetchWithRedirects(ctx, u, 0, uaDesktopChrome)
	if err != nil || !blocked {
		return out, err
	}
	// 命中反爬页：换移动端 UA 再试一次（清缓存，避免拦截结果残留）
	urlFetchCache.del(u)
	out, blocked, err = fetchWithRedirects(ctx, u, 0, uaMobileSafari)
	if err != nil || !blocked {
		return out, err
	}
	return botWallMessage(u), nil
}

// sameHost 判断两个 URL 的主机（含端口）是否一致。
func sameHost(a, b string) bool {
	hostOf := func(raw string) string {
		h := raw
		if i := strings.Index(h, "://"); i >= 0 {
			h = h[i+3:]
		}
		if i := strings.IndexAny(h, "/?#"); i >= 0 {
			h = h[:i]
		}
		return strings.ToLower(h)
	}
	return hostOf(a) == hostOf(b)
}

// resolveRedirect 把 Location 解析为绝对 URL（相对路径按当前 URL 补全）。
func resolveRedirect(base, loc string) string {
	if strings.HasPrefix(loc, "http://") || strings.HasPrefix(loc, "https://") {
		return loc
	}
	i := strings.Index(base, "://")
	if i < 0 {
		return loc
	}
	schemeEnd := i + 3
	root := base
	if j := strings.Index(base[schemeEnd:], "/"); j >= 0 {
		root = base[:schemeEnd+j]
	}
	if strings.HasPrefix(loc, "/") {
		return root + loc
	}
	dir := base
	if k := strings.LastIndex(base, "/"); k > schemeEnd {
		dir = base[:k+1]
	}
	return dir + loc
}

// fetchWithRedirects 带深度的抓取主体：同主机重定向自动跟随（≤3 跳），
// 跨主机返回 REDIRECT 让模型显式确认。
//
// 返回 blocked=true 表示响应是反爬验证页（调用方据此换 UA 重试），
// 此时 out 为空、不写缓存。
func fetchWithRedirects(ctx context.Context, u string, depth int, ua string) (out string, blocked bool, err error) {
	cctx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
	if err != nil {
		return "", false, fmt.Errorf("url 不合法: %w", err)
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8,application/json;q=0.7,*/*;q=0.5")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	resp, err := webFetchClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("抓取失败: %w", err)
	}
	defer resp.Body.Close()

	// 重定向：同主机安全跳转自动跟随（≤3 跳）；跨主机不自动跟随——
	// 目标交给模型显式确认（审计友好，也防被 302 引去私网）。
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		if loc == "" {
			return "", false, fmt.Errorf("%s 返回重定向但没有 Location 头", u)
		}
		next := resolveRedirect(u, loc)
		if sameHost(u, next) && depth < 3 {
			return fetchWithRedirects(ctx, next, depth+1, ua)
		}
		return fmt.Sprintf("REDIRECT: %s 返回 %s，重定向到 %s。需要继续时用 web_fetch 抓取该地址（跨主机跳转必须显式确认）。",
			u, resp.Status, next), false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("上游返回 %s", resp.Status)
	}
	ct := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	switch ct {
	case "text/html", "text/plain", "application/json", "":
		// 允许的类型；无 Content-Type 时按文本处理
	default:
		return "", false, fmt.Errorf("不支持的内容类型 %s（web_fetch 只处理 text/html、text/plain、application/json）", ct)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBytes+1))
	if err != nil {
		return "", false, fmt.Errorf("读取响应失败: %w", err)
	}
	if len(body) > webFetchMaxBytes {
		return "", false, fmt.Errorf("页面超过 %dMB 上限", webFetchMaxBytes>>20)
	}
	text := string(body)
	if ct == "text/html" {
		text = htmlToText(extractMainContent(text, u))
	}
	text = strings.TrimSpace(text)

	// 反爬验证页：站点用 200 + 验证页替代正文，必须识别出来，
	// 否则会把「环境异常/去验证」当成文章内容喂给模型。
	if looksLikeBotWall(u, text) {
		return "", true, nil
	}

	if text == "" {
		return fmt.Sprintf("%s 返回 200 但没有文本内容（%s）", u, ct), false, nil
	}
	if len(text) > webFetchOutputMax {
		text = truncateBytes(text, webFetchOutputMax) + "\n…(正文已按 16KB 截断)"
	}
	out = fmt.Sprintf("URL: %s\n\n%s", u, text)
	urlFetchCache.put(u, out)
	return out, false, nil
}

// botWallMarkers 是反爬验证页的正文特征。命中即判定站点未返回真实内容，
// 而是把用户引向验证/登录流程。
var botWallMarkers = []string{
	"当前环境异常",
	"完成验证后即可继续访问",
	"访问过于频繁",
	"请完成验证",
	"安全验证",
	"环境异常",
	"请在微信客户端打开",
}

// looksLikeBotWall 判断抓取结果是反爬验证页而非正文。
// URL 里带验证参数（如微信 wappoc_appmsgcaptcha）也算强证据——
// 这种情况下页面正文往往已被替换成验证提示。
func looksLikeBotWall(url, text string) bool {
	lowerURL := strings.ToLower(url)
	for _, marker := range []string{"wappoc_appmsgcaptcha", "captcha", "verify", "antispider"} {
		if strings.Contains(lowerURL, marker) {
			return true
		}
	}
	for _, m := range botWallMarkers {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// botWallMessage 是两种 UA 都被反爬拦截时返回给模型的说明。
// 明确告知「不要再重试」，并给出可行的替代路径。
func botWallMessage(url string) string {
	return fmt.Sprintf("反爬拦截：%s 返回的是验证/风控页面，不是文章正文（已尝试桌面与移动端 UA 均被拦）。\n"+
		"继续用 web_fetch 重试无效（每次都会拿到新的验证 token）。请改用以下方式之一：\n"+
		"1. 请用户把正文内容复制粘贴给你；\n"+
		"2. 请用户在浏览器中打开该链接后，把内容复制给你；\n"+
		"3. 找该文章的公开转载版本（其它站点镜像）再抓取。", url)
}

// extractMainContent 从 HTML 中提取正文容器，减少导航/页脚/侧栏噪声。
// 微信公众号优先取 id="js_content"；通用站点取 <article>/<main>；
// 都没有则原样返回（交给 htmlToText 处理全文）。
func extractMainContent(html, url string) string {
	lower := strings.ToLower(html)
	isWeixin := strings.Contains(strings.ToLower(url), "mp.weixin.qq.com")

	// 微信正文容器
	if isWeixin {
		if s, e, ok := findTagBlock(lower, `id="js_content"`); ok {
			return html[s:e]
		}
		if s, e, ok := findTagBlock(lower, `id='js_content'`); ok {
			return html[s:e]
		}
	}
	// 通用正文容器
	for _, marker := range []string{"<article", "<main"} {
		if s, e, ok := findTagBlock(lower, marker); ok {
			if e-s > 200 { // 太短的多半是空壳，退回全文
				return html[s:e]
			}
		}
	}
	return html
}

// findTagBlock 在 lower（已转小写的 HTML）中定位 marker 所在的标签块，
// 返回原串中该标签「开始」到「对应闭合标签结束」的区间。
// 找不到闭合标签时返回从标签开始到文末的区间。
func findTagBlock(lower, marker string) (start, end int, ok bool) {
	idx := strings.Index(lower, marker)
	if idx < 0 {
		return 0, 0, false
	}
	// marker 形如 "<article" 时起点就是 idx；形如 "id=\"js_content\"" 时
	// 需要回退到所在标签的 '<'。
	start = idx
	if marker[0] != '<' {
		if s := strings.LastIndex(lower[:idx], "<"); s >= 0 {
			start = s
		}
	}
	// 取出标签名（<article ...> → article）
	nameEnd := start + 1
	for nameEnd < len(lower) && (isASCIIAlnum(lower[nameEnd]) || lower[nameEnd] == '-') {
		nameEnd++
	}
	name := lower[start+1 : nameEnd]
	if name == "" {
		return 0, 0, false
	}
	openEnd := strings.Index(lower[start:], ">")
	if openEnd < 0 {
		return 0, 0, false
	}
	// 从开标签之后找同名闭合标签（不处理嵌套，正文容器极少嵌套自身）
	closeTag := "</" + name + ">"
	if c := strings.Index(lower[start+openEnd:], closeTag); c >= 0 {
		return start, start + openEnd + c + len(closeTag), true
	}
	return start, len(lower), true
}

func isASCIIAlnum(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

// htmlToText 极简 HTML 转文本：剥 script/style、常见块级标签换行、去标签、
// 解 HTML 实体、折叠空白。不追求排版保真——目标是给模型干净可读的正文。
func htmlToText(src string) string {
	h := src
	lower := strings.ToLower(h)
	// 剥 script/style/noscript 整段
	for _, tag := range []string{"script", "style", "noscript"} {
		for {
			open := strings.Index(lower, "<"+tag)
			if open < 0 {
				break
			}
			close := strings.Index(lower[open:], "</"+tag+">")
			if close < 0 {
				h = h[:open]
				lower = lower[:open]
				break
			}
			end := open + close + len(tag) + 3
			h = h[:open] + h[end:]
			lower = strings.ToLower(h)
		}
	}
	// 块级标签转换行
	replacer := strings.NewReplacer(
		"<br", "\n<br", "</p>", "\n", "</div>", "\n", "</li>", "\n",
		"</h1>", "\n", "</h2>", "\n", "</h3>", "\n", "</h4>", "\n",
		"</tr>", "\n", "</pre>", "\n", "</section>", "\n", "</article>", "\n",
		// head 区的 title/meta 也要断开：否则 <title>标题</title><p>正文</p>
		// 会被粘成「标题正文」，标题混进正文首句。
		"</title>", "\n", "</head>", "\n", "</body>", "\n", "</nav>", "\n",
	)
	h = replacer.Replace(h)
	// 剥所有标签
	var b strings.Builder
	inTag := false
	for _, r := range h {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
		case !inTag:
			b.WriteRune(r)
		}
	}
	text := b.String()
	// 解 HTML 实体（&nbsp; &amp; &#39; 等）——不去解码会原样混进正文
	text = html.UnescapeString(text)
	// 折叠空白行
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	text = strings.Join(lines, "\n")
	for strings.Contains(text, "\n\n\n") {
		text = strings.ReplaceAll(text, "\n\n\n", "\n\n")
	}
	return text
}
