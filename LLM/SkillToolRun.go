package llm

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// maxSkillMetaBytes 解析 frontmatter 时只读文件头部这么多字节。
// 真实 SKILL.md 动辄几十 KB，为了两行元数据整文件读进来是浪费。
const maxSkillMetaBytes = 4096

// maxSkillBodyChars 单次返回给模型的技能正文上限（按字符算）。
// 实测最大是 taste 的 SKILL.md（30.2KB，约 3 万字符），30k 会正好切到它，
// 所以放到 50k；再大也不怕，ChatLoop 的压缩会在 80% 预算时接管。
const maxSkillBodyChars = 50000

// maxSkillDescChars 只作为病态输入的保险丝，不是常规截断线。
// 实测 9 个真实技能最大 904 字（taste），全量注入约 3,500 字 ≈ 1,000 token，
// 占 60k 预算不到 2%；清单固定还能吃到前缀缓存。
// 留 2000 上限，是防止哪天有人塞进一份 10KB 的 description 把预算打穿。
const maxSkillDescChars = 2000

// errNoSkillFrontMatter 表示这个 SKILL.md 格式不合格。
var errNoSkillFrontMatter = errors.New("缺少 YAML frontmatter")

// ================= 扫描：只认一层 =================

// LoadSkills 扫描技能根目录，返回全部技能元数据。
//
// 发现规则按 Agent Skills 的约定，不做递归：
//
//	<根目录>/<技能名>/SKILL.md        收下
//	<根目录>/<仓库>/skills/<技能名>/  不收（那是仓库的打包结构，交给安装器铺平）
//
// 好处是确定：一个目录在不在清单里，看它的层级一眼就知道，
// 不会因为某个仓库多套了两层目录，就把示例、变体一起塞进 prompt。
func LoadSkills(roots []string) []SkillMeta {
	var out []SkillMeta
	seen := make(map[string]bool) // 同名去重：先扫到的根优先
	for _, root := range roots {
		out = append(out, scanSkillRoot(root, seen)...)
	}
	return out
}

// scanSkillRoot 只看根目录的直接子目录，每个子目录当且仅当有 SKILL.md 才算技能。
// os.ReadDir 返回按文件名排序，所以扫描结果顺序稳定，清单不会每轮变样（前缀缓存友好）。
func scanSkillRoot(root string, seen map[string]bool) []SkillMeta {
	entries, err := os.ReadDir(root)
	if err != nil {
		// 还没建技能目录是正常状态，不当错误刷屏
		if !os.IsNotExist(err) {
			log.Printf("技能目录 %s 打不开: %v", root, err)
		}
		return nil
	}

	var out []SkillMeta
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") { // .git / .github / .claude-plugin
			continue
		}
		meta, ok := loadOneSkill(filepath.Join(root, e.Name()), seen)
		if !ok {
			continue
		}
		out = append(out, meta)
	}
	return out
}

// loadOneSkill 判断一个目录是不是技能目录：有 SKILL.md 且能解析出 name。
func loadOneSkill(dir string, seen map[string]bool) (SkillMeta, bool) {
	mdPath := filepath.Join(dir, "SKILL.md")
	if _, err := os.Stat(mdPath); err != nil {
		return SkillMeta{}, false
	}
	meta, err := readSkillMeta(mdPath)
	if err != nil {
		log.Printf("跳过技能 %s: %v", mdPath, err)
		return SkillMeta{}, false
	}
	meta.Dir = dir // 技能目录绝对路径，模型拼 references/ 时用
	if seen[meta.Name] {
		log.Printf("技能名 %s 重复，跳过 %s", meta.Name, mdPath)
		return SkillMeta{}, false
	}
	seen[meta.Name] = true
	return meta, true
}

// ================= 解析 frontmatter =================

// readSkillMeta 打开 SKILL.md，只读头部解析元数据。
func readSkillMeta(path string) (SkillMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return SkillMeta{}, err
	}
	defer f.Close()

	head, err := io.ReadAll(io.LimitReader(f, maxSkillMetaBytes))
	if err != nil {
		return SkillMeta{}, err
	}

	name, desc, err := parseSkillMeta(head)
	if err != nil {
		return SkillMeta{}, err
	}
	return SkillMeta{Name: name, Description: desc, Path: path}, nil
}

