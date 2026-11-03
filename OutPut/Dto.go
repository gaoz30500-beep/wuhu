package output

// ReportItem 输出层的数据模型（DTO）。
// 与 scanner.Resource 解耦：output 不依赖具体的扫描模型，
// 只接收已经格式化好的报告条目，未来可自由扩展 md/json/csv 等导出格式。
type ReportItem struct {
	Path    string // 文件路径
	Size    int64  // 文件大小（字节）
	IdleDur string // 已闲置时长（格式化后的字符串）
}
