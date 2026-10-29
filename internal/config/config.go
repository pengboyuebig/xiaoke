// Package config 负责「小柯」的配置加载：
// 默认值 -> config.json 覆盖 -> 环境变量覆盖（XIAOKE_* 优先级最高）。
package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config 是小柯后端的全部配置。
type Config struct {
	Server    ServerConfig `json:"server"`
	LLM       LLMConfig    `json:"llm"`
	DataDir   string       `json:"dataDir"`
	AssetsDir string       `json:"assetsDir"`
	WebDist   string       `json:"webDist"`
}

type ServerConfig struct {
	Addr string `json:"addr"`
}

type LLMConfig struct {
	// mode: "mock"（免 Key 演示）或 "openai"（任意 OpenAI 兼容上游）
	Mode        string  `json:"mode"`
	BaseURL     string  `json:"baseURL"`
	APIKey      string  `json:"apiKey"`
	Model       string  `json:"model"`
	Temperature float64 `json:"temperature"`
	MaxHistory  int     `json:"maxHistory"`
}

// Defaults 返回开箱即用的默认配置（mock 模式，无需 API Key）。
func Defaults() *Config {
	return &Config{
		Server: ServerConfig{Addr: "127.0.0.1:18790"},
		LLM: LLMConfig{
			Mode:        "mock",
			BaseURL:     "https://api.deepseek.com",
			Model:       "deepseek-chat",
			Temperature: 0.7,
			MaxHistory:  20,
		},
		DataDir:   "data",
		AssetsDir: "assets",
		WebDist:   "web/dist",
	}
}

// Load 读取 path 指向的 config.json（不存在时只用默认值），再应用环境变量覆盖。
func Load(path string) (*Config, error) {
	cfg := Defaults()
	data, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("解析 %s 失败: %w", path, err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// 环境变量覆盖：凭据优先走环境变量，避免明文落盘。
	if v := os.Getenv("XIAOKE_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
	}
	if v := os.Getenv("XIAOKE_BASE_URL"); v != "" {
		cfg.LLM.BaseURL = v
	}
	if v := os.Getenv("XIAOKE_MODEL"); v != "" {
		cfg.LLM.Model = v
	}
	if v := os.Getenv("XIAOKE_MODE"); v != "" {
		cfg.LLM.Mode = v
	}
	if v := os.Getenv("XIAOKE_ADDR"); v != "" {
		cfg.Server.Addr = v
	}
	return cfg, nil
}
