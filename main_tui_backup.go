//go:build ignore

package main

import (
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	llm "agent/LLM"
)

// ===== 涓変釜鐣岄潰闃舵 =====
type phase int

const (
	phaseInput    phase = iota // 绛夊緟鐢ㄦ埛杈撳叆鐩樼
	phaseScanning              // 姝ｅ湪鎵弿
	phaseDone                  // 瀹屾垚
)

type model struct {
	apiKey string
	phase  phase
	input  textinput.Model // 鏀堕泦鐩樼
	answer string          // Agent鏈€缁堝洖绛?	err    error
}

// scanDoneMsg 鎵弿瀹屾垚鐨勫紓姝ユ秷鎭?type scanDoneMsg struct {
	answer string
	err    error
}

func initialModel(apiKey string) model {
	ti := textinput.New()
	ti.Placeholder = `C:\`
	ti.Prompt = "瑕佹壂鎻忕殑鐩樼> "
	ti.Focus()
	return model{apiKey: apiKey, phase: phaseInput, input: ti}
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m.phase {
	case phaseInput: // 杈撳叆闃舵锛氱瓑鐢ㄦ埛鍥炶溅
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg) // 浜ょ粰TextInput澶勭悊鎸夐敭
		if key, ok := msg.(tea.KeyMsg); ok && key.String() == "enter" {
			path := m.input.Value()
			if path == "" {
				path = `C:\`
			}
			m.phase = phaseScanning
			return m, scanCmd(m.apiKey, path) // 鍥炶溅鈫掑彂璧峰紓姝ュ伐鍏疯皟鐢?		}
		return m, cmd
	case phaseScanning: // 鎵弿闃舵锛氱瓑寮傛缁撴灉
		if done, ok := msg.(scanDoneMsg); ok {
			m.answer, m.err = done.answer, done.err
			m.phase = phaseDone
			return m, tea.Quit
		}
		return m, nil
	default:
		return m, tea.Quit
	}
}

func (m model) View() string {
	switch m.phase {
	case phaseInput:
		return "璇烽€夋嫨瑕佹壂鎻忕殑纾佺洏/鐩綍锛堝洖杞︾‘璁わ紝鐣欑┖榛樿C鐩橈級锛歕n\n" + m.input.View()
	case phaseScanning:
		return "姝ｅ湪鎵弿 " + m.input.Value() + " ...\n"
	case phaseDone:
		if m.err != nil {
			return "鎵弿鍑洪敊锛? + m.err.Error() + "\n"
		}
		return m.answer + "\n"
	}
	return ""
}

// scanCmd 寮傛鎵ц宸ュ叿璋冪敤锛氭嬁鐢ㄦ埛鐩樼鎷紁rompt 鈫?璧癓LM function call
func scanCmd(apiKey, path string) tea.Cmd {
	return func() tea.Msg {
		answer, err := llm.RunAgent(apiKey, "甯垜鎵弿"+path+"鐨勯棽缃枃浠?)
		return scanDoneMsg{answer: answer, err: err}
	}
}

func main() {
	apiKey := os.Getenv("DOUBAO_API_KEY")
	if apiKey == "" {
		apiKey = "sk-56bf822941e5423fb88a4a985e3f3b9f"
	}
	p := tea.NewProgram(initialModel(apiKey))
	if _, err := p.Run(); err != nil {
		fmt.Println("鍚姩澶辫触:", err)
		os.Exit(1)
	}
}

