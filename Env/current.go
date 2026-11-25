package Env

import (
	"errors"
	"strings"
	"sync"
)

// current 当前生效的一套配置。启动时 Use 一次，之后只读。
var (
	mu      sync.RWMutex
	current AIEnv
)

// Use 设定当前配置。必需字段缺失时直接拒绝，
// 避免带着空 key / 空 model 一路跑到 401 或 model_not_found。
func Use(cfg AIEnv) error {
	if strings.TrimSpace(cfg.Model) == "" {
		return errors.New("model 为空：配置文件里该条的 model 字段没填")
	}
	if strings.TrimSpace(cfg.API_KEY) == "" {
		return errors.New("API_KEY 为空：配置文件里该条的 API_KEY 字段没填")
	}
	if strings.TrimSpace(cfg.BASE_URL) == "" {
		return errors.New("BASE_URL 为空：配置文件里该条的 BASE_URL 字段没填")
	}

	cfg.Model = strings.TrimSpace(cfg.Model)
	cfg.API_KEY = strings.TrimSpace(cfg.API_KEY)
	cfg.BASE_URL = strings.TrimSpace(cfg.BASE_URL)

	mu.Lock()
	current = cfg
	mu.Unlock()
	return nil
}

// Current 返回当前配置的副本；没 Use 过就是零值。
func Current() AIEnv {
	mu.RLock()
	defer mu.RUnlock()
	return current
}
