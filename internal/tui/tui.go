package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/AamindMandragora/pragma/internal/agent"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	userStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	agentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	toolStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

type Message struct {
	Role    string
	Content string
}

type agentMessage struct {
	res string
	err error
}

type toolMessage struct {
	eventType string
	name      string
	content   string
}

type confirmMessage struct {
	command string
}

type TUIState int

const (
	StateOnboarding TUIState = iota
	StateChat
)

type TUIModel struct {
	agent       *agent.Agent
	input       textinput.Model
	viewport    viewport.Model
	messages    []Message
	streaming   bool
	width       int
	height      int
	confirming  bool
	confirmCmd  string
	confirmChan chan bool
	state       TUIState
	onboardStep int
	onboardData map[string]string
}

func (t TUIModel) Init() tea.Cmd {
	return textinput.Blink
}

func firstArg(argsJSON string) string {
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return ""
	}
	for _, v := range m {
		s := fmt.Sprintf("%v", v)
		if len(s) > 80 {
			s = s[:80] + "..."
		}
		return s
	}
	return ""
}

func (t *TUIModel) updateViewportContent() {
	var b strings.Builder
	for _, msg := range t.messages {
		switch msg.Role {
		case "user":
			b.WriteString(userStyle.Render("  ▶ You"))
			b.WriteString("\n")
			wrapped := wrap(msg.Content, t.width-4)
			for _, line := range strings.Split(wrapped, "\n") {
				b.WriteString("  ")
				b.WriteString(line)
				b.WriteString("\n")
			}
			b.WriteString("\n")
		case "assistant":
			b.WriteString(agentStyle.Render("  ◆ Pragma"))
			b.WriteString("\n")
			wrapped := wrap(msg.Content, t.width-4)
			for _, line := range strings.Split(wrapped, "\n") {
				b.WriteString("  ")
				b.WriteString(line)
				b.WriteString("\n")
			}
			b.WriteString("\n")
		case "system":
			b.WriteString(dimStyle.Render("  ℹ " + msg.Content))
			b.WriteString("\n\n")
		case "error":
			b.WriteString(errorStyle.Render("  ✗ Error"))
			b.WriteString("\n")
			b.WriteString("  ")
			b.WriteString(msg.Content)
			b.WriteString("\n\n")
		case "tool_call":
			b.WriteString(toolStyle.Render("  ⚡ Tool: " + msg.Content))
			b.WriteString("\n\n")
		case "tool_result":
			b.WriteString(toolStyle.Render("  ↳ Result"))
			b.WriteString("\n")
			wrapped := wrap(msg.Content, t.width-4)
			for _, line := range strings.Split(wrapped, "\n") {
				b.WriteString("  ")
				b.WriteString(line)
				b.WriteString("\n")
			}
			b.WriteString("\n")
		}
	}
	if t.streaming {
		b.WriteString(dimStyle.Render("  thinking..."))
		b.WriteString("\n\n")
	}
	if t.confirming {
		b.WriteString(toolStyle.Render("  ⚡ Run command: " + t.confirmCmd))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render("  allow? [y/n]"))
		b.WriteString("\n\n")
	}
	t.viewport.SetContent(b.String())
	t.viewport.GotoBottom()
}

func (t *TUIModel) handleSlashCommand(cmd string) string {
	parts := strings.Fields(cmd)
	command := parts[0]

	switch command {
	case "/help":
		return `Available commands:
  /help        — show this message
  /clear       — clear chat history
  /status      — show session info
  /model       — show current model and tool mode
  /docs        — show recent task summaries
  /arch        — show architecture doc
  /cost        — show token usage (coming soon)
  /quit        — exit Pragma`
	case "/clear":
		t.messages = t.messages[:0]
		if len(t.agent.History) > 0 {
			t.agent.History = t.agent.History[:1]
		}
		return "Chat cleared."
	case "/status":
		msgCount := len(t.agent.History) - 1
		return fmt.Sprintf("Messages: %d | Model: %s | Tool mode: %s", msgCount, t.agent.ProviderConfig.ModelName, t.agent.ToolMode)
	case "/model":
		return fmt.Sprintf("Provider config:\n  Model: %s\n  Max tokens: %d\n  Tool mode: %s", t.agent.ProviderConfig.ModelName, t.agent.ProviderConfig.MaxTokens, t.agent.ToolMode)
	case "/docs":
		recent := agent.LoadRecentDocs(3)
		if recent == "" {
			return "No task docs yet."
		}
		return recent
	case "/arch":
		arch := agent.LoadArchitecture()
		if arch == "" {
			return "No architecture doc yet."
		}
		return arch
	case "/quit":
		return "EXIT"
	default:
		return fmt.Sprintf("Unknown command: %s. Type /help for available commands.", command)
	}
}

