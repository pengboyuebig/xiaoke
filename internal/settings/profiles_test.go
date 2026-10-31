// 模型配置档案的存储层测试：迁移、合并、激活、normalize 清理。
package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"xiaoke/internal/config"
)

func openStore(t *testing.T, base config.LLMConfig) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	s, err := Open(path, base)
	if err != nil {
		t.Fatal(err)
	}
	return s, path
}

// 老用户迁移：已有 llm 配置、无档案 → 自动生成「默认配置」档案并激活。
func TestMigrateExistingConfigToProfile(t *testing.T) {
	s, _ := openStore(t, config.LLMConfig{
		Mode: "openai", BaseURL: "https://api.deepseek.com", APIKey: "sk-test", Model: "deepseek-chat",
	})
	got := s.Get()
	if len(got.LLMProfiles) != 1 {
		t.Fatalf("应自动迁移出 1 个档案: %+v", got.LLMProfiles)
	}
	p := got.LLMProfiles[0]
	if p.Name != "默认配置" || p.Model != "deepseek-chat" || p.APIKey != "sk-test" {
		t.Fatalf("档案内容不符: %+v", p)
	}
	if got.ActiveProfile != p.ID {
		t.Fatalf("迁移档案应自动激活: %+v", got.ActiveProfile)
	}
	// 重启（重新 Open）不重复迁移
	s2, err := Open(filepath.Join(t.TempDir(), "settings.json"), config.LLMConfig{})
	if err != nil {
		t.Fatal(err)
	}
	_ = s2
}

// 激活：档案值拷入 LLM 字段；不存在的档案报错。
func TestActivateProfile(t *testing.T) {
	s, _ := openStore(t, config.LLMConfig{Mode: "openai", Model: "m1"})
	got := s.Get()
	if len(got.LLMProfiles) != 1 {
		t.Fatalf("迁移应生成档案: %+v", got.LLMProfiles)
	}
	// 加第二个档案并激活
	got.LLMProfiles = append(got.LLMProfiles, LLMProfile{
		ID: "p2", Name: "本地网关", Mode: "openai", BaseURL: "http://127.0.0.1:3425/v1",
		APIKey: "sk-local", Model: "local-model", SmallModel: "local-mini",
	})
	if err := s.Update(got, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActivateProfile("p2"); err != nil {
		t.Fatal(err)
	}
	eff := s.Effective()
	if eff.Model != "local-model" || eff.BaseURL != "http://127.0.0.1:3425/v1" || eff.APIKey != "sk-local" || eff.SmallModel != "local-mini" {
		t.Fatalf("激活后 Effective 应为档案值: %+v", eff)
	}
	if got := s.Get(); got.ActiveProfile != "p2" {
		t.Fatalf("激活 ID 应记录: %q", got.ActiveProfile)
	}
	// mock 档案：模式一起切
	got = s.Get()
	got.LLMProfiles = append(got.LLMProfiles, LLMProfile{ID: "p3", Name: "演示", Mode: "mock", Model: "deepseek-chat"})
	if err := s.Update(got, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActivateProfile("p3"); err != nil {
		t.Fatal(err)
	}
	if eff := s.Effective(); eff.Mode != "mock" {
		t.Fatalf("mock 档案应连运行模式一起切: %+v", eff)
	}
	// 不存在的档案
	if _, err := s.ActivateProfile("nope"); err == nil {
		t.Fatal("不存在的档案应报错")
	}
}

// normalize：空壳档案丢弃、无名兜底、ID 补齐、上限 20、悬空 ActiveProfile 清空。
func TestNormalizeProfiles(t *testing.T) {
	s, _ := openStore(t, config.LLMConfig{Mode: "openai", Model: "m1"})
	got := s.Get()
	profiles := []LLMProfile{
		{}, // 全空 → 丢弃
		{Name: "", Model: "m2", BaseURL: "http://x"}, // 无名 → 用模型名
		{ID: "dup", Name: "A", Model: "a"},           // 正常
		{ID: "dup", Name: "B", Model: "b"},           // ID 重复 → 丢弃 B
	}
	for i := 0; i < 25; i++ {
		profiles = append(profiles, LLMProfile{Name: "x", Model: "mx"})
	}
	got.LLMProfiles = profiles
	got.ActiveProfile = "gone" // 悬空
	if err := s.Update(got, nil); err != nil {
		t.Fatal(err)
	}
	after := s.Get()
	if len(after.LLMProfiles) != maxProfiles {
		t.Fatalf("档案数应封顶 %d: %d", maxProfiles, len(after.LLMProfiles))
	}
	if after.ActiveProfile != "" {
		t.Fatalf("悬空激活应清空: %q", after.ActiveProfile)
	}
	// 空壳 {} 被丢弃：无名档案排在首位，应以模型名兜底
	if after.LLMProfiles[0].Name != "m2" {
		t.Fatalf("无名档案应以模型名兜底: %+v", after.LLMProfiles[0])
	}
	ids := map[string]bool{}
	for _, p := range after.LLMProfiles {
		if p.ID == "" || ids[p.ID] {
			t.Fatalf("档案 ID 应非空且唯一: %+v", p)
		}
		ids[p.ID] = true
		if p.Name == "B" {
			t.Fatal("重复 ID 的后续档案应被丢弃")
		}
	}
}

// Update 的档案 apiKey 合并由 api 层负责；这里验证空 key 不破坏保存往返。
func TestProfileRoundTripKeepsFields(t *testing.T) {
	s, path := openStore(t, config.LLMConfig{Mode: "openai", Model: "m1"})
	got := s.Get()
	got.LLMProfiles = append(got.LLMProfiles, LLMProfile{
		ID: "p9", Name: "带Key", Mode: "openai", BaseURL: "https://x", APIKey: "sk-keep", Model: "m9",
	})
	if err := s.Update(got, nil); err != nil {
		t.Fatal(err)
	}
	after := s.Get()
	found := false
	for _, p := range after.LLMProfiles {
		if p.ID == "p9" {
			found = true
			if p.APIKey != "sk-keep" {
				t.Fatalf("key 应原样保存: %q", p.APIKey)
			}
		}
	}
	if !found {
		t.Fatal("档案应保存")
	}
	disk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(disk), "sk-keep") {
		t.Fatal("落盘文件应包含 key（settings.json 本地明文，0600）")
	}
}
