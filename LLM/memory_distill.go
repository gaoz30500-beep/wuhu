package llm

import (
	"context"
	"strings"
	"time"
	"unicode"

	"agent/Env"
	"agent/memory"

	"github.com/sashabaranov/go-openai"
)

// ================= 旁路提炼：对话结束后自动把"值得记的"沉到长期记忆 =================
//
// 为什么要有它：
//   - save_memory 靠模型自觉，deepseek-flash 基本不主动调；
//   - autoRemember 只认"记住/别忘"几个关键词，用户说别的话全漏；
//   - 结果就是"关掉重开全忘"。这条链路补的就是这个缺口。
//
// 三条铁律：
//   1. 异步（go distill）：绝不能拖慢用户等回复的时间；
//   2. 失败一律不管：提炼挂了、网络断了，都不影响对话；
//   3. 只提炼"本轮"（最后一条 user 消息之后），不碰历史，省 token。

const (
	// distillOn 总开关，不想要自动提炼就改成 false
	distillOn = true
	// distillMaxItems 一轮最多沉淀几条，防止一句话刷出十条垃圾
	distillMaxItems = 3
	// distillMsgChars 单条消息喂给提炼器的截断长度（字符）
	distillMsgChars = 600
	// distillTimeout 提炼请求的超时，超时就当这轮没有
	distillTimeout = 20 * time.Second
)

// distillPrompt 提炼器的系统提示：只认"长期有价值"的东西
const distillPrompt = `你是长期记忆提炼器。下面是用户与助手的一轮对话，请提炼其中值得长期保留的信息：

可以提炼：
1. 用户的稳定事实：身份、设备、软件环境、文件路径、账号、正在做的长期项目；
2. 用户的偏好：称呼、习惯、明确要求、明确讨厌的做法；
3. 已经达成的决定、结论，以及尚未完成的待办。

不要提炼：
- 寒暄、问候、语气词；
- 一次性的临时指令（例如“扫一下 E 盘”“打开这个文件”）；
- 工具调用的过程与中间结果；
- 助手自己的解释、道歉、说明。

输出规则：
- 有值得记的，每条一行，以 “- ” 开头，一句话、自包含、写清主语，不超过 40 个字，最多 3 条；
- 没有值得记的，只输出两个字：“无”；
- 不编造、不复述原话，用陈述句。`

// distill 提炼本轮对话并写入记忆库。调用方必须用 go distill(...) 异步跑。
func distill(hist []openai.ChatCompletionMessage) {
	if !distillOn {
		return
	}

	part := lastUserTurn(hist)
	if len(part) == 0 {
		return
	}
	text := distillInput(part)
	if strings.TrimSpace(text) == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), distillTimeout)
	defer cancel()

	cfg := Env.Current()
	config := openai.DefaultConfig(cfg.API_KEY)
	config.BaseURL = cfg.BASE_URL
	client := openai.NewClientWithConfig(config)

	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: cfg.Model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: distillPrompt},
			{Role: openai.ChatMessageRoleUser, Content: text},
		},
	})
	if err != nil || len(resp.Choices) == 0 {
		return // 提炼失败就当没有，绝不影响对话
	}

	items := parseDistill(resp.Choices[0].Message.Content)
	path := memoryPath()
	for _, it := range items {
		// 判重交给 memory.Write（指纹 + 相似度）；它自己会决定"合并"还是"新增"，
		// 原来的 isDupMemory 只比前 10 个字，判不了"换种说法"。
		_, err := memory.Write(path, it, "自动,提炼", "distill")
		if err != nil {
			// 内容为空这类跳过错误直接忽略；真故障也不影响对话
			continue
		}
	}
}

// lastUserTurn 取"本轮"：从末尾往前找到最后一条 user 消息，返回它到结尾的片段。
// 历史末尾永远是刚发的那条 user 消息，所以这就是本轮起始点。
func lastUserTurn(hist []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	for i := len(hist) - 1; i >= 0; i-- {
		if hist[i].Role == openai.ChatMessageRoleUser {
			return hist[i:]
		}
	}
	return nil
}

// distillInput 把本轮消息拼成提炼器的输入。
// tool 消息和工具调用一律跳过：工具过程不是记忆。
func distillInput(part []openai.ChatCompletionMessage) string {
	var sb strings.Builder
	for _, m := range part {
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		switch m.Role {
		case openai.ChatMessageRoleUser:
			sb.WriteString("用户：" + truncateChars(content, distillMsgChars) + "\n")
		case openai.ChatMessageRoleAssistant:
			// 只取模型真正说出口的话，工具调用那一版内容通常是空的
			sb.WriteString("助手：" + truncateChars(content, distillMsgChars) + "\n")
		}
	}
	return strings.TrimSpace(sb.String())
}

// parseDistill 解析提炼器的输出：去掉 "- " 之类前缀，最多 distillMaxItems 条。
// 输出“无”或全是空行 → 返回 nil（这轮没什么可存的，正常情况）。
func parseDistill(out string) []string {
	var items []string
	for _, raw := range strings.Split(out, "\n") {
		line := strings.TrimSpace(raw)
		// 兼容 - / * / • / 1. 这几种模型爱用的列表前缀
		line = strings.TrimLeft(line, "-*• ")
		line = strings.TrimSpace(line)
		if line == "" || line == "无" || line == "无。" {
			continue
		}
		items = append(items, line)
		if len(items) >= distillMaxItems {
			break
		}
	}
	return items
}

// isDupMemory 轻量查重：拿候选记忆的前 10 个字，去已有记忆里找子串。
// 比"整句互相包含"实用得多，也比较 save_memory 那个永远不命中的查重可靠。
func isDupMemory(path, cand string) bool {
	exist, err := memory.LoadAll(path)
	if err != nil || len(exist) == 0 {
		return false
	}

	// 归一化：去空白、转小写，避免全角/空格造成假阴性
	norm := func(s string) string {
		s = strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, s)
		return strings.ToLower(s)
	}

	key := norm(cand)
	if rs := []rune(key); len(rs) > 10 {
		key = string(rs[:10])
	}
	if key == "" {
		return false
	}

	for _, r := range exist {
		if strings.Contains(norm(r.Content), key) {
			return true
		}
	}
	return false
}
