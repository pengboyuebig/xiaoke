package tools

import (
	"strings"
	"testing"
	"time"
)

// 单次调用的 timeout_sec 覆盖：0 用默认、正数生效、超大值按上限钳制。
func TestCommandTimeoutFor(t *testing.T) {
	env := &Env{}
	if got := commandTimeoutFor(env, 0); got != cmdTimeout {
		t.Errorf("override=0 应回默认 %v，实际 %v", cmdTimeout, got)
	}
	if got := commandTimeoutFor(env, 180); got != 180*time.Second {
		t.Errorf("override=180 应为 180s，实际 %v", got)
	}
	// 超过上限必须钳制，否则一次误传就能把回合挂死
	if got := commandTimeoutFor(env, 99999); got != maxCommandTimeoutSec*time.Second {
		t.Errorf("override=99999 应钳到 %ds，实际 %v", maxCommandTimeoutSec, got)
	}
	if got := commandTimeoutFor(env, -5); got != cmdTimeout {
		t.Errorf("负数应回默认，实际 %v", got)
	}
	// settings 里的自定义默认值仍要生效
	custom := &Env{CommandTimeoutSec: 60}
	if got := commandTimeoutFor(custom, 0); got != 60*time.Second {
		t.Errorf("settings=60 override=0 应为 60s，实际 %v", got)
	}
	// 覆盖优先于 settings
	if got := commandTimeoutFor(custom, 120); got != 120*time.Second {
		t.Errorf("settings=60 override=120 应为 120s，实际 %v", got)
	}
}

// task_output 的等待上限必须足够覆盖构建/安装，否则会退化成多次轮询。
func TestTaskOutputMaxWait(t *testing.T) {
	if taskOutputMaxWait < 120*time.Second {
		t.Errorf("等待上限 %v 太小，长构建仍会被拆成多次轮询", taskOutputMaxWait)
	}
	if taskOutputMaxWait > 10*time.Minute {
		t.Errorf("等待上限 %v 过大，任务卡死时会让回合长时间无响应", taskOutputMaxWait)
	}
}

// run_command 的 schema 必须暴露 timeout_sec，否则模型不知道可以显式加时。
func TestRunCommandSchemaExposesTimeout(t *testing.T) {
	var found bool
	for _, tl := range Builtin() {
		if tl.Name != "run_command" {
			continue
		}
		found = true
		schema := string(tl.Parameters)
		if !strings.Contains(schema, "timeout_sec") {
			t.Errorf("run_command 的 Parameters 缺少 timeout_sec: %s", schema)
		}
		if !strings.Contains(tl.Description, "timeout_sec") {
			t.Error("run_command 的描述没提到 timeout_sec")
		}
	}
	if !found {
		t.Fatal("未注册 run_command")
	}
}

// task_output 的描述必须写清楚等待上限，引导模型一次等够。
func TestTaskOutputDescriptionMentionsWait(t *testing.T) {
	for _, tl := range Builtin() {
		if tl.Name != "task_output" {
			continue
		}
		if !strings.Contains(tl.Description, "wait_ms") {
			t.Error("task_output 描述未提到 wait_ms")
		}
		if !strings.Contains(tl.Description, "180000") {
			t.Error("task_output 描述应给出建议的等待量级")
		}
		return
	}
	t.Fatal("未注册 task_output")
}
