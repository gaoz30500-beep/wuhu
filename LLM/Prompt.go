package llm

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"

	"agent/memory"

	"github.com/sashabaranov/go-openai"
)

const (
	// 模型上下文上限（deepseek-flash = 1M）。只当天花板看，不当预算用。
	modelContextLimit = 1_000_000
	// 预算 = 上限的 6% ≈ 60k token。成本随 token 线性涨，没必要贴着 1M 用
	budgetRatio = 0.06
	// 到预算的 80% 触发压缩：必须留余量给"压缩请求"本身，真满了再压会 400
	compactTriggerRatio = 0.8
	// 压缩时保留最近这一段（占预算的 40%）
	compactKeepRatio = 0.4
	// 压缩后总量必须低于预算的 30%，否则判定压缩失败 → 硬裁
	compactTargetRatio = 0.3
	// 摘要长度上限（token，中文 1 字≈1 token）
	summaryMaxTokens = 1000
	// 长期记忆每轮注入条数
	memoryPromptLimit = 20
	// 单条消息喂给压缩器时的截断长度（字符），防压缩请求自己爆掉
	compactMsgChars = 1200
)

// TokenBudget 单轮上下文预算（token）
var TokenBudget = float64(modelContextLimit) * budgetRatio

// compactPrompt 压缩器的系统提示
const compactPrompt = `你是对话压缩器。把下面这段历史对话压成一段摘要，要求：
1. 保留：用户的事实与偏好、达成的决定、工具调用得到的关键结果与结论；
2. 删除：寒暄、重复、失败重试、无关的中间过程；
3. 按时间顺序写，不编造，不写"用户说""助手说"这类冗余主语；
4. 纯文本，不超过 800 字。`

// ================= 路径 =================

// memoryPath 必须和 mcp_config.json 里两个记忆工具的 storePath 一致
func memoryPath() string {
	if p := os.Getenv("AGENT_MEMORY_PATH"); p != "" {
		return p
	}
	return memory.DefaultPath
}

// ================= system prompt =================

// buildSystemPrompt 每轮重新读一次记忆文件：
// 上一轮刚写的记忆，这一轮就能用上（不用重启）
func buildSystemPrompt() string {
	var sb strings.Builder
	sb.WriteString("你是一个运行在用户 Windows 电脑上的 AI 助手，可以调用工具操作本机文件、查询网络。回答简洁、准确、口语化。\n\n")
	sb.WriteString("记忆规则：\n")
	sb.WriteString("1. 当用户说明稳定的偏好、身份/设备事实，或明确要求你记住某件事时，调用 save_memory 保存（一句话，写清主语）。\n")
	sb.WriteString("2. 当用户提到“以前”“上次”“我说过”“我的…”这类需要跨对话回忆的内容时，先调用 recall_memory 检索，再回答。\n")
	sb.WriteString("3. 一次性的临时指令（例如“帮我扫 E 盘”）不要存。\n")

	if facts := memory.Render(memoryPath(), memoryPromptLimit); facts != "" {
		sb.WriteString("\n【关于用户的长期记忆】（按时间先后，越靠后越新）\n")
		sb.WriteString(facts)
	}
	return sb.String()
}

// ================= 规则兜底：用户明说要记住 =================

// rememberKeywords 命中这些词，就认为用户在要求"记住"
var rememberKeywords = []string{"记住", "记一下", "记下", "别忘", "以后都", "以后不要"}

// autoRemember 规则兜底写入。模型判断会漏，这条只是保险，失败不影响对话
func autoRemember(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	hit := false
	for _, kw := range rememberKeywords {
		if strings.Contains(text, kw) {
			hit = true
			break
		}
	}
	if !hit {
		return
	}

	// 先查重，避免同一句话存十遍
	if exist, _ := memory.Search(memoryPath(), text, 5); len(exist) > 0 {
		return
	}
	_, _ = memory.Append(memoryPath(), "用户说过："+text, "自动,规则")
}

// ================= token 粗估 =================
// 不引 tokenizer 库：中文按字、英文按 4 字符，够用来判阈值

func estTokens(s string) int {
	cjk, other := 0, 0
	for _, r := range s {
		switch {
		case unicode.IsSpace(r):
			continue
		case r > 0x2E7F: // 中日韩等全角字符
			cjk++
		default:
			other++
		}
	}
	return cjk + other/4 + 1
}

func estMessages(h []openai.ChatCompletionMessage) int {
	total := 0
	for _, m := range h {
		total += 4 // 每条消息的格式开销
		total += estTokens(m.Content)
		for _, tc := range m.ToolCalls {
			total += 8 + estTokens(tc.Function.Name) + estTokens(tc.Function.Arguments)
		}
	}
	return total
}

