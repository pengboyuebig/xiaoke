package tools

import (
	"net"
	"testing"
)

func TestParseRemoteIP(t *testing.T) {
	cases := []struct {
		addr string
		want string
	}{
		{"127.0.0.1:80", "127.0.0.1"},
		{"192.168.1.1:443", "192.168.1.1"},
		{"[::1]:443", "::1"},
		{"[fe80::1]:8080", "fe80::1"},
		{"[2001:db8::1]:80", "2001:db8::1"},
	}
	for _, c := range cases {
		a := fakeAddr{network: "tcp", addr: c.addr}
		ip := parseRemoteIP(a)
		if ip == nil {
			t.Errorf("parseRemoteIP(%q) = nil，应为 %s", c.addr, c.want)
			continue
		}
		if ip.String() != c.want {
			t.Errorf("parseRemoteIP(%q) = %s，应为 %s", c.addr, ip.String(), c.want)
		}
	}
}

// IPv6 回环地址必须被识别为私网（否则 SSRF 防护失效）。
func TestIsPrivateIPIPv6(t *testing.T) {
	for _, s := range []string{"::1", "fe80::1", "fc00::1", "0.0.0.0", "127.0.0.1", "10.0.0.1"} {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("无法解析 %s", s)
		}
		if !isPrivateIP(ip) {
			t.Errorf("isPrivateIP(%s) = false，应为 true", s)
		}
	}
	// 公网地址不应被误判
	for _, s := range []string{"8.8.8.8", "1.2.3.4", "2001:db8::1"} {
		ip := net.ParseIP(s)
		if isPrivateIP(ip) {
			t.Errorf("isPrivateIP(%s) = true，应为 false", s)
		}
	}
}

type fakeAddr struct {
	network string
	addr    string
}

func (f fakeAddr) Network() string { return f.network }
func (f fakeAddr) String() string  { return f.addr }
