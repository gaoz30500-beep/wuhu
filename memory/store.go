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

// DefaultPath 默认记忆文件路径（相对项目根目录）。
// 必须和 mcp_config.json 里 save_memory / recall_memory 的 storePath 保持一致！
const DefaultPath = "memory/memory.jsonl"

// Record 一条长期记忆
type Record struct {
	ID      int64  `json:"id"`
	Time    string `json:"time"`           // 2026-11-07 21:30
	Content string `json:"content"`        // 记忆正文，一句话且自包含（写清主语）
	Tags    string `json:"tags,omitempty"` // 逗号分隔标签，用于检索
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
	nextID := int64(1)
	if len(list) > 0 {
		nextID = list[len(list)-1].ID + 1
	}

	r := Record{
		ID:      nextID,
		Time:    time.Now().Format("2006-01-02 15:04"),
		Content: content,
		Tags:    strings.TrimSpace(tags),
	}
	line, err := json.Marshal(r)
	if err != nil {
		return Record{}, fmt.Errorf("序列化记忆失败: %w", err)
	}

	// O_APPEND：只往后加，绝不覆写已有内容
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Record{}, fmt.Errorf("打开记忆文件写失败: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(string(line) + "\n"); err != nil {
		return Record{}, fmt.Errorf("写入记忆失败: %w", err)
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
