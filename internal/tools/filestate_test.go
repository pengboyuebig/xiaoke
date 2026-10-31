package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 新鲜度护栏核心语义：
// 1. write_file 写过的文件视为「已知内容」，可直接 edit
// 2. 外部创建的文件必须先 read_file 才能 edit
// 3. 读取后文件被外部改动 → edit 拒绝并要求重读
// 4. read 同一未变文件返回存根（省 token）
// 5. partial 视图（offset/limit）禁止编辑
func TestFileFreshnessGuard(t *testing.T) {
	env, ws := testEnv(t)
	ctx := context.Background()

	// 1) write → 直接 edit 应成功（写过的内容模型已知）
	writeTemp(t, env, "known.txt", "written by xiaoke\n")
	editArgs, _ := json.Marshal(map[string]string{
		"path": "known.txt", "old_string": "written", "new_string": "edited",
	})
	if out, err := execEditFile(ctx, env, editArgs); err != nil {
		t.Fatalf("write 后 edit 应放行: %v (%s)", err, out)
	}

	// 2) 外部创建 → 不读就改拒绝
	external := filepath.Join(ws, "by-user.txt")
	if err := os.WriteFile(external, []byte("user content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ng, _ := json.Marshal(map[string]string{
		"path": "by-user.txt", "old_string": "user", "new_string": "x",
	})
	if _, err := execEditFile(ctx, env, ng); err == nil || !strings.Contains(err.Error(), "尚未读取") {
		t.Fatalf("外部文件未读先改应拒绝: %v", err)
	}
	readTemp(t, env, "by-user.txt")

	// 3) 读取后外部改动 → edit 拒绝要求重读
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(external, future, future); err != nil {
		t.Fatal(err)
	}
	stale, _ := json.Marshal(map[string]string{
		"path": "by-user.txt", "old_string": "user content", "new_string": "x",
	})
	if _, err := execEditFile(ctx, env, stale); err == nil || !strings.Contains(err.Error(), "被外部修改") {
		t.Fatalf("stale 文件应拒绝编辑: %v", err)
	}
	// 重读后放行
	readTemp(t, env, "by-user.txt")
	ok, _ := json.Marshal(map[string]string{
		"path": "by-user.txt", "old_string": "user content", "new_string": "user edited",
	})
	if _, err := execEditFile(ctx, env, ok); err != nil {
		t.Fatalf("重读后 edit 应放行: %v", err)
	}

	// 4) 同视图重复读取 → 存根
	args, _ := json.Marshal(map[string]any{"path": "known.txt"})
	stub, err := execReadFile(ctx, env, args)
	if err != nil || !strings.Contains(stub, "没有变化") {
		t.Fatalf("重复读取应返回存根: %v %q", err, stub)
	}
	// partial 视图不算「已知全文」
	pargs, _ := json.Marshal(map[string]any{"path": "known.txt", "offset": 1, "limit": 1})
	if _, err := execReadFile(ctx, env, pargs); err != nil {
		t.Fatal(err)
	}
	pedit, _ := json.Marshal(map[string]string{
		"path": "known.txt", "old_string": "edited", "new_string": "x",
	})
	if _, err := execEditFile(ctx, env, pedit); err == nil || !strings.Contains(err.Error(), "一部分") {
		t.Fatalf("partial 视图后编辑应拒绝: %v", err)
	}

	// 5) 不存在的文件：read 给相近名建议
	miss, _ := json.Marshal(map[string]any{"path": "knwon.txt"}) // 拼错
	hint, err := execReadFile(ctx, env, miss)
	if err == nil || !strings.Contains(err.Error(), "你是想找") || !strings.Contains(err.Error(), "known.txt") {
		t.Fatalf("相近文件名建议缺失: %v %q", err, hint)
	}

	// 6) write_file 覆盖外部文件同样要读过
	if err := os.WriteFile(filepath.Join(ws, "cover.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wargs, _ := json.Marshal(map[string]string{"path": "cover.txt", "content": "new\n"})
	if _, err := execWriteFile(ctx, env, wargs); err == nil || !strings.Contains(err.Error(), "尚未读取") {
		t.Fatalf("覆盖未读文件应拒绝: %v", err)
	}
}
