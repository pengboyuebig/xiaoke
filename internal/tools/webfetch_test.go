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