func (t TUIModel) updateOnboarding(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		t.width = msg.Width
		t.height = msg.Height
		t.input.Width = msg.Width - 4
		return t, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			return t, tea.Quit
		case "enter":
			val := strings.TrimSpace(t.input.Value())
			t.input.SetValue("")

			switch t.onboardStep {
			case 0:
				if val == "2" {
					t.onboardData["provider"] = "openai"
					t.onboardData["api_key_var"] = "OPENAI_API_KEY"
					t.onboardData["default_model"] = "gpt-5.4-mini"
				} else {
					t.onboardData["provider"] = "openrouter"
					t.onboardData["api_key_var"] = "OPENROUTER_API_KEY"
					t.onboardData["default_model"] = "qwen/qwen3-coder:free"
				}
				t.onboardStep = 1
				t.input.Placeholder = "Model name [" + t.onboardData["default_model"] + "]"

			case 1:
				if val == "" {
					val = t.onboardData["default_model"]
				}
				t.onboardData["model"] = val
				t.onboardStep = 2
				t.input.Placeholder = "Paste API key (or enter to skip)"

			case 2:
				t.onboardData["api_key"] = val
				t.writeOnboardConfig()
				return t, tea.Quit
			}
			return t, nil
		}
	}
	var cmd tea.Cmd
	t.input, cmd = t.input.Update(msg)
	return t, cmd
}

func (t TUIModel) updateChat(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		t.width = msg.Width
		t.height = msg.Height
		t.input.Width = msg.Width - 4
		headerHeight := 3
		inputHeight := 3
		t.viewport.Width = msg.Width
		t.viewport.Height = msg.Height - headerHeight - inputHeight
		t.updateViewportContent()
		return t, nil
	case tea.KeyMsg:
		if t.confirming {
			switch msg.String() {
			case "y", "Y":
				t.confirming = false
				t.messages = append(t.messages, Message{Role: "tool_call", Content: "approved: " + t.confirmCmd})
				t.confirmChan <- true
			case "n", "N":
				t.confirming = false
				t.messages = append(t.messages, Message{Role: "tool_call", Content: "rejected: " + t.confirmCmd})
				t.confirmChan <- false
			}
			t.updateViewportContent()
			return t, nil
		}
		switch msg.String() {
		case "ctrl+c":
			return t, tea.Quit
		case "enter":
			if t.streaming {
				break
			}
			val := strings.TrimSpace(t.input.Value())
			if val == "" {
				return t, nil
			}
			if strings.ToLower(val) == "exit" || strings.ToLower(val) == "quit" {
				return t, tea.Quit
			}
			if strings.HasPrefix(val, "/") {
				t.input.SetValue("")
				result := t.handleSlashCommand(val)
				if result == "EXIT" {
					return t, tea.Quit
				}
				if result != "" {
					t.messages = append(t.messages, Message{Role: "system", Content: result})
					t.updateViewportContent()
				}
				return t, nil
			}
			t.input.SetValue("")
			t.messages = append(t.messages, Message{Role: "user", Content: val})
			t.streaming = true
			a := t.agent
			input := val
			t.updateViewportContent()
			return t, func() tea.Msg {
				res, err := a.Run(context.Background(), input)
				return agentMessage{res: res, err: err}
			}
		}
	case agentMessage:
		t.streaming = false
		if msg.err != nil {
			t.messages = append(t.messages, Message{Role: "error", Content: msg.err.Error()})
		} else {
			t.messages = append(t.messages, Message{Role: "assistant", Content: msg.res})
		}
		t.updateViewportContent()
		return t, nil
	case toolMessage:
		switch msg.eventType {
		case "tool_call":
			arg := firstArg(msg.content)
			display := msg.name
			if arg != "" {
				display = fmt.Sprintf("%s [%s]", msg.name, arg)
			}
			t.messages = append(t.messages, Message{Role: "tool_call", Content: display})
			t.updateViewportContent()
		case "tool_result":
			content := msg.content
			if len(content) > 500 {
				content = content[:500] + "\n... (truncated)"
			}
			t.messages = append(t.messages, Message{Role: "tool_result", Content: content})
			t.updateViewportContent()
		}
	case confirmMessage:
		t.confirming = true
		t.confirmCmd = msg.command
		t.updateViewportContent()
		return t, nil
	}
	var cmd tea.Cmd
	t.input, cmd = t.input.Update(msg)
	var vpCmd tea.Cmd
	t.viewport, vpCmd = t.viewport.Update(msg)
	return t, tea.Batch(cmd, vpCmd)
}

