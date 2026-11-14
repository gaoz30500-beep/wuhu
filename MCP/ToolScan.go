package MCP

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	output "agent/OutPut"
	"agent/scanner"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ScanConfig 是 mcp_config.json 里 tools.scan_idle_file 节点，
// 只作为"默认值来源"，handler 只读不改。
type ScanConfig struct {
	TargetPath   string `json:"targetPath,omitempty"`
	IdleDays     int64  `json:"idleDays,omitempty"`
	ExportMdPath string `json:"exportMdPath,omitempty"`
}

// ScanIn 就是本工具的入参声明：字段名 = JSON key，标签 = 参数说明，
// 不带 omitempty 的字段会被自动标记为 required。
// 注意：标签里不能出现反斜杠，StructTag 会对它做转义解析。
type ScanIn struct {
	TargetPath   string `json:"targetPath,omitempty" jsonschema:"要扫描的目录，例如 E: 或 E: 下的某个子目录；不填则用 mcp_config.json 里的默认目录"`
	IdleDays     int64  `json:"idleDays,omitempty" jsonschema:"闲置判定阈值，单位天：超过这个天数没有访问即算闲置，不填默认 90"`
	ExportMdPath string `json:"exportMdPath,omitempty" jsonschema:"可选，Markdown 报告的保存路径；不填则用配置里的值，配置也没有就不导出文件"`
}

// registerScanTool 注册闲置文件扫描工具
func registerScanTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "scan_idle_file",
		Description: "扫描本机指定目录，找出超过阈值天数没有访问（atime）的闲置文件；" +
			"返回匹配数量，并把明细导出为 Markdown 报告（导出路径可指定）。",
	}, runScanIdleFile)
}

// runScanIdleFile 真正的工具实现。
// 出错时返回 error 即可：SDK 会把它包成 IsError=true 的工具结果，
// 模型能看到错误原文并自行调整参数重试（这正是 MCP 规范要求的语义）。
func runScanIdleFile(ctx context.Context, req *mcp.CallToolRequest, in ScanIn) (*mcp.CallToolResult, any, error) {
	conf := cfg.Tools.Scan

	// 参数优先，其次配置里的默认值
	targetPath := in.TargetPath
	if targetPath == "" {
		targetPath = conf.TargetPath
	}
	if targetPath == "" {
		return nil, nil, fmt.Errorf("缺少 targetPath：请在参数里指定要扫描的目录，或在 mcp_config.json 的 tools.scan_idle_file.targetPath 里配默认值")
	}

	idleDays := in.IdleDays
	if idleDays <= 0 {
		idleDays = conf.IdleDays
	}
	if idleDays <= 0 {
		idleDays = 90
	}

	// 路径先校验：否则 WalkDir 遇到不存在的根目录只会静默返回 0 个文件，
	// 模型会以为"这个盘没有闲置文件"，而不是"你给的路径不对"
	if err := scanner.ValidateDirPath(targetPath); err != nil {
		return nil, nil, fmt.Errorf("扫描 %s 失败: %w", targetPath, err)
	}

	exportMdPath := in.ExportMdPath
	if exportMdPath == "" {
		exportMdPath = conf.ExportMdPath
	}

	// 1. 扫描
	resList, err := scanner.NewScanner(targetPath, idleDays).Scan()
	if err != nil {
		return nil, nil, fmt.Errorf("扫描 %s 失败: %w", targetPath, err)
	}

	// 2. 导出 Markdown（路径里有目录就先建，否则 os.Create 会失败）
	if exportMdPath != "" {
		if dir := filepath.Dir(exportMdPath); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, nil, fmt.Errorf("创建报告目录失败: %w", err)
			}
		}
		items := make([]output.ReportItem, 0, len(resList))
		for _, r := range resList {
			if r.Error != nil {
				continue
			}
			items = append(items, output.ReportItem{
				Path:    r.Path,
				Size:    r.Size,
				IdleDur: time.Since(r.Atime).String(),
			})
		}
		if err := output.ExportToMarkdown(exportMdPath, items); err != nil {
			return nil, nil, fmt.Errorf("扫描成功，但导出 Markdown 失败: %w", err)
		}
	}

	// 3. 回给模型的是简短摘要，明细在报告文件里，不往上下文里灌几千行路径
	result := fmt.Sprintf(
		"扫描完成\n路径：%s\n闲置阈值：%d天\n匹配文件数量：%d",
		targetPath, idleDays, len(resList),
	)
	if exportMdPath != "" {
		result += fmt.Sprintf("\n报告输出：%s", exportMdPath)
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: result}},
	}, nil, nil
}
