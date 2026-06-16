package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/AamindMandragora/pragma/internal/agent"
	"github.com/AamindMandragora/pragma/internal/db"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea" // bubbletea is what allows us to create our tui and it's industry standard
	"github.com/charmbracelet/lipgloss"
)

// message styling for different roles
var (
	userStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true)
	agentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	toolStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	errorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	dimStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// messages will just have roles and contents
type Message struct {
	Role    string
	Content string
}

// agent message holds the output of agent.Run
type agentMessage struct {
	res string
	err error
}

// tool message holds the type (call/result/cost), the name of the tool (or cost), and the content
type toolMessage struct {
	eventType string
	name      string
	content   string
}

// confirm messages just hold the summary
type confirmMessage struct {
	command string
}

// either onboarding or chat
type TUIState int

const (
	StateOnboarding TUIState = iota
	StateChat
)

// TUIModel holds an agent, text input, viewport, messages, height and width, state, onboarding steps, data, tiers, whether its streaming or confirming
type TUIModel struct {
	agent        *agent.Agent
	input        textinput.Model
	viewport     viewport.Model
	messages     []Message
	streaming    bool
	width        int
	height       int
	confirming   bool
	confirmCmd   string
	confirmChan  chan bool
	state        TUIState
	onboardStep  int
	onboardData  map[string]string
	onboardTiers []map[string]string
}

// when the model starts we have the textinput cursor blink
func (t *TUIModel) Init() tea.Cmd {
	return textinput.Blink
}

// gets the first arg in the json
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

// renders the messages in the viewport based on their roles, appends streaming or confirming text at the end
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
		case "process_output":
			b.WriteString(dimStyle.Render(fmt.Sprintf("  ⏳ [running] %s", msg.Content)))
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

