package MCP

import (
	"agent/memory"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MemoryConfig 记忆工具的配置（对应 mcp_config.json 里各自的节点）
type MemoryConfig struct {
	StorePath string `json:"storePath,omitempty"`
}

// ================= save_memory =================

// SaveMemoryTool 写入一条长期记忆
type SaveMemoryTool struct{}

func (t *SaveMemoryTool) GetName() string { return "save_memory" }

func (t *SaveMemoryTool) GetDescription() string {
	return `把值得长期记住的信息写入记忆库。适用：用户的稳定偏好、身份/设备事实、明确要求"记住"的事、已完成的重要结论。不要存一次性的临时指令（例如"帮我扫 E 盘"）。`
}

func (t *SaveMemoryTool) GetSchema() string {
	return `{
		"type": "object",
		"properties": {
			"content": {"type": "string", "description": "要记住的一句话，必须自包含（写清主语），例如：用户偏好用中文回答"},
			"tags": {"type": "string", "description": "可选，逗号分隔的标签，用于检索，例如：偏好,语言"}
		},
		"required": ["content"]
	}`
}

func (t *SaveMemoryTool) NewConfig() any { return &MemoryConfig{} }

func (t *SaveMemoryTool) Run(ctx context.Context, req *mcp.CallToolRequest, cfg any) (*mcp.CallToolResult, error) {
	conf := cfg.(*MemoryConfig)
	var args struct {
		Content string `json:"content"`
		Tags    string `json:"tags"`
	}
	if req.Params.Arguments != nil {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return nil, fmt.Errorf("参数解析失败: %w", err)
		}
	}

	// 写之前先查重：同一条事实不要存十遍
	if exist, _ := memory.Search(conf.StorePath, args.Content, 20); len(exist) > 0 {
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("已存在相似记忆（#%d）：%s", exist[0].ID, exist[0].Content)},
		}}, nil
	}

	r, err := memory.Append(conf.StorePath, args.Content, args.Tags)
	if err != nil {
		return nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: fmt.Sprintf("已记住（#%d）：%s", r.ID, r.Content)},
	}}, nil
}

func init() { Register(&SaveMemoryTool{}) }

// ================= recall_memory =================

// RecallMemoryTool 检索长期记忆
type RecallMemoryTool struct{}

func (t *RecallMemoryTool) GetName() string { return "recall_memory" }

func (t *RecallMemoryTool) GetDescription() string {
	return `从记忆库中检索与关键词相关的长期记忆。当用户提到"以前""上次""我之前说过""我的……"等需要跨对话回忆的内容时，先调用它再回答。`
}

func (t *RecallMemoryTool) GetSchema() string {
	return `{
		"type": "object",
		"properties": {
			"keyword": {"type": "string", "description": "检索关键词，中文直接给词即可，例如：项目、显示器、偏好"},
			"limit": {"type": "integer", "description": "最多返回几条，默认 5"}
		},
		"required": ["keyword"]
	}`
}

func (t *RecallMemoryTool) NewConfig() any { return &MemoryConfig{} }

func (t *RecallMemoryTool) Run(ctx context.Context, req *mcp.CallToolRequest, cfg any) (*mcp.CallToolResult, error) {
	conf := cfg.(*MemoryConfig)
	var args struct {
		Keyword string `json:"keyword"`
		Limit   int    `json:"limit"`
	}
	if req.Params.Arguments != nil {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return nil, fmt.Errorf("参数解析失败: %w", err)
		}
	}
	list, err := memory.Search(conf.StorePath, args.Keyword, args.Limit)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: "没有检索到相关记忆。"},
		}}, nil
	}
	var sb strings.Builder
	for _, r := range list {
		fmt.Fprintf(&sb, "#%d [%s] %s", r.ID, r.Time, r.Content)
		if r.Tags != "" {
			fmt.Fprintf(&sb, "（标签：%s）", r.Tags)
		}
		sb.WriteString("\n")
	}
	return &mcp.CallToolResult{Content: []mcp.Content{
		&mcp.TextContent{Text: fmt.Sprintf("检索到 %d 条相关记忆：\n%s", len(list), sb.String())},
	}}, nil
}

func init() { Register(&RecallMemoryTool{}) }
