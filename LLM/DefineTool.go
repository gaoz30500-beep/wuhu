package llm

import "github.com/sashabaranov/go-openai"

type ScanFileArgs struct {
	TargetPath   string `json:"targetPath"`
	IdleDays     int64  `json:"idleDays"`
	ExportMdPath string `json:"exportMdPath,omitempty"` // 自定义markdown保存路径，留空使用默认值
}

var ScanIdleFileTool = openai.FunctionDefinition{
	Name: "scan_idle_files",
	Description: `扫描指定Windows磁盘目录，找出超过指定天数没有访问(atime)的闲置文件，返回文件路径、大小、闲置时长。
targetPath：需要扫描的Windows盘符/目录。如果用户没有指定扫描路径，默认使用 C:\
idleDays：判定闲置的天数，用户没指定默认90
exportMdPath：可选，markdown报告保存路径，不填默认 idle_file_report.md`,
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"targetPath": map[string]any{
				"type":        "string",
				"description": "需要扫描的Windows盘符/目录，例如 E:\\",
			},
			"idleDays": map[string]any{
				"type":        "integer",
				"description": "闲置判定阈值，单位天，超过这个天数未访问则判定为闲置文件，默认90",
			},
			"exportMdPath": map[string]any{
				"type":        "string",
				"description": "扫描结果保存为markdown文件的路径，可选。不填默认保存到 idle_file_report.md",
			},
		},
		"required": []string{"targetPath"},
	},
}
