package tools

import (
	"net"
	"runtime"
	"testing"
	"time"
)

// 进程树终止必须释放端口：模拟 npm 拉子进程的场景（cmd /c node ...）。
func TestKillTaskFreesPort(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("仅 Windows 需要验证进程树")
	}
	const port = "39123"
	// cmd /c 会再拉起 node：只杀 cmd 时 node 仍占着端口（这正是 npm 的问题）
	id, err := startBackgroundCommand("cmd.exe",
		[]string{"/c", "node", "-e", `require('http').createServer(function(q,s){s.end('ok')}).listen(` + port + `)`},
		t.TempDir(), "s", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = KillBackgroundTask(id) })

	// 等端口起来
	ok := false
	for i := 0; i < 40; i++ {
		if c, e := net.DialTimeout("tcp", "127.0.0.1:"+port, 200*time.Millisecond); e == nil {
			c.Close()
			ok = true
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	if !ok {
		t.Fatal("服务没起来，测试前提不成立")
	}

	if err := KillBackgroundTask(id); err != nil {
		t.Fatalf("终止失败: %v", err)
	}

	// 端口应可重新监听（说明子进程也死了）
	deadline := time.Now().Add(5 * time.Second)
	for {
		ln, e := net.Listen("tcp", "127.0.0.1:"+port)
		if e == nil {
			ln.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("终止后端口仍被占用: %v", e)
		}
		time.Sleep(150 * time.Millisecond)
	}
}
