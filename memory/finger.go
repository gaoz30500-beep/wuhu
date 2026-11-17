package memory

import (
	"strings"
	"unicode"
)

// similarMerge 相似度 ≥ 它，就认为两条记忆是"同一件事"。
// 0.55 不是随便定的：用你库里 #8/#9 那种"换种说法"的句子标出来的，
// 改之前先跑 finger_test.go 里的标定用例，看数字再动。
const similarMerge = 0.55

// stopRunes 对语义没贡献的中文虚词，归一化时删掉。
// 只删纯虚词。绝对不要删"不 / 没 / 要 / 用 / 能"这类会翻转语义的字，
// 否则「不能删」和「能删」会归一化成同一个指纹，那是灾难。
var stopRunes = []rune{
	'的', '了', '是', '在', '和', '与', '把', '被', '对', '就', '都', '也',
	'很', '个', '这', '那', '而', '但', '还', '又', '再', '吗', '呢', '吧',
	'啊', '着', '过', '得', '地', '之', '其', '或', '及', '以', '于', '并',
}

// isStopRune 判断是不是虚词
func isStopRune(r rune) bool {
	for _, x := range stopRunes {
		if x == r {
			return true
		}
	}
	return false
}

// Normalize 把正文归一化成"只剩实义字的骨头"：小写、删标点、删空白、删虚词。
// 故意不解散英文词（write_file → writefile），因为两条都这么处理，比较时不吃亏。
func Normalize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		if unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r) {
			continue
		}
		if isStopRune(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Fingerprint 判重指纹：归一化后的正文本身，不取 md5。
// 好处：打开 jsonl 一眼看出它代表哪句话，出问题能人工核对。
func Fingerprint(content string) string {
	return Normalize(content)
}

// bigrams 字符二元组集合：中文没有空格、切不了词，就用"相邻两个字"当特征。
// map[string]bool 在这里当集合用，value 是不是 true 不重要，只看 key 存不存在。
func bigrams(s string) map[string]bool {
	rs := []rune(s)
	set := make(map[string]bool, len(rs))
	if len(rs) == 0 {
		return set
	}
	if len(rs) == 1 {
		set[string(rs)] = true
		return set
	}
	for i := 0; i+1 < len(rs); i++ {
		set[string(rs[i:i+2])] = true
	}
	return set
}

// Similarity 两条正文的相似度，0~1。
// 用 Dice 系数 2*交集/(|A|+|B|)：比 Jaccard 对短文本更宽容，不容易漏判。
func Similarity(a, b string) float64 {
	na, nb := Normalize(a), Normalize(b)
	if na == "" || nb == "" {
		return 0
	}
	if na == nb {
		return 1
	}
	sa, sb := bigrams(na), bigrams(nb)
	inter := 0
	for g := range sa {
		if sb[g] {
			inter++
		}
	}
	total := len(sa) + len(sb)
	if total == 0 {
		return 0
	}
	return 2 * float64(inter) / float64(total)
}

// Coverage 单向覆盖度：a 的二元组有多大比例能在 b 里找到。
// 为什么要它：Dice 的分母是 |A|+|B|，一条详细、一条简略说同一件事时，
// 长的把分母撑大、分数掉到 0.4 以下，判重就漏了（实测 #2 换说法只拿到 0.36）。
// 覆盖度只问"新的这句是不是被旧的那条装下了"，长短不影响。
func Coverage(a, b string) float64 {
	na, nb := Normalize(a), Normalize(b)
	if na == "" || nb == "" {
		return 0
	}
	if na == nb {
		return 1
	}
	sa, sb := bigrams(na), bigrams(nb)
	if len(sa) == 0 {
		return 0
	}
	hit := 0
	for g := range sa {
		if sb[g] {
			hit++
		}
	}
	return float64(hit) / float64(len(sa))
}

// DupScore 判重综合分：覆盖度为主（"装下没装下"），Dice 为辅（"整体像不像"）。
// 两个方向取覆盖度大的那个，所以"长的装下短的"和"短的装下长的"都能命中。
//
// 短句例外：归一化后不足 6 个字时，覆盖度极容易撞车
//（"用户电脑有问题"这种话，随便一条长记忆都能把它装下），
// 这时退回 Dice，宁可不合并也别错并。
func DupScore(a, b string) float64 {
	na, nb := Normalize(a), Normalize(b)
	short := len([]rune(na))
	if len([]rune(nb)) < short {
		short = len([]rune(nb))
	}
	if short < 6 {
		return Similarity(a, b)
	}

	cab := Coverage(a, b)
	cba := Coverage(b, a)
	cov := cab
	if cba > cov {
		cov = cba
	}
	return 0.6*cov + 0.4*Similarity(a, b)
}

// IsDup 判断两段文字算不算同一件事：指纹相同直接算，否则看综合分过没过线。
// M3 的 Write 会用它决定"合并还是追加"。
func IsDup(a, b string) bool {
	if Fingerprint(a) == Fingerprint(b) {
		return true
	}
	return DupScore(a, b) >= similarMerge
}
