package memory

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// 这两类不是系统故障，而是"这次不值得存"。
// 上层（MCP 工具）应当把它当"正常跳过"返回，不要标成工具失败弹给用户看。
var (
	ErrEmptyContent = errors.New("记忆内容为空，已跳过本次保存")
	ErrNoSemantic   = errors.New("记忆内容去掉虚词后为空，已跳过本次保存")
)

// 写入的两种动作
const (
	ActionAdded  = "added"  // 新增一条
	ActionMerged = "merged" // 合并进已有的一条
)

// WriteResult 一次写入的结果，方便上层打日志、也能用来调试
type WriteResult struct {
	Record  Record  // 落盘后的那条（新增的或合并后的）
	Action  string  // ActionAdded / ActionMerged
	Matched int64   // 合并时命中的原 ID；新增时为 0
	Score   float64 // 合并时的相似度；1 表示指纹精确命中
}

// Write 唯一记忆写入口：先查重，重复就合并，不重复才新增。
// source 取值：manual / tool / rule / distill
func Write(path, content, tags, source string) (WriteResult, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return WriteResult{}, ErrEmptyContent
	}
	if Normalize(content) == "" {
		return WriteResult{}, ErrNoSemantic
	}

	mu.Lock()
	defer mu.Unlock()

	path = resolve(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return WriteResult{}, fmt.Errorf("创建记忆目录失败: %w", err)
	}

	list, err := LoadAll(path)
	if err != nil {
		return WriteResult{}, err
	}

	cleanTags := strings.TrimSpace(tags)
	kind := KindOf(Record{Tags: cleanTags})

	// ① 在 active 记录里找"最像"的一条
	newFinger := Fingerprint(content) // 循环外算一次，别在循环里反复归一化
	bestIdx := -1
	bestScore := 0.0
	for i := range list {
		if list[i].Status != StatusActive {
			continue // 已完成/已丢弃的不参与合并，避免把旧结论复活
		}
		// 指纹精确命中：不管 kind，直接算同一条
		if newFinger == list[i].Finger || newFinger == Fingerprint(list[i].Content) {
			bestIdx, bestScore = i, 1
			break
		}
		// 综合分（覆盖度为主 + Dice 为辅）：一条详细、一条简略说同一件事时，
		// 纯 Dice 会被长度差拖到 0.3 以下，覆盖度能救回来。
		score := DupScore(content, list[i].Content)
		// 像，但类型不同 → 不算同一件事（"怎么做" ≠ "做不到"）
		if score >= similarMerge && KindOf(list[i]) == kind && score > bestScore {
			bestIdx, bestScore = i, score
		}
	}

	now := time.Now().Format("2006-01-02 15:04")

	// ② 命中 → 合并，整份重写
	if bestIdx >= 0 {
		old := list[bestIdx]
		merged := mergeRecord(old, content, cleanTags, source, now)
		list[bestIdx] = merged
		if err := saveAll(path, list); err != nil {
			return WriteResult{}, err
		}
		return WriteResult{
			Record: merged, Action: ActionMerged, Matched: old.ID, Score: bestScore,
		}, nil
	}

	// ③ 没命中 → 新增，只追加一行
	nextID := int64(1)
	for _, r := range list {
		if r.ID >= nextID {
			nextID = r.ID + 1
		}
	}
	r := Record{
		ID:       nextID,
		Time:     now,
		Updated:  now,
		Kind:     kind,
		Content:  content,
		Tags:     cleanTags,
		Source:   source,
		Weight:   DefaultWeight,
		Status:   StatusActive,
		Verified: now[:10],
		Finger:   Fingerprint(content),
	}
	if err := appendLine(path, r); err != nil {
		return WriteResult{}, err
	}
	return WriteResult{Record: r, Action: ActionAdded}, nil
}

// mergeRecord 把新说法并进老记录：
// 内容取信息更全的那句，权重 +1（封顶 5），updated/verified 刷成今天，tags 取并集。
func mergeRecord(old Record, content, tags, source, now string) Record {
	r := old
	r.Updated = now
	r.Verified = now[:10]
	if r.Weight < 5 {
		r.Weight++
	}
	// 新说法字更多 = 信息更全，就换掉；否则保留原文，别越合并越短
	if len([]rune(content)) > len([]rune(r.Content)) {
		r.Content = content
	}
	r.Tags = unionTags(r.Tags, tags)
	r.Finger = Fingerprint(r.Content)
	if r.Source == "" {
		r.Source = source
	}
	if r.Status == "" {
		r.Status = StatusActive
	}
	return r
}

// unionTags 标签并集，逗号分隔、去空、去重、保持顺序
func unionTags(a, b string) string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(a+","+b, ",") {
		t := strings.TrimSpace(part)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return strings.Join(out, ",")
}

// appendLine 往文件末尾追加一行（调用方必须已持有 mu）
func appendLine(path string, r Record) error {
	line, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("序列化记忆失败: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("打开记忆文件写失败: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(string(line) + "\n"); err != nil {
		return fmt.Errorf("写入记忆失败: %w", err)
	}
	return nil
}

// saveAll 整份重写：先写 .tmp，写完再 rename 覆盖原文件。
// 顺序不能反 —— 中途崩了，原文件还是完整的，最多丢这一次合并。
func saveAll(path string, list []Record) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	w := bufio.NewWriter(f)
	for _, r := range list {
		line, err := json.Marshal(r)
		if err != nil {
			f.Close()
			return fmt.Errorf("序列化记忆失败: %w", err)
		}
		if _, err := w.WriteString(string(line) + "\n"); err != nil {
			f.Close()
			return fmt.Errorf("写临时文件失败: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		f.Close()
		return fmt.Errorf("刷新临时文件失败: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("替换记忆文件失败: %w", err)
	}
	return nil
}
