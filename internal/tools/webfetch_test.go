package tools

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// allowLoopback 临时放行回环地址（httptest 监听 127.0.0.1），返回恢复函数。
func allowLoopback(t *testing.T) func() {
	t.Helper()
	old := webFetchCheckPrivate
	webFetchCheckPrivate = func(net.IP) bool { return false }
	return func() { webFetchCheckPrivate = old }
}

func mustEnv(t *testing.T) *Env {
	t.Helper()
	env, _ := testEnv(t)
	return env
}

func webFetchArgs(t *testing.T, url string) json.RawMessage {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"url": url})
	return args
}

func TestWebFetchBasic(t *testing.T) {
	defer allowLoopback(t)()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<html><head><style>x{}</style></head><body><h1>标题</h1><p>正文  内容</p><script>evil()</script></body></html>"))
	}))
	defer srv.Close()
	out, err := execWebFetch(context.Background(), mustEnv(t), webFetchArgs(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "标题") || !strings.Contains(out, "正文  内容") {
		t.Fatalf("正文应保留: %q", out)
	}
	if strings.Contains(out, "evil()") || strings.Contains(out, "<h1>") {
		t.Fatalf("script 应剥除、标签应去掉: %q", out)
	}
	if !strings.Contains(out, srv.URL) {
		t.Fatalf("应带 URL 头: %q", out)
	}
}

func TestWebFetchCache(t *testing.T) {
	defer allowLoopback(t)()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte("hello"))
	}))
	defer srv.Close()
	env := mustEnv(t)
	url := srv.URL + "/cached"
	if _, err := execWebFetch(context.Background(), env, webFetchArgs(t, url)); err != nil {
		t.Fatal(err)
	}
	if _, err := execWebFetch(context.Background(), env, webFetchArgs(t, url)); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("同 URL 15 分钟内应走缓存，实际请求 %d 次", calls)
	}
}

func TestWebFetchRedirectNotFollowed(t *testing.T) {
	defer allowLoopback(t)()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/target", http.StatusFound)
	}))
	defer srv.Close()
	out, err := execWebFetch(context.Background(), mustEnv(t), webFetchArgs(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "REDIRECT") || !strings.Contains(out, "/target") {
		t.Fatalf("重定向应显式返回目标: %q", out)
	}
}

func TestWebFetchPrivateRejected(t *testing.T) {
	// 无点主机名
	if _, err := execWebFetch(context.Background(), mustEnv(t), webFetchArgs(t, "http://localhost/x")); err == nil {
		t.Fatal("localhost 应被拒绝")
	}
	// 私网地址（真实 Dial 到 127.0.0.1 被拒）——不放开钩子，验证生产路径
	if _, err := execWebFetch(context.Background(), mustEnv(t), webFetchArgs(t, "http://127.0.0.1:1/x")); err == nil {
		t.Fatal("回环地址应被拒绝")
	}
}

func TestWebFetchUnsupportedType(t *testing.T) {
	defer allowLoopback(t)()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write([]byte{0, 1, 2})
	}))
	defer srv.Close()
	if _, err := execWebFetch(context.Background(), mustEnv(t), webFetchArgs(t, srv.URL)); err == nil {
		t.Fatal("二进制类型应拒绝")
	}
}

func TestWebFetchInvalidURL(t *testing.T) {
	if _, err := execWebFetch(context.Background(), mustEnv(t), webFetchArgs(t, "")); err == nil {
		t.Fatal("空 url 应拒绝")
	}
}

