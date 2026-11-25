package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"agent/Env"
	"agent/MCP"
	"agent/memory"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sashabaranov/go-openai"

	llm "agent/LLM"
)

// ===== 界面阶段 =====
type phase int

const (
	phaseIdle    phase = iota // 等待用户输入
	phaseWorking              // AI 正在处理
)

// ===== 对话式美化（bubbletea 自带能力）=====
// tea.WithAltScreen 全屏窗口 / tea.WithMouseCellMotion 滚轮 / lipgloss 配色边框 / spinner 加载动画 / viewport 对话滚动
var (
	accent = lipgloss.Color("63") // 蓝紫主题色
	subtl  = lipgloss.Color("241")

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230")).
			Background(accent).
			Padding(0, 3).
			Margin(0, 0, 1, 0)

	boxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(accent).
			Padding(1, 2)

	aiLabel = lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("230")).
		Background(accent).
		Padding(0, 1)

	userLabel = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("42")).
			Padding(0, 1)

	errStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	hintStyle = lipgloss.NewStyle().Foreground(subtl).Italic(true)
)

// 滚动相关按键白名单：只把这几类键交给 viewport，
// 其余按键照常进输入框（否则空格/f/j/k 等会被 viewport 吃掉）。
func isScrollKey(k string) bool {
	switch k {
	case "pgup", "pgdown", "ctrl+u", "ctrl+d", "up", "down":
		return true
	}
	return false
}

// ===== 记忆维护 =====
const (
	maintainEveryTurns = 10  // 每 10 轮对话后台维护一次
	maxFactRecords     = 200 // 事实库上限（超过按权重+新鲜度淘汰）
	maxEpisodes        = 300 // 情节库上限（只用来接上下文，留最近这些够用）
)

// neverFail 把三个返回值的维护调用包成无返回值，专门给 go 用。
// 维护失败一律静默：记忆坏一点不耽误聊天。
func neverFail(_ memory.CompactStats, _ int, _ error) {}

// ===== 对话消息 =====
type speaker string

const (
	spAI   speaker = "AI"
	spUser speaker = "你"
)

type chatMsg struct {
	who  speaker
	text string
}

type model struct {
	session  *mcp.ClientSession // MCP 会话，聊天时用它动态调工具
	phase    phase
	input    textinput.Model
	spinner  spinner.Model
	viewport viewport.Model                 // 对话滚动区
	msgs     []chatMsg                      // 界面展示的对话历史
	history  []openai.ChatCompletionMessage // 传给 LLM 的多轮历史
	turns    int                            // 本次会话已完成的对话轮数，用来触发记忆维护
	width    int
	height   int
}

// chatDoneMsg AI 回复完成的异步消息
type chatDoneMsg struct {
	answer  string
	history []openai.ChatCompletionMessage
	err     error
}

func initialModel(session *mcp.ClientSession) model {
	ti := textinput.New()
	ti.Placeholder = "输入你的问题，回车发送（例如：扫描 E 盘的闲置文件）"
	ti.Prompt = ""
	ti.Width = 60
	ti.Focus()

	sp := spinner.New()
	sp.Style = lipgloss.NewStyle().Foreground(accent)

	// viewport 键位：默认键位里有 空格/f/u/d/j/k/h/l，会和打字冲突，这里收紧
	vp := viewport.New(0, 0)
	km := viewport.DefaultKeyMap()
	km.PageUp = key.NewBinding(key.WithKeys("pgup"), key.WithHelp("pgup", "上翻一页"))
	km.PageDown = key.NewBinding(key.WithKeys("pgdown"), key.WithHelp("pgdown", "下翻一页"))
	km.HalfPageUp = key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "上翻半页"))
	km.HalfPageDown = key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "下翻半页"))
	km.Up = key.NewBinding(key.WithKeys("up"), key.WithHelp("↑", "上移一行"))
	km.Down = key.NewBinding(key.WithKeys("down"), key.WithHelp("↓", "下移一行"))
	km.Left = key.NewBinding()  // 关掉 h
	km.Right = key.NewBinding() // 关掉 l
	vp.KeyMap = km

	// 中性的开场白：不预设扫盘，只是一个可对话的 AI
	msgs := []chatMsg{
		{who: spAI, text: "你好，我是 AI 助手。你可以随意问我问题，也可以让我扫描磁盘的闲置文件，比如：扫描 E 盘的闲置文件。"},
	}

	return model{session: session, phase: phaseIdle, input: ti, spinner: sp, viewport: vp, msgs: msgs}
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg: // 窗口尺寸变化 → 对话区自适应
		m.width, m.height = msg.Width, msg.Height
		// boxStyle: 边框左右各 1 + Padding 左右各 2 = 6，宽度必须扣掉，否则右边被裁
		m.viewport.Width = msg.Width - 6
		m.viewport.Height = msg.Height - 8
		m.viewport.SetContent(renderChat(m.msgs))
		m.viewport.GotoBottom()
		return m, nil

	case tea.MouseMsg: // 鼠标滚轮翻看历史
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		return m, cmd

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc": // 任何阶段都能退出
			return m, tea.Quit
		}
		if isScrollKey(msg.String()) { // 翻页 / 上下行 → 滚历史，不再进输入框
			var cmd tea.Cmd
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		}

	case chatDoneMsg: // AI 回复完成
		atBottom := m.viewport.AtBottom() // 用户正在往上看历史时，别把他拽回底部
		if msg.err != nil {
			m.msgs = append(m.msgs, chatMsg{who: spAI, text: "出错了：" + errStyle.Render(msg.err.Error())})
		} else {
			m.msgs = append(m.msgs, chatMsg{who: spAI, text: msg.answer})
		}
		m.history = msg.history
		m.phase = phaseIdle
		m.input.Focus()
		m.viewport.SetContent(renderChat(m.msgs))
		if atBottom {
			m.viewport.GotoBottom()
		}

		// 每 10 轮做一次记忆维护（后台跑，不调模型、不花钱）：
		// 事实库合并重复 + 淘汰过期，情节库裁到最近 300 条。
		m.turns++
		if m.turns%maintainEveryTurns == 0 {
			go neverFail(memory.Maintain("", "", maxFactRecords, maxEpisodes))
		}
		return m, nil
	}

	// 分阶段处理
	switch m.phase {
	case phaseIdle:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		if key, ok := msg.(tea.KeyMsg); ok && key.String() == "enter" {
			return m.send()
		}
		return m, cmd
	case phaseWorking: // 转动画
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd
	}
	return m, nil
}

