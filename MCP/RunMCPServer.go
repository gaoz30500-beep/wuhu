package MCP

import (
	"context"
	"fmt"
	"os"
)

func RunMCPServer() {

	if err := LoadRootConfig("MCP/mcp_config.json"); err != nil {
		fmt.Fprintln(os.Stderr, "加载配置失败:", err)
		os.Exit(1)
	}

	if err := StartAutoServer(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "MCP Server 退出:", err)
		os.Exit(1)
	}
}
