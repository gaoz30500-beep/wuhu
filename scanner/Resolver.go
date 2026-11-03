package scanner

import (
	"errors"
	"time"
)

// IdleResolver 计算文件闲置时长的策略接口。
// 面向对象：抽象出"闲置判定"这一可变行为，Scanner 只依赖该接口，
// 未来想改成按修改时间(mtime)或自定义规则计算，只需新增一个实现，无需改动 Scanner。
type IdleResolver interface {
	Resolve(atime time.Time) (time.Duration, error)
}

// AtimeResolver 基于最后访问时间(atime)计算闲置时长。
// 从 Resource 中拆出来的原 GetIdleDuration 逻辑。
type AtimeResolver struct{}

// Resolve 返回自 atime 以来经过的时间；若 atime 在未来，返回错误
func (AtimeResolver) Resolve(atime time.Time) (time.Duration, error) {
	dur := time.Since(atime)
	if dur > 0 {
		return dur, nil
	}
	return 0, errors.New("atime in future, invalid file access time")
}
