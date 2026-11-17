package memory

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// CompactStats 一次压缩的战报，方便打日志
type CompactStats struct {
	Before  int // 处理前条数
	After   int // 处理后条数
	Merged  int // 合并掉的条数
	Removed int // 丢弃/淘汰掉的条数
}

// Rewrite 整份重写记忆库。这是唯一能"改动已有行"的底层操作，Write/Update/Delete/Compact 都靠它。
// 注意：必须先 LoadAll 再改再 Rewrite，不能"边读边写"。
func Rewrite(path string, list []Record) error {
	mu.Lock()
	defer mu.Unlock()
	return saveAll(resolve(path), list)
}

// Update 按 ID 改一条：fn 拿到指针，你想改什么改什么（内容、标签、权重、状态）。
// 函数本身负责刷新 Updated、重算 Finger、把权重夹在 1~5。
func Update(path string, id int64, fn func(*Record)) (Record, error) {
	mu.Lock()
	defer mu.Unlock()

	path = resolve(path)
	list, err := LoadAll(path)
	if err != nil {
		return Record{}, err
	}
	idx := -1
	for i := range list {
		if list[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return Record{}, fmt.Errorf("库里没有 id=%d 这条记忆", id)
	}

	old := list[idx]
	fn(&list[idx])
	r := list[idx]

	// 正文被清空是灾难：这条记忆就变成空壳，检索时永远命中不到。
	// 真想删就走 Delete，不靠"把正文改成空"。
	if strings.TrimSpace(r.Content) == "" {
		return Record{}, fmt.Errorf("id=%d 正文不能为空；要删请用 Delete", id)
	}

	// 这几项不允许 fn 乱动：ID 是身份，Time 是"首次写入"，动了就说不清历史
	r.ID = old.ID
	r.Time = old.Time
	r.Updated = time.Now().Format("2006-01-02 15:04")
	if r.Content != old.Content {
		r.Finger = Fingerprint(r.Content)          // 正文变了，指纹必须重算
		r.Verified = time.Now().Format("2006-01-02") // 内容换了，原来的验证日期就不再代表它
	}
	if r.Status == "" {
		r.Status = StatusActive
	}
	if r.Weight <= 0 {
		r.Weight = DefaultWeight
	}
	if r.Weight > 5 {
		r.Weight = 5
	}

	list[idx] = r
	if err := saveAll(path, list); err != nil {
		return Record{}, err
	}
	return r, nil
}

// Delete 按 ID 删若干条，返回真的删掉了几条。找不到的 ID 直接忽略，不报错。
func Delete(path string, ids ...int64) (int, error) {
	mu.Lock()
	defer mu.Unlock()

	path = resolve(path)
	list, err := LoadAll(path)
	if err != nil {
		return 0, err
	}

	keep := make([]Record, 0, len(list))
	removed := 0
	for _, r := range list {
		if containsID(ids, r.ID) {
			removed++
			continue
		}
		keep = append(keep, r)
	}
	if removed == 0 {
		return 0, nil // 一条没动就别重写文件，省一次 IO
	}
	if err := saveAll(path, keep); err != nil {
		return 0, err
	}
	return removed, nil
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// Compact 一次做完三件事：丢 dropped → 两两合并重复 → 超过上限按分数淘汰。
// maxRecords <= 0 时用 200。
func Compact(path string, maxRecords int) (CompactStats, error) {
	mu.Lock()
	defer mu.Unlock()

	path = resolve(path)
	list, err := LoadAll(path)
	if err != nil {
		return CompactStats{}, err
	}
	if maxRecords <= 0 {
		maxRecords = 200
	}

	st := CompactStats{Before: len(list)}

	// ① 丢掉已丢弃的
	keep := make([]Record, 0, len(list))
	for _, r := range list {
		if r.Status == StatusDropped {
			st.Removed++
			continue
		}
		keep = append(keep, r)
	}

	// ② 两两合并重复（只合 active；kind 必须相同，或者指纹完全一样）
	out := make([]Record, 0, len(keep))
	now := time.Now().Format("2006-01-02 15:04")
	for _, r := range keep {
		hit := -1
		for i := range out {
			if out[i].Status != StatusActive || r.Status != StatusActive {
				continue
			}
			if Fingerprint(r.Content) == Fingerprint(out[i].Content) ||
				(KindOf(r) == KindOf(out[i]) && DupScore(r.Content, out[i].Content) >= similarMerge) {
				hit = i
				break
			}
		}
		if hit >= 0 {
			out[hit] = mergeRecord(out[hit], r.Content, r.Tags, r.Source, now)
			st.Merged++
			continue
		}
		out = append(out, r)
	}

	// ③ 超过上限：按分数排序，preference 一律保命
	// 原来的写法有个洞：preference 排在后半段时会被整段保留，结果条数可能远超上限。
	// 改成"先扣名额给 preference，剩下的名额按分数发"。
	if len(out) > maxRecords {
		sort.SliceStable(out, func(i, j int) bool {
			return evictScore(out[i]) > evictScore(out[j])
		})

		kept := make([]Record, 0, len(out))
		room := maxRecords
		// 先无条件留下所有 preference（画像类），它们占名额
		for _, r := range out {
			if KindOf(r) == KindPreference {
				kept = append(kept, r)
				room--
			}
		}
		// 剩下的名额，按分数从高到低发
		for _, r := range out {
			if KindOf(r) == KindPreference {
				continue
			}
			if room <= 0 {
				st.Removed++
				continue
			}
			kept = append(kept, r)
			room--
		}
		// 重排一次，保证文件里的顺序也是"重要的在前"
		sort.SliceStable(kept, func(i, j int) bool {
			return evictScore(kept[i]) > evictScore(kept[j])
		})
		out = kept
	}

	// ID 重排成连续，方便人工核对（历史 ID 不保留，反正没人依赖它）
	for i := range out {
		out[i].ID = int64(i + 1)
	}

	st.After = len(out)
	if err := saveAll(path, out); err != nil {
		return CompactStats{}, err
	}
	return st, nil
}

// Maintain 一次"常驻维护"：压缩事实库（去重 + 淘汰过期），再裁剪情节库。
//
// 为什么是这两个动作：
//   - 事实库越用越可能攒重复，合并能让权重变成真实信号；
//   - 情节库只用来接上下文，留最近 300 条足够，再多纯属占磁盘。
//
// 调用方式是 go Maintain(...)：随时可调用、失败不影响主流程。
// 注意它不调模型 —— 巩固不做二次 LLM 提炼，避免"每轮都多一次请求"。
func Maintain(factPath, epPath string, maxRecords, maxEpisodes int) (CompactStats, int, error) {
	st, err := Compact(factPath, maxRecords)
	if err != nil {
		return CompactStats{}, 0, err
	}
	removed, err := PruneEpisodes(epPath, maxEpisodes)
	if err != nil {
		return st, 0, err
	}
	return st, removed, nil
}

// evictScore 淘汰打分：权重为主，新鲜度为辅。
// 30 天内验证过的加分，半年前没碰过的不加分 —— 这就是"遗忘"的最小模型。
func evictScore(r Record) float64 {
	base := float64(r.Weight) * 10
	days := daysSince(r.Verified)
	switch {
	case days <= 7:
		base += 6
	case days <= 30:
		base += 3
	}
	return base
}

// daysSince 从 "2006-01-02" 或 "2006-01-02 15:04" 算出距今多少天；解析失败就当很久以前
func daysSince(s string) int {
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02"} {
		t, err := time.ParseInLocation(layout, s, time.Local)
		if err == nil {
			return int(time.Since(t).Hours() / 24)
		}
	}
	return 9999
}