// send 用户回车：把消息发给 deepseek，由模型自行决定是否调用 MCP 工具
func (m model) send() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	// 追加用户消息到界面 + 多轮历史
	m.msgs = append(m.msgs, chatMsg{who: spUser, text: text})
	m.input.SetValue("")
	m.history = append(m.history, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: text,
	})
	m.phase = phaseWorking
	m.viewport.SetContent(renderChat(m.msgs))
	m.viewport.GotoBottom() // 自己发的消息，总是跟到底部
	// 并行：转动画 + 异步调用 deepseek（带 MCP 会话）
	return m, tea.Batch(m.spinner.Tick, chatCmd(m.history, m.session))
}

// chatCmd 异步执行：把整段历史交给 deepseek，工具由 MCP 会话动态执行
func chatCmd(history []openai.ChatCompletionMessage, session *mcp.ClientSession) tea.Cmd {
	return func() tea.Msg {
		answer, newHistory, err := llm.ChatTurn(context.Background(), history, session)
		return chatDoneMsg{answer: answer, history: newHistory, err: err}
	}
}

// renderChat 把对话历史渲染成带"AI/你"标签的消息流
func renderChat(msgs []chatMsg) string {
	var sb strings.Builder
	for _, m := range msgs {
		label := aiLabel.Render(" AI ")
		if m.who == spUser {
			label = userLabel.Render(" 你 ")
		}
		sb.WriteString(label + " " + m.text + "\n\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (m model) View() string {
	title := titleStyle.Render(" AI 助手 ")
	chat := boxStyle.Render(m.viewport.View())

	var bottom string
	if m.phase == phaseWorking {
		bottom = m.spinner.View() + " AI 正在思考……（涉及工具时会自动调用）"
	} else {
		bottom = m.input.View() + "\n" + hintStyle.Render("回车发送 · PgUp/PgDn 或滚轮翻历史 · Ctrl+C 退出")
	}
	return title + "\n" + chat + "\n" + bottom
}

// connectMCPServer 拉起自己的 MCP 服务器子进程（--mcp），建立客户端会话
func connectMCPServer(ctx context.Context) (*mcp.ClientSession, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "my-tui", Version: "v0.0.1"}, nil)
	transport := &mcp.CommandTransport{
		// os.Args[0] = 当前可执行文件路径；让它再跑一次 --mcp 当服务器
		Command: exec.Command(os.Args[0], "--mcp"),
	}
	return client.Connect(ctx, transport, nil)
}

func main() {
	//agent配置
	list, err := Env.ReloadAISetting()
	if err != nil {
		log.Panic(err)
	}
	aicfg, err := llm.CheckConnect(list, 5*time.Second)
	// 直接使用已验证有效的 key，忽略环境变量，避免旧环境变量覆盖导致 401
	if err != nil {
		log.Panic(err)
	}
	// 启动即把当前这套配置交给 Env 统一保管：
	// 之后 LLM 包只认 Env.Current()，key / url / model 不再到处当参数传。
	if err := Env.Use(aicfg); err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		os.Exit(1)
	}
	// --mcp 分支：本程序作为 MCP 服务器运行
	if len(os.Args) > 1 && os.Args[1] == "--mcp" {
		MCP.RunMCPServer()
		return
	}
	// 技能索引：启动时扫一次 skills 目录，之后每轮 system prompt 直接读内存清单。
	// 必须在 TUI 之前跑，否则第一轮对话的 prompt 里没有【可用技能】。
	llm.InitSkills(llm.DefaultSkillRoots())

	// 启动时后台维护一次：上次退出可能留下重复条目和过长的情节库，先理顺再开聊
	go neverFail(memory.Maintain("", "", maxFactRecords, maxEpisodes))

	// TUI 启动前，先连上自己的 MCP 服务器（子进程跑 --mcp）
	session, err := connectMCPServer(context.Background())
	if err != nil {
		fmt.Println("连接 MCP 服务器失败:", err)
		os.Exit(1)
	}
	defer session.Close()

	// WithAltScreen: 备用屏幕全屏模式，界面占满终端，像一个独立窗口
	// WithMouseCellMotion: 开启鼠标事件，滚轮才能滚动对话历史
	p := tea.NewProgram(initialModel(session), tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := p.Run(); err != nil {
		fmt.Println("启动失败:", err)
		os.Exit(1)
	}
}
