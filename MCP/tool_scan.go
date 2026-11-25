package MCP

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"agent/output"
	"agent/scanner"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ScanConfig 是 mcp_config.json 里 tools.scan_idle_file 节点，
// 只作为"默认值来源"，handler 只读不改。
type ScanConfig struct {
	TargetPath   string   `json:"targetPath,omitempty"`
	IdleDays     int64    `json:"idleDays,omitempty"`
	ExportMdPath string   `json:"exportMdPath,omitempty"`
	ExcludeDirs  []string `json:"excludeDirs,omitempty"`
	Extensions   []string `json:"extensions,omitempty"`
	MinSizeMB    int64    `json:"minSizeMB,omitempty"`
	MaxResults   int      `json:"maxResults,omitempty"`
}

// ScanIn 就是本工具的入参声明：字段名 = JSON key，标签 = 参数说明，
// 不带 omitempty 的字段会被自动标记为 required。
// 注意：标签里不能出现反斜杠，StructTag 会对它做转义解析。
type ScanIn struct {
	TargetPath   string `json:"targetPath,omitempty" jsonschema:"要扫描的目录，例如 E: 或 E: 下的某个子目录；不填则用 mcp_config.json 里的默认目录"`
	IdleDays     int64  `json:"idleDays,omitempty" jsonschema:"闲置判定阈值，单位天：超过这个天数没有访问即算闲置，不填默认 90"`
	ExportMdPath string `json:"exportMdPath,omitempty" jsonschema:"可选，Markdown 报告的保存路径；不填则用配置里的值，配置也没有就不导出文件"`
	Extensions   string `json:"extensions,omitempty" jsonschema:"可选，只统计这些扩展名，逗号分隔，例如 log,tmp,zip；不填表示不限扩展名"`
	ExcludeDirs  string `json:"excludeDirs,omitempty" jsonschema:"可选，跳过的目录名，逗号分隔，例如 node_modules,.git,venv；不填用默认排除名单"`
	MinSizeMB    int64  `json:"minSizeMB,omitempty" jsonschema:"可选，只统计不小于这么多 MB 的文件，用来优先找大文件；不填为 0 表示不限"`
	MaxResults   int64  `json:"maxResults,omitempty" jsonschema:"可选，最多收集多少条结果，防止扫整盘时结果爆表；不填为 0 表示不限"`
}

// defaultExcludeDirs 默认排除的目录名。
// 这些目录里的文件基本不会被"手动访问"，atime 天然很老，混进结果里全是噪音。
var defaultExcludeDirs = []string{
	"node_modules", ".git", ".idea", ".venv", "venv", "__pycache__",
	".gradle", ".m2", ".cache", "dist", "build", "target",
}

// splitList 把逗号/分号/空格分隔的字符串切成切片，去空去重
func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || r == '，' || r == ';' || r == '；' || r == ' ' || r == '\t'
	})
	seen := make(map[string]bool, len(fields))
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" || seen[strings.ToLower(f)] {
			continue
		}
		seen[strings.ToLower(f)] = true
		out = append(out, f)
	}
	return out
}

// humanSize 把字节数转成人类可读，报告里给模型看比一堆数字直观
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	val := float64(n)
	for _, u := range units {
		val /= unit
		if val < unit {
			return fmt.Sprintf("%.1f %s", val, u)
		}
	}
	return fmt.Sprintf("%.1f PB", val)
}

