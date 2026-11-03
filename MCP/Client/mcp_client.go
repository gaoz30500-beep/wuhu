package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{
		Name:    "my-client",
		Version: "V1.0.0",
	}, nil)
	//--------------------
	transport := &mcp.LoggingTransport{
		Transport: &mcp.CommandTransport{
			Command: exec.Command("go", "run", ".", "--mcp"),
		},
		Writer: os.Stderr,
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		log.Fatalf("mcp-server连接失败%v", err)
	}
	defer session.Close()
	fmt.Println("=== 服务器提供的工具 ===")
	for tool, err := range session.Tools(ctx, &mcp.ListToolsParams{}) {
		if err != nil {
			log.Fatalf("列出工具失败%v", err)
		}
		fmt.Printf("  - %s : %s\n", tool.Name, tool.Description)
	}
	fmt.Println("\n=== 调用 get_location ===")
	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "get_location",
		Arguments: map[string]any{},
	})
	if err != nil {
		log.Fatalf("调用失败: %v", err)
	}
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			fmt.Println(tc.Text)
		}
	}
}
