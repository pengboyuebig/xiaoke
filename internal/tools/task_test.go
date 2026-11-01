package tools

import (
	"os/exec"
	"testing"
	"time"
)

// 后台任务应能列出、查到，并能被终止（真实进程）。
func TestBackgroundTaskListAndKill(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("需要 node 才能跑这个测试")
	}

	id, err := startBackgroundCommand(node,
		[]string{"-e", "setTimeout(()=>{console.log('still-alive')},60000)"},
		t.TempDir(), "sess-test", nil)
	if err != nil {
		t.Fatalf("启动后台任务失败: %v", err)
	}
	if id == "" {
		t.Fatal("task id 为空")
	}

	// 列表里应能看到它，且状态为 running
	found := false
	for _, t2 := range ListBackgroundTasks() {
		if t2.ID == id {
			found = true
			if t2.State != "running" {
				t.Errorf("刚启动的任务状态应为 running，实际 %s", t2.State)
			}
			if t2.SessionID != "sess-test" {
				t.Errorf("sessionId 未记录，实际 %q", t2.SessionID)
			}
		}
	}
	if !found {
		t.Fatal("列表里找不到刚启动的任务")
	}

	// 详情可用
	info, _, ok := TaskDetail(id)
	if !ok || info.ID != id {
		t.Fatalf("TaskDetail 查不到任务: ok=%v", ok)
	}

	// 终止后状态应变为非 running
	if err := KillBackgroundTask(id); err != nil {
		t.Fatalf("终止失败: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, _, ok := TaskDetail(id)
		if !ok || info.State != "running" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("终止后任务仍显示运行中")
		}
		time.Sleep(100 * time.Millisecond)
	}

	// 重复终止应给出明确错误而不是崩
	if err := KillBackgroundTask(id); err == nil {
		t.Error("重复终止应返回错误")
	}
}

// 不存在的任务：查询与终止都要给出明确错误。
func TestBackgroundTaskMissing(t *testing.T) {
	if _, _, ok := TaskDetail("bg-not-exist"); ok {
		t.Error("不存在的任务不应查到")
	}
	if err := KillBackgroundTask("bg-not-exist"); err == nil {
		t.Error("终止不存在的任务应报错")
	}
}

// 已完成任务的「运行时长」必须固定——用 time.Since(started) 会一直增长，
// 界面上看着像任务还在跑。
func TestTaskDurationFrozenAfterEnd(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("需要 node 才能跑这个测试")
	}
	id, err := startBackgroundCommand(node, []string{"-e", "setTimeout(()=>{},300)"}, t.TempDir(), "s", nil)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(6 * time.Second)
	for {
		info, _, ok := TaskDetail(id)
		if ok && info.State != "running" {
			first := info.DurationMs
			time.Sleep(700 * time.Millisecond)
			again, _, _ := TaskDetail(id)
			if again.DurationMs != first {
				t.Fatalf("完成后的时长仍在增长：%d → %d", first, again.DurationMs)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("任务未按预期结束")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
