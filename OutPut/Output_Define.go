package output

import (
	"os"
	"text/template"
	"time"
)

// ExportToMarkdown 将报告条目(DTO)保存为markdown文件
// filePath: 输出md的保存路径，例如 idle_files.md
// items: 已由编排层转换好的 ReportItem 列表
func ExportToMarkdown(filePath string, items []ReportItem) error {
	// markdown模板
	tplText := `# 闲置文件扫描报告
扫描时间：{{.ScanTime}}
总共找到 **{{.TotalCount}}** 个闲置文件

| 文件路径 | 文件大小(字节) | 已闲置时长 |
| ---- | ---- | ---- |
{{range .Items}}| {{.Path}} | {{.Size}} | {{.IdleDur}} |
{{end}}
`
	type reportData struct {
		ScanTime   string
		TotalCount int
		Items      []ReportItem
	}

	data := reportData{
		ScanTime:   time.Now().Format("2006-01-02 15:04:05"),
		TotalCount: len(items),
		Items:      items,
	}

	tpl, err := template.New("mdreport").Parse(tplText)
	if err != nil {
		return err
	}

	f, err := os.Create(filePath)
	if err != nil {
		return err
	}
	defer f.Close()

	return tpl.Execute(f, data)
}
