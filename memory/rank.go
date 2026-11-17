package memory

import (
	"math"
	"sort"
	"strings"
)

// 三槽条数上限：画像 / 待办 / 相关
//
// slotProfile 从 8 收到 3：画像槽是"每一轮都带"的，不是"能带多少带多少"。
// 8 条的后果实测过 —— 随便存一条偏好（比如"习惯用 VS Code"）就会黏在每次 prompt 里，
// 问"我叫什么名字"也跟着出现，把真正该说的那句稀释掉。
// 剩下的偏好不丢：走相关槽（问到了才出现）或 recall_memory 主动查。
const (
	slotProfile = 3
	slotTask    = 4
	slotRelated = 5
)

// prompt 里长期记忆的总预算（字符）。超了就从后往前砍，宁可少放也不糊一堆。
const promptBudgetChars = 600

// slotMinRelevance 相关槽的最低相关度门槛。
// 不加这道门槛，任何一条记忆都会因为"新鲜度 + 权重"的底分（0.3+0.2）挤进来，
// 等于又回到"全塞"——库小的时候看不出来，库一涨就是灾难。
const slotMinRelevance = 0.10

// 新鲜度半衰期（天）：30 天没碰过的记忆，新鲜度掉一半。
// 这就是"遗忘曲线"的最小实现，不需要真去算指数衰减以外的东西。
const freshHalfLife = 30.0

// lastTouch 取"最后一次被碰过"的时间：优先 Updated，其次 Verified，最后 Time
func lastTouch(r Record) string {
	if r.Updated != "" {
		return r.Updated
	}
	if r.Verified != "" {
		return r.Verified
	}
	return r.Time
}

// Freshness 新鲜度 0~1。刚碰过 = 1，半年前 = 接近 0。
func Freshness(r Record) float64 {
	days := float64(daysSince(lastTouch(r)))
	if days < 0 {
		days = 0 // 系统时间被改到未来时别算出 >1 的分
	}
	return math.Pow(0.5, days/freshHalfLife)
}

// Relevance 相关度 0~1：当前这句话和这条记忆有多沾边。
//
// 关键点：短问句对长记忆不能用 Dice（分母被长文本撑大，分数会全部趋近 0），
// 改用"覆盖度"——查询里的二元组有多少比例出现在这条记忆里。
// 覆盖度 = "提到没提到"，Dice = "整体有多像"，两者加权。
func Relevance(r Record, query string) float64 {
	q := strings.TrimSpace(query)
	if q == "" {
		return 0
	}
	text := r.Content
	if r.Tags != "" {
		text += " " + strings.ReplaceAll(r.Tags, ",", " ")
	}

	nq := Normalize(q)
	nt := Normalize(text)
	if nq == "" || nt == "" {
		return 0
	}

	// 一两个字的问句没有二元组，直接看整串包含
	if len([]rune(nq)) <= 2 {
		if strings.Contains(nt, nq) {
			return 1
		}
		return 0
	}

	qb := bigrams(nq)
	tb := bigrams(nt)
	if len(qb) == 0 {
		return 0
	}
	hit := 0
	for g := range qb {
		if tb[g] {
			hit++
		}
	}
	cover := float64(hit) / float64(len(qb))

	score := 0.8*cover + 0.2*Similarity(nq, nt)
	if score > 1 {
		score = 1
	}
	return score
}

// Score 检索打分：0.5 相关 + 0.3 新鲜 + 0.2 权重。
// 系数是拍定的，别随手调 —— 调了以后"为什么这条进来了"就说不清了。
func Score(r Record, query string) float64 {
	w := float64(r.Weight) / 5.0
	if w > 1 {
		w = 1
	}
	return 0.5*Relevance(r, query) + 0.3*Freshness(r) + 0.2*w
}

