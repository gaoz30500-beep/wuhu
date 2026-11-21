package llm

import (
	"os"
	"strings"

	"agent/memory"
)

// ================= 路径 =================

// memoryPath 必须和 mcp_config.json 里两个记忆工具的 storePath 一致
func memoryPath() string {
	if p := os.Getenv("AGENT_MEMORY_PATH"); p != "" {
		return p
	}
	return memory.DefaultPath
}

// ================= system prompt =================

// buildSystemPrompt 每轮重新读一次记忆文件：
// 上一轮刚写的记忆，这一轮就能用上（不用重启）。
// query 是用户当前这句话，用来挑"相关经验"那一槽；为空时第三槽按权重+新鲜取。
// withEpisodes 为 true（新会话第一句）时，附上上次会话的尾巴，用来接上"刚才那个"。
func buildSystemPrompt(query string, withEpisodes bool) string {
	var sb strings.Builder
	sb.WriteString("你是一个运行在用户 Windows 电脑上的 AI 助手，可以调用工具操作本机文件、查询网络。回答简洁、准确、口语化。\n\n")
	sb.WriteString("记忆规则：\n")
	sb.WriteString("1. 下面【长期记忆】里的内容已经注入，直接用，不要为了这些再调用 recall_memory。\n")
	sb.WriteString("2. 当用户说明稳定的偏好、身份/设备事实，或明确要求你记住某件事时，调用 save_memory 保存（一句话，写清主语）。\n")
	sb.WriteString("3. 只有当用户问“以前/上次/我之前说过”这类具体历史细节、而下面没写时，才调用 recall_memory。一次回答最多调 1 次。\n")
	sb.WriteString("4. 一次性的临时指令（例如“帮我扫 E 盘”）不要存。\n")

	// 三槽注入：画像 / 待办 / 相关。预算 600 字，超了从尾部砍。
	if facts := memory.RenderSlots(memoryPath(), query); facts != "" {
		sb.WriteString("\n【长期记忆】\n")
		sb.WriteString(facts)
	}

	if withEpisodes {
		if eps := memory.RenderEpisodes(episodePath(), recentEpisodeLimit); eps != "" {
			sb.WriteString("\n\n【最近几段对话】（─── 新会话 ─── 前面的是更早的会话，仅用于接上下文，不代表结论）\n")
			sb.WriteString(eps)
		}
	}
	if list := RenderSkills(Skills()); list != "" {
		sb.WriteString(list)
	}
	return sb.String()
}

// ================= 规则兜底：用户明说要记住 =================

// rememberKeywords 命中这些词，就认为用户在要求"记住"
var rememberKeywords = []string{"记住", "记一下", "记下", "别忘了", "别忘", "记得", "以后都", "以后不要", "记住我"}

// autoRemember 规则兜底写入。模型判断会漏，这条只是保险，失败不影响对话
func autoRemember(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	hit := false
	for _, kw := range rememberKeywords {
		if strings.Contains(text, kw) {
			hit = true
			break
		}
	}
	if !hit {
		return
	}
	// 判重交给 memory.Write（指纹 + 相似度双闸）。
	// 原来那句 memory.Search(text) 是整句子串匹配，永远命中不了，等于没查重。
	_, _ = memory.Write(memoryPath(), "用户说过："+text, "自动,规则", "rule")
}

// ================= 情节记忆（本次/上次会话发生过什么） =================

// recentEpisodeLimit 注入 prompt 的"上次会话尾巴"条数。多了纯属浪费预算。
const recentEpisodeLimit = 6

// episodePath 情节文件路径，可用 AGENT_EPISODE_PATH 覆盖
func episodePath() string {
	if p := os.Getenv("AGENT_EPISODE_PATH"); p != "" {
		return p
	}
	return memory.DefaultEpisodePath
}

// recordEpisode 异步记一条情节。失败静默：情节丢了不影响这次回答。
// 只记"说了什么"，不记工具的中间过程 —— 那是压缩器的活。
func recordEpisode(role, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	go func() {
		_ = memory.AppendEpisode(episodePath(), role, truncateChars(text, 200))
	}()
}