// holds all the hash commands and executes them
func (t *TUIModel) handleHashCommand(cmd string) string {
	parts := strings.Fields(cmd)
	command := parts[0]

	switch command {
	case "#help":
		return `Available commands:
  #help            — show this message
  #clear           — clear chat history
  #status          — show session info
  #model           — show current model and tool mode
  #switch <model>  — switch to a different model
  #budget [amount] — show or set dollar budget
  #tiers           — show configured model tiers
  #cost            — show token usage and cost
  #docs            — show recent task summaries
  #arch            — show architecture doc
  #sessions        — see list of recent session info
  #undo            — revert to the last checkpoint
  #quit            — exit Pragma`
	case "#clear":
		t.messages = t.messages[:0]
		if len(t.agent.History) > 0 {
			t.agent.History = t.agent.History[:1]
		}
		return "Chat cleared."
	case "#status":
		msgCount := len(t.agent.History) - 1
		return fmt.Sprintf("Messages: %d | Model: %s | Tool mode: %s", msgCount, t.agent.CurrentModel.Name, t.agent.CurrentModel.ToolMode)
	case "#docs":
		recent := agent.LoadRecentDocs(3)
		if recent == "" {
			return "No task docs yet."
		}
		return recent
	case "#arch":
		arch := agent.LoadArchitecture()
		if arch == "" {
			return "No architecture doc yet."
		}
		return arch
	case "#cost":
		if t.agent == nil {
			return "No agent running."
		}
		msg := fmt.Sprintf("Current task: %d in / %d out | Task Cost: $%.4f | Session Cost: $%.4f | Model: '%s'", t.agent.TaskInputTokens, t.agent.TaskOutputTokens, t.agent.TaskCost, t.agent.SessionCost, t.agent.LastModelUsed)
		if t.agent.Budget > 0 {
			pct := (t.agent.SessionCost / t.agent.Budget) * 100
			msg += fmt.Sprintf("\nBudget: $%.2f (%.1f%% used)", t.agent.Budget, pct)
		}
		return msg
	case "#model":
		m := t.agent.CurrentModel
		return fmt.Sprintf("Model: %s\n  Max tokens: %d\n  Tool mode: %s\n  Provider: %s", m.Name, m.MaxTokens, m.ToolMode, m.Provider.GetName())
	case "#switch":
		if len(parts) < 2 {
			return "Usage: #switch <model_name>"
		}
		for _, tier := range t.agent.ModelTiers {
			if strings.Contains(tier.Model.Name, parts[1]) {
				t.agent.CurrentModel = tier.Model
				return fmt.Sprintf("Switched to %s (%s)", tier.Model.Name, tier.Model.ToolMode)
			}
		}
		return fmt.Sprintf("No tier matching '%s'. Type #tiers to see available.", parts[1])
	case "#tiers":
		var out strings.Builder
		for _, tier := range t.agent.ModelTiers {
			marker := "  "
			if tier.Model.Name == t.agent.CurrentModel.Name {
				marker = "→ "
			}
			out.WriteString(fmt.Sprintf("%s%s (%s) at %.0f%%\n", marker, tier.Model.Name, tier.Model.ToolMode, tier.Threshold*100))
		}
		return out.String()
	case "#budget":
		if len(parts) < 2 {
			if t.agent.Budget > 0 {
				pct := (t.agent.SessionCost / t.agent.Budget) * 100
				return fmt.Sprintf("Budget: $%.2f (%.1f%% used)", t.agent.Budget, pct)
			}
			return "No budget set. Usage: #budget <amount>"
		}
		var amount float64
		fmt.Sscanf(parts[1], "%f", &amount)
		if amount <= 0 {
			return "Budget must be positive."
		}
		t.agent.Budget = amount
		return fmt.Sprintf("Budget set to $%.2f", amount)
	case "#sessions":
		var sessions, err = db.ListSessions(10)
		if err != nil {
			return "Error fetching sessions."
		} else if sessions == nil {
			return "No previous sessions."
		} else {
			var out strings.Builder
			out.WriteString("sessions:\n")
			for _, session := range sessions {
				out.WriteString(fmt.Sprintf("\t- %s\t%s\n", session.Id.String(), session.Title))
			}
			return out.String()
		}
	case "#undo":
		cmd := exec.Command("git", "stash", "list")
		data, err := cmd.Output()
		if err != nil {
			return "Error fetching checkpoints."
		}
		stashed := strings.Split(string(data), "\n")
		re := regexp.MustCompile(`^stash@{(?P<number>\d+)}: .*pragma-checkpoint-(?P<timestamp>\d+)$`)
		for _, stash := range stashed {
			matches := re.FindStringSubmatch(stash)
			if len(matches) > 0 {
				idx := matches[re.SubexpIndex("number")]
				ref := fmt.Sprintf("stash@{%s}", idx)
				cmd = exec.Command("git", "checkout", ".")
				if err := cmd.Run(); err != nil {
					return "Failed to discard current changes."
				}
				cmd = exec.Command("git", "clean", "-fd")
				if err := cmd.Run(); err != nil {
					return "Failed to clean untracked files."
				}
				cmd = exec.Command("git", "stash", "apply", ref)
				if err := cmd.Run(); err != nil {
					return "Failed to restore checkpoint."
				}
				cmd = exec.Command("git", "stash", "drop", ref)
				cmd.Run()
				return "Checkpoint restored."
			}
		}
		return "No previous checkpoints."
	case "#quit":
		return "EXIT"
	default:
		return fmt.Sprintf("Unknown command: %s. Type #help for available commands.", command)
	}
}

