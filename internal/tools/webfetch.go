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
	webFetchMaxBytes   = 2 << 20   // 2MB 下载上限
	webFetchOutputMax  = 16 * 1024 // 返回给模型的正文截断
	webFetchCacheTTL   = 15 * time.Minute
	webFetchCacheLimit = 32
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

// execWebFetch 是 web_fetch 工具的执行入口（ctx 取消优先于缓存命中判断之外的一切）。
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
	return fetchWithRedirects(ctx, u, 0)
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
func fetchWithRedirects(ctx context.Context, u string, depth int) (string, error) {
	if hit, ok := urlFetchCache.get(u); ok {
		return hit, nil
	}

	cctx, cancel := context.WithTimeout(ctx, webFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
	if err != nil {
		return "", fmt.Errorf("url 不合法: %w", err)
	}
	req.Header.Set("User-Agent", "xiaoke-webfetch")
	req.Header.Set("Accept", "text/html, text/plain, application/json;q=0.9, */*;q=0.1")
	resp, err := webFetchClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("抓取失败: %w", err)
	}
	defer resp.Body.Close()

	// 重定向：同主机安全跳转自动跟随（≤3 跳）；跨主机不自动跟随——
	// 目标交给模型显式确认（审计友好，也防被 302 引去私网）。
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		loc := resp.Header.Get("Location")
		if loc == "" {
			return "", fmt.Errorf("%s 返回重定向但没有 Location 头", u)
		}
		next := resolveRedirect(u, loc)
		if sameHost(u, next) && depth < 3 {
			return fetchWithRedirects(ctx, next, depth+1)
		}
		return fmt.Sprintf("REDIRECT: %s 返回 %s，重定向到 %s。需要继续时用 web_fetch 抓取该地址（跨主机跳转必须显式确认）。",
			u, resp.Status, next), nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("上游返回 %s", resp.Status)
	}
	ct := strings.ToLower(strings.TrimSpace(strings.Split(resp.Header.Get("Content-Type"), ";")[0]))
	switch ct {
	case "text/html", "text/plain", "application/json", "":
		// 允许的类型；无 Content-Type 时按文本处理
	default:
		return "", fmt.Errorf("不支持的内容类型 %s（web_fetch 只处理 text/html、text/plain、application/json）", ct)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBytes+1))
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}
	if len(body) > webFetchMaxBytes {
		return "", fmt.Errorf("页面超过 %dMB 上限", webFetchMaxBytes>>20)
	}
	text := string(body)
	if ct == "text/html" {
		text = htmlToText(text)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Sprintf("%s 返回 200 但没有文本内容（%s）", u, ct), nil
	}
	if len(text) > webFetchOutputMax {
		text = truncateBytes(text, webFetchOutputMax) + "\n…(正文已按 16KB 截断)"
	}
	out := fmt.Sprintf("URL: %s\n\n%s", u, text)
	urlFetchCache.put(u, out)
	return out, nil
}

// htmlToText 极简 HTML 转文本：剥 script/style、常见块级标签换行、去标签、折叠空白。
// 不追求排版保真——目标是给模型干净可读的正文。
func htmlToText(html string) string {
	lower := strings.ToLower(html)
	// 剥 script/style/noscript 整段
	for _, tag := range []string{"script", "style", "noscript"} {
		for {
			open := strings.Index(lower, "<"+tag)
			if open < 0 {
				break
			}
			close := strings.Index(lower[open:], "</"+tag+">")
			if close < 0 {
				html = html[:open]
				lower = lower[:open]
				break
			}
			end := open + close + len(tag) + 3
			html = html[:open] + html[end:]
			lower = strings.ToLower(html)
		}
	}
	// 块级标签转换行
	replacer := strings.NewReplacer(
		"<br", "\n<br", "</p>", "\n", "</div>", "\n", "</li>", "\n",
		"</h1>", "\n", "</h2>", "\n", "</h3>", "\n", "</h4>", "\n",
		"</tr>", "\n", "</pre>", "\n", "</section>", "\n", "</article>", "\n",
	)
	html = replacer.Replace(html)
	// 剥所有标签
	var b strings.Builder
	inTag := false
	for _, r := range html {
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
