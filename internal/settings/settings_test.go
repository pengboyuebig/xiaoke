package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"xiaoke/internal/config"
)

// 空切片必须序列化为 [] 而不是 null（否则前端 map() 会崩）。
func TestEmptySlicesAreNotNull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := Open(path, config.LLMConfig{Mode: "mock", Model: "deepseek-chat"})
	if err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if got.MCPServers == nil || got.ReadDirs == nil || got.CommandAllowlist == nil {
		t.Fatalf("空切片退化成 nil: %+v", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"mcpServers", "readDirs", "commandAllowlist"} {
		v, ok := raw[key]
		if !ok {
			continue // 未写入也安全（前端按 undefined 处理）
		}
		if v == nil {
			t.Fatalf("%s 序列化成了 null", key)
		}
	}
}

// 更新后 apiKey 的保持/覆盖语义。
func TestUpdateAPIKeySemantics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := Open(path, config.LLMConfig{Mode: "openai", APIKey: "original-key-1234"})
	if err != nil {
		t.Fatal(err)
	}
	// apiKey 为 nil：保持原值
	next := s.Get()
	next.LLM.Model = "new-model"
	if err := s.Update(next, nil); err != nil {
		t.Fatal(err)
	}
	if s.Get().LLM.APIKey != "original-key-1234" {
		t.Fatalf("nil 不应改动 apiKey，got %q", s.Get().LLM.APIKey)
	}
	// 显式新值：覆盖
	k := "changed-key-9999"
	if err := s.Update(s.Get(), &k); err != nil {
		t.Fatal(err)
	}
	if s.Get().LLM.APIKey != "changed-key-9999" {
		t.Fatalf("应覆盖 apiKey，got %q", s.Get().LLM.APIKey)
	}
	// 空串：清除
	empty := ""
	if err := s.Update(s.Get(), &empty); err != nil {
		t.Fatal(err)
	}
	if s.Get().LLM.APIKey != "" {
		t.Fatalf("空串应清除 apiKey，got %q", s.Get().LLM.APIKey)
	}
}

func TestNormalizeBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := Open(path, config.LLMConfig{Mode: "mock"})
	if err != nil {
		t.Fatal(err)
	}
	next := s.Get()
	next.MaxToolSteps = 999
	next.LLM.MaxHistory = 0
	next.LLM.Temperature = -1
	if err := s.Update(next, nil); err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if got.MaxToolSteps != 32 {
		t.Fatalf("maxToolSteps 应钳到 32，got %d", got.MaxToolSteps)
	}
	if got.LLM.MaxHistory != 20 {
		t.Fatalf("maxHistory 应回默认 20，got %d", got.LLM.MaxHistory)
	}
	if got.LLM.Temperature <= 0 {
		t.Fatalf("temperature 应回默认，got %f", got.LLM.Temperature)
	}
}

func TestMaskKey(t *testing.T) {
	has, tail := MaskKey("sk-abcdef1234")
	if !has || tail != "1234" {
		t.Fatalf("MaskKey 异常: %v %q", has, tail)
	}
	if has, _ := MaskKey("  "); has {
		t.Fatal("空白 key 应视为未配置")
	}
}

// 新字段钳制：温度上限、maxHistory 上限、maxTokens 负值、压缩保留区不超过窗口。
func TestNormalizeNewFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	s, err := Open(path, config.LLMConfig{Mode: "mock"})
	if err != nil {
		t.Fatal(err)
	}
	next := s.Get()
	next.LLM.Temperature = 5
	next.LLM.MaxHistory = 9999
	next.LLM.MaxTokens = -7
	next.CompactKeepCount = 500
	if err := s.Update(next, nil); err != nil {
		t.Fatal(err)
	}
	got := s.Get()
	if got.LLM.Temperature != 2 {
		t.Fatalf("温度应钳到 2，got %v", got.LLM.Temperature)
	}
	if got.LLM.MaxHistory != 200 {
		t.Fatalf("maxHistory 应钳到 200，got %d", got.LLM.MaxHistory)
	}
	if got.LLM.MaxTokens != 0 {
		t.Fatalf("负 maxTokens 应归零，got %d", got.LLM.MaxTokens)
	}
	if got.CompactKeepCount != got.LLM.MaxHistory {
		t.Fatalf("保留区应不超过窗口，got %d", got.CompactKeepCount)
	}
	if !got.AutoCompact {
		t.Fatal("默认应开启自动压缩")
	}
}

// ContextWindow 钳制：负值/过小视为未配置（回退字节水位），上限封顶。
func TestNormalizeContextWindow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	s, err := Open(path, config.LLMConfig{Mode: "mock"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		in   int
		want int
	}{
		{-5, 0},      // 负值归零
		{0, 0},       // 0 = 未配置（字节水位）
		{100, 0},     // 有值但比 4096 小 → 视为未配置
		{4096, 4096}, // 合法下界
		{128_000, 128_000},
		{99_999_999, 10_000_000}, // 上限封顶
	}
	for _, c := range cases {
		next := s.Get()
		next.LLM.ContextWindow = c.in
		if err := s.Update(next, nil); err != nil {
			t.Fatal(err)
		}
		if got := s.Get().LLM.ContextWindow; got != c.want {
			t.Fatalf("ContextWindow in=%d 应钳到 %d，got %d", c.in, c.want, got)
		}
	}
}
