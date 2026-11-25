package llm

import (
	"agent/Env"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
)

func explainProbeError(err error) error {
	var apiErr *openai.APIError
	if errors.As(err, &apiErr) {
		switch apiErr.HTTPStatusCode {
		case 401:
			return errors.New("API_KEY 无效或已被吊销")
		case 403:
			return errors.New("API_KEY 没有访问权限")
		case 404:
			return errors.New("BASE_URL 路径不对")
		case 429:
			return errors.New("触发限流或余额不足")
		}
		return fmt.Errorf("服务端返回 %d: %s", apiErr.HTTPStatusCode, apiErr.Message)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("连接超时：BASE_URL 不可达或被墙")
	}
	return fmt.Errorf("网络请求失败: %w", err)
}

func Probe(cfg Env.AIEnv, timeout time.Duration) error {
	oc := openai.DefaultConfig(cfg.API_KEY)
	oc.BaseURL = strings.TrimSuffix(cfg.BASE_URL, "/") // 结尾多个 / 会拼成 //chat/completions
	client := openai.NewClientWithConfig(oc)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	_, err := client.ListModels(ctx)
	if err != nil {
		return explainProbeError(err)
	}
	return nil
}

func CheckConnect(cfg []Env.AIEnv, timeout time.Duration) (Env.AIEnv, error) {

	for pos, env := range cfg {
		err := Probe(env, timeout)
		if err != nil {
			log.Printf("第%d配置有误: %s", pos+1, err)
		} else {
			log.Printf("第%d配置正确,正在启用此配置", pos+1)
			return env, nil
		}
	}
	return Env.AIEnv{}, fmt.Errorf("配置全部有误！")
}
