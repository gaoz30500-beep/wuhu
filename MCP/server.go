package MCP

import (
	"context"
	"log"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// StartAutoServer 组装 server 并跑在 stdio 上。
//
// 这里用官方推荐的泛型注册方式（mcp.AddTool[In, Out]）：
// 工具名、描述写在这里，入参 schema 由 In 结构体自动生成，
// 参数反序列化、按 schema 校验、错误包成 IsError 结果，全部由 SDK 负责。
func StartAutoServer(ctx context.Context) error {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "auto-mcp-server",
		Version: "v1.1.0",
	}, &mcp.ServerOptions{
		Instructions: "本 server 提供五类工具：本机命令执行与文件读写、闲置文件扫描、基于公网 IP 的城市级定位、长期记忆的写入与检索、记忆库管理。" +
			"当用户提到“以前”“上次”“我说过”“我的偏好”这类需要跨对话回忆的内容时，先调用 recall_memory 再回答；" +
			"当用户说明稳定的偏好、身份或设备事实，或明确要求记住某事时，调用 save_memory；" +
			"记忆记错、过时或用户要求忘掉时，用 manage_memory 修改或删除。" +
			"执行命令时优先用只读命令；涉及新建、修改、删除的改动类命令必须先说明影响并等用户确认。",
	})

	// 新增工具时在这里挂一行即可，不需要再维护 registry 和配置文件里的映射表
	registerScanTool(server)
	registerLocationTool(server)
	registerMemoryTools(server)
	registerMemoryAdminTool(server)
	registerShellInto(server)
	registerFileTools(server)
	log.Println("auto server start")

	// 官方 SDK 没有 ServeStdio，用 Run + StdioTransport
	return server.Run(ctx, &mcp.StdioTransport{})
}
