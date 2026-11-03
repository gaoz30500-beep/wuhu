package scanner

import "time"

// Resource 扫描产出的数据模型：只存数据，不包含任何计算逻辑。
// 计算闲置时长等业务规则由 IdleResolver 独立实现，见 resolver.go
type Resource struct {
	Type  string    `json:"type"`
	Size  int64     `json:"size"`
	Path  string    `json:"path"`
	Atime time.Time `json:"lastAccess"`
	Error error     `json:"-"`
}
