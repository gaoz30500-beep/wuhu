package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sashabaranov/go-openai"
)

// maxTurns 最大对话轮次：防止 function call 死循环烧掉 API 额度
const maxTurns = 40

// ChatTurn 多轮对话：工具从 MCP 服务器动态拉取，调用统一走 session.CallTool 执行。
func ChatTurn(ctx context.Context, apiKey string, history []openai.ChatCompletionMessage, session *mcp.ClientSession) (string, []openai.ChatCompletionMessage, error) {
	config := openai.DefaultConfig(apiKey)
	config.BaseURL = "https://api.deepseek.com"
	client := openai.NewClientWithConfig(config)

	// ① 规则兜底：用户这句话像"要我记住"，就先落一条记忆（失败不影响对话）
	// 同时记一条情节：这一轮"用户说了什么"，供下次开会话时接上下文。
	lastUser := ""
	if n := len(history); n > 0 && history[n-1].Role == openai.ChatMessageRoleUser {
		lastUser = strings.TrimSpace(history[n-1].Content)
		autoRemember(lastUser)
		recordEpisode("user", lastUser)
	}

	// ② 懒压缩：平时什么都不做，预算到 80% 才压一次；压不下去就硬裁。
	// 返回的 history 头部最多带一条 system 摘要。
	base := compactIfNeeded(ctx, client, history)

	// ③ 组装本轮请求：system（长期记忆三槽 + 前文摘要）+ 其余历史
	summary, rest := extractSummary(base)
	// rest 只剩这一条用户消息 = 新会话第一句，这时才附上"上次会话的尾巴"
	systemContent := buildSystemPrompt(lastUser, len(rest) <= 1)
	if summary != "" {
		systemContent += "\n\n【前文对话摘要】\n" + summary
	}
	messages := make([]openai.ChatCompletionMessage, 0, len(rest)+1)
	messages = append(messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleSystem,
		Content: systemContent,
	})
	messages = append(messages, rest...)

	// keep 返回给调用方（main.go 存回 m.history）的历史：
	// system 必须摘掉（留着会一轮一轮越滚越长，还和新读的记忆打架），
	// 但"前文摘要"要按原样挂回头部，下一轮才能接上。
	keep := func() []openai.ChatCompletionMessage {
		out := make([]openai.ChatCompletionMessage, 0, len(messages))
		if summary != "" {
			out = append(out, openai.ChatCompletionMessage{
				Role:    openai.ChatMessageRoleSystem,
				Content: summary,
			})
		}
		return append(out, messages[1:]...)
	}

	// 会话期间工具列表不变，循环前拉一次即可
	tools, err := MCPToolsToOpenAI(ctx, session)
	if err != nil {
		return "", keep(), fmt.Errorf("拉取MCP工具失败: %w", err)
	}

	turns := 0
	for {
		turns++
		if turns > maxTurns {
			landMsg := make([]openai.ChatCompletionMessage, len(messages), len(messages)+1)
			copy(landMsg, messages)
			landMsg = append(landMsg, openai.ChatCompletionMessage{
				Role: openai.ChatMessageRoleUser,
				Content: fmt.Sprintf("已达到本轮工具调用上限 %d 次。不要再调用任何工具，"+
					"直接基于上面已有的结果回答用户；没做完的部分，说清楚还差什么。", maxTurns),
			})
			finalResp, ferr := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
				Model:    "deepseek-flash",
				Messages: landMsg,
			})
			if ferr != nil || len(finalResp.Choices) == 0 {
				return "", keep(), fmt.Errorf("达到最大轮次 %d，且收尾请求失败: %w", maxTurns, ferr)
			}

			// 收尾也算一轮对话，同样旁路提炼一次
			answer := finalResp.Choices[0].Message.Content
			recordEpisode("assistant", answer)
			go distill(apiKey, messages)
			return answer, keep(), nil
		}

		// 请求模型（带上从 MCP 动态拉取的工具声明）
		resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model:    "deepseek-flash",
			Messages: messages,
			Tools:    tools,
		})
		if err != nil {
			return "", keep(), fmt.Errorf("LLM请求失败: %w", err)
		}

		msg := resp.Choices[0].Message
		messages = append(messages, msg)

		// 没有工具调用 → 普通对话，直接返回
		if len(msg.ToolCalls) == 0 {
			// 旁路提炼：异步把本轮里"值得长期记"的东西沉淀进记忆库，
			// 不阻塞这次回复（goroutine 挂了也不影响对话）
			recordEpisode("assistant", msg.Content)
			go distill(apiKey, messages)
			return msg.Content, keep(), nil
		}

		// 有工具调用 → 统一交给 MCP 服务器执行，结果回填给模型
		for _, toolCall := range msg.ToolCalls {
			// 模型返回的参数是 JSON 串，解析成 map
			var args map[string]any
			if toolCall.Function.Arguments != "" {
				if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
					args = nil
				}
			}

			// 动态执行：模型要调谁就调谁，不写死工具名
			res, err := session.CallTool(ctx, &mcp.CallToolParams{
				Name:      toolCall.Function.Name,
				Arguments: args,
			})
			var toolContent string
			if err != nil {
				toolContent = fmt.Sprintf("【工具调用失败】调用 %s 出错：%v", toolCall.Function.Name, err)
			} else {
				toolContent = resultToText(res)
			}

			messages = append(messages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: toolCall.ID,
				Content:    toolContent,
			})
		}
		// 本轮结束，下一轮让模型基于工具结果继续
	}
}

// resultToText 把 CallToolResult 的 Content 拼成文本。
// IsError 的结果会带上标记：服务端按规范把业务失败放进 Content + isError=true，
// 这样模型能看到"失败了、失败在哪"，而不是当成一段正常输出继续往下编。
func resultToText(res *mcp.CallToolResult) string {
	var texts []string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			texts = append(texts, tc.Text)
		}
	}
	if len(texts) == 0 {
		return "(工具无返回内容)"
	}
	s := strings.Join(texts, "\n")
	if res.IsError {
		return "【工具执行失败】" + s
	}
	return s
}

// RunAgent 单轮对话入口（需要传入 MCP 会话）
func RunAgent(ctx context.Context, apiKey string, userPrompt string, session *mcp.ClientSession) (string, error) {
	history := []openai.ChatCompletionMessage{
		{Role: openai.ChatMessageRoleUser, Content: userPrompt},
	}
	answer, _, err := ChatTurn(ctx, apiKey, history, session)
	return answer, err
}