// registerScanTool 注册闲置文件扫描工具
func registerScanTool(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name: "scan_idle_file",
		Description: "扫描本机指定目录，找出超过阈值天数没有访问（atime）的闲置文件；" +
			"支持按扩展名、最小体积过滤，并自动跳过 node_modules/.git/venv 等目录。" +
			"返回匹配数量、可释放空间总量和最大的几个文件，明细可导出为 Markdown 报告。",
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

	// 过滤条件：入了参就用参，没入参用配置，配置也没有就用默认排除名单
	excludeDirs := splitList(in.ExcludeDirs)
	if len(excludeDirs) == 0 {
		excludeDirs = conf.ExcludeDirs
	}
	if len(excludeDirs) == 0 {
		excludeDirs = defaultExcludeDirs
	}

	exts := splitList(in.Extensions)
	if len(exts) == 0 {
		exts = conf.Extensions
	}

	minSizeMB := in.MinSizeMB
	if minSizeMB <= 0 {
		minSizeMB = conf.MinSizeMB
	}

	maxResults := int(in.MaxResults)
	if maxResults <= 0 {
		maxResults = conf.MaxResults
	}
	// 默认给个上限：整盘扫描结果很容易几十万条，全导出成 md 也没人看得完
	if maxResults <= 0 {
		maxResults = 5000
	}

	opts := scanner.Options{
		ExcludeDirs: excludeDirs,
		Extensions:  exts,
		MinSize:     minSizeMB * 1024 * 1024,
		MaxResults:  maxResults,
	}

	// 1. 扫描
	resList, err := scanner.NewScannerWithOptions(targetPath, idleDays, opts).Scan()
	if err != nil {
		return nil, nil, fmt.Errorf("扫描 %s 失败: %w", targetPath, err)
	}

	// 2. 汇总：总可释放空间 + 最大的几个文件（模型据此决定要不要真删）
	var totalSize int64
	items := make([]output.ReportItem, 0, len(resList))
	okList := make([]scanner.Resource, 0, len(resList))
	for _, r := range resList {
		if r.Error != nil || r.Path == "" {
			continue
		}
		totalSize += r.Size
		okList = append(okList, r)
		items = append(items, output.ReportItem{
			Path:    r.Path,
			Size:    r.Size,
			IdleDur: time.Since(r.Atime).String(),
		})
	}

	// 3. 导出 Markdown（路径里有目录就先建，否则 os.Create 会失败）
	if exportMdPath != "" && len(items) > 0 {
		if dir := filepath.Dir(exportMdPath); dir != "" && dir != "." {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return nil, nil, fmt.Errorf("创建报告目录失败: %w", err)
			}
		}
		if err := output.ExportToMarkdown(exportMdPath, items); err != nil {
			return nil, nil, fmt.Errorf("扫描成功，但导出 Markdown 失败: %w", err)
		}
	}

	// 4. 回给模型的是简短摘要，明细在报告文件里，不往上下文里灌几千行路径
	var sb strings.Builder
	fmt.Fprintf(&sb, "扫描完成\n路径：%s\n闲置阈值：%d 天\n匹配文件数量：%d\n可释放空间合计：%s",
		targetPath, idleDays, len(items), humanSize(totalSize))
	if len(excludeDirs) > 0 {
		fmt.Fprintf(&sb, "\n已跳过目录：%s", strings.Join(excludeDirs, ","))
	}
	if len(exts) > 0 {
		fmt.Fprintf(&sb, "\n扩展名限制：%s", strings.Join(exts, ","))
	}
	if minSizeMB > 0 {
		fmt.Fprintf(&sb, "\n最小体积：%d MB", minSizeMB)
	}
	if len(items) >= maxResults && maxResults > 0 {
		fmt.Fprintf(&sb, "\n提示：结果已达到上限 %d 条，可能有更多未列出；可加 extensions 或 minSizeMB 缩小范围", maxResults)
	}

	// Top 5 大文件：这是最想知道的信息，直接给出来省一次读报告
	if len(okList) > 0 {
		top := make([]scanner.Resource, len(okList))
		copy(top, okList)
		sort.Slice(top, func(i, j int) bool { return top[i].Size > top[j].Size })
		if len(top) > 5 {
			top = top[:5]
		}
		sb.WriteString("\n最大的几个闲置文件：")
		for _, r := range top {
			fmt.Fprintf(&sb, "\n- %s（%s，闲置 %d 天）", r.Path, humanSize(r.Size),
				int(time.Since(r.Atime).Hours()/24))
		}
	}

	if exportMdPath != "" && len(items) > 0 {
		fmt.Fprintf(&sb, "\n报告输出：%s", exportMdPath)
	}

	return textResult(sb.String()), nil, nil
}
