package llm

import "github.com/sashabaranov/go-openai"

// SkillMeta 一个技能的元数据。
// 故意不含正文：正文留在磁盘上，等模型点名了再读，这是整套机制省 token 的根本。
type SkillMeta struct {
	Name        string // 以 frontmatter 的 name 为准，不是文件夹名
	Description string // 自然语言写的"什么时候用"，模型靠它判断
	Dir         string // 技能目录绝对路径，模型拼 references/xxx.md 用
	Path        string // SKILL.md 绝对路径
}

// LoadSkillArgs load_skill 的入参，走 json 标签，ExecLoadSkill 里直接 Unmarshal。
type LoadSkillArgs struct {
	Name string `json:"name"`
}

// LoadSkillTool 工具声明，风格同 ScanIdleFileTool：只描述何时调、参数是什么。
// ChatLoop 通过 LoadSkillTool.Name 做本地分流，名字只写这一处。
var LoadSkillTool = openai.FunctionDefinition{
	Name: "load_skill",
	Description: `加载一个技能的完整说明书（SKILL.md 正文）。
当用户任务与【可用技能】清单里某条用途吻合时，先调用本工具读正文，再按正文步骤执行。
多个技能可以配合：例如做落地页时用 design-taste-frontend 定审美、用 gsap-scrolltrigger 定滚动动画，
两者都需要就先后或同轮并行调用本工具，不必一轮只挑一个。
返回内容会给出技能目录的绝对路径；正文里提到的 references/、scripts/ 等相对路径都相对该目录。
只加载这次任务真正用得上的技能，不要把清单当成菜单全点一遍。`,
	Parameters: map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "技能名，取【可用技能】清单里冒号前的名字，例如 gsap-core",
			},
		},
		"required": []string{"name"},
	},
}
