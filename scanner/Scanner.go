package scanner

import (
	"io/fs"
	"log"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Options 扫描过滤条件。全部留零值 = 不启用任何过滤，行为与旧版一致。
type Options struct {
	ExcludeDirs []string // 目录名（不含路径，如 node_modules）；命中就整棵子树跳过
	Extensions  []string // 只要这些扩展名（带点，如 .log；大小写不敏感）；留空不过滤
	MinSize     int64    // 小于该字节数的文件不计入；0 不限制
	MaxResults  int      // 最多收集多少条；0 不限制
}

// skipDir 判断某个目录名是否在排除名单里（大小写不敏感，兼容 .git 这类带点目录）
func (o Options) skipDir(name string) bool {
	for _, d := range o.ExcludeDirs {
		if strings.EqualFold(strings.TrimSpace(d), name) {
			return true
		}
	}
	return false
}

// matchExt 扩展名白名单。名单为空 = 全部通过。
func (o Options) matchExt(name string) bool {
	if len(o.Extensions) == 0 {
		return true
	}
	ext := filepath.Ext(name)
	if ext == "" {
		return false
	}
	for _, e := range o.Extensions {
		// 允许调用方写 "log" 或 ".log"，统一补点再比
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		if strings.EqualFold(e, ext) {
			return true
		}
	}
	return false
}

// Scanner 扫描器：只负责遍历磁盘、按闲置阈值筛选文件。
// 通过注入 IdleResolver 策略计算闲置时长，不再自包含业务计算，也不包含任何控制台交互。
type Scanner struct {
	TargetPath    string        // 扫描根路径
	IdleThreshold time.Duration // 闲置判定阈值
	Resolver      IdleResolver  // 闲置时长计算策略（依赖接口，可替换实现）
	Opts          Options       // 过滤条件（排除目录 / 扩展名 / 最小体积 / 条数上限）
	Results       []Resource    // 扫描结果
}

// NewScanner 构造扫描器，默认注入按 atime 计算的闲置策略
func NewScanner(path string, idleDays int64) *Scanner {
	return NewScannerWithOptions(path, idleDays, Options{})
}

// NewScannerWithOptions 带过滤条件的构造入口
func NewScannerWithOptions(path string, idleDays int64, opts Options) *Scanner {
	return &Scanner{
		TargetPath:    path,
		IdleThreshold: time.Duration(idleDays) * 24 * time.Hour,
		Resolver:      AtimeResolver{},
		Opts:          opts,
	}
}

// Scan 遍历目录，收集闲置时长超过阈值的文件
func (s *Scanner) Scan() ([]Resource, error) {
	var idleList []Resource
	err := filepath.WalkDir(s.TargetPath, func(path string, d fs.DirEntry, walkErr error) error {
		// 【第一步】先处理遍历错误：比如权限拒绝，此时d=nil，不能访问d
		if walkErr != nil {
			// 必须走 stderr：本包会被 MCP server（stdio 模式）调用，
			// stdout 是 JSON-RPC 通道，往那里打一行字就会把协议流冲乱
			log.Printf("跳过目录 %s, 原因: %v", path, walkErr)
			return nil // 返回nil，继续遍历，不要return walkErr，否则扫描直接终止
		}
		// d判空，防止空指针
		if d == nil {
			return nil
		}

		// 目录：命中排除名单就整棵子树跳过（node_modules / .git / venv 这类
		// 目录动辄几万个文件，扫进去只会拖慢速度、把真正常用的文件淹掉）
		if d.IsDir() {
			if path != s.TargetPath && s.Opts.skipDir(d.Name()) {
				return fs.SkipDir
			}
			return nil
		}

		// 扩展名 / 体积过滤放在取 atime 之前，省掉不必要的 syscall
		if !s.Opts.matchExt(d.Name()) {
			return nil
		}

		var resp Resource
		info, err := d.Info()
		if err != nil {
			resp.Error = err
			idleList = append(idleList, resp)
			return nil
		}
		if s.Opts.MinSize > 0 && info.Size() < s.Opts.MinSize {
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
			// 条数上限：扫整个盘时结果可能有几十万条，全留在内存里既占内存
			// 又没法给模型看，到量就停（fs.SkipAll 让 WalkDir 正常结束）
			if s.Opts.MaxResults > 0 && len(idleList) >= s.Opts.MaxResults {
				return fs.SkipAll
			}
		}
		return nil
	})
	return idleList, err
}

// ScanIdleFiles 给Agent调用的便捷入口：构造扫描器并执行
func ScanIdleFiles(path string, idleDays int64) ([]Resource, error) {
	return NewScanner(path, idleDays).Scan()
}
