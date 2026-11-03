package MCP

import (
	"agent/scanner"
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ScanConfig struct {
	TargetPath   string `json:"targetPath"`
	IdleDays     int64  `json:"idleDays"`
	ExportMdPath string `json:"exportMdPath,omitempty"`
}

// ScanTool 扫描工具本体
type ScanTool struct{}

func (s *ScanTool) GetName() string {
	return "scan_idle_file"
}

func (s *ScanTool) GetDescription() string {
	return "扫描目录闲置文件"
}

func (s *ScanTool) GetSchema() string {
	return `{
		"type": "object",
		"properties": {
			"targetPath": {"type":"string"},
			"idleDays": {"type":"integer"},
			"exportMdPath": {"type":"string"}
		}
	}`
}

func (s *ScanTool) NewConfig() any {
	return &ScanConfig{}
}

func (s *ScanTool) Run(ctx context.Context, req *mcp.CallToolRequest, cfg any) (*mcp.CallToolResult, error) {
	conf := cfg.(*ScanConfig)
	var args ScanConfig
	if req.Params.Arguments != nil {
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return nil, fmt.Errorf("参数解析失败: %w", err)
		}
	}
	if args.TargetPath != "" {
		conf.TargetPath = args.TargetPath
	}
	if args.IdleDays != 0 {
		conf.IdleDays = args.IdleDays
	}
	if args.ExportMdPath != "" {
		conf.ExportMdPath = args.ExportMdPath
	}
	scan := scanner.NewScanner(conf.TargetPath, conf.IdleDays)
	resList, err := scan.Scan()
	if err != nil {
		return nil, fmt.Errorf("Scan() failed: %w", err)
	}
	result := fmt.Sprintf(
		"✅扫描完成\n路径：%s\n闲置阈值：%d天\n匹配文件数量：%d",
		conf.TargetPath,
		conf.IdleDays,
		len(resList),
	)
	if conf.ExportMdPath != "" {
		result += fmt.Sprintf("\n报告输出：%s", conf.ExportMdPath)
	}
	res := &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{
				Text: result,
			},
		},
	}
	return res, nil

}

func init() {
	Register(&ScanTool{})
}