func wrap(text string, width int) string {
	if width <= 0 {
		return text
	}
	var result strings.Builder
	for _, line := range strings.Split(text, "\n") {
		if len(line) <= width {
			result.WriteString(line)
			result.WriteString("\n")
			continue
		}
		words := strings.Fields(line)
		current := 0
		for _, word := range words {
			if current+len(word)+1 > width && current > 0 {
				result.WriteString("\n")
				current = 0
			}
			if current > 0 {
				result.WriteString(" ")
				current++
			}
			result.WriteString(word)
			current += len(word)
		}
		result.WriteString("\n")
	}
	return result.String()
}

func (t TUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch t.state {
	case StateOnboarding:
		return t.updateOnboarding(msg)
	case StateChat:
		return t.updateChat(msg)
	}
	return t, nil
}

func (t TUIModel) viewOnboarding() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(agentStyle.Render("  ◆ Welcome to Pragma"))
	b.WriteString("\n\n")

	switch t.onboardStep {
	case 0:
		b.WriteString("  Which LLM provider?\n\n")
		b.WriteString("  1. OpenRouter (access to many models)\n")
		b.WriteString("  2. OpenAI\n\n")
		b.WriteString(dimStyle.Render("  Type 1 or 2 and press enter"))
		b.WriteString("\n")
	case 1:
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Provider: %s", t.onboardData["provider"])))
		b.WriteString("\n\n")
		b.WriteString("  Enter model name:\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Press enter for default: %s", t.onboardData["default_model"])))
		b.WriteString("\n")
	case 2:
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Provider: %s", t.onboardData["provider"])))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Model: %s", t.onboardData["model"])))
		b.WriteString("\n\n")
		b.WriteString("  Paste your API key:\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Press enter to set %s later", t.onboardData["api_key_var"])))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(t.input.View())
	b.WriteString("\n")
	return b.String()
}

func (t *TUIModel) writeOnboardConfig() {
	os.MkdirAll(".agent", 0755)
	config := fmt.Sprintf(`[model]
provider = "%s"
model_name = "%s"
api_key_var_name = "%s"
tool_mode = "auto"

[behavior]
verbosity = "minimal"
test_policy = "none"
max_output_tokens = 4096
`, t.onboardData["provider"], t.onboardData["model"], t.onboardData["api_key_var"])

	os.WriteFile(".agent/config.toml", []byte(config), 0644)

	if t.onboardData["api_key"] != "" {
		envLine := fmt.Sprintf("%s=%s\n", t.onboardData["api_key_var"], t.onboardData["api_key"])
		f, err := os.OpenFile(".env", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err == nil {
			f.WriteString(envLine)
			f.Close()
		}
	}
}

func (t TUIModel) viewChat() string {
	var b strings.Builder
	b.WriteString(dimStyle.Render("  pragma v1.0.0 -- ctrl+c to quit"))
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(strings.Repeat("-", max(t.width, 40))))
	b.WriteString("\n")
	b.WriteString(t.viewport.View())
	b.WriteString("\n")
	b.WriteString(dimStyle.Render(strings.Repeat("-", max(t.width, 40))))
	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(t.input.View())
	b.WriteString("\n")
	return b.String()
}

func (t TUIModel) View() string {
	switch t.state {
	case StateOnboarding:
		return t.viewOnboarding()
	case StateChat:
		return t.viewChat()
	}
	return ""
}

func Start(a *agent.Agent, setProg func(*tea.Program)) {
	ti := textinput.New()
	ti.Focus()
	ti.Width = 80
	ti.CharLimit = 4096

	vp := viewport.New(80, 20)
	vp.SetContent("")

	confirmChan := make(chan bool)

	state := StateChat
	if a == nil {
		state = StateOnboarding
		ti.Placeholder = ""
	} else {
		ti.Placeholder = "Ask pragma..."
	}

	m := TUIModel{agent: a, input: ti, viewport: vp, width: 80, confirmChan: confirmChan, state: state, onboardData: make(map[string]string)}
	p := tea.NewProgram(m, tea.WithAltScreen())

	if a != nil && a.Registry != nil {
		a.Registry.Confirm = func(toolName string, summary string) bool {
			p.Send(confirmMessage{command: fmt.Sprintf("[%s] %s", toolName, summary)})
			return <-confirmChan
		}

		a.OnEvent = func(event agent.AgentEvent) {
			content := event.Content
			if event.Type == "tool_call" {
				content = event.Args
			}
			p.Send(toolMessage{eventType: event.Type, name: event.Name, content: content})
		}
	}

	if setProg != nil {
		setProg(p)
	}
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
