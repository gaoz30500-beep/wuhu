package MCP

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func StartAutoServer(ctx context.Context) error {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "auto-mcp-server",
		Version: "v1.0.0",
	}, nil)

	tools := ListTools()
	for _, t := range tools {
		// 生成配置对象，读取 mcp_config.json 里对应工具的配置
		cfg := t.NewConfig()
		if err := GetToolConfig(t.GetName(), cfg); err != nil {
			return fmt.Errorf("load tool %s config err: %w", t.GetName(), err)
		}

		tool := t
		toolCfg := cfg

		// 官方 go-sdk：AddTool 传 *Tool（指针），返回 void（不返回 error），
		// InputSchema 是 any 类型，直接塞 json.RawMessage，不需要 MustParseJSONSchema。
		// handler 的 req 是 *mcp.CallToolRequest（指针）。
		server.AddTool(&mcp.Tool{
			Name:        tool.GetName(),
			Description: tool.GetDescription(),
			InputSchema: json.RawMessage(tool.GetSchema()),
		}, func(ctxInner context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			log.Printf("call tool: %s", tool.GetName())
			return tool.Run(ctxInner, req, toolCfg)
		})
	}

	log.Println("auto server start")
	// 官方 SDK 没有 ServeStdio，要用 Run + StdioTransport
	return server.Run(ctx, &mcp.StdioTransport{})
}
