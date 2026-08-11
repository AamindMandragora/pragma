package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"image/color"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/AamindMandragora/pragma/internal/agent"
	"github.com/AamindMandragora/pragma/internal/db"
	"github.com/AamindMandragora/pragma/internal/llm/catalog"
)

// The shell deliberately owns one set of visual tokens. Components use these
// styles directly, which keeps the interface legible when a terminal removes
// color and makes a theme change a one-file edit.
var (
	// The terminal owns the background. These colors are deliberately limited
	// to text, borders, and state accents so the shell can sit naturally inside
	// any terminal theme.
	colorBorder  = lipgloss.Color("#A957B5")
	colorText    = lipgloss.Color("#F5EFFA")
	colorMuted   = lipgloss.Color("#A99BB5")
	colorSubtle  = lipgloss.Color("#75677F")
	colorPurple  = lipgloss.Color("#C084FC")
	colorPink    = lipgloss.Color("#F0A0D0")
	colorMagenta = lipgloss.Color("#E879F9")
	colorSuccess = lipgloss.Color("#86EFAC")
	colorWarning = lipgloss.Color("#FDE68A")
	colorError   = lipgloss.Color("#FDA4AF")

	panelStyle    = lipgloss.NewStyle().Foreground(colorText).BorderForeground(colorBorder)
	focusedPanel  = panelStyle.Copy().BorderForeground(colorPink)
	headerStyle   = lipgloss.NewStyle().Foreground(colorText)
	sectionTitle  = lipgloss.NewStyle().Foreground(colorPink).Bold(true)
	lineStyle     = lipgloss.NewStyle().Foreground(colorBorder)
	mutedStyle    = lipgloss.NewStyle().Foreground(colorMuted)
	subtleStyle   = lipgloss.NewStyle().Foreground(colorSubtle)
	keyHintStyle  = lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	userMessage   = lipgloss.NewStyle().Foreground(colorPink).Bold(true)
	agentMessage  = lipgloss.NewStyle().Foreground(colorMagenta).Bold(true)
	toolRunning   = lipgloss.NewStyle().Foreground(colorPurple).Bold(true)
	toolSuccess   = lipgloss.NewStyle().Foreground(colorSuccess).Bold(true)
	toolFailure   = lipgloss.NewStyle().Foreground(colorError).Bold(true)
	approvalStyle = lipgloss.NewStyle().Foreground(colorWarning).Bold(true)
	composerStyle = lipgloss.NewStyle().Foreground(colorText)
	ansiEscape    = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]`)
)

const (
	appName       = "PRAGMA"
	appVersion    = "1.0.0"
	defaultWidth  = 80
	defaultHeight = 24

	keyTogglePlan     = "alt+p"
	keyCommandPalette = "alt+k"
	keyInspectorPlan  = "alt+shift+p"
	keyInspectorFiles = "alt+f"
	keyInspectorTools = "alt+t"
	keyInspectorGit   = "alt+g"
	keyHelp           = "f1"
	keyHelpAlt        = "alt+h"
	keyQuit           = "alt+c"
	keyStop           = "alt+s"
	keyExpand         = "alt+o"
	keyFiles          = "alt+d"
	keyRetry          = "alt+r"
	keyApprove        = "alt+y"
	keyApproveSession = "alt+a"
	keyReject         = "alt+n"
	keyEdit           = "alt+e"
	maxScroll         = 1 << 30
)

// EventType identifies a record in the conversation timeline.
type EventType string

const (
	EventUser       EventType = "user"
	EventAgent      EventType = "agent"
	EventPlan       EventType = "plan"
	EventTool       EventType = "tool"
	EventToolOutput EventType = "tool_output"
	EventApproval   EventType = "approval"
	EventFile       EventType = "file"
	EventDiff       EventType = "diff"
	EventError      EventType = "error"
	EventSystem     EventType = "system"
)

// EventStatus is intentionally textual as well as colored. Symbols and labels
// remain meaningful in monochrome terminals.
type EventStatus string

const (
	StatusPending EventStatus = "pending"
	StatusRunning EventStatus = "running"
	StatusDone    EventStatus = "done"
	StatusFailed  EventStatus = "failed"
	StatusWaiting EventStatus = "waiting"
)

// TimelineEvent is the stable, renderable unit of conversation history.
// Structured metadata is kept alongside body text so inspectors can link back
// to tools and files without parsing rendered strings.
type TimelineEvent struct {
	ID          string
	At          time.Time
	Type        EventType
	Status      EventStatus
	Title       string
	Body        string
	ToolName    string
	RelatedFile string
	Expanded    bool
}

// Typed messages are the boundary between asynchronous agent/tool work and
// the Bubble Tea update loop.
type (
	AgentTokenMsg struct {
		EventID string
		Token   string
	}
	AgentTurnFinishedMsg struct {
		Response string
		Err      error
	}
	ToolStartedMsg struct {
		ID      string
		Name    string
		Command string
		Summary string
	}
	ToolOutputMsg struct {
		ID     string
		Name   string
		Chunk  string
		Stderr bool
	}
	ToolFinishedMsg struct {
		ID       string
		Name     string
		Output   string
		ExitCode int
		Err      error
		Duration time.Duration
	}
	ApprovalRequestedMsg struct {
		Command   string
		Directory string
		Files     []string
		Network   bool
		Risk      string
		Reason    string
		Response  chan bool
	}
	DiffUpdatedMsg struct {
		File string
		Diff string
	}
	GitStatusMsg struct {
		Branch string
		Path   string
		Files  []ChangedFile
		Err    error
	}
	ContextUsageMsg struct {
		Used  int
		Limit int
	}
	AgentErrorMsg struct {
		Err error
	}
	TimelineEventMsg struct {
		Event TimelineEvent
	}
	HashCommandMsg struct {
		Command string
	}
	PlanGeneratedMsg struct {
		Plan *agent.Plan
		Raw  string
		Err  error
	}
	PlanStepResultMsg struct {
		StepIndex int
		Result    agent.StepResult
	}
	PlanCompleteMsg struct {
		Plan      *agent.Plan
		Files     []ChangedFile
		FileCount int
		TotalCost float64
	}
)

type TUIState int

const (
	StateOnboarding TUIState = iota
	StateChat
)

type FocusArea int

const (
	FocusComposer FocusArea = iota
	FocusTimeline
	FocusPlan
	FocusInspector
)

type InspectorTab string

const (
	InspectorPlan  InspectorTab = "plan"
	InspectorFiles InspectorTab = "files"
	InspectorTools InspectorTab = "tools"
	InspectorGit   InspectorTab = "git"
)

type RunState string

const (
	RunIdle     RunState = "idle"
	RunWorking  RunState = "working"
	RunApproval RunState = "approval"
	RunError    RunState = "error"
)

type PlanStep struct {
	Title  string
	Status EventStatus
}

type ChangedFile struct {
	Status  string
	Path    string
	Added   int
	Removed int
}

// Editor is a small multiline editor used by both onboarding and the chat
// composer. It intentionally has no I/O of its own; the parent model decides
// what Enter means.
type Editor struct {
	value       []rune
	cursor      int
	Width       int
	Placeholder string
	Focused     bool
}

// keyName preserves modifier information for printable keys. Bubble Tea's
// String method intentionally returns only the text (for example, "p" for
// Alt+P when Text is populated), so modified shortcuts must use Keystroke.
func keyName(key tea.KeyPressMsg) string {
	if key.Mod != 0 {
		return key.Keystroke()
	}
	return key.String()
}

func (e *Editor) Value() string { return string(e.value) }

func (e *Editor) SetValue(value string) {
	e.value = []rune(value)
	e.cursor = len(e.value)
}

func (e *Editor) Reset() {
	e.value = nil
	e.cursor = 0
}

func (e *Editor) insert(value string) {
	runes := []rune(value)
	if len(runes) == 0 {
		return
	}
	e.value = append(e.value[:e.cursor], append(runes, e.value[e.cursor:]...)...)
	e.cursor += len(runes)
}

func (e *Editor) Update(msg tea.Msg) {
	if !e.Focused {
		return
	}
	if paste, ok := msg.(tea.PasteMsg); ok {
		e.insert(strings.ReplaceAll(paste.Content, "\r\n", "\n"))
		return
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return
	}
	switch keyName(key) {
	case "backspace":
		if e.cursor > 0 {
			e.value = append(e.value[:e.cursor-1], e.value[e.cursor:]...)
			e.cursor--
		}
	case "delete":
		if e.cursor < len(e.value) {
			e.value = append(e.value[:e.cursor], e.value[e.cursor+1:]...)
		}
	case "left":
		if e.cursor > 0 {
			e.cursor--
		}
	case "right":
		if e.cursor < len(e.value) {
			e.cursor++
		}
	case "home":
		for e.cursor > 0 && e.value[e.cursor-1] != '\n' {
			e.cursor--
		}
	case "end":
		for e.cursor < len(e.value) && e.value[e.cursor] != '\n' {
			e.cursor++
		}
	case "alt+a":
		e.cursor = 0
	case "alt+e":
		for e.cursor < len(e.value) {
			if e.value[e.cursor] == '\n' {
				break
			}
			e.cursor++
		}
	case "alt+u":
		e.Reset()
	case "alt+w":
		for e.cursor > 0 && e.value[e.cursor-1] == ' ' {
			e.cursor--
		}
		for e.cursor > 0 && e.value[e.cursor-1] != ' ' && e.value[e.cursor-1] != '\n' {
			e.cursor--
		}
	case "shift+enter", "alt+enter":
		e.insert("\n")
	default:
		if key.Text != "" && key.Mod == 0 {
			e.insert(key.Text)
		}
	}
}

func (e *Editor) View() string {
	width := max(e.Width, 8)
	if len(e.value) == 0 {
		cursor := "▌"
		if !e.Focused {
			cursor = ""
		}
		return mutedStyle.Render(e.Placeholder) + cursor
	}
	before := string(e.value[:e.cursor])
	after := string(e.value[e.cursor:])
	text := before + "▌" + after
	return lipgloss.Wrap(text, width, " ")
}

// TUIModel is the root shell. Agent and process callbacks only send typed
// messages into this model; rendering never waits on external work.
type TUIModel struct {
	agent        *agent.Agent
	input        Editor
	state        TUIState
	width        int
	height       int
	focus        FocusArea
	runState     RunState
	streaming    bool
	runCancel    context.CancelFunc
	lastPrompt   string
	events       []TimelineEvent
	eventSeq     int
	selected     int
	conversation int
	followTail   bool
	// timelineScroll is an absolute physical-line offset from the top of the
	// conversation. Keeping it absolute prevents streaming tool output at the
	// tail from moving a user who is reading older messages.
	timelineScroll     int
	timelineMaxOffset  int
	plan               []PlanStep
	planCursor         int
	inspectorTab       InspectorTab
	showNarrow         bool
	changedFiles       []ChangedFile
	branch             string
	projectPath        string
	contextUsed        int
	contextLimit       int
	menu               *Menu
	confirming         bool
	confirmCmd         string
	confirmInfo        ApprovalRequestedMsg
	confirmChan        chan bool
	asking             bool
	askTried           []string
	askProblem         string
	askQuestion        string
	askChan            chan string
	onboardStep        int
	onboardData        map[string]string
	onboardTiers       []map[string]string
	planMode           bool
	activePlan         *agent.Plan
	planApproval       bool
	planRunning        bool
	planCostStart      float64
	planScroll         int
	planApprovalScroll int
}

func NewModel(a *agent.Agent) *TUIModel {
	m := &TUIModel{
		agent:        a,
		state:        StateChat,
		width:        defaultWidth,
		height:       defaultHeight,
		focus:        FocusComposer,
		runState:     RunIdle,
		followTail:   true,
		inspectorTab: InspectorPlan,
		projectPath:  workingDirectory(),
		input:        Editor{Width: defaultWidth - 6, Placeholder: "Ask the agent…", Focused: true},
		confirmChan:  make(chan bool, 1),
		askChan:      make(chan string, 1),
		onboardData:  make(map[string]string),
	}
	if a == nil {
		m.state = StateOnboarding
		m.input.Placeholder = "Type 1, 2 or 3"
	}
	return m
}

func (m *TUIModel) Init() tea.Cmd {
	if m.state == StateOnboarding {
		return nil
	}
	return gitStatusCmd()
}

func (m *TUIModel) nextID(prefix string) string {
	m.eventSeq++
	return fmt.Sprintf("%s-%d", prefix, m.eventSeq)
}

func (m *TUIModel) appendEvent(event TimelineEvent) {
	if event.ID == "" {
		event.ID = m.nextID("event")
	}
	if event.At.IsZero() {
		event.At = time.Now()
	}
	if event.Status == "" {
		event.Status = StatusDone
	}
	event.Body = sanitizeTerminal(event.Body)
	m.events = append(m.events, event)
	if m.followTail {
		m.conversation = max(0, len(m.events)-1)
	}
}

func (m *TUIModel) eventByID(id string) *TimelineEvent {
	for i := range m.events {
		if m.events[i].ID == id {
			return &m.events[i]
		}
	}
	return nil
}

func (m *TUIModel) latestTool(name string) *TimelineEvent {
	for i := len(m.events) - 1; i >= 0; i-- {
		if m.events[i].Type == EventTool && (name == "" || m.events[i].ToolName == name) && m.events[i].Status == StatusRunning {
			return &m.events[i]
		}
	}
	return nil
}

func (m *TUIModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.menu != nil {
		if key, ok := msg.(tea.KeyPressMsg); ok {
			done, cmd := m.menu.HandleKey(key)
			if done {
				m.menu = nil
			}
			return m, cmd
		}
		if wheel, ok := msg.(tea.MouseWheelMsg); ok {
			switch wheel.Button {
			case tea.MouseWheelUp:
				m.menu.Move(-1)
			case tea.MouseWheelDown:
				m.menu.Move(1)
			}
			return m, nil
		}
		return m, nil
	}
	if m.state == StateOnboarding {
		return m.updateOnboarding(msg)
	}
	return m.updateChat(msg)
}

func (m *TUIModel) updateChat(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(0, msg.Width), max(0, msg.Height)
		m.resize()
		return m, nil
	case TimelineEventMsg:
		m.appendEvent(msg.Event)
		return m, nil
	case GitStatusMsg:
		m.branch, m.changedFiles = msg.Branch, msg.Files
		if msg.Path != "" {
			m.projectPath = msg.Path
		}
		if msg.Err != nil {
			m.appendEvent(TimelineEvent{Type: EventSystem, Status: StatusFailed, Title: "GIT STATUS", Body: msg.Err.Error()})
		}
		return m, nil
	case ContextUsageMsg:
		m.contextUsed, m.contextLimit = msg.Used, msg.Limit
		return m, nil
	case AgentTokenMsg:
		m.streaming = true
		m.runState = RunWorking
		event := m.eventByID(msg.EventID)
		if event == nil {
			event = &TimelineEvent{ID: msg.EventID, At: time.Now(), Type: EventAgent, Status: StatusRunning, Title: "PRAGMA", Expanded: true}
			m.appendEvent(*event)
			event = m.eventByID(msg.EventID)
		}
		if event != nil {
			event.Body += sanitizeTerminal(msg.Token)
		}
		return m, nil
	case AgentTurnFinishedMsg:
		m.streaming, m.runState = false, RunIdle
		if m.runCancel != nil {
			m.runCancel()
			m.runCancel = nil
		}
		if msg.Err != nil {
			m.runState = RunError
			m.appendEvent(TimelineEvent{Type: EventError, Status: StatusFailed, Title: "FAILED", Body: msg.Err.Error()})
		} else if strings.TrimSpace(msg.Response) != "" {
			m.appendEvent(TimelineEvent{Type: EventAgent, Status: StatusDone, Title: "PRAGMA", Body: msg.Response})
		}
		return m, nil
	case AgentErrorMsg:
		m.streaming, m.runState = false, RunError
		if msg.Err != nil {
			m.appendEvent(TimelineEvent{Type: EventError, Status: StatusFailed, Title: "FAILED", Body: msg.Err.Error()})
		}
		return m, nil
	case ToolStartedMsg:
		m.streaming, m.runState = true, RunWorking
		id := msg.ID
		if id == "" {
			id = m.nextID("tool")
		}
		body := msg.Summary
		if body == "" {
			body = msg.Command
		}
		m.appendEvent(TimelineEvent{ID: id, Type: EventTool, Status: StatusRunning, Title: msg.Name, Body: body, ToolName: msg.Name, Expanded: true})
		return m, nil
	case ToolOutputMsg:
		event := m.eventByID(msg.ID)
		if event == nil {
			event = m.latestTool(msg.Name)
		}
		if event != nil {
			event.Body = capOutput(event.Body + sanitizeTerminal(msg.Chunk))
		}
		return m, nil
	case ToolFinishedMsg:
		event := m.eventByID(msg.ID)
		if event == nil {
			event = m.latestTool(msg.Name)
		}
		if event == nil {
			id := msg.ID
			if id == "" {
				id = m.nextID("tool")
			}
			event = &TimelineEvent{ID: id, Type: EventTool, Title: msg.Name, ToolName: msg.Name}
			m.appendEvent(*event)
			event = m.eventByID(id)
		}
		if event != nil {
			event.Status = StatusDone
			if msg.Err != nil || msg.ExitCode != 0 {
				event.Status = StatusFailed
			}
			if msg.Output != "" {
				event.Body = capOutput(msg.Output)
			}
		}
		return m, nil
	case ApprovalRequestedMsg:
		m.confirming, m.runState = true, RunApproval
		m.confirmInfo, m.confirmCmd = msg, msg.Command
		m.appendEvent(TimelineEvent{Type: EventApproval, Status: StatusWaiting, Title: "APPROVAL REQUIRED", Body: msg.Command})
		if msg.Response != nil {
			m.confirmChan = msg.Response
		}
		return m, nil
	case DiffUpdatedMsg:
		m.inspectorTab = InspectorFiles
		m.appendEvent(TimelineEvent{Type: EventDiff, Status: StatusDone, Title: "DIFF AVAILABLE", Body: msg.Diff, RelatedFile: msg.File, Expanded: true})
		return m, nil
	case HashCommandMsg:
		return m, m.handleHashInput(msg.Command)
	case askUserMessage:
		m.asking = true
		m.askTried, m.askProblem, m.askQuestion = msg.tried, msg.problem, msg.question
		if msg.response != nil {
			m.askChan = msg.response
		}
		m.input.Placeholder = "Answer the agent’s question…"
		return m, nil
	case PlanGeneratedMsg:
		m.streaming, m.runState = false, RunIdle
		if m.runCancel != nil {
			m.runCancel()
			m.runCancel = nil
		}
		if msg.Err != nil {
			m.appendEvent(TimelineEvent{Type: EventError, Status: StatusFailed, Title: "PLAN GENERATION FAILED", Body: msg.Err.Error()})
			if msg.Raw != "" {
				m.appendEvent(TimelineEvent{Type: EventAgent, Status: StatusDone, Title: "RAW RESPONSE", Body: msg.Raw})
			}
			// Keep an existing plan editable when $EDITOR returns invalid JSON
			// or exits with an error. Initial generation has no active plan, so
			// this does not create a misleading approval prompt for new plans.
			if m.activePlan != nil && !m.planRunning {
				m.planApproval = true
			}
			return m, nil
		}
		if msg.Plan == nil {
			m.appendEvent(TimelineEvent{Type: EventError, Status: StatusFailed, Title: "PLAN GENERATION FAILED", Body: "The agent returned an empty plan."})
			return m, nil
		}
		m.activePlan = msg.Plan
		m.planApproval = true
		m.planScroll = 0
		m.planApprovalScroll = 0
		m.plan = planToTUISteps(msg.Plan)
		m.appendEvent(TimelineEvent{Type: EventPlan, Status: StatusDone, Title: "PLAN GENERATED", Body: msg.Plan.Format()})
		return m, nil
	case PlanStepResultMsg:
		if !m.planRunning || m.activePlan == nil || msg.StepIndex < 0 || msg.StepIndex >= len(m.activePlan.Steps) {
			return m, nil
		}
		step := &m.activePlan.Steps[msg.StepIndex]
		m.plan = planToTUISteps(m.activePlan)
		for _, warn := range msg.Result.ScopeWarns {
			m.appendEvent(TimelineEvent{Type: EventSystem, Status: StatusDone, Title: "SCOPE WARNING", Body: warn})
		}
		statusTitle := "PASSED"
		evStatus := StatusDone
		if !msg.Result.Passed {
			statusTitle = "FAILED"
			evStatus = StatusFailed
		}
		title := fmt.Sprintf("STEP %d %s", msg.StepIndex+1, statusTitle)
		body := step.Description
		if msg.Result.Err != nil {
			body += "\nError: " + msg.Result.Err.Error()
		}
		if msg.Result.Retries > 0 {
			body += fmt.Sprintf("\nRetries: %d", msg.Result.Retries)
		}
		m.appendEvent(TimelineEvent{Type: EventPlan, Status: evStatus, Title: title, Body: body})
		return m, m.runPlanStep(msg.StepIndex + 1)
	case PlanCompleteMsg:
		m.streaming, m.runState = false, RunIdle
		if m.runCancel != nil {
			m.runCancel()
			m.runCancel = nil
		}
		m.planRunning = false
		if msg.Files != nil {
			m.changedFiles = msg.Files
		}
		summary := "Plan finished."
		if msg.Plan != nil {
			summary = msg.Plan.Summary()
			for i, step := range msg.Plan.Steps {
				summary += fmt.Sprintf("\nStep %d: %s — %s", i+1, strings.ToUpper(string(step.Status)), step.Description)
			}
		}
		fileCount := msg.FileCount
		if msg.Files != nil {
			fileCount = len(msg.Files)
		}
		summary += fmt.Sprintf("\nFiles changed: %d", fileCount)
		summary += fmt.Sprintf("\nTotal cost: $%.4f", msg.TotalCost)
		m.appendEvent(TimelineEvent{Type: EventPlan, Status: StatusDone, Title: "PLAN COMPLETE", Body: summary})
		m.activePlan = nil
		m.plan = nil
		m.planScroll = 0
		m.planApprovalScroll = 0
		return m, nil
	case tea.KeyPressMsg:
		return m, m.handleKey(msg)
	case tea.MouseWheelMsg:
		if m.planApproval {
			if msg.Button == tea.MouseWheelUp {
				m.planApprovalScroll = max(0, m.planApprovalScroll-1)
			} else if msg.Button == tea.MouseWheelDown {
				m.planApprovalScroll++
			}
			return m, nil
		}
		if m.confirming {
			if msg.Button == tea.MouseWheelUp {
				m.scrollTimeline(-1)
			} else if msg.Button == tea.MouseWheelDown {
				m.scrollTimeline(1)
			}
			return m, nil
		}
		switch msg.Button {
		case tea.MouseWheelUp:
			m.scrollFocused(-1)
		case tea.MouseWheelDown:
			m.scrollFocused(1)
		}
		return m, nil
	}
	if m.focus == FocusComposer {
		m.input.Update(msg)
	}
	return m, nil
}

type askUserMessage struct {
	tried             []string
	problem, question string
	response          chan string
}

func (m *TUIModel) handleKey(key tea.KeyPressMsg) tea.Cmd {
	name := keyName(key)
	if m.confirming {
		if name == "home" {
			m.followTail = false
			m.timelineScroll = 0
			return nil
		}
		if name == "end" {
			m.followTail = true
			m.timelineScroll = 0
			return nil
		}
		if delta, ok := timelineScrollDelta(name); ok {
			m.scrollTimeline(delta)
			return nil
		}
	}
	if m.asking {
		if name == "home" {
			m.followTail = false
			m.timelineScroll = 0
			return nil
		}
		if name == "end" {
			m.followTail = true
			m.timelineScroll = 0
			return nil
		}
		if delta, ok := timelineScrollDelta(name); ok {
			m.scrollTimeline(delta)
			return nil
		}
	}
	if m.confirming {
		switch name {
		case keyApprove:
			m.confirming, m.runState = false, RunWorking
			m.confirmChan <- true
		case keyApproveSession:
			m.confirming, m.runState = false, RunWorking
			m.confirmChan <- true
		case keyReject, "esc":
			m.confirming, m.runState = false, RunWorking
			m.confirmChan <- false
		case keyEdit:
			m.input.SetValue(m.confirmCmd)
			m.input.Focused = true
			m.confirming, m.runState = false, RunWorking
		}
		return nil
	}
	if m.planApproval {
		switch name {
		case "up":
			m.planApprovalScroll = max(0, m.planApprovalScroll-1)
			return nil
		case "down":
			m.planApprovalScroll++
			return nil
		case "pgup":
			m.planApprovalScroll = max(0, m.planApprovalScroll-5)
			return nil
		case "pgdown":
			m.planApprovalScroll += 5
			return nil
		case "home":
			m.planApprovalScroll = 0
			return nil
		case "end":
			m.planApprovalScroll = maxScroll
			return nil
		case keyApprove:
			m.planApproval = false
			m.planApprovalScroll = 0
			m.appendEvent(TimelineEvent{Type: EventSystem, Status: StatusDone, Title: "PLAN APPROVED", Body: "Executing plan…"})
			return m.startPlanExecution()
		case keyEdit:
			m.planApproval = false
			m.planApprovalScroll = 0
			return m.editPlanInEditor()
		case keyReject, "esc":
			m.planApproval = false
			m.planApprovalScroll = 0
			m.activePlan = nil
			m.plan = nil
			m.planScroll = 0
			m.appendEvent(TimelineEvent{Type: EventSystem, Status: StatusDone, Title: "PLAN REJECTED", Body: "Plan rejected."})
		}
		return nil
	}
	if name == keyQuit {
		if m.streaming {
			return m.stopRun()
		}
		return tea.Quit
	}
	if name == keyTogglePlan {
		return m.handleHashInput("#plan")
	}
	if name == keyCommandPalette {
		m.openCommandPalette()
		return nil
	}
	if name == keyHelp || name == keyHelpAlt {
		m.openHelp()
		return nil
	}
	if name == "tab" || name == "shift+tab" {
		m.moveFocus(name == "shift+tab")
		return nil
	}
	if name == "esc" {
		if m.showNarrow {
			m.showNarrow = false
			return nil
		}
		if m.streaming {
			return m.stopRun()
		}
		m.input.Reset()
		return nil
	}
	// Printable, unmodified keys belong to the composer whenever it has focus.
	// This check must happen before other navigation handling so a prompt can
	// begin with words such as "plan", "files", or "stop".
	if m.focus == FocusComposer && key.Mod == 0 && key.Text != "" {
		m.input.Update(key)
		return nil
	}
	if name == keyStop && m.focus != FocusComposer {
		return m.stopRun()
	}
	if name == keyInspectorPlan || name == keyInspectorFiles || name == keyInspectorTools || name == keyInspectorGit {
		switch name {
		case keyInspectorPlan:
			m.inspectorTab = InspectorPlan
		case keyInspectorFiles:
			m.inspectorTab = InspectorFiles
		case keyInspectorTools:
			m.inspectorTab = InspectorTools
		case keyInspectorGit:
			m.inspectorTab = InspectorGit
		}
		m.focus = FocusInspector
		if m.layoutMode() == layoutNarrow {
			m.showNarrow = true
		}
		return nil
	}
	if m.focus == FocusTimeline {
		if delta, ok := timelineScrollDelta(name); ok {
			if name == "home" {
				m.followTail = false
				m.timelineScroll = 0
			} else if name == "end" {
				m.followTail = true
				m.timelineScroll = 0
				if len(m.events) > 0 {
					m.conversation = len(m.events) - 1
				}
			} else {
				m.scrollTimeline(delta)
			}
			return nil
		}
		switch name {
		case "alt+space", keyExpand:
			if len(m.events) > 0 {
				m.events[m.conversation].Expanded = !m.events[m.conversation].Expanded
			}
			return nil
		case keyFiles:
			m.inspectorTab = InspectorFiles
			return nil
		}
	}
	if m.focus == FocusPlan || (m.focus == FocusInspector && m.inspectorTab == InspectorPlan) {
		switch name {
		case "up":
			m.planScroll = max(0, m.planScroll-1)
			return nil
		case "down":
			m.planScroll++
			return nil
		case "pgup":
			m.planScroll = max(0, m.planScroll-5)
			return nil
		case "pgdown":
			m.planScroll += 5
			return nil
		case "home":
			m.planScroll = 0
			return nil
		case "end":
			m.planScroll = maxScroll
			return nil
		}
	}
	if name == keyRetry && m.focus != FocusComposer && m.runState == RunError && m.lastPrompt != "" {
		return m.startAgent(m.lastPrompt)
	}
	if name == "enter" {
		if m.asking {
			value := strings.TrimSpace(m.input.Value())
			if value == "" {
				return nil
			}
			m.input.Reset()
			m.asking = false
			m.input.Placeholder = "Ask the agent…"
			m.appendEvent(TimelineEvent{Type: EventUser, Status: StatusDone, Title: "YOU", Body: value})
			m.askChan <- value
			return nil
		}
		if m.focus != FocusComposer || m.streaming {
			return nil
		}
		value := strings.TrimSpace(m.input.Value())
		if value == "" {
			return nil
		}
		m.input.Reset()
		if strings.EqualFold(value, "exit") || strings.EqualFold(value, "quit") {
			return tea.Quit
		}
		if strings.HasPrefix(value, "#") {
			return m.handleHashInput(value)
		}
		m.lastPrompt = value
		m.appendEvent(TimelineEvent{Type: EventUser, Status: StatusDone, Title: "YOU", Body: value})
		if m.planMode && m.activePlan == nil && !m.planRunning {
			return m.startPlanGeneration(value)
		}
		return m.startAgent(value)
	}
	m.input.Update(key)
	return nil
}

func (m *TUIModel) moveFocus(reverse bool) {
	order := []FocusArea{FocusComposer, FocusTimeline, FocusInspector}
	if m.layoutMode() == layoutWide {
		order = []FocusArea{FocusComposer, FocusPlan, FocusTimeline, FocusInspector}
	}
	index := 0
	for i, item := range order {
		if item == m.focus {
			index = i
			break
		}
	}
	if reverse {
		index = (index - 1 + len(order)) % len(order)
	} else {
		index = (index + 1) % len(order)
	}
	m.focus = order[index]
	m.input.Focused = m.focus == FocusComposer
}

func (m *TUIModel) selectEvent(delta int) {
	if len(m.events) == 0 {
		return
	}
	m.conversation = max(0, min(len(m.events)-1, m.conversation+delta))
	m.followTail = m.conversation == len(m.events)-1
}

func (m *TUIModel) scrollFocused(delta int) {
	switch m.focus {
	case FocusComposer, FocusTimeline:
		m.scrollTimeline(delta)
	case FocusPlan, FocusInspector:
		if m.focus == FocusPlan || m.inspectorTab == InspectorPlan {
			m.planScroll = max(0, m.planScroll+delta)
		}
	}
}

func (m *TUIModel) scrollTimeline(delta int) {
	if len(m.events) == 0 || delta == 0 {
		return
	}
	if m.timelineMaxOffset == 0 && delta < 0 {
		return
	}
	if m.followTail {
		if delta > 0 {
			return
		}
		m.followTail = false
		m.timelineScroll = m.timelineMaxOffset
	}
	m.timelineScroll = max(0, m.timelineScroll+delta)
	if m.timelineScroll >= m.timelineMaxOffset {
		m.timelineScroll = m.timelineMaxOffset
	}
	if delta > 0 && m.timelineScroll >= m.timelineMaxOffset {
		m.followTail = true
		m.timelineScroll = 0
		m.conversation = len(m.events) - 1
	}
}

func timelineScrollDelta(name string) (int, bool) {
	switch name {
	case "up":
		return -1, true
	case "down":
		return 1, true
	case "pgup":
		return -5, true
	case "pgdown":
		return 5, true
	case "home":
		return 0, true
	case "end":
		return 0, true
	default:
		return 0, false
	}
}

func (m *TUIModel) stopRun() tea.Cmd {
	wasPlanRunning := m.planRunning
	if m.runCancel != nil {
		m.runCancel()
		m.runCancel = nil
	}
	if m.agent != nil && m.agent.Manager != nil {
		m.agent.Manager.Cleanup()
	}
	m.streaming, m.runState = false, RunIdle
	m.planRunning = false
	m.planApproval = false
	if wasPlanRunning {
		m.activePlan = nil
		m.plan = nil
		m.planScroll = 0
		m.planApprovalScroll = 0
	}
	m.appendEvent(TimelineEvent{Type: EventSystem, Status: StatusDone, Title: "STOPPED", Body: "The active run was cancelled."})
	return nil
}

func (m *TUIModel) startAgent(prompt string) tea.Cmd {
	if m.agent == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel = cancel
	m.streaming, m.runState = true, RunWorking
	a := m.agent
	return func() tea.Msg {
		response, err := a.Run(ctx, prompt)
		return AgentTurnFinishedMsg{Response: response, Err: err}
	}
}

func (m *TUIModel) startPlanGeneration(prompt string) tea.Cmd {
	if m.agent == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel = cancel
	m.streaming, m.runState = true, RunWorking
	a := m.agent
	return func() tea.Msg {
		wrapped := agent.PlanGenerationPrompt(prompt)
		response, err := a.Run(ctx, wrapped)
		if err != nil {
			return PlanGeneratedMsg{Err: err}
		}
		plan, parseErr := agent.ParsePlan(response)
		if parseErr != nil {
			return PlanGeneratedMsg{Raw: response, Err: parseErr}
		}
		return PlanGeneratedMsg{Plan: plan, Raw: response}
	}
}

func (m *TUIModel) startPlanExecution() tea.Cmd {
	if m.agent == nil || m.activePlan == nil {
		return nil
	}
	m.planRunning = true
	m.planCostStart = m.agent.SessionCost
	return m.runPlanStep(0)
}

func (m *TUIModel) runPlanStep(stepIndex int) tea.Cmd {
	if m.activePlan == nil || stepIndex >= len(m.activePlan.Steps) {
		plan := m.activePlan
		costStart := m.planCostStart
		a := m.agent
		return func() tea.Msg {
			files := currentChangedFiles()
			return PlanCompleteMsg{
				Plan:      plan,
				Files:     files,
				FileCount: len(files),
				TotalCost: a.SessionCost - costStart,
			}
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.runCancel = cancel
	m.streaming, m.runState = true, RunWorking

	plan := m.activePlan
	step := &plan.Steps[stepIndex]
	step.Status = agent.StepRunning
	m.plan = planToTUISteps(plan)
	m.planCursor = stepIndex

	m.appendEvent(TimelineEvent{Type: EventPlan, Status: StatusRunning, Title: fmt.Sprintf("STEP %d", stepIndex+1), Body: step.Description})

	a := m.agent
	return func() tea.Msg {
		result := agent.ExecuteStep(ctx, a, plan, stepIndex)
		return PlanStepResultMsg{StepIndex: stepIndex, Result: result}
	}
}

func (m *TUIModel) editPlanInEditor() tea.Cmd {
	data, err := json.MarshalIndent(m.activePlan, "", "  ")
	if err != nil {
		return func() tea.Msg { return PlanGeneratedMsg{Err: err} }
	}
	tmp, createErr := os.CreateTemp(os.TempDir(), "pragma-plan-*.json")
	if createErr != nil {
		return func() tea.Msg { return PlanGeneratedMsg{Err: createErr} }
	}
	tmpFile := tmp.Name()
	if _, writeErr := tmp.Write(data); writeErr != nil {
		tmp.Close()
		os.Remove(tmpFile)
		return func() tea.Msg { return PlanGeneratedMsg{Err: writeErr} }
	}
	if closeErr := tmp.Close(); closeErr != nil {
		os.Remove(tmpFile)
		return func() tea.Msg { return PlanGeneratedMsg{Err: closeErr} }
	}
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	editorParts := strings.Fields(editor)
	if len(editorParts) == 0 {
		editorParts = []string{"vi"}
	}
	cmd := exec.Command(editorParts[0], append(editorParts[1:], tmpFile)...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			os.Remove(tmpFile)
			return PlanGeneratedMsg{Err: fmt.Errorf("editor failed: %w", err)}
		}
		edited, readErr := os.ReadFile(tmpFile)
		os.Remove(tmpFile)
		if readErr != nil {
			return PlanGeneratedMsg{Err: readErr}
		}
		plan, parseErr := agent.ParsePlan(string(edited))
		if parseErr != nil {
			return PlanGeneratedMsg{Err: parseErr, Raw: string(edited)}
		}
		return PlanGeneratedMsg{Plan: plan}
	})
}

func (m *TUIModel) renderPlanApprovalCard(maxHeight int) string {
	width := max(20, m.width)
	innerWidth := cardContentWidth(width)
	contentHeight := max(1, maxHeight-4)
	lines := []string{approvalStyle.Render("PLAN APPROVAL"), ""}
	var planLines []string
	if m.activePlan != nil {
		planLines = wrapPhysicalLines(strings.Split(m.activePlan.Format(), "\n"), innerWidth)
	}
	helpLines := wrapPhysicalLines([]string{keyHintStyle.Render("[Alt+Y] approve  [Alt+E] edit in $EDITOR  [Alt+N] reject  ·  ↑/↓ scroll")}, innerWidth)
	bodyHeight := max(1, contentHeight-len(lines)-len(helpLines)-1)
	planLines = windowLines(planLines, bodyHeight, m.planApprovalScroll)
	lines = append(lines, planLines...)
	lines = append(lines, "")
	lines = append(lines, helpLines...)
	return renderBoundedCard(lines, width, maxHeight, colorWarning)
}

func (m *TUIModel) requestApproval(request ApprovalRequestedMsg) tea.Cmd {
	if request.Response == nil {
		request.Response = m.confirmChan
	}
	return func() tea.Msg { return request }
}

func (m *TUIModel) handleHashInput(value string) tea.Cmd {
	if value == "#help" {
		m.openHelp()
		return nil
	}
	result := m.handleHashCommand(value)
	if result == "EXIT" {
		return tea.Quit
	}
	if result != "" {
		m.appendEvent(TimelineEvent{Type: EventSystem, Status: StatusDone, Title: "COMMAND", Body: result})
	}
	return nil
}

func (m *TUIModel) openCommandPalette() {
	hash := func(command string) func() tea.Cmd {
		return func() tea.Cmd { return func() tea.Msg { return HashCommandMsg{Command: command} } }
	}
	m.menu = &Menu{Title: "Command palette", Options: []MenuOption{
		{Label: "/plan", Description: "toggle plan mode", OnSelect: hash("#plan")},
		{Label: "/files", Description: "show changed files", OnSelect: hash("#status")},
		{Label: "/tests", Description: "show the test pane", OnSelect: hash("#status")},
		{Label: "/compact", Description: "compact model context", OnSelect: hash("#compact")},
		{Label: "/clear", Description: "clear the conversation", OnSelect: hash("#clear")},
		{Label: "/stop", Description: "stop the active run", OnSelect: func() tea.Cmd { return func() tea.Msg { return HashCommandMsg{Command: "#stop"} } }},
	}}
}

func (m *TUIModel) openHelp() {
	m.menu = &Menu{Title: "Help · shortcuts and hash commands", Options: []MenuOption{
		{Label: "SHORTCUTS", Description: ""},
		{Label: "Enter", Description: "send prompt or confirm"},
		{Label: "Tab / Shift+Tab", Description: "move focus"},
		{Label: "↑ / ↓ / Alt+J / Alt+K", Description: "scroll the conversation or move in menus"},
		{Label: "PageUp / PageDown", Description: "page through the conversation or menu"},
		{Label: "Home / End", Description: "jump to the start or tail of the focused view"},
		{Label: "Alt+Space / Alt+O", Description: "expand a selected event"},
		{Label: "Alt+P", Description: "toggle plan mode; next prompt generates a plan"},
		{Label: "Alt+Shift+P", Description: "open the plan inspector"},
		{Label: "Alt+F/T/G", Description: "open files, tools, or git inspector"},
		{Label: "Alt+K", Description: "open the command palette"},
		{Label: "Alt+H / F1", Description: "open this help"},
		{Label: "Alt+S", Description: "stop the active run when focus is outside the composer"},
		{Label: "Alt+D", Description: "open the files inspector from the timeline"},
		{Label: "Alt+R", Description: "retry the last failed prompt"},
		{Label: "Alt+A/E/U/W", Description: "editor start/end, clear, and delete-word actions"},
		{Label: "Alt+Enter / Shift+Enter", Description: "insert a newline in the composer"},
		{Label: "Alt+C", Description: "cancel, then quit"},
		{Label: "Alt+Y / Alt+A / Alt+E / Alt+N", Description: "approve, approve session, edit, or reject"},
		{Label: "HASH COMMANDS", Description: ""},
		{Label: "#help", Description: "show this shortcuts and hash commands help"},
		{Label: "#plan", Description: "toggle plan mode"},
		{Label: "#clear", Description: "clear the conversation and agent context"},
		{Label: "#compact", Description: "compact the agent context"},
		{Label: "#status", Description: "show current state, model, and plan mode"},
		{Label: "#model", Description: "show the active model details"},
		{Label: "#switch <model_name>", Description: "switch to a configured model tier"},
		{Label: "#cost", Description: "show token and cost totals"},
		{Label: "#docs", Description: "show recent task docs"},
		{Label: "#arch", Description: "show the architecture doc"},
		{Label: "#tiers", Description: "list configured model tiers"},
		{Label: "#budget [amount]", Description: "show or set the session budget"},
		{Label: "#sessions", Description: "list recent sessions"},
		{Label: "#stop", Description: "stop the active run"},
		{Label: "#undo", Description: "explain where to undo a reviewed diff"},
		{Label: "#quit", Description: "quit Pragma"},
		{Label: "Esc", Description: "stop the active run or clear the composer"},
	}}
}

func (m *TUIModel) handleHashCommand(command string) string {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return ""
	}
	switch parts[0] {
	case "#help":
		m.openHelp()
		return ""
	case "#plan":
		if m.planRunning {
			return "Cannot toggle plan mode while a plan is executing."
		}
		m.planMode = !m.planMode
		if m.planMode {
			return "Plan mode enabled. Your next prompt will generate a plan for approval before execution."
		}
		m.activePlan = nil
		m.plan = nil
		m.planScroll = 0
		m.planApprovalScroll = 0
		return "Plan mode disabled."
	case "#clear":
		m.events = nil
		m.followTail = true
		m.timelineScroll = 0
		m.conversation = 0
		m.timelineMaxOffset = 0
		if m.agent != nil {
			m.agent.ClearContext()
		}
		return "Conversation cleared."
	case "#compact":
		if m.agent == nil {
			return "No agent running."
		}
		return m.agent.Compact()
	case "#status":
		if m.agent == nil {
			return fmt.Sprintf("State: %s | Focus: %s", m.runState, focusName(m.focus))
		}
		planStatus := "off"
		if m.planMode {
			planStatus = "on"
		}
		return fmt.Sprintf("Messages: %d | Model: %s | Tool mode: %s | Plan mode: %s", max(0, len(m.agent.History)-1), m.agent.CurrentModel.Name, m.agent.CurrentModel.ToolMode, planStatus)
	case "#model":
		if m.agent == nil || m.agent.CurrentModel == nil {
			return "No model configured."
		}
		model := m.agent.CurrentModel
		return fmt.Sprintf("Model: %s\n  Max tokens: %d\n  Effort: %s\n  Tool mode: %s\n  Provider: %s", model.Name, model.MaxTokens, model.Effort, model.ToolMode, model.Provider.GetName())
	case "#switch":
		if m.agent == nil {
			return "No agent running."
		}
		if len(parts) < 2 {
			return "Usage: #switch <model_name>"
		}
		for _, tier := range m.agent.ModelTiers {
			if strings.Contains(tier.Model.Name, parts[1]) {
				m.agent.CurrentModel = tier.Model
				return fmt.Sprintf("Switched to %s (%s)", tier.Model.Name, tier.Model.ToolMode)
			}
		}
		return fmt.Sprintf("No tier matching %q. Type #tiers to see available.", parts[1])
	case "#cost":
		if m.agent == nil {
			return "No agent running."
		}
		return fmt.Sprintf("Current task: %d in / %d out | Task cost: $%.4f | Session cost: $%.4f", m.agent.TaskInputTokens, m.agent.TaskOutputTokens, m.agent.TaskCost, m.agent.SessionCost)
	case "#docs":
		if recent := agent.LoadRecentDocs(3); recent != "" {
			return recent
		}
		return "No task docs yet."
	case "#arch":
		if arch := agent.LoadArchitecture(); arch != "" {
			return arch
		}
		return "No architecture doc yet."
	case "#tiers":
		if m.agent == nil {
			return "No agent running."
		}
		var out strings.Builder
		for _, tier := range m.agent.ModelTiers {
			marker := "  "
			if tier.Model.Name == m.agent.CurrentModel.Name {
				marker = "→ "
			}
			out.WriteString(fmt.Sprintf("%s%s (%s) at %.0f%%\n", marker, tier.Model.Name, tier.Model.ToolMode, tier.Threshold*100))
		}
		return out.String()
	case "#budget":
		if m.agent == nil {
			return "No agent running."
		}
		if len(parts) < 2 {
			if m.agent.Budget > 0 {
				return fmt.Sprintf("Budget: $%.2f (%.1f%% used)", m.agent.Budget, m.agent.SessionCost/m.agent.Budget*100)
			}
			return "No budget set. Usage: #budget <amount>"
		}
		var amount float64
		if _, err := fmt.Sscanf(parts[1], "%f", &amount); err != nil || amount <= 0 {
			return "Budget must be positive."
		}
		m.agent.Budget = amount
		return fmt.Sprintf("Budget set to $%.2f", amount)
	case "#sessions":
		sessions, err := db.ListSessions(10)
		if err != nil {
			return "Error fetching sessions."
		}
		if len(sessions) == 0 {
			return "No previous sessions."
		}
		var out strings.Builder
		for _, session := range sessions {
			out.WriteString(fmt.Sprintf("- %s  %s\n", session.Id.String(), session.Title))
		}
		return out.String()
	case "#stop":
		m.stopRun()
		return "Run stopped."
	case "#undo":
		return "Undo is available from the Files inspector after reviewing the diff."
	case "#quit":
		return "EXIT"
	default:
		return fmt.Sprintf("Unknown command: %s. Type #help for available commands.", parts[0])
	}
}

func firstArg(argsJSON string) string {
	var values map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &values); err != nil {
		return ""
	}
	for _, value := range values {
		return truncateCells(fmt.Sprint(value), 80)
	}
	return ""
}

func (m *TUIModel) resize() {
	if m.width <= 0 {
		return
	}
	m.input.Width = max(8, m.width-8)
}

type layoutMode int

const (
	layoutNarrow layoutMode = iota
	layoutMedium
	layoutWide
)

func (m *TUIModel) layoutMode() layoutMode {
	switch {
	case m.width >= 120:
		return layoutWide
	case m.width >= 80:
		return layoutMedium
	default:
		return layoutNarrow
	}
}

// Panels use a fixed outer size. Keeping the border and padding math in one
// place prevents the columns from drifting apart as the terminal resizes.
func panelContentWidth(width int) int {
	return max(1, width-6) // two border cells + two cells of padding per side
}

func panelContentHeight(height int) int {
	return max(1, height-4) // two border rows + one row of padding per side
}

func cardContentWidth(width int) int {
	return max(8, width-6)
}

func windowLines(lines []string, height, offset int) []string {
	if height <= 0 || len(lines) == 0 {
		return nil
	}
	maxOffset := max(0, len(lines)-height)
	offset = max(0, min(offset, maxOffset))
	end := min(len(lines), offset+height)
	return lines[offset:end]
}

func wrapPhysicalLines(lines []string, width int) []string {
	var wrapped []string
	for _, line := range lines {
		if line == "" {
			wrapped = append(wrapped, "")
			continue
		}
		wrapped = append(wrapped, strings.Split(lipgloss.Wrap(line, max(1, width), " "), "\n")...)
	}
	return wrapped
}

func renderBoundedCard(lines []string, width, maxHeight int, border color.Color) string {
	contentHeight := max(1, maxHeight-4) // border and one row of padding on each side
	lines = windowLines(lines, contentHeight, 0)
	style := lipgloss.NewStyle().Foreground(colorText).BorderForeground(border).BorderStyle(lipgloss.NormalBorder()).Width(width).Padding(1, 2)
	return style.Render(strings.Join(lines, "\n"))
}

func fitLine(value string, width int) string {
	if width <= 0 {
		return ""
	}
	value = truncateCells(value, width)
	return value + strings.Repeat(" ", max(0, width-lipgloss.Width(value)))
}

func inspectorTabLines(active InspectorTab, width int) []string {
	labels := []string{"[Alt+P] PLAN", "[Alt+F] FILES", "[Alt+T] TOOLS", "[Alt+G] GIT"}
	tabs := []InspectorTab{InspectorPlan, InspectorFiles, InspectorTools, InspectorGit}
	for i, tab := range tabs {
		if active == tab {
			labels[i] = keyHintStyle.Render(labels[i])
		} else {
			labels[i] = subtleStyle.Render(labels[i])
		}
	}
	if width >= 20 {
		return []string{
			labels[0] + "  " + labels[1],
			labels[2] + "  " + labels[3],
		}
	}
	return labels
}

func (m *TUIModel) View() tea.View {
	var view tea.View
	if m.width <= 0 || m.height <= 0 {
		view.SetContent("")
		return view
	}
	if m.state == StateOnboarding {
		view.SetContent(m.viewOnboarding())
	} else {
		view.SetContent(m.viewChat())
	}
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	// Leave BackgroundColor unset so the terminal's own background shows
	// through. In particular, do not replace a user's light/dark theme here.
	view.BackgroundColor = nil
	view.WindowTitle = "Pragma"
	return view
}

func (m *TUIModel) viewChat() string {
	header := m.renderHeader()
	footer := m.renderFooter()
	composer := m.renderComposer()
	bottom := composer
	fixedHeight := max(1, m.height-lipgloss.Height(header)-lipgloss.Height(composer)-lipgloss.Height(footer))
	// Panels have two border rows and one padding row on each side, so leave
	// enough room for their five-row minimum while a modal is open.
	cardHeight := max(1, fixedHeight-5)
	if m.confirming {
		bottom = lipgloss.JoinVertical(lipgloss.Left, m.renderApprovalCard(cardHeight), composer)
	}
	if m.planApproval {
		bottom = lipgloss.JoinVertical(lipgloss.Left, m.renderPlanApprovalCard(cardHeight), composer)
	}
	if m.menu != nil {
		bottom = lipgloss.JoinVertical(lipgloss.Left, m.renderMenuCard(cardHeight), bottom)
	}
	mainHeight := max(5, m.height-lipgloss.Height(header)-lipgloss.Height(bottom)-lipgloss.Height(footer))
	main := m.renderMain(mainHeight)
	return lipgloss.JoinVertical(lipgloss.Left, header, main, bottom, footer)
}

func (m *TUIModel) renderHeader() string {
	width := max(20, m.width)
	path := abbreviatePath(m.projectPath)
	branch := m.branch
	if branch == "" {
		branch = "no branch"
	}
	provider := "offline"
	if m.agent != nil && m.agent.CurrentModel != nil {
		provider = m.agent.CurrentModel.Name
	}
	contextText := "Context: —"
	if m.contextLimit > 0 {
		contextText = fmt.Sprintf("Context: %d%%", min(100, m.contextUsed*100/m.contextLimit))
	}
	statusSymbol, statusText := "○", "Ready"
	switch m.runState {
	case RunWorking:
		statusSymbol, statusText = "◉", "Working"
	case RunApproval:
		statusSymbol, statusText = "!", "Approval"
	case RunError:
		statusSymbol, statusText = "✕", "Error"
	}
	fileText := fmt.Sprintf("Changes: %d files", len(m.changedFiles))
	if m.planMode {
		modeLabel := "PLAN"
		if m.planRunning {
			modeLabel = "PLAN (executing)"
		}
		statusText = modeLabel + " · " + statusText
	}
	left := fmt.Sprintf("  %s %s", appName, appVersion)
	right := fmt.Sprintf("%s  ·  %s", branch, path)
	top := fitLine(left+strings.Repeat(" ", max(2, width-lipgloss.Width(left)-lipgloss.Width(right)))+right, width)
	status := fitLine(fmt.Sprintf("  %s %s    %s    %s    %s", statusSymbol, statusText, provider, contextText, fileText), width)
	rule := strings.Repeat("─", width)
	return headerStyle.Bold(true).Render(top) + "\n" + mutedStyle.Render(status) + "\n" + lineStyle.Render(rule)
}

func (m *TUIModel) renderMain(height int) string {
	width := max(20, m.width)
	if m.layoutMode() == layoutNarrow && m.showNarrow {
		return m.renderPanel("INSPECTOR · "+strings.ToUpper(string(m.inspectorTab)), m.renderInspectorLines(panelContentWidth(width)), width, height, m.focus == FocusInspector)
	}
	switch m.layoutMode() {
	case layoutWide:
		planWidth, inspectorWidth := 27, 32
		conversationWidth := width - planWidth - inspectorWidth - 2 // one breathing cell between panels
		if conversationWidth < 40 {
			return m.renderPanel("CONVERSATION", m.renderTimelineLines(panelContentWidth(width), height), width, height, m.focus == FocusTimeline)
		}
		return lipgloss.JoinHorizontal(lipgloss.Top,
			m.renderPanel("RUN / PLAN", m.renderPlanLines(), planWidth, height, m.focus == FocusPlan),
			" ",
			m.renderPanel("CONVERSATION", m.renderTimelineLines(panelContentWidth(conversationWidth), height), conversationWidth, height, m.focus == FocusTimeline),
			" ",
			m.renderPanel("INSPECTOR", m.renderInspectorLines(panelContentWidth(inspectorWidth)), inspectorWidth, height, m.focus == FocusInspector),
		)
	case layoutMedium:
		inspectorWidth := 32
		conversationWidth := width - inspectorWidth - 1
		if conversationWidth < 42 {
			return m.renderPanel("CONVERSATION", m.renderTimelineLines(panelContentWidth(width), height), width, height, m.focus == FocusTimeline)
		}
		return lipgloss.JoinHorizontal(lipgloss.Top,
			m.renderPanel("CONVERSATION", m.renderTimelineLines(panelContentWidth(conversationWidth), height), conversationWidth, height, m.focus == FocusTimeline),
			" ",
			m.renderPanel("INSPECTOR · "+strings.ToUpper(string(m.inspectorTab)), m.renderInspectorLines(panelContentWidth(inspectorWidth)), inspectorWidth, height, m.focus == FocusInspector),
		)
	default:
		return m.renderPanel("CONVERSATION", m.renderTimelineLines(panelContentWidth(width), height), width, height, m.focus == FocusTimeline)
	}
}

func (m *TUIModel) renderPanel(title string, lines []string, width, height int, focused bool) string {
	width = max(10, width)
	height = max(5, height)
	innerWidth := panelContentWidth(width)
	innerHeight := panelContentHeight(height)
	content := []string{sectionTitle.Render(title)}
	if strings.Contains(strings.ToLower(title), "plan") && (m.focus == FocusPlan || (m.focus == FocusInspector && m.inspectorTab == InspectorPlan)) {
		lines = windowLines(lines, max(1, innerHeight-1), m.planScroll)
	}
	for _, line := range lines {
		// A logical item may contain newlines (for example, the plan hint).
		// Expand those before applying the fixed panel height so joined panels
		// always have identical physical rows.
		content = append(content, strings.Split(line, "\n")...)
	}
	if len(content) > innerHeight {
		content = content[:innerHeight]
	}
	for len(content) < innerHeight {
		content = append(content, "")
	}
	for i := range content {
		content[i] = truncateCells(content[i], innerWidth)
	}
	style := panelStyle
	if focused {
		style = focusedPanel
	}
	return style.Width(width).BorderStyle(lipgloss.NormalBorder()).Padding(1, 2).Render(strings.Join(content, "\n"))
}

func (m *TUIModel) renderPlanLines() []string {
	lines := []string{}
	for i, step := range m.plan {
		symbol := "○"
		style := subtleStyle
		switch step.Status {
		case StatusDone:
			symbol, style = "✓", toolSuccess
		case StatusRunning:
			symbol, style = "◉", keyHintStyle
		}
		prefix := "  "
		if i == m.planCursor {
			prefix = "› "
		}
		lines = append(lines, style.Render(prefix+symbol+" "+step.Title))
		if i < len(m.plan)-1 {
			lines = append(lines, "")
		}
	}
	lines = append(lines, "", subtleStyle.Render("The active step stays"), subtleStyle.Render("visible while work runs."))
	return lines
}

func (m *TUIModel) renderTimelineLines(width, height int) []string {
	if len(m.events) == 0 {
		return []string{"", agentMessage.Render("◆ PRAGMA"), mutedStyle.Render("Ready when you are."), "", subtleStyle.Render("Type a prompt below to start a run.")}
	}
	var lines []string
	for index, event := range m.events {
		marker, style := eventMarker(event)
		title := fmt.Sprintf("%s %s", marker, event.Title)
		if index == m.conversation {
			title = "› " + title
		}
		lines = append(lines, style.Render(title))
		body := event.Body
		if event.Type == EventTool && event.Status == StatusDone && !event.Expanded && body != "" {
			body = "output hidden · press Space to expand"
		}
		if body != "" {
			wrapped := lipgloss.Wrap(body, max(8, width-2), " ")
			for _, line := range strings.Split(wrapped, "\n") {
				lines = append(lines, "  "+line)
			}
		}
		lines = append(lines, "")
	}
	visible := max(1, height-4)
	if len(lines) > visible {
		maxOffset := len(lines) - visible
		m.timelineMaxOffset = maxOffset
		start := maxOffset
		if !m.followTail {
			start = min(maxOffset, max(0, m.timelineScroll))
		}
		lines = lines[start:]
	} else {
		m.timelineMaxOffset = 0
	}
	return lines
}

func eventMarker(event TimelineEvent) (string, lipgloss.Style) {
	switch event.Type {
	case EventUser:
		return "▶", userMessage
	case EventAgent:
		return "◆", agentMessage
	case EventTool, EventToolOutput:
		switch event.Status {
		case StatusRunning:
			return "◈ RUNNING", toolRunning
		case StatusFailed:
			return "◈ FAILED", toolFailure
		default:
			return "◈ DONE", toolSuccess
		}
	case EventApproval:
		return "! APPROVAL", approvalStyle
	case EventError:
		return "✕ FAILED", toolFailure
	case EventPlan:
		return "▸ PLAN", keyHintStyle
	case EventDiff, EventFile:
		return "▣ FILE", userMessage
	default:
		return "·", mutedStyle
	}
}

func (m *TUIModel) renderInspectorLines(width int) []string {
	lines := append(inspectorTabLines(m.inspectorTab, width), "")
	switch m.inspectorTab {
	case InspectorPlan:
		lines = append(lines, m.renderPlanLines()...)
	case InspectorFiles:
		if len(m.changedFiles) == 0 {
			lines = append(lines, mutedStyle.Render("No changed files."))
		} else {
			for _, file := range m.changedFiles {
				stats := fmt.Sprintf("+%d -%d", file.Added, file.Removed)
				prefix := fmt.Sprintf("%-2s ", truncateCells(file.Status, 2))
				pathWidth := max(1, width-lipgloss.Width(prefix)-lipgloss.Width(stats)-1)
				path := fitLine(truncateCells(file.Path, pathWidth), pathWidth)
				lines = append(lines, prefix+path+" "+stats)
			}
		}
	case InspectorTools:
		found := false
		for _, event := range m.events {
			if event.Type == EventTool {
				found = true
				marker, _ := eventMarker(event)
				lines = append(lines, fmt.Sprintf("%s %s", marker, truncateCells(event.Title, max(8, width-12))))
			}
		}
		if !found {
			lines = append(lines, mutedStyle.Render("No tools have run."))
		}
	case InspectorGit:
		lines = append(lines, fmt.Sprintf("Branch: %s", fallback(m.branch, "not detected")), fmt.Sprintf("Path: %s", abbreviatePath(m.projectPath)), "")
		if len(m.changedFiles) > 0 {
			lines = append(lines, fmt.Sprintf("%d changed files", len(m.changedFiles)))
		} else {
			lines = append(lines, mutedStyle.Render("Working tree clean."))
		}
	}
	return lines
}

func (m *TUIModel) renderApprovalCard(maxHeight int) string {
	width := max(20, m.width)
	lines := []string{
		approvalStyle.Render("! APPROVAL REQUIRED"),
		fmt.Sprintf("Run: %s", truncateCells(m.confirmInfo.Command, width-8)),
		fmt.Sprintf("Directory: %s", abbreviatePath(fallback(m.confirmInfo.Directory, m.projectPath))),
		fmt.Sprintf("Risk: %s", fallback(m.confirmInfo.Risk, "review required")),
	}
	if len(m.confirmInfo.Files) > 0 {
		lines = append(lines, "Files: "+strings.Join(m.confirmInfo.Files, ", "))
	}
	if m.confirmInfo.Network {
		lines = append(lines, "Network: access requested")
	}
	if m.confirmInfo.Reason != "" {
		lines = append(lines, "Reason: "+m.confirmInfo.Reason)
	}
	lines = append(lines, "", keyHintStyle.Render("[Alt+Y] approve once  [Alt+A] approve session  [Alt+E] edit  [Alt+N] reject"))
	innerWidth := cardContentWidth(width)
	var wrapped []string
	for _, line := range lines {
		wrapped = append(wrapped, strings.Split(lipgloss.Wrap(line, innerWidth, " "), "\n")...)
	}
	return renderBoundedCard(wrapped, width, maxHeight, colorWarning)
}

func (m *TUIModel) renderMenuCard(maxHeight int) string {
	width := max(20, m.width)
	innerWidth := cardContentWidth(width)
	contentLimit := max(1, maxHeight-4)
	help := []string{subtleStyle.Render("↑/↓ select · PgUp/PgDn page · Enter confirm · Esc close")}
	if strings.HasPrefix(m.menu.Title, "Help") {
		// Keep a compact pointer to the lower section visible while the full
		// help list is paged. This also makes the scrollable nature of the
		// modal discoverable before the user reaches its final rows.
		help = append(help, subtleStyle.Render("More: HASH COMMANDS · #plan · #budget [amount]"))
	}
	helpLines := wrapPhysicalLines(help, innerWidth)
	optionRows := max(1, contentLimit-len(helpLines)-2)
	var body []string
	for {
		m.menu.SetVisibleRows(optionRows)
		body = wrapPhysicalLines(strings.Split(strings.TrimSuffix(m.menu.ViewWindow(optionRows), "\n"), "\n"), innerWidth)
		if len(body) <= max(1, contentLimit-len(helpLines)-1) || optionRows == 1 {
			break
		}
		optionRows--
	}
	lines := append(body, "")
	lines = append(lines, helpLines...)
	return renderBoundedCard(lines, width, maxHeight, colorPurple)
}

func (m *TUIModel) renderComposer() string {
	width := max(20, m.width)
	placeholder := "Ask the agent…"
	if m.asking {
		placeholder = "Answer the agent’s question…"
	}
	if m.streaming && !m.asking {
		placeholder = "Agent is working · press Esc to stop"
	}
	editor := m.input
	editor.Placeholder = placeholder
	editor.Width = max(8, width-8)
	content := "> " + composerStyle.Render(editor.View())
	style := composerStyle.Copy().BorderForeground(colorPink)
	return style.Width(width).Padding(0, 2).BorderStyle(lipgloss.NormalBorder()).Render(content)
}

func (m *TUIModel) renderFooter() string {
	focus := focusName(m.focus)
	shortcuts := "Enter send · Alt+P plan · Alt+K commands · Tab focus · F1 help · Alt+C quit"
	if m.focus == FocusTimeline {
		shortcuts = "↑/↓ scroll chat · PgUp/PgDn page · Home/End jump · Tab focus"
	}
	if m.focus == FocusPlan || (m.focus == FocusInspector && m.inspectorTab == InspectorPlan) {
		shortcuts = "↑/↓ scroll plan · PgUp/PgDn page · Tab focus · Alt+C quit"
	}
	if m.streaming {
		shortcuts = "Esc stop · Tab focus · Alt+C cancel"
	}
	if m.layoutMode() == layoutNarrow {
		shortcuts = "Alt+P plan · Alt+F files · Alt+T tools · Alt+G git · " + shortcuts
		if m.showNarrow {
			shortcuts = "Esc conversation · " + shortcuts
		}
	}
	footer := fmt.Sprintf("  FOCUS %s   %s", strings.ToUpper(focus), shortcuts)
	return subtleStyle.Render(truncateCells(footer, max(1, m.width)))
}

func (m *TUIModel) updateOnboarding(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = max(0, msg.Width), max(0, msg.Height)
		m.resize()
		return m, nil
	case tea.KeyPressMsg:
		if keyName(msg) == keyQuit {
			return m, tea.Quit
		}
		if keyName(msg) == "enter" {
			value := strings.TrimSpace(m.input.Value())
			m.input.Reset()
			switch m.onboardStep {
			case 0, 4:
				provider := "openrouter"
				switch value {
				case "2":
					provider = "openai"
				case "3":
					provider = "anthropic"
				}
				m.onboardData = map[string]string{"provider": provider, "api_key_var": catalog.APIKeyVarForProvider(provider), "default_model": catalog.DefaultModelForProvider(provider)}
				if m.onboardData["api_key_var"] == "" {
					m.onboardData["api_key_var"] = strings.ToUpper(provider) + "_API_KEY"
				}
				if m.onboardData["default_model"] == "" {
					m.onboardData["default_model"] = "gpt-5.4-mini"
				}
				m.onboardStep++
				m.input.Placeholder = "Model name [" + m.onboardData["default_model"] + "]"
			case 1, 5:
				if value == "" {
					value = m.onboardData["default_model"]
				}
				m.onboardData["model"] = value
				m.onboardStep++
				if m.onboardStep == 2 {
					m.input.Placeholder = "Paste API key (or Enter to skip)"
				} else {
					m.input.Placeholder = "Fallback cost threshold fraction [0.5]"
				}
			case 2:
				m.onboardData["api_key"], m.onboardData["threshold"] = value, "0.0"
				m.onboardTiers = append(m.onboardTiers, m.onboardData)
				m.onboardData = make(map[string]string)
				m.onboardStep = 3
				m.input.Placeholder = "y/n [n]"
			case 3:
				if strings.EqualFold(value, "y") || strings.EqualFold(value, "yes") {
					m.onboardStep = 4
					m.input.Placeholder = "Type 1, 2 or 3"
				} else {
					m.writeOnboardConfig()
					return m, tea.Quit
				}
			case 6:
				if value == "" {
					value = "0.5"
				}
				m.onboardData["threshold"] = value
				m.onboardStep = 7
				m.input.Placeholder = "Paste fallback API key (or Enter to skip)"
			case 7:
				m.onboardData["api_key"] = value
				m.onboardTiers = append(m.onboardTiers, m.onboardData)
				m.writeOnboardConfig()
				return m, tea.Quit
			}
			return m, nil
		}
		m.input.Update(msg)
	}
	return m, nil
}

func (m *TUIModel) viewOnboarding() string {
	width := max(30, m.width)
	var lines []string
	lines = append(lines, agentMessage.Render("◆ Welcome to Pragma Setup"), "")
	switch m.onboardStep {
	case 0, 4:
		lines = append(lines, fmt.Sprintf("[%d/2] Select %s LLM provider:", map[bool]int{true: 2, false: 1}[m.onboardStep == 4], fallback(map[bool]string{true: "fallback", false: "primary"}[m.onboardStep == 4], "primary")), "", "1. OpenRouter", "2. OpenAI", "3. Anthropic")
	case 1, 5:
		lines = append(lines, mutedStyle.Render("Provider: "+m.onboardData["provider"]), "", "Enter the model ID:", mutedStyle.Render("Default: "+m.onboardData["default_model"]))
	case 2:
		lines = append(lines, mutedStyle.Render("Provider: "+m.onboardData["provider"]), mutedStyle.Render("Model: "+m.onboardData["model"]), "", "Paste the primary API key:", mutedStyle.Render("Environment variable: "+m.onboardData["api_key_var"]))
	case 3:
		lines = append(lines, toolSuccess.Render("✓ Primary tier configured"), "", "Configure a secondary fallback model?", mutedStyle.Render("Type y or n (default n)"))
	case 6:
		lines = append(lines, mutedStyle.Render("Fallback provider: "+m.onboardData["provider"]), mutedStyle.Render("Fallback model: "+m.onboardData["model"]), "", "When should Pragma downgrade?", mutedStyle.Render("Enter a fraction from 0.0 to 1.0 (default 0.5)"))
	case 7:
		lines = append(lines, mutedStyle.Render("Fallback provider: "+m.onboardData["provider"]), mutedStyle.Render("Fallback model: "+m.onboardData["model"]), mutedStyle.Render("Switch point: "+m.onboardData["threshold"]), "", "Paste the fallback API key:", mutedStyle.Render("Environment variable: "+m.onboardData["api_key_var"]))
	}
	lines = append(lines, "", "> "+m.input.View(), "", subtleStyle.Render("Enter continue · Alt+C quit"))
	return m.renderPanel("ONBOARDING", lines, min(width-4, 78), max(10, m.height-4), true)
}

func (m *TUIModel) writeOnboardConfig() {
	_ = os.MkdirAll(".agent", 0755)
	var tiers strings.Builder
	for _, tier := range m.onboardTiers {
		tiers.WriteString("[[model.tiers]]\n")
		tiers.WriteString(fmt.Sprintf("model = %q\nprovider = %q\napi_key_var_name = %q\neffort = \"\"\nthreshold = %s\n\n", tier["model"], tier["provider"], tier["api_key_var"], tier["threshold"]))
	}
	config := fmt.Sprintf("%s[behavior]\nverbosity = \"minimal\"\ntest_policy = \"none\"\nmax_output_tokens = 8192\n", tiers.String())
	_ = os.WriteFile(".agent/config.toml", []byte(config), 0644)
	if file, err := os.OpenFile(".agent/.env", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644); err == nil {
		defer file.Close()
		for _, tier := range m.onboardTiers {
			if tier["api_key"] != "" {
				_, _ = file.WriteString(fmt.Sprintf("%s=%s\n", tier["api_key_var"], tier["api_key"]))
			}
		}
	}
}

func gitStatusCmd() tea.Cmd {
	return func() tea.Msg {
		cwd := workingDirectory()
		branchOutput, branchErr := exec.Command("git", "branch", "--show-current").Output()
		statusOutput, statusErr := exec.Command("git", "status", "--short").Output()
		if branchErr != nil && statusErr != nil {
			return GitStatusMsg{Path: cwd, Err: statusErr}
		}
		files := parseGitStatus(string(statusOutput))
		return GitStatusMsg{Branch: strings.TrimSpace(string(branchOutput)), Path: cwd, Files: files}
	}
}

func currentChangedFiles() []ChangedFile {
	output, err := exec.Command("git", "status", "--short").Output()
	if err != nil {
		return nil
	}
	return parseGitStatus(string(output))
}

func parseGitStatus(output string) []ChangedFile {
	var files []ChangedFile
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if len(line) < 4 {
			continue
		}
		status := strings.TrimSpace(line[:2])
		path := strings.TrimSpace(line[3:])
		if strings.Contains(path, " -> ") {
			path = strings.TrimSpace(strings.SplitN(path, " -> ", 2)[1])
		}
		files = append(files, ChangedFile{Status: fallback(status, "M"), Path: path})
	}
	return files
}

func planToTUISteps(p *agent.Plan) []PlanStep {
	if p == nil {
		return nil
	}
	steps := make([]PlanStep, len(p.Steps))
	for i, s := range p.Steps {
		title := s.Description
		if len(title) > 30 {
			title = title[:30] + "…"
		}
		steps[i] = PlanStep{
			Title:  title,
			Status: stepStatusToEventStatus(s.Status),
		}
	}
	return steps
}

func stepStatusToEventStatus(s agent.StepStatus) EventStatus {
	switch s {
	case agent.StepRunning:
		return StatusRunning
	case agent.StepPassed:
		return StatusDone
	case agent.StepFailed:
		return StatusFailed
	case agent.StepBlocked:
		return StatusWaiting
	default:
		return StatusPending
	}
}

func capOutput(value string) string {
	value = sanitizeTerminal(value)
	const limit = 8000
	if len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[len(runes)-limit:]) + "\n… output capped; showing tail"
}

func sanitizeTerminal(value string) string {
	return ansiEscape.ReplaceAllString(value, "")
}

func truncateCells(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	var b strings.Builder
	used := 0
	for _, r := range value {
		part := string(r)
		partWidth := lipgloss.Width(part)
		if used+partWidth > max(0, width-1) {
			break
		}
		b.WriteRune(r)
		used += partWidth
	}
	return b.String() + "…"
}

func workingDirectory() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func abbreviatePath(path string) string {
	if path == "" {
		return "."
	}
	if home, err := os.UserHomeDir(); err == nil {
		if relative, err := filepath.Rel(home, path); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return "~/" + relative
		}
	}
	return path
}

func fallback(value, otherwise string) string {
	if value == "" {
		return otherwise
	}
	return value
}

func focusName(focus FocusArea) string {
	switch focus {
	case FocusTimeline:
		return "conversation"
	case FocusPlan:
		return "plan"
	case FocusInspector:
		return "inspector"
	default:
		return "composer"
	}
}

// Start wires the shared agent into the shell. The callbacks only enqueue
// messages; they never mutate view state or render from a worker goroutine.
func Start(a *agent.Agent) {
	m := NewModel(a)
	p := tea.NewProgram(m)
	if a != nil && a.Registry != nil {
		a.Registry.Confirm = func(toolName, summary string) bool {
			response := make(chan bool, 1)
			p.Send(ApprovalRequestedMsg{Command: summary, Risk: "tool confirmation", Reason: "This operation may change files or execute a command.", Response: response})
			return <-response
		}
		a.Registry.AskUser = func(tried []string, problem, question string) string {
			response := make(chan string, 1)
			p.Send(askUserMessage{tried: tried, problem: problem, question: question, response: response})
			return <-response
		}
		a.OnEvent = func(event agent.AgentEvent) {
			switch event.Type {
			case "tool_call":
				p.Send(ToolStartedMsg{ID: fmt.Sprintf("agent-tool-%d", time.Now().UnixNano()), Name: event.Name, Command: event.Args, Summary: firstArg(event.Args)})
			case "tool_result":
				p.Send(ToolFinishedMsg{Name: event.Name, Output: event.Content})
			case "plan_validate_fail":
				p.Send(TimelineEventMsg{Event: TimelineEvent{Type: EventSystem, Status: StatusFailed, Title: "VALIDATION FAILED", Body: event.Content}})
			case "cost", "context":
				p.Send(TimelineEventMsg{Event: TimelineEvent{Type: EventSystem, Status: StatusDone, Title: strings.ToUpper(event.Type), Body: event.Content}})
			}
		}
		if a.Manager != nil {
			var lastSend time.Time
			a.Manager.OnOutput = func(line string) {
				line = strings.TrimSpace(line)
				if line == "" || time.Since(lastSend) < 100*time.Millisecond {
					return
				}
				lastSend = time.Now()
				p.Send(ToolOutputMsg{Chunk: line})
			}
		}
	}
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