// parseSkillMeta 手写 frontmatter 解析，不引 yaml 库。
// 只认 name / description 两个顶层键；version、license、metadata、
// hooks（taste 里就有，带一大段缩进的 shell 命令）全部跳过。
func parseSkillMeta(head []byte) (name, desc string, err error) {
	s := strings.ReplaceAll(string(head), "\r\n", "\n") // Windows 换行统一掉
	s = strings.TrimPrefix(s, "\ufeff")                 // 记事本可能带 BOM

	// 跳过开头空行，以及个别仓库把整个文件包在 ``` 代码块里的写法。
	// 正常文件这一步什么都不做，纯防御。
	for stripped := true; stripped; {
		stripped = false
		if strings.HasPrefix(s, "\n") {
			s = s[1:]
			stripped = true
			continue
		}
		if strings.HasPrefix(s, "```") {
			if nl := strings.Index(s, "\n"); nl >= 0 {
				s = s[nl+1:]
				stripped = true
				continue
			}
			return "", "", errNoSkillFrontMatter
		}
	}

	// frontmatter 必须顶格以 --- 开头
	if !strings.HasPrefix(s, "---\n") {
		return "", "", errNoSkillFrontMatter
	}
	body := s[len("---\n"):]

	end := strings.Index(body, "\n---")
	if end < 0 {
		return "", "", errNoSkillFrontMatter
	}

	lines := strings.Split(body[:end], "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]

		// 顶层键必须顶格。缩进行是上一层的子键：
		// metadata 里的 bins、hooks 里的 command 都属于这种，
		// 误读会把一段 shell 命令当成技能名。
		// 注意这里不能 TrimSpace：一 trim 缩进就没了，下面的顶格判断永远不成立。
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}

		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)

		// 折叠块写法：description: >- 然后换行缩进继续写。
		// taste 的 description 是这种，跨 10 行、约 900 字；
		// > 和 | 对"给模型看的触发描述"没影响，统一拼成一行。
		if val == ">" || val == "|" || val == ">-" || val == "|-" || val == ">+" || val == "|+" {
			var parts []string
			j := i + 1
			for ; j < len(lines); j++ {
				next := lines[j]
				if next == "" {
					continue // 块内空行直接跳过
				}
				if next[0] != ' ' && next[0] != '\t' {
					break // 遇到下一个顶层键，块结束
				}
				parts = append(parts, strings.TrimSpace(next))
			}
			i = j - 1 // 让外层 i++ 正好落到下一个顶层键
			val = strings.Join(parts, " ")
		}

		switch key {
		case "name":
			name = unquoteYAML(val)
		case "description":
			desc = unquoteYAML(val)
		}
	}

	if name == "" {
		return "", "", fmt.Errorf("%w：缺少 name", errNoSkillFrontMatter)
	}
	return name, unquoteYAML(desc), nil
}

// unquoteYAML 去掉最外层一对引号（双引号、单引号都去）。
// 内部出现的引号（比如 gsap 里的 "AI-made"）原样保留。
func unquoteYAML(s string) string {
	if len(s) >= 2 {
		first, last := s[0], s[len(s)-1]
		if (first == '"' && last == '"') || (first == '\'' && last == '\'') {
			s = s[1 : len(s)-1]
		}
	}
	return strings.TrimSpace(s)
}

// ================= 进程内缓存 =================

// skillIndex 进程内只扫一次盘。
// 故意不加锁：InitSkills 只在 main 启动时调一次，之后只读。
var skillIndex []SkillMeta

// InitSkills 启动时调用一次。
func InitSkills(roots []string) { skillIndex = LoadSkills(roots) }

// ReloadSkills 重新扫盘，以后做热加载用。
func ReloadSkills(roots []string) []SkillMeta {
	skillIndex = LoadSkills(roots)
	return skillIndex
}

// Skills 返回当前索引（只读）。
func Skills() []SkillMeta { return skillIndex }

// FindSkill 按技能名查元数据。
// 绝不拿模型给的字符串拼路径，只在索引里查，路径永远来自扫描结果。
func FindSkill(name string) (SkillMeta, bool) {
	name = strings.TrimSpace(name)
	for _, m := range skillIndex {
		if strings.EqualFold(name, m.Name) {
			return m, true
		}
	}
	return SkillMeta{}, false
}

// SkillNames 列出全部技能名，给“找不到技能”的报错用。
func SkillNames() []string {
	out := make([]string, 0, len(skillIndex))
	for _, m := range skillIndex {
		out = append(out, m.Name)
	}
	return out
}

// ================= 注入 system prompt 的清单 =================