// Rank 按分数取前 n 条（只排 active）。
// 这是 recall_memory 和注入层共用的排序，保证"工具查到的"和"提前塞进去的"是同一套标准。
func Rank(list []Record, query string, n int) []Record {
	if n <= 0 {
		n = slotRelated
	}
	cands := make([]Record, 0, len(list))
	for _, r := range list {
		if r.Status == StatusActive {
			cands = append(cands, r)
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		return Score(cands[i], query) > Score(cands[j], query)
	})
	if n > len(cands) {
		n = len(cands)
	}
	return cands[:n]
}

// RenderSlots 生成注入 system prompt 的三槽文本：
//
//	画像（preference/fact，按权重）→ 永远在场，保证"我叫什么"不会丢
//	待办（task，active）           → 没做完的事
//	相关（按 score 排序）          → 和当前这句话沾边的经验/项目
//
// query 为空时第三槽退化成"按权重+新鲜取前几条"，用于开场第一句。
func RenderSlots(path, query string) string {
	list, err := LoadAll(path)
	if err != nil || len(list) == 0 {
		return ""
	}

	used := make(map[int64]bool, len(list))
	var blocks []string

	addBlock := func(title string, rs []Record) {
		var lines []string
		for _, r := range rs {
			if used[r.ID] {
				continue
			}
			used[r.ID] = true
			lines = append(lines, "- "+r.Content)
		}
		if len(lines) == 0 {
			return
		}
		blocks = append(blocks, title+"\n"+strings.Join(lines, "\n"))
	}

	// 槽一：画像。preference 优先于 fact（"他是谁"比零散事实更该常驻），
	// 同类里按权重降序、同权重按新到旧。只放前 3 条。
	var profile []Record
	for _, r := range list {
		k := KindOf(r)
		if r.Status != StatusActive {
			continue
		}
		if k == KindPreference || k == KindFact {
			profile = append(profile, r)
		}
	}
	sort.SliceStable(profile, func(i, j int) bool {
		ci, cj := KindOf(profile[i]), KindOf(profile[j])
		if (ci == KindPreference) != (cj == KindPreference) {
			return ci == KindPreference
		}
		si, sj := Score(profile[i], ""), Score(profile[j], "")
		if si != sj {
			return si > sj
		}
		return lastTouch(profile[i]) > lastTouch(profile[j])
	})
	if len(profile) > slotProfile {
		profile = profile[:slotProfile]
	}
	addBlock("【用户画像】", profile)

	// 槽二：待办
	var tasks []Record
	for _, r := range list {
		if r.Status != StatusActive {
			continue
		}
		if KindOf(r) == KindTask {
			tasks = append(tasks, r)
		}
	}
	sortRecords(tasks, "")
	if len(tasks) > slotTask {
		tasks = tasks[:slotTask]
	}
	addBlock("【未完成的待办】", tasks)

	// 槽三：相关。用 rank 的同一套打分，跳过已经进前两槽的；
	// 并且只放"真的沾边"的（Relevance 过门槛），沾边的少就不放，宁可空着。
	var rest []Record
	for _, r := range list {
		if r.Status != StatusActive || used[r.ID] {
			continue
		}
		if query != "" && Relevance(r, query) < slotMinRelevance {
			continue
		}
		rest = append(rest, r)
	}
	sortRecords(rest, query)
	if len(rest) > slotRelated {
		rest = rest[:slotRelated]
	}
	addBlock("【相关经验】", rest)

	if len(blocks) == 0 {
		return ""
	}

	out := strings.Join(blocks, "\n\n")
	return truncateRunes(out, promptBudgetChars)
}

// sortRecords 按打分降序；query 为空时只按权重+新鲜，此时相关度项为 0
func sortRecords(list []Record, query string) {
	sort.SliceStable(list, func(i, j int) bool {
		return Score(list[i], query) > Score(list[j], query)
	})
}

// truncateRunes 按字符截断（中文不会被切成半个），超预算就加省略号。
// 注意：宁可截掉最后一条，也不要把预算提到 2000 —— 那就等于回到"全塞"。
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}