// the TUIModel's update step during onboarding
func (t *TUIModel) updateOnboarding(msg tea.Msg) (tea.Model, tea.Cmd) {
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
			// selects a provider
			case 0, 4:
				switch val {
				case "2":
					t.onboardData["provider"] = "openai"
					t.onboardData["api_key_var"] = "OPENAI_API_KEY"
					t.onboardData["default_model"] = "gpt-5.4-mini"
				case "3":
					t.onboardData["provider"] = "anthropic"
					t.onboardData["api_key_var"] = "ANTHROPIC_API_KEY"
					t.onboardData["default_model"] = "claude-3-5-sonnet-latest"
				default:
					t.onboardData["provider"] = "openrouter"
					t.onboardData["api_key_var"] = "OPENROUTER_API_KEY"
					t.onboardData["default_model"] = "qwen/qwen3-coder:free"
				}
				t.onboardStep++
				t.input.Placeholder = "Model name [" + t.onboardData["default_model"] + "]"

			// selects a model
			case 1, 5:
				if val == "" {
					val = t.onboardData["default_model"]
				}
				t.onboardData["model"] = val
				t.onboardStep++
				// if it's the first model, ask for the api key, otherwise the threshold fraction for when we fall back to it
				if t.onboardStep == 2 {
					t.input.Placeholder = "Paste API key (or enter to skip)"
				} else {
					t.input.Placeholder = "Fallback cost threshold fraction e.g. 0.5 [0.5]"
				}

			// sets the api key and 0.0 threshold for our first model, then add it to the tiers
			case 2:
				t.onboardData["api_key"] = val
				t.onboardData["threshold"] = "0.0"
				t.onboardTiers = append(t.onboardTiers, t.onboardData)
				t.onboardData = make(map[string]string)
				t.onboardStep = 3
				t.input.Placeholder = "y/n [n]"

			// if we want a fallback model, goes to step 4, otherwise finishes onboarding
			case 3:
				if strings.ToLower(val) == "y" || strings.ToLower(val) == "yes" {
					t.onboardStep = 4
					t.input.Placeholder = "Type 1, 2 or 3"
				} else {
					t.writeOnboardConfig()
					return t, tea.Quit
				}

			// chooses the threshold fraction for the fallback model and asks for its api key
			case 6:
				if val == "" {
					val = "0.5"
				}
				t.onboardData["threshold"] = val
				t.onboardStep = 7
				t.input.Placeholder = "Paste fallback API key (or enter to skip)"

			// gets the fallback model api key
			case 7:
				t.onboardData["api_key"] = val
				t.onboardTiers = append(t.onboardTiers, t.onboardData)
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

// the TUIModel's update step during chat
func (t *TUIModel) updateChat(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		// if we're confirming treat the message as a yes or no
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
			// if it starts with a hash handle the command and update the messages accordingly
			if strings.HasPrefix(val, "#") {
				t.input.SetValue("")
				result := t.handleHashCommand(val)
				if result == "EXIT" {
					return t, tea.Quit
				}
				if result != "" {
					t.messages = append(t.messages, Message{Role: "system", Content: result})
					t.updateViewportContent()
				}
				return t, nil
			}
			// otherwise it's a message to agent, return the command that will run the agent
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
		// sends an error message if one exists and an assistant message otherwise
		if msg.err != nil {
			t.messages = append(t.messages, Message{Role: "error", Content: msg.err.Error()})
		} else {
			t.messages = append(t.messages, Message{Role: "assistant", Content: msg.res})
		}
		t.updateViewportContent()
		return t, nil
	case toolMessage:
		// if it's a tool call display the name and first arg, if its a tool result show the text for that, if its a cost then just append normally, if its a process output then either overwrite the last one or append it as a new line
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
			// remove the live output indicator if present
			if len(t.messages) > 0 && t.messages[len(t.messages)-1].Role == "process_output" {
				t.messages = t.messages[:len(t.messages)-1]
			}
			content := msg.content
			if len(content) > 500 {
				content = content[:500] + "\n... (truncated)"
			}
			t.messages = append(t.messages, Message{Role: "tool_result", Content: content})
			t.updateViewportContent()
		case "cost":
			t.messages = append(t.messages, Message{Role: "system", Content: msg.content})
			t.updateViewportContent()
		case "process_output":
			if len(t.messages) > 0 && t.messages[len(t.messages)-1].Role == "process_output" {
				t.messages[len(t.messages)-1].Content = msg.content
			} else {
				t.messages = append(t.messages, Message{Role: "process_output", Content: msg.content})
			}
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

// inserts newlines in the text based on width to wrap line
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

// update wrapper function that delegates to onboarding or chat depending on state
func (t *TUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch t.state {
	case StateOnboarding:
		return t.updateOnboarding(msg)
	case StateChat:
		return t.updateChat(msg)
	}
	return t, nil
}

// the display during onboarding
func (t *TUIModel) viewOnboarding() string {
	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(agentStyle.Render("  ◆ Welcome to Pragma Setup"))
	b.WriteString("\n\n")

	switch t.onboardStep {
	case 0:
		b.WriteString("  [1/2] Select primary LLM provider:\n\n")
		b.WriteString("  1. OpenRouter (Access any model tier)\n")
		b.WriteString("  2. OpenAI\n")
		b.WriteString("  3. Anthropic\n\n")
		b.WriteString(dimStyle.Render("  Type 1, 2, or 3 and hit Enter"))
		b.WriteString("\n")
	case 1:
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Provider Chosen: %s", t.onboardData["provider"])))
		b.WriteString("\n\n")
		b.WriteString("  Enter primary model ID string:\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Press enter for default: %s", t.onboardData["default_model"])))
		b.WriteString("\n")
	case 2:
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Provider Chosen: %s", t.onboardData["provider"])))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Model Chosen: %s", t.onboardData["model"])))
		b.WriteString("\n\n")
		b.WriteString("  Paste your primary API key string:\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Press enter to specify %s in your context environment later", t.onboardData["api_key_var"])))
		b.WriteString("\n")
	case 3:
		b.WriteString(agentStyle.Render("  ✔ Primary Tier Configured Successfully!"))
		b.WriteString("\n\n")
		b.WriteString("  Would you like to configure a secondary/cheaper fallback model tier?\n")
		b.WriteString(dimStyle.Render("  Type y/n (Defaults to no)"))
		b.WriteString("\n")
	case 4:
		b.WriteString("  [2/2] Select fallback LLM provider:\n\n")
		b.WriteString("  1. OpenRouter\n")
		b.WriteString("  2. OpenAI\n")
		b.WriteString("  3. Anthropic\n\n")
		b.WriteString(dimStyle.Render("  Type 1, 2, or 3 and hit Enter"))
		b.WriteString("\n")
	case 5:
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Fallback Provider Chosen: %s", t.onboardData["provider"])))
		b.WriteString("\n\n")
		b.WriteString("  Enter fallback model ID string:\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Press enter for default: %s", t.onboardData["default_model"])))
		b.WriteString("\n")
	case 6:
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Fallback Provider Chosen: %s", t.onboardData["provider"])))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Fallback Model Chosen: %s", t.onboardData["model"])))
		b.WriteString("\n\n")
		b.WriteString("  At what percentage of total dollar budget should we downgrade to this model?\n")
		b.WriteString(dimStyle.Render("  Enter decimal between 0.0 and 1.0 (e.g. 0.5 = 50% budget consumed)"))
		b.WriteString("\n")
	case 7:
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Fallback Provider Chosen: %s", t.onboardData["provider"])))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Fallback Model Chosen: %s", t.onboardData["model"])))
		b.WriteString("\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Fallback Switch Point: %s", t.onboardData["threshold"])))
		b.WriteString("\n\n")
		b.WriteString("  Paste your fallback API key string:\n")
		b.WriteString(dimStyle.Render(fmt.Sprintf("  Press enter to look up %s out of environment variables later", t.onboardData["api_key_var"])))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString("  ")
	b.WriteString(t.input.View())
	b.WriteString("\n")
	return b.String()
}