// RenderSkills 生成注入 system prompt 的清单：一行一个“名字：用途”，正文一个字都不带。
// 不限制技能数量：一个任务可能需要多个技能配合，规则只表达“按需加载”。
func RenderSkills(metas []SkillMeta) string {
	if len(metas) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n【可用技能】以下是本机已安装技能，只给了名字和用途，正文尚未加载。\n")
	fmt.Fprintf(&sb, "规则：任务与清单里某条用途吻合时，先调用 %s 读正文再执行；", LoadSkillTool.Name)
	sb.WriteString("一个任务可能需要多个技能配合，按需逐个加载即可，不存在“一次只能一个”的限制；")
	sb.WriteString("但每个技能正文都占上下文，只加载真正用得上的，别把清单当菜单全点一遍。\n")
	for _, m := range metas {
		fmt.Fprintf(&sb, "- %s：%s\n", m.Name, shortSkillDesc(m.Description, maxSkillDescChars))
	}
	return sb.String()
}

// shortSkillDesc 把病态超长的描述压到 max 字以内。
// 保留头 3/4 + 尾 1/4：头是“这技能干什么”，尾通常是 “Use when …” 的触发语。
// 正常技能（最长 904 字）永远走不到截断这一支。
func shortSkillDesc(s string, max int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if max <= 0 || len(r) <= max {
		return s
	}
	head := max * 3 / 4
	tail := max - head
	return string(r[:head]) + "……" + string(r[len(r)-tail:])
}

// ================= 工具执行 =================

// ExecLoadSkill 模型触发 load_skill 时执行。
// 约定和 ExecScanIdleFile 一致：入参是模型给的 rawArgs，出参直接回填给模型。
func ExecLoadSkill(rawArgs string) (string, error) {
	var args LoadSkillArgs
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return "", fmt.Errorf("解析 load_skill 参数失败: %w", err)
	}

	name := strings.TrimSpace(args.Name)
	if name == "" {
		return "", fmt.Errorf("缺少 name 参数；可用技能：%s", strings.Join(SkillNames(), "、"))
	}

	m, ok := FindSkill(name)
	if !ok {
		return "", fmt.Errorf("没有名为 %q 的技能；可用技能：%s", name, strings.Join(SkillNames(), "、"))
	}

	raw, err := os.ReadFile(m.Path)
	if err != nil {
		return "", fmt.Errorf("读取 %s 失败: %w", m.Path, err)
	}

	// truncateChars 在同包的 ContextWindow.go 里，按 rune 截，中文不会切半个。
	// 上限用 maxSkillBodyChars（50k），别用 maxSkillDescChars，否则 taste 的 30k 正文会被切到 2000。
	text := truncateChars(string(raw), maxSkillBodyChars)

	var sb strings.Builder
	fmt.Fprintf(&sb, "# 技能：%s\n技能目录：%s\n\n%s", m.Name, m.Dir, text)
	if n := len([]rune(string(raw))); n > maxSkillBodyChars {
		fmt.Fprintf(&sb, "\n\n（正文共 %d 字，已截断到 %d 字；需要后面的部分，用 read_file 从 %s 继续读）",
			n, maxSkillBodyChars, m.Path)
	}
	return sb.String(), nil
}

// ================= 技能根目录 =================

// DefaultSkillRoots 计算技能根目录，顺序即优先级（同名先到先得）：
//  1. 环境变量 AGENT_SKILLS_ROOT（多个用 ; 隔开）——技能放项目外时用
//  2. 当前工作目录下的 skills（项目级）
//  3. 可执行文件同目录的 skills（用户级，打包后不依赖 cwd）
//
// 这个形状和 Claude 的 .claude/skills、Cursor 的 .cursor/skills 同构，
// 只是去掉了厂商前缀；以后要接别家的技能包，把它的目录塞进 roots 即可。
func DefaultSkillRoots() []string {
	if env := strings.TrimSpace(os.Getenv("AGENT_SKILLS_ROOT")); env != "" {
		var roots []string
		// 分隔符必须是 PathListSeparator（Windows 上是 ;）。
		// PathSeparator 在 Windows 上是 \，会把每个路径按反斜杠切开。
		for _, r := range strings.Split(env, string(os.PathListSeparator)) {
			if r = strings.TrimSpace(r); r != "" {
				roots = append(roots, r)
			}
		}
		return roots
	}

	const defaultDir = "skills"
	roots := []string{defaultDir}

	if exe, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Join(filepath.Dir(exe), defaultDir))
	}
	return roots
}
