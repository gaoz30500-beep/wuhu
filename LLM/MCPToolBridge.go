package llm

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sashabaranov/go-openai"
)

// MCPToolToOpenAI 把一个 MCP 工具，转成 OpenAI 的 function 声明（"说明书"）
func MCPToolToOpenAI(t *mcp.Tool) (openai.FunctionDefinition, error) {
	var params map[string]any

	// 客户端拿到的 InputSchema 已经是 map[string]any，直接断言即可
	if m, ok := t.InputSchema.(map[string]any); ok {
		params = m
	} else if t.InputSchema != nil {
		// 兜底：万一拿到别的形态（如 JSON 字节），再走一次序列化
		b, err := json.Marshal(t.InputSchema)
		if err != nil {
			return openai.FunctionDefinition{}, err
		}
		if err := json.Unmarshal(b, &params); err != nil {
			return openai.FunctionDefinition{}, err
		}
	}

	return openai.FunctionDefinition{
		Name:        t.Name,
		Description: t.Description,
		Parameters:  params,
	}, nil
}

// MCPToolsToOpenAI 从 MCP 会话拉取全部工具，批量转成 openai.Tool 列表
func MCPToolsToOpenAI(ctx context.Context, session *mcp.ClientSession) ([]openai.Tool, error) {
	var tools []openai.Tool
	for tool, err := range session.Tools(ctx, &mcp.ListToolsParams{}) {
		if err != nil {
			return nil, err
		}
		fn, err := MCPToolToOpenAI(tool)
		if err != nil {
			return nil, err
		}
		tools = append(tools, openai.Tool{
			Type:     openai.ToolTypeFunction,
			Function: &fn,
		})
	}
	return tools, nil
}
