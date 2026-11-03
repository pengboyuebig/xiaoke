package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNoDirList(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.FileServer(noDirList{http.Dir(dir)}))
	defer srv.Close()

	get := func(p string) int {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	if got := get("/a.txt"); got != http.StatusOK {
		t.Fatalf("普通文件应可访问: %d", got)
	}
	if got := get("/"); got != http.StatusNotFound {
		t.Fatalf("根目录不应列出: %d", got)
	}
	if got := get("/sub/"); got != http.StatusNotFound {
		t.Fatalf("子目录不应列出: %d", got)
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, k := range []string{
		"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Content-Security-Policy",
	} {
		if rec.Header().Get(k) == "" {
			t.Errorf("缺少安全头 %s", k)
		}
	}
}

func TestLocalGuard(t *testing.T) {
	h := localGuard("127.0.0.1:18790")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	cases := []struct {
		name   string
		host   string
		origin string
		want   int
	}{
		{"回环 Host", "127.0.0.1:18790", "", http.StatusNoContent},
		{"localhost Host", "localhost:18790", "", http.StatusNoContent},
		{"DNS rebinding", "evil.example:18790", "", http.StatusForbidden},
		{"跨站 Origin", "127.0.0.1:18790", "http://evil.example", http.StatusForbidden},
		{"同源 Origin", "127.0.0.1:18790", "http://127.0.0.1:18790", http.StatusNoContent},
		{"回环 localhost Origin", "127.0.0.1:18790", "http://localhost:18790", http.StatusNoContent},
		{"null Origin", "127.0.0.1:18790", "null", http.StatusForbidden},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Host = c.host
			if c.origin != "" {
				req.Header.Set("Origin", c.origin)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != c.want {
				t.Fatalf("host=%q origin=%q: got %d, want %d", c.host, c.origin, rec.Code, c.want)
			}
		})
	}
}
