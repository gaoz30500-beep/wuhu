package scanner

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"syscall"
	"time"
)

// Scanner 扫描器：只负责遍历磁盘、按闲置阈值筛选文件。
// 通过注入 IdleResolver 策略计算闲置时长，不再自包含业务计算，也不包含任何控制台交互。
type Scanner struct {
	TargetPath    string       // 扫描根路径
	IdleThreshold time.Duration // 闲置判定阈值
	Resolver      IdleResolver // 闲置时长计算策略（依赖接口，可替换实现）
	Results       []Resource   // 扫描结果
}

// NewScanner 构造扫描器，默认注入按 atime 计算的闲置策略
func NewScanner(path string, idleDays int64) *Scanner {
	return &Scanner{
		TargetPath:    path,
		IdleThreshold: time.Duration(idleDays) * 24 * time.Hour,
		Resolver:      AtimeResolver{},
	}
}

// Scan 遍历目录，收集闲置时长超过阈值的文件
func (s *Scanner) Scan() ([]Resource, error) {
	var idleList []Resource
	err := filepath.WalkDir(s.TargetPath, func(path string, d fs.DirEntry, walkErr error) error {
		// 【第一步】先处理遍历错误：比如权限拒绝，此时d=nil，不能访问d
		if walkErr != nil {
			fmt.Printf("跳过目录 %s, 原因: %v\n", path, walkErr)
			return nil // 返回nil，继续遍历，不要return walkErr，否则扫描直接终止
		}
		// d判空，防止空指针
		if d == nil {
			return nil
		}

		var resp Resource
		info, err := d.Info()
		if err != nil {
			resp.Error = err
			idleList = append(idleList, resp)
			return nil
		}

		winSys, ok := info.Sys().(*syscall.Win32FileAttributeData)
		if !ok {
			return nil
		}
		atime := time.Unix(0, winSys.LastAccessTime.Nanoseconds())
		resp.Path = path
		resp.Size = info.Size()
		resp.Atime = atime

		// 用注入的策略计算闲置时长（原 GetIdleDuration 已移入 AtimeResolver）
		dur, err := s.Resolver.Resolve(atime)
		if err != nil {
			resp.Error = err
			idleList = append(idleList, resp)
			return nil
		}
		// 闲置时长超过阈值才加入结果
		if dur >= s.IdleThreshold {
			idleList = append(idleList, resp)
		}
		return nil
	})
	return idleList, err
}

// ScanIdleFiles 给Agent调用的便捷入口：构造扫描器并执行
func ScanIdleFiles(path string, idleDays int64) ([]Resource, error) {
	return NewScanner(path, idleDays).Scan()
}
