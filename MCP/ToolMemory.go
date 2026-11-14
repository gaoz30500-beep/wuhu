package MCP

import (
	"context"
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

	// 写之前先查重：同一条事实不要存十遍
	if exist, _ := memory.Search(storePath, in.Content, 20); len(exist) > 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("已存在相似记忆（#%d）：%s", exist[0].ID, exist[0].Content),
			}},
		}, nil, nil
	}

	r, err := memory.Append(storePath, in.Content, in.Tags)
	if err != nil {
		return nil, nil, fmt.Errorf("写入记忆失败: %w", err)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("已记住（#%d）：%s", r.ID, r.Content),
		}},
	}, nil, nil
}

func runRecallMemory(ctx context.Context, req *mcp.CallToolRequest, in RecallMemoryIn) (*mcp.CallToolResult, any, error) {
	storePath := cfg.Tools.Recall.StorePath

	list, err := memory.Search(storePath, in.Keyword, in.Limit)
	if err != nil {
		return nil, nil, fmt.Errorf("检索记忆失败: %w", err)
	}
	if len(list) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "没有检索到相关记忆。"}},
		}, nil, nil
	}

	var sb strings.Builder
	for _, r := range list {
		fmt.Fprintf(&sb, "#%d [%s] %s", r.ID, r.Time, r.Content)
		if r.Tags != "" {
			fmt.Fprintf(&sb, "（标签：%s）", r.Tags)
		}
		sb.WriteString("\n")
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("检索到 %d 条相关记忆：\n%s", len(list), sb.String()),
		}},
	}, nil, nil
}
