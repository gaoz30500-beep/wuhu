package Env

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type AIEnv struct {
	ID       uint64 `json:"id"`
	Model    string `json:"model"`
	API_KEY  string `json:"API_KEY"`
	BASE_URL string `json:"BASE_URL"`
}

func findConfig(filename string) (string, error) {
	cands := []string{filename}
	if exe, err := os.Executable(); err == nil {
		cands = append(cands, filepath.Join(filepath.Dir(exe), filename))
	}
	for _, p := range cands {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("找不到配置文件 %s，已尝试: %v", filename, cands)
}

func ReloadAISetting() ([]AIEnv, error) {
	var settings []AIEnv
	p, err := findConfig("config/agent.json")
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("读取配置失败: %w", err)
	}
	if err = json.Unmarshal(data, &settings); err != nil {
		return nil, fmt.Errorf("解析配置失败: %w", err)
	}
	for i := range settings {
		s := &settings[i]
		if s.Model == "" {
			return nil, fmt.Errorf("第 %d 条配置缺少 model（例如 deepseek-chat、qwen-plus）", i+1)
		}
		if s.API_KEY == "" {
			return nil, fmt.Errorf("第 %d 条配置缺少 API_KEY", i+1)
		}
		if s.BASE_URL == "" {
			return nil, fmt.Errorf("第 %d 条配置缺少 BASE_URL", i+1)
		}
	}
	return settings, nil
}
