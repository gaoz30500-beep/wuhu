package llm

import (
	"os"
	"path/filepath"
	"testing"

	"agent/memory"

	"github.com/sashabaranov/go-openai"
)

// 本轮切分：只取最后一条 user 消息到结尾
func TestLastUserTurn(t *testing.T) {
	hist := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "旧摘要"},
		{Role: openai.ChatMessageRoleUser, Content: "第一轮问题"},
		{Role: openai.ChatMessageRoleAssistant, Content: "第一轮回答"},
		{Role: openai.ChatMessageRoleUser, Content: "我叫高子豪，记住"},
		{Role: openai.ChatMessageRoleAssistant, Content: "好的，记住了"},
	}
	part := lastUserTurn(hist)
	if len(part) != 2 {
		t.Fatalf("应切出 2 条（最后一条 user + 其后的回答），实际 %d 条", len(part))
	}
	if part[0].Content != "我叫高子豪，记住" {
		t.Fatalf("切点不对，首条是 %q", part[0].Content)
	}

	if got := lastUserTurn([]openai.ChatCompletionMessage{{Role: openai.ChatMessageRoleAssistant, Content: "只有回答"}}); got != nil {
		t.Fatalf("没有 user 消息时应返回 nil，实际 %v", got)
	}
}

// 提炼器输出的解析：去前缀、跳过“无”、最多 3 条
func TestParseDistill(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want []string
	}{
		{"没有可存的", "无", nil},
		{"带句号的没有", "无。\n", nil},
		{"标准三条", "- 用户是河南科技学院网络242班学生\n- 用户称呼偏好为 wuhu\n- 用户主力机是机械革命蛟龙16", []string{
			"用户是河南科技学院网络242班学生",
			"用户称呼偏好为 wuhu",
			"用户主力机是机械革命蛟龙16",
		}},
		{"超过三条要截断", "- 一\n- 二\n- 三\n- 四", []string{"一", "二", "三"}},
		{"星号列表", "* 用户偏好用中文\n• 用户不想每轮总结", []string{"用户偏好用中文", "用户不想每轮总结"}},
		{"空输出", "\n  \n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := parseDistill(c.out)
			if len(got) != len(c.want) {
				t.Fatalf("条数不对：want %d got %d (%v)", len(c.want), len(got), got)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Fatalf("第 %d 条不对：want %q got %q", i, c.want[i], got[i])
				}
			}
		})
	}
}

// 查重：前 10 个字撞上已有记忆就算重复
func TestIsDupMemory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "memory.jsonl")

	if _, err := memory.Append(path, "用户希望被称呼为 wuhu", "偏好,称呼"); err != nil {
		t.Fatalf("准备数据失败: %v", err)
	}

	if !isDupMemory(path, "用户希望被称呼为 wuhu！") {
		t.Fatal("同一句话（只差标点）应判为重复")
	}
	if isDupMemory(path, "用户主力机是机械革命蛟龙16") {
		t.Fatal("不同事实被误判成重复")
	}

	// 文件不存在时不该报错、也不该判重复
	if isDupMemory(filepath.Join(dir, "none.jsonl"), "随便一句") {
		t.Fatal("空库不应判重复")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("记忆文件应仍存在: %v", err)
	}
}
