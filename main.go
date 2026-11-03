package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"agent/MCP"

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
// tea.WithAltScreen 全屏窗口 / lipgloss 配色边框 / spinner 加载动画 / viewport 对话滚动
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
	apiKey   string
	session  *mcp.ClientSession // MCP 会话，聊天时用它动态调工具
	phase    phase
	input    textinput.Model
	spinner  spinner.Model
	viewport viewport.Model                 // 对话滚动区
	msgs     []chatMsg                      // 界面展示的对话历史
	history  []openai.ChatCompletionMessage // 传给 LLM 的多轮历史
	width    int
	height   int
}

// chatDoneMsg AI 回复完成的异步消息
type chatDoneMsg struct {
	answer  string
	history []openai.ChatCompletionMessage
	err     error
}

func initialModel(apiKey string, session *mcp.ClientSession) model {
	ti := textinput.New()
	ti.Placeholder = "输入你的问题，回车发送（例如：扫描 E 盘的闲置文件）"
	ti.Prompt = ""
	ti.Width = 60
	ti.Focus()

	sp := spinner.New()
	sp.Style = lipgloss.NewStyle().Foreground(accent)

	// 中性的开场白：不预设扫盘，只是一个可对话的 AI
	msgs := []chatMsg{
		{who: spAI, text: "你好，我是 AI 助手。你可以随意问我问题，也可以让我扫描磁盘的闲置文件，比如：扫描 E 盘的闲置文件。"},
	}

	return model{apiKey: apiKey, session: session, phase: phaseIdle, input: ti, spinner: sp, msgs: msgs}
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg: // 窗口尺寸变化 → 对话区自适应
		m.width, m.height = msg.Width, msg.Height
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = msg.Height - 8
		m.viewport.SetContent(renderChat(m.msgs))
		m.viewport.GotoBottom()
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "esc": // 任何阶段都能退出
			return m, tea.Quit
		}

	case chatDoneMsg: // AI 回复完成
		if msg.err != nil {
			m.msgs = append(m.msgs, chatMsg{who: spAI, text: "出错了：" + errStyle.Render(msg.err.Error())})
		} else {
			m.msgs = append(m.msgs, chatMsg{who: spAI, text: msg.answer})
		}
		m.history = msg.history
		m.phase = phaseIdle
		m.input.Focus()
		m.viewport.SetContent(renderChat(m.msgs))
		m.viewport.GotoBottom()
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
	m.viewport.GotoBottom()
	// 并行：转动画 + 异步调用 deepseek（带 MCP 会话）
	return m, tea.Batch(m.spinner.Tick, chatCmd(m.apiKey, m.history, m.session))
}

// chatCmd 异步执行：把整段历史交给 deepseek，工具由 MCP 会话动态执行
func chatCmd(apiKey string, history []openai.ChatCompletionMessage, session *mcp.ClientSession) tea.Cmd {
	return func() tea.Msg {
		answer, newHistory, err := llm.ChatTurn(context.Background(), apiKey, history, session)
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
		bottom = m.input.View() + "\n" + hintStyle.Render("回车发送；Ctrl+C / Esc 退出")
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
	// --mcp 分支：本程序作为 MCP 服务器运行
	if len(os.Args) > 1 && os.Args[1] == "--mcp" {
		MCP.RunMCPServer()
		return
	}

	// 直接使用已验证有效的 key，忽略环境变量，避免旧环境变量覆盖导致 401
	apiKey := "xxxxxxx"

	// TUI 启动前，先连上自己的 MCP 服务器（子进程跑 --mcp）
	session, err := connectMCPServer(context.Background())
	if err != nil {
		fmt.Println("连接 MCP 服务器失败:", err)
		os.Exit(1)
	}
	defer session.Close()

	// WithAltScreen: 备用屏幕全屏模式，界面占满终端，像一个独立窗口
	p := tea.NewProgram(initialModel(apiKey, session), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Println("启动失败:", err)
		os.Exit(1)
	}
}
