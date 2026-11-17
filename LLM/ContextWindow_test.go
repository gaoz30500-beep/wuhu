package llm

import (
	"strings"
	"testing"

	"github.com/sashabaranov/go-openai"
)

func TestEstTokens(t *testing.T) {
	// 中文 1 字≈1 token，英文 4 字符≈1 token
	if n := estTokens("你好世界"); n < 4 || n > 6 {
		t.Fatalf("中文 4 字估算应约 4~6，实际 %d", n)
	}
	if n := estTokens("abcdefgh"); n < 1 || n > 4 {
		t.Fatalf("英文 8 字符估算应约 2~4，实际 %d", n)
	}
	if n := estTokens(""); n != 1 {
		t.Fatalf("空串应返回 1（基础开销），实际 %d", n)
	}
}

func TestExtractSummary(t *testing.T) {
	h := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "[前文摘要] 甲"},
		{Role: openai.ChatMessageRoleSystem, Content: "乙"},
		{Role: openai.ChatMessageRoleUser, Content: "你好"},
		{Role: openai.ChatMessageRoleAssistant, Content: "在"},
	}
	s, rest := extractSummary(h)
	if s != "[前文摘要] 甲\n乙" {
		t.Fatalf("摘要合并异常: %q", s)
	}
	if len(rest) != 2 || rest[0].Role != openai.ChatMessageRoleUser {
		t.Fatalf("剩余消息异常: %+v", rest)
	}

	// 没有摘要时，rest 必须等于原切片长度
	_, rest2 := extractSummary([]openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "hi"},
	})
	if len(rest2) != 1 {
		t.Fatalf("无摘要时应原样返回，实际 %d", len(rest2))
	}
}

func TestCutIndexAlignsToUser(t *testing.T) {
	long := strings.Repeat("很", 4000)
	h := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: "第一问"},
		{Role: openai.ChatMessageRoleAssistant, Content: long},
		{Role: openai.ChatMessageRoleUser, Content: "第二问"},
		{Role: openai.ChatMessageRoleAssistant, Content: "第二答"},
	}

	i := cutIndex(h, 100)
	if i >= len(h) {
		t.Fatalf("切点不应越界，实际 %d", i)
	}
	if h[i].Role != openai.ChatMessageRoleUser {
		t.Fatalf("切点必须落在 user 消息，实际角色 %s", h[i].Role)
	}
	if i != 2 {
		t.Fatalf("按预算应切在第 3 条（index=2），实际 %d", i)
	}
}

// 硬裁必须保留头部摘要，且总量落进目标
func TestHardDropKeepsSummary(t *testing.T) {
	h := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "[前文摘要] 之前聊过扫盘"},
	}
	for i := 0; i < 6; i++ {
		h = append(h,
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleUser, Content: "问" + strings.Repeat("话", 200)},
			openai.ChatCompletionMessage{Role: openai.ChatMessageRoleAssistant, Content: "答" + strings.Repeat("话", 200)},
		)
	}

	out := hardDrop(h, 500)
	if len(out) == 0 || out[0].Role != openai.ChatMessageRoleSystem {
		t.Fatalf("硬裁后必须保留摘要消息，实际首条 %+v", out[:1])
	}
	if estMessages(out) > 500 {
		t.Fatalf("硬裁后仍超预算：%d", estMessages(out))
	}
	if len(out) >= len(h) {
		t.Fatalf("硬裁应丢掉老消息，实际长度 %d / %d", len(out), len(h))
	}
}

func TestTruncateChars(t *testing.T) {
	if got := truncateChars("一二三四五", 3); got != "一二三…" {
		t.Fatalf("截断异常: %q", got)
	}
	if got := truncateChars("短", 5); got != "短" {
		t.Fatalf("未超长不应改动: %q", got)
	}
}
