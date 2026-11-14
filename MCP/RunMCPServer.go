package MCP

import (
	"context"
	"fmt"
	"os"
)

// RunMCPServer 本程序以 MCP 服务器身份运行的入口（main 里 --mcp 分支调用）。
func RunMCPServer() {
	path := resolveConfigPath()
	if err := loadConfig(path); err != nil {
		// 注意：所有日志只能写 stderr，stdout 是 JSON-RPC 通道
		fmt.Fprintln(os.Stderr, "加载配置失败:", err)
		os.Exit(1)
	}

	if err := StartAutoServer(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "MCP Server 退出:", err)
		os.Exit(1)
	}
}
