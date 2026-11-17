package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultEpisodePath 情节记忆单独放一个文件。
// 它和事实记忆的生命周期完全不同：情节只管"最近发生过什么"，用完就过期；
// 事实要长期沉淀、要去重、要被淘汰。混在一个文件里，Compact 会把情节当事实删掉。
const DefaultEpisodePath = "memory/episodes.jsonl"

// Episode 一条情节：某一轮对话里发生了什么，不是结论。
// 这里刻意不存记忆 ID —— Compact 会重排 ID，存了就指错行。
type Episode struct {
	ID   int64  `json:"id"`
	Time string `json:"time"`           // 2006-01-02 15:04:05
	Role string `json:"role,omitempty"` // user / assistant
	Text string `json:"text"`           // 原文（已截断）
	Tags string `json:"tags,omitempty"`
}

// episodePath 空路径兜底
func episodePath(path string) string {
	if strings.TrimSpace(path) == "" {
		return DefaultEpisodePath
	}
	return path
}

// AppendEpisode 追加一条情节。
// 情节不做判重：同一句话今天说两遍，在"发生过什么"这件事上确实是两件事。
func AppendEpisode(path, role, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	mu.Lock()
	defer mu.Unlock()

	path = episodePath(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建情节目录失败: %w", err)
	}

	list, err := LoadEpisodes(path, 0)
	if err != nil {
		return err
	}
	nextID := int64(1)
	for _, e := range list {
		if e.ID >= nextID {
			nextID = e.ID + 1
		}
	}

	ep := Episode{
		ID:   nextID,
		Time: time.Now().Format("2006-01-02 15:04:05"),
		Role: role,
		Text: text,
	}
	line, err := json.Marshal(ep)
	if err != nil {
		return fmt.Errorf("序列化情节失败: %w", err)
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("打开情节文件失败: %w", err)
	}
	defer f.Close()
	if _, err := f.WriteString(string(line) + "\n"); err != nil {
		return fmt.Errorf("写入情节失败: %w", err)
	}
	return nil
}

// LoadEpisodes 读情节。limit > 0 时只返回最近 limit 条（顺序仍是旧→新）。
func LoadEpisodes(path string, limit int) ([]Episode, error) {
	path = episodePath(path)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("打开情节文件失败: %w", err)
	}
	defer f.Close()

	var list []Episode
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e Episode
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // 坏行跳过，同事实库的策略
		}
		list = append(list, e)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("读取情节文件失败: %w", err)
	}
	if limit > 0 && len(list) > limit {
		list = list[len(list)-limit:]
	}
	return list, nil
}

// sessionGap 相邻两条情节间隔超过这个时长，就认为换了一次会话。
// 30 分钟是拍定的：一次连续对话里不会沉默半小时，跨天/跨时段必然断开。
const sessionGap = 30 * time.Minute

// RenderEpisodes 拼成给 prompt 用的一段"刚才聊了什么"。
// 只输出 user/assistant 的正文，不带 ID 和时间，省字符。
// 顺序是旧→新：注入内容要像一段对话记录，倒着放会让模型把因果搞反。
// 跨会话的地方插一行分隔线 —— 否则模型会把昨天的话题当成眼下的上下文。
func RenderEpisodes(path string, limit int) string {
	if limit <= 0 {
		limit = 10
	}
	list, err := LoadEpisodes(path, limit)
	if err != nil || len(list) == 0 {
		return ""
	}
	var sb strings.Builder
	for i, e := range list {
		if i > 0 {
			// 解析失败就当同一会话，宁可不插线，也不要凭空造出一个分界
			if gap, ok := episodeGap(list[i-1].Time, e.Time); ok && gap > sessionGap {
				sb.WriteString("─── 新会话 ───\n")
			}
		}
		role := e.Role
		if role == "" {
			role = "user"
		}
		sb.WriteString(role + ": " + e.Text + "\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// episodeGap 两条情节之间的时间间隔。时间格式解析失败返回 ok=false。
func episodeGap(a, b string) (time.Duration, bool) {
	ta, err := time.ParseInLocation("2006-01-02 15:04:05", a, time.Local)
	if err != nil {
		return 0, false
	}
	tb, err := time.ParseInLocation("2006-01-02 15:04:05", b, time.Local)
	if err != nil {
		return 0, false
	}
	return tb.Sub(ta), true
}

// PruneEpisodes 只保留最近 keep 条（整份重写）。
// 情节是会无限增长的，启动或退出时清一次就够了，不用每轮都清。
func PruneEpisodes(path string, keep int) (int, error) {
	mu.Lock()
	defer mu.Unlock()

	path = episodePath(path)
	list, err := LoadEpisodes(path, 0)
	if err != nil {
		return 0, err
	}
	if keep <= 0 {
		keep = 200
	}
	if len(list) <= keep {
		return 0, nil
	}
	removed := len(list) - keep
	if err := saveAllEpisodes(path, list[len(list)-keep:]); err != nil {
		return 0, err
	}
	return removed, nil
}

// saveAllEpisodes 整份重写情节文件，套路和 saveAll 一样：先 .tmp 再 rename
func saveAllEpisodes(path string, list []Episode) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	w := bufio.NewWriter(f)
	for _, e := range list {
		line, err := json.Marshal(e)
		if err != nil {
			f.Close()
			return fmt.Errorf("序列化情节失败: %w", err)
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
		return fmt.Errorf("替换情节文件失败: %w", err)
	}
	return nil
}
