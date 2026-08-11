package tui

import (
	"context"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/AamindMandragora/pragma/internal/agent"
	"github.com/AamindMandragora/pragma/internal/tools"
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
		Response  chan tools.ConfirmResponse
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
	confirmAwaitReason bool
	confirmCmd         string
	confirmInfo        ApprovalRequestedMsg
	confirmChan        chan tools.ConfirmResponse
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
		confirmChan:  make(chan tools.ConfirmResponse, 1),
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

type askUserMessage struct {
	tried             []string
	problem, question string
	response          chan string
}