// 反爬验证页必须被识别出来，不能当成正文喂给模型。
func TestWebFetchBotWallDetected(t *testing.T) {
	defer allowLoopback(t)()
	// 两种 UA 都返回验证页 → 应返回明确的「被反爬拦截」提示
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body><div>当前环境异常，完成验证后即可继续访问。</div><div>去验证</div></body></html>`))
	}))
	defer srv.Close()

	out, err := execWebFetch(context.Background(), mustEnv(t), webFetchArgs(t, srv.URL))
	if err != nil {
		t.Fatalf("反爬页不应返回 error（应返回提示文本）: %v", err)
	}
	if !strings.Contains(out, "反爬拦截") {
		t.Fatalf("应返回反爬拦截提示，实际: %q", out)
	}
	if strings.Contains(out, "当前环境异常") {
		t.Fatalf("验证页文字不应出现在返回正文里: %q", out)
	}
}

// 桌面 UA 被拦、移动端 UA 放行时，应自动重试并拿到正文。
func TestWebFetchUARetry(t *testing.T) {
	defer allowLoopback(t)()
	var desktopUA, mobileUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if strings.Contains(ua, "iPhone") {
			mobileUA = ua
			_, _ = w.Write([]byte(`<html><body><div id="js_content"><p>真实正文内容</p></div></body></html>`))
			return
		}
		desktopUA = ua
		_, _ = w.Write([]byte(`<html><body><div>当前环境异常，完成验证后即可继续访问。</div></body></html>`))
	}))
	defer srv.Close()

	out, err := execWebFetch(context.Background(), mustEnv(t), webFetchArgs(t, srv.URL))
	if err != nil {
		t.Fatal(err)
	}
	if desktopUA == "" || mobileUA == "" {
		t.Fatalf("应先后用两种 UA 请求（desktop=%q mobile=%q）", desktopUA, mobileUA)
	}
	if strings.Contains(desktopUA, "iPhone") {
		t.Fatalf("第一次应使用桌面 UA，实际: %q", desktopUA)
	}
	if !strings.Contains(out, "真实正文内容") {
		t.Fatalf("移动端 UA 重试后应拿到正文，实际: %q", out)
	}
	if strings.Contains(out, "当前环境异常") {
		t.Fatalf("返回不应含验证页文字: %q", out)
	}
}

// 微信正文容器 js_content 应被优先提取，导航/页脚噪声被丢弃。
func TestExtractWeixinMainContent(t *testing.T) {
	src := `<html><body>
<div class="header">导航 首页 关于</div>
<div id="js_content"><p>这是文章正文第一段。</p><p>这是第二段。</p></div>
<div class="footer">版权声明 广告</div>
</body></html>`
	got := extractMainContent(src, "https://mp.weixin.qq.com/s/abc")
	if !strings.Contains(got, "这是文章正文第一段") {
		t.Fatalf("应提取到正文: %q", got)
	}
	if strings.Contains(got, "导航 首页") || strings.Contains(got, "版权声明") {
		t.Fatalf("导航/页脚不应进入正文容器: %q", got)
	}
}

// 通用站点 <article> 容器应被提取。
func TestExtractGenericArticle(t *testing.T) {
	body := strings.Repeat("正文内容填充。", 60) // 超过 200 字节阈值
	src := `<html><body><nav>菜单</nav><article><p>` + body + `</p></article><footer>页脚</footer></body></html>`
	got := extractMainContent(src, "https://example.com/post")
	if !strings.Contains(got, "正文内容填充") {
		t.Fatalf("应提取到 article 正文: %q", got)
	}
	if strings.Contains(got, "菜单") || strings.Contains(got, "页脚") {
		t.Fatalf("article 外的导航/页脚不应包含: %q", got)
	}
}

// HTML 实体必须被解码，不能原样混进正文。
func TestHtmlToTextUnescapeEntities(t *testing.T) {
	out := htmlToText(`<p>A&nbsp;B</p><p>x&amp;y</p><p>&lt;tag&gt;</p>`)
	if strings.Contains(out, "&nbsp;") || strings.Contains(out, "&amp;") || strings.Contains(out, "&lt;") {
		t.Fatalf("HTML 实体未解码: %q", out)
	}
	if !strings.Contains(out, "A") || !strings.Contains(out, "x&y") {
		t.Fatalf("实体解码结果异常: %q", out)
	}
	if !strings.Contains(out, "<tag>") {
		t.Fatalf("&lt;tag&gt; 应解码为 <tag>: %q", out)
	}
}

// <title> 与正文之间必须断开，不能粘成「标题正文」（example.com 这类
// 标题只在 title 里的页面会踩到）。
func TestHtmlToTextTitleSeparated(t *testing.T) {
	out := htmlToText(`<html><head><title>文章标题</title></head><body><p>正文内容</p></body></html>`)
	if strings.Contains(out, "文章标题正文内容") {
		t.Fatalf("title 与正文粘连了: %q", out)
	}
	if !strings.Contains(out, "文章标题") || !strings.Contains(out, "正文内容") {
		t.Fatalf("title 与正文都应保留: %q", out)
	}
}

// 超过 8MB 的页面应报错（微信文章 3~4MB 则必须能通过）。
func TestWebFetchSizeLimit(t *testing.T) {
	defer allowLoopback(t)()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		// 4MB：微信文章量级，应通过
		chunk := strings.Repeat("a", 1<<20)
		for i := 0; i < 4; i++ {
			_, _ = w.Write([]byte(chunk))
		}
	}))
	defer srv.Close()
	if _, err := execWebFetch(context.Background(), mustEnv(t), webFetchArgs(t, srv.URL)); err != nil {
		t.Fatalf("4MB 页面应通过 8MB 上限: %v", err)
	}
}

// 验证页不能被缓存：第二次抓取应重新请求（否则重试永远拿不到正文）。
func TestWebFetchBotWallNotCached(t *testing.T) {
	defer allowLoopback(t)()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body>当前环境异常，完成验证后即可继续访问。</body></html>`))
	}))
	defer srv.Close()
	env := mustEnv(t)
	url := srv.URL + "/wall"
	for i := 0; i < 2; i++ {
		if _, err := execWebFetch(context.Background(), env, webFetchArgs(t, url)); err != nil {
			t.Fatal(err)
		}
	}
	// 每次执行会尝试桌面+移动端两次 UA，共 4 次请求；若被缓存则只有 2 次
	if calls != 4 {
		t.Fatalf("验证页不应缓存，期望 4 次请求（2 轮 × 2 UA），实际 %d", calls)
	}
}

// 同主机重定向到验证页时，blocked 必须正确传播（否则会把验证页当正文）。
func TestWebFetchRedirectToBotWall(t *testing.T) {
	defer allowLoopback(t)()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/wall", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><body>请完成验证</body></html>`))
	}))
	defer srv.Close()
	out, err := execWebFetch(context.Background(), mustEnv(t), webFetchArgs(t, srv.URL+"/start"))
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if !strings.Contains(out, "反爬拦截") {
		t.Fatalf("重定向到验证页应识别为反爬，实际: %q", out)
	}
}
