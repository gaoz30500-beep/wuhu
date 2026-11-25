package llm

import (
	"agent/output"
	"agent/scanner"
	"agent/sortresult"
	"encoding/json"
	"fmt"
	"time"
)

// ExecScanIdleFile 当大模型触发function call时执行。
// 职责：编排各层——校验路径、扫描、排序、转换DTO、导出报告。
func ExecScanIdleFile(rawArgs string) (string, error) {
	var args ScanFileArgs
	err := json.Unmarshal([]byte(rawArgs), &args)
	if err != nil {
		return "", err
	}

	// 闲置天数默认90
	if args.IdleDays <= 0 {
		args.IdleDays = 90
	}
	targetPath := args.TargetPath

	// 1. 路径校验（scanner层）
	if err := scanner.ValidateDirPath(targetPath); err != nil {
		return "", err
	}

	// 2. 扫描（scanner层，内部已注入闲置策略）
	fileList, err := scanner.ScanIdleFiles(targetPath, args.IdleDays)
	if err != nil {
		return "", err
	}

	// 3. 按闲置时长排序（sortresult层）
	sortresult.SortByAtimeAsc(fileList)

	// 4. 转换 Resource → ReportItem（解耦：output 不依赖 scanner.Resource）
	items := make([]output.ReportItem, 0, len(fileList))
	for _, r := range fileList {
		items = append(items, output.ReportItem{
			Path:    r.Path,
			Size:    r.Size,
			IdleDur: time.Since(r.Atime).String(),
		})
	}

	// 5. 导出markdown（output层，只依赖DTO）
	mdPath := "idle_file_report.md"
	if args.ExportMdPath != "" {
		mdPath = args.ExportMdPath
	}
	if err := output.ExportToMarkdown(mdPath, items); err != nil {
		return "", fmt.Errorf("扫描成功，但导出markdown失败: %w", err)
	}

	// 6. 序列化原始扫描结果返回给LLM（Resource 已是纯数据，可正常序列化）
	resultJson, err := json.MarshalIndent(fileList, "", "  ")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("扫描完成，markdown报告已保存到 %s\n扫描结果:\n%s", mdPath, string(resultJson)), nil
}