// writes the config created through onboarding to .agents/config.toml
func (t *TUIModel) writeOnboardConfig() {
	os.MkdirAll(".agent", 0755)

	var tiersBuilder strings.Builder
	for _, tier := range t.onboardTiers {
		tiersBuilder.WriteString("[[model.tiers]]\n")
		tiersBuilder.WriteString(fmt.Sprintf("model = \"%s\"\n", tier["model"]))
		tiersBuilder.WriteString(fmt.Sprintf("provider = \"%s\"\n", tier["provider"]))
		tiersBuilder.WriteString(fmt.Sprintf("api_key_var_name = \"%s\"\n", tier["api_key_var"]))
		tiersBuilder.WriteString(fmt.Sprintf("threshold = %s\n\n", tier["threshold"]))
	}

	config := fmt.Sprintf(`%s[behavior]
verbosity = "minimal"
test_policy = "none"
max_output_tokens = 4096
`, tiersBuilder.String())

	os.WriteFile(".agent/config.toml", []byte(config), 0644)

	// writes all the api keys to a .env
	f, err := os.OpenFile(".env", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err == nil {
		defer f.Close()
		for _, tier := range t.onboardTiers {
			if tier["api_key"] != "" {
				envLine := fmt.Sprintf("%s=%s\n", tier["api_key_var"], tier["api_key"])
				f.WriteString(envLine)
			}
		}
	}
}

// the display during chatting
func (t *TUIModel) viewChat() string {
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

// wrapper function that delegates to onboarding or chat
func (t *TUIModel) View() string {
	switch t.state {
	case StateOnboarding:
		return t.viewOnboarding()
	case StateChat:
		return t.viewChat()
	}
	return ""
}

// starts the tui
func Start(a *agent.Agent) {
	// creates and focuses a text input, sets its width and max input chars
	ti := textinput.New()
	ti.Focus()
	ti.Width = 80
	ti.CharLimit = 4096

	// creates a blank viewpoint
	vp := viewport.New(80, 20)
	vp.SetContent("")

	// channel that holds the confirm events
	confirmChan := make(chan bool)

	// if a nil model was passed in we're onboarding otherwise chatting
	state := StateChat
	if a == nil {
		state = StateOnboarding
		ti.Placeholder = "Type 1, 2 or 3"
	} else {
		ti.Placeholder = "Ask pragma..."
	}

	// creates the tui model with all the params
	m := TUIModel{
		agent:        a,
		input:        ti,
		viewport:     vp,
		width:        80,
		confirmChan:  confirmChan,
		state:        state,
		onboardData:  make(map[string]string),
		onboardTiers: []map[string]string{},
	}

	// renders messages already in the history from a resumed session
	if a != nil && len(a.History) > 1 {
		for _, msg := range a.History[1:] {
			switch msg.Role {
			case "user":
				m.messages = append(m.messages, Message{Role: "user", Content: msg.Content})
			case "assistant":
				m.messages = append(m.messages, Message{Role: "assistant", Content: msg.Content})
			case "tool":
				content := msg.Content
				if len(content) > 500 {
					content = content[:500] + "\n... (truncated)"
				}
				m.messages = append(m.messages, Message{Role: "tool_result", Content: content})
			}
		}
	}

	// runs the model as a bubbletea program
	p := tea.NewProgram(&m, tea.WithAltScreen())

	// if we have an agent and tools then we create the confirm function that just sends the message to the user asking yes or no
	if a != nil && a.Registry != nil {
		a.Registry.Confirm = func(toolName string, summary string) bool {
			p.Send(confirmMessage{command: fmt.Sprintf("[%s] %s", toolName, summary)})
			return <-confirmChan
		}
		// whenever the agent emits an event, if it's a tool call, send the tool message to the tui
		a.OnEvent = func(event agent.AgentEvent) {
			content := event.Content
			if event.Type == "tool_call" {
				content = event.Args
			}
			p.Send(toolMessage{eventType: event.Type, name: event.Name, content: content})
		}
		// whenever the process manager receives a new line, send it to the tui
		var lastSend time.Time
		a.Manager.OnOutput = func(line string) {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				return
			}
			now := time.Now()
			if now.Sub(lastSend) > 100*time.Millisecond {
				lastSend = now
				p.Send(toolMessage{eventType: "process_output", content: trimmed})
			}
		}
	}

	// runs the bubbletea program and prints any error
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
