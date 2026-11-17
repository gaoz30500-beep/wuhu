package MCP

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"agent/memory"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MemoryConfig 对应 mcp_config.json 里 tools.save_memory / tools.recall_memory 节点
type MemoryConfig struct {
	StorePath string `json:"storePath,omitempty"`
}

// SaveMemoryIn save_memory 的入参
type SaveMemoryIn struct {
	Content string `json:"content" jsonschema:"要记住的一句话，必须自包含（写清主语），例如：用户偏好用中文回答"`
	Tags    string `json:"tags,omitempty" jsonschema:"可选，逗号分隔的标签，用于检索，例如：偏好,语言"`
}

// RecallMemoryIn recall_memory 的入参
type RecallMemoryIn struct {
	Keyword string `json:"keyword" jsonschema:"检索关键词，中文直接给词即可，例如：项目、显示器、偏好"`
	Limit   int    `json:"limit,omitempty" jsonschema:"最多返回几条，不填默认 5"`
}

// registerMemoryTools 注册长期记忆的写入与检索两个工具
func registerMemoryTools(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "save_memory",
		Description: "把值得长期记住的信息写入记忆库。适用：用户的稳定偏好、身份或设备事实、" +
			"明确要求记住的事、已完成的重要结论。不要存一次性的临时指令（例如“帮我扫 E 盘”）。",
	}, runSaveMemory)

	mcp.AddTool(server, &mcp.Tool{
		Name: "recall_memory",
		Description: "从记忆库中检索与关键词相关的长期记忆。" +
			"当用户提到“以前”“上次”“我之前说过”“我的……”等需要跨对话回忆的内容时，先调用它再回答。",
	}, runRecallMemory)
}

func runSaveMemory(ctx context.Context, req *mcp.CallToolRequest, in SaveMemoryIn) (*mcp.CallToolResult, any, error) {
	storePath := cfg.Tools.Save.StorePath

	// 写入统一走 memory.Write：它自己判重、合并、刷权重。
	// 原来那套"先 Search 整句子串"永远命中不了，等于没查重。
	res, err := memory.Write(storePath, in.Content, in.Tags, "tool")
	if err != nil {
		// 内容为空 / 没有实义词 = 调用方的问题，不是系统故障：
		// 当正常结果返回，别让用户屏幕上冒一行【工具执行失败】。
		if errors.Is(err, memory.ErrEmptyContent) || errors.Is(err, memory.ErrNoSemantic) {
			return textResult("已跳过本次保存：内容为空或没有实义词，不用重试"), nil, nil
		}
		return nil, nil, fmt.Errorf("保存记忆失败: %w", err)
	}

	if res.Action == memory.ActionMerged {
		return textResult(fmt.Sprintf("已并入已有记忆 #%d（权重 %d，相似度 %.2f）：%s",
			res.Matched, res.Record.Weight, res.Score, res.Record.Content)), nil, nil
	}
	return textResult(fmt.Sprintf("已记住 #%d：%s", res.Record.ID, res.Record.Content)), nil, nil
}

func runRecallMemory(ctx context.Context, req *mcp.CallToolRequest, in RecallMemoryIn) (*mcp.CallToolResult, any, error) {
	storePath := cfg.Tools.Recall.StorePath

	limit := in.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10 // 一次返回太多会吃掉上下文预算，加速触发压缩
	}

	list, err := memory.LoadAll(storePath)
	if err != nil {
		return nil, nil, fmt.Errorf("读取记忆库失败: %w", err)
	}
	if len(list) == 0 {
		return textResult("记忆库还是空的。"), nil, nil
	}

	// 用和注入层同一套打分（0.5 相关 + 0.3 新鲜 + 0.2 权重），
	// 保证"工具查到的"和"提前塞进 prompt 的"是同一个标准。
	hits := memory.Rank(list, in.Keyword, limit)
	if len(hits) == 0 {
		return textResult("没有检索到相关记忆。"), nil, nil
	}

	var sb strings.Builder
	for _, r := range hits {
		fmt.Fprintf(&sb, "- #%d %s", r.ID, r.Content)
		if r.Tags != "" {
			fmt.Fprintf(&sb, "（%s）", r.Tags)
		}
		sb.WriteString("\n")
	}
	text := fmt.Sprintf("检索到 %d 条相关记忆：\n%s", len(hits), strings.TrimRight(sb.String(), "\n"))
	return textResult(text), nil, nil
}

// textResult 包一层，省得每处都写三行结构体
func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: s}},
	}
}
