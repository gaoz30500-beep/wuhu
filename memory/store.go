// Package memory 长期记忆的本地存储：append-only JSONL，一行一条。
// 只负责「增 / 查 / 渲染」，不关心谁调用它（MCP 工具、TUI、压缩流程都可以用）。
package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	KindFact       = "fact"       // 身份、设备、路径、账号
	KindPreference = "preference" // 称呼、习惯、喜好
	KindTask       = "task"       // 未完成的待办
	KindProject    = "project"    // 项目：路径/结构/状态
	KindRule       = "rule"       // 已验证的做法/规程
	KindEnv        = "env"        // 环境限制

	StatusActive  = "active"
	StatusDone    = "done"
	StatusDropped = "dropped"
	DefaultPath   = "memory/memory.jsonl"
	DefaultWeight = 3
)

// Record 一条长期记忆
type Record struct {
	ID       int64  `json:"id"`
	Time     string `json:"time"`               // 首次写入 2006-01-02 15:04
	Updated  string `json:"updated,omitempty"`  // 最后一次合并/修改
	Kind     string `json:"kind,omitempty"`     // fact/preference/task/project/rule/env  分清类别，方便检索
	Content  string `json:"content"`            // 记忆正文
	Tags     string `json:"tags,omitempty"`     // 逗号分隔标签
	Source   string `json:"source,omitempty"`   // manual/tool/rule/distill 从哪里来的信息
	Weight   int    `json:"weight,omitempty"`   // 1~5，默认 3 按时间排优先级
	Status   string `json:"status,omitempty"`   // active/done/dropped 标记完成状态
	Verified string `json:"verified,omitempty"` // 最后一次验证日期
	Finger   string `json:"finger,omitempty"`   // 判重指纹
}

// mu 进程内互斥锁：防止同一进程里两次写入（工具调用 / 规则兜底）同时动文件
var mu sync.Mutex

// resolve 空路径兜底成 DefaultPath
func resolve(path string) string {
	if strings.TrimSpace(path) == "" {
		return DefaultPath
	}
	return path
}

// KindOf 取记录类型；老数据没有 kind 字段，就从 tags 的第一个词推断。
// 这里用 switch 而不是 map，是为了以后一眼看得懂、改得动。
func KindOf(r Record) string {
	if r.Kind != "" {
		return r.Kind
	}
	tags := strings.Split(r.Tags, ",")
	if len(tags) == 0 {
		return KindFact
	}
	switch strings.TrimSpace(tags[0]) {
	case "偏好":
		return KindPreference
	case "事实":
		return KindFact
	case "待办":
		return KindTask
	case "项目":
		return KindProject
	case "规程":
		return KindRule
	case "环境":
		return KindEnv
	default:
		return KindFact
	}
}

// LoadAll 读出全部记忆。
// 文件不存在 → 返回 nil, nil（首次运行时的正常情况，不是错误）
// 坏行直接跳过：宁可丢一行，也不要因为一行写坏导致整份记忆读不出来
func LoadAll(path string) ([]Record, error) {
	path = resolve(path)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("打开记忆文件失败: %w", err)
	}
	defer f.Close()

	var list []Record
	sc := bufio.NewScanner(f)
	// 单行上限放到 4MB：默认 64KB 会把长记忆截断成坏行
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r Record
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue // 坏行跳过
		}
		list = append(list, r)
	}

	// 只在内存里补，不写盘；真正落盘发生在 Write/Update 的时候。
	for i := range list {
		if list[i].Kind == "" {
			list[i].Kind = KindOf(list[i])
		}
		if list[i].Weight == 0 {
			list[i].Weight = DefaultWeight
		}
		if list[i].Status == "" {
			list[i].Status = StatusActive
		}
		if list[i].Verified == "" && len(list[i].Time) >= 10 {
			list[i].Verified = list[i].Time[:10]
		}
		if list[i].Finger == "" {
			list[i].Finger = Fingerprint(list[i].Content)
		}

	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取记忆文件失败: %w", err)
	}
	return list, nil
}

// Append 追加一条记忆，返回落盘后的 Record（ID 自增）
func Append(path, content, tags string) (Record, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return Record{}, fmt.Errorf("记忆内容不能为空")
	}
	mu.Lock()
	defer mu.Unlock()

	path = resolve(path)
	// 目录不存在就先建，否则 OpenFile 会报 no such file or directory
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return Record{}, fmt.Errorf("创建记忆目录失败: %w", err)
	}

	// 先读一遍拿下一个 ID（自增 ID 只为显示和人工核对，不影响功能）
	list, err := LoadAll(path)
	if err != nil {
		return Record{}, err
	}
	// 取"最大 ID + 1"，不能用最后一条 +1：中间删过条目后 ID 会有空洞，末尾那条可能不是最大的
	nextID := int64(1)
	for _, r := range list {
		if r.ID >= nextID {
			nextID = r.ID + 1
		}
	}

	cleanTags := strings.TrimSpace(tags)
	// 先把 tags 包成一条临时 Record 再取 Kind，复用 KindOf，避免另写一份映射
	kind := KindOf(Record{Tags: cleanTags})

	r := Record{
		ID:       nextID,
		Time:     time.Now().Format("2006-01-02 15:04"),
		Updated:  time.Now().Format("2006-01-02 15:04"),
		Kind:     kind,
		Content:  content,
		Tags:     cleanTags,
		Source:   "legacy",
		Finger:   Fingerprint(content),
		Weight:   DefaultWeight,
		Status:   StatusActive,
		Verified: time.Now().Format("2006-01-02"),
	}
	// 复用 write.go 里的 appendLine，避免两处各写一份追加逻辑
	if err := appendLine(path, r); err != nil {
		return Record{}, err
	}
	return r, nil
}

// Search 关键词检索（子串匹配，从新到旧）。keyword 为空 = 取最近 limit 条
func Search(path, keyword string, limit int) ([]Record, error) {
	if limit <= 0 {
		limit = 5
	}
	list, err := LoadAll(path)
	if err != nil {
		return nil, err
	}
	kw := strings.ToLower(strings.TrimSpace(keyword))
	var hits []Record
	for i := len(list) - 1; i >= 0; i-- {
		r := list[i]
		text := strings.ToLower(r.Content + " " + r.Tags)
		if kw == "" || strings.Contains(text, kw) {
			hits = append(hits, r)
			if len(hits) >= limit {
				break
			}
		}
	}
	return hits, nil
}

// Render 取最近 n 条，拼成给 system prompt 用的一段纯文本
func Render(path string, n int) string {
	list, err := LoadAll(path)
	if err != nil || len(list) == 0 {
		return ""
	}
	if n <= 0 || n > len(list) {
		n = len(list)
	}
	var sb strings.Builder
	for _, r := range list[len(list)-n:] {
		sb.WriteString("- ")
		sb.WriteString(r.Content)
		if r.Tags != "" {
			sb.WriteString("（" + r.Tags + "）")
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}