// ================= 懒压缩 =================

// extractSummary 取出 history 头部连续的 system 消息（= 上轮摘要），
// 其余消息才是真正要喂给模型的对话。
func extractSummary(h []openai.ChatCompletionMessage) (string, []openai.ChatCompletionMessage) {
	var parts []string
	i := 0
	for ; i < len(h); i++ {
		if h[i].Role != openai.ChatMessageRoleSystem {
			break
		}
		if c := strings.TrimSpace(h[i].Content); c != "" {
			parts = append(parts, c)
		}
	}
	return strings.Join(parts, "\n"), h[i:]
}

// compactIfNeeded 平时什么都不做；预算到 80% 才压一次，压不下去就硬裁。
// 返回的 history 头部最多带一条 system 摘要。
func compactIfNeeded(ctx context.Context, client *openai.Client, h []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	if h == nil || estMessages(h) < int(TokenBudget*compactTriggerRatio) {
		return h
	}

	// 切点：从后往前装到 40% 预算，并对齐到 user 消息
	keepFrom := cutIndex(h, TokenBudget*compactKeepRatio)
	if keepFrom <= 0 {
		return h // 没有可压的老消息（一轮就吃满了），交给硬裁
	}

	if summary, err := summarize(ctx, client, h[:keepFrom]); err == nil && summary != "" {
		out := make([]openai.ChatCompletionMessage, 0, len(h)-keepFrom+1)
		out = append(out, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: "[前文摘要] " + summary,
		})
		out = append(out, h[keepFrom:]...)

		// 压缩必须真的变小，否则会陷入「压→更满→再压」的死循环
		if estMessages(out) <= int(TokenBudget*compactTargetRatio) {
			return out
		}
	}

	// 压缩失败 / 摘要太长 → 降级：直接丢最老的，不再压第二次
	return hardDrop(h, TokenBudget*compactTargetRatio)
}

// cutIndex 从尾部往前累加到 keepTokens，返回切点。
// 切点会对齐到 user 消息：从中间硬切会切出「孤儿 tool 结果」，接口会报 400。
func cutIndex(h []openai.ChatCompletionMessage, keepTokens float64) int {
	total := 0
	i := len(h)
	for i > 0 {
		m := h[i-1]
		t := 4 + estTokens(m.Content)
		for _, tc := range m.ToolCalls {
			t += 8 + estTokens(tc.Function.Arguments)
		}
		if float64(total+t) > keepTokens {
			break
		}
		total += t
		i--
	}
	for i < len(h) && h[i].Role != openai.ChatMessageRoleUser {
		i++
	}
	return i
}

// hardDrop 保住头部摘要，其余只留最近一段
func hardDrop(h []openai.ChatCompletionMessage, keepTokens float64) []openai.ChatCompletionMessage {
	summary, rest := extractSummary(h)
	if len(rest) == 0 {
		return h
	}

	left := keepTokens - float64(estTokens(summary))
	if left < 0 {
		left = 0
	}
	i := cutIndex(rest, left)
	if i >= len(rest) {
		return h // 一条都丢不掉，保持原样
	}

	out := make([]openai.ChatCompletionMessage, 0, len(rest)-i+1)
	if summary != "" {
		out = append(out, openai.ChatCompletionMessage{Role: openai.ChatMessageRoleSystem, Content: summary})
	}
	return append(out, rest[i:]...)
}

// summarize 把一段老对话交给模型压成摘要
func summarize(ctx context.Context, client *openai.Client, part []openai.ChatCompletionMessage) (string, error) {
	if len(part) == 0 {
		return "", nil
	}

	var sb strings.Builder
	for _, m := range part {
		sb.WriteString("[" + m.Role + "] ")
		if c := strings.TrimSpace(m.Content); c != "" {
			sb.WriteString(truncateChars(c, compactMsgChars))
		}
		for _, tc := range m.ToolCalls {
			fmt.Fprintf(&sb, " <调用工具 %s 参数 %s>", tc.Function.Name, truncateChars(tc.Function.Arguments, 200))
		}
		sb.WriteString("\n")
	}

	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: "deepseek-flash",
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: compactPrompt},
			{Role: openai.ChatMessageRoleUser, Content: sb.String()},
		},
	})
	if err != nil {
		return "", fmt.Errorf("压缩请求失败: %w", err)
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("压缩请求没有返回内容")
	}

	summary := strings.TrimSpace(resp.Choices[0].Message.Content)
	return truncateChars(summary, summaryMaxTokens), nil
}

// truncateChars 按字符（rune）截断，中文不会被截成半个
func truncateChars(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
