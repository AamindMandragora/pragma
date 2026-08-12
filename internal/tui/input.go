package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/AamindMandragora/pragma/internal/agent"
	"github.com/AamindMandragora/pragma/internal/db"
	"github.com/AamindMandragora/pragma/internal/tools"
)

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
		m.confirming, m.confirmAwaitReason, m.runState = true, false, RunApproval
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

func (m *TUIModel) handleKey(key tea.KeyPressMsg) tea.Cmd {
	name := keyName(key)
	if m.confirming && m.confirmAwaitReason {
		switch name {
		case "esc":
			m.confirmAwaitReason = false
			m.input.Reset()
			m.input.Placeholder = "Ask the agent…"
			return nil
		case "enter":
			reason := strings.TrimSpace(m.input.Value())
			m.input.Reset()
			m.input.Placeholder = "Ask the agent…"
			m.confirmAwaitReason, m.confirming, m.runState = false, false, RunWorking
			if reason == "" {
				m.confirmChan <- tools.ConfirmResponse{Action: tools.ConfirmRejectSilent}
			} else {
				m.confirmChan <- tools.ConfirmResponse{Action: tools.ConfirmRejectReason, Reason: reason}
			}
			return nil
		}
		if key.Mod == 0 && key.Text != "" {
			m.input.Update(key)
		}
		return nil
	}
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
		case keyApprove, keyApproveSession:
			m.confirming, m.runState = false, RunWorking
			m.confirmChan <- tools.ConfirmResponse{Action: tools.ConfirmApprove}
		case keyReject, "esc":
			m.confirming, m.runState = false, RunWorking
			m.confirmChan <- tools.ConfirmResponse{Action: tools.ConfirmRejectSilent}
		case keyRejectReason:
			m.confirmAwaitReason = true
			m.input.Reset()
			m.input.Placeholder = "Rejection reason (Enter to submit, Esc to cancel)…"
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
		} else {
			m.followTail = false
			m.timelineScroll = m.timelineMaxOffset
		}
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
		{Label: "Alt+Y / Alt+A / Alt+E / Alt+N / Alt+R", Description: "approve, approve session, edit, reject, or reject with reason (during approval)"},
		{Label: "HASH COMMANDS", Description: ""},
		{Label: "#help", Description: "show this shortcuts and hash commands help"},
		{Label: "#plan", Description: "toggle plan mode"},
		{Label: "#confirm [---|r--|rw-|rwx]", Description: "show or set tool confirm mode"},
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
	case "#confirm":
		if m.agent == nil || m.agent.Registry == nil {
			return "No agent running."
		}
		if len(parts) < 2 {
			return fmt.Sprintf("Confirm mode: %s (---=confirm all, r--=auto-approve read, rw-=auto-approve read+write, rwx=auto-approve all)", m.agent.Registry.ConfirmMode)
		}
		mode, err := tools.ParseConfirmMode(parts[1])
		if err != nil {
			return err.Error()
		}
		m.agent.Registry.ConfirmMode = mode
		return fmt.Sprintf("Confirm mode set to %s", mode)
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
		confirmMode := tools.ConfirmModeR
		if m.agent.Registry != nil {
			confirmMode = m.agent.Registry.ConfirmMode
		}
		return fmt.Sprintf("Messages: %d | Model: %s | Tool mode: %s | Plan mode: %s | Confirm: %s", max(0, len(m.agent.History)-1), m.agent.CurrentModel.Name, m.agent.CurrentModel.ToolMode, planStatus, confirmMode)
	case "#model":
		if m.agent == nil || m.agent.CurrentModel == nil {
			return "No model configured."
		}
		model := m.agent.CurrentModel
		confirmMode := tools.ConfirmModeR
		if m.agent.Registry != nil {
			confirmMode = m.agent.Registry.ConfirmMode
		}
		return fmt.Sprintf("Model: %s\n  Max tokens: %d\n  Effort: %s\n  Tool mode: %s\n  Confirm: %s\n  Provider: %s", model.Name, model.MaxTokens, model.Effort, model.ToolMode, confirmMode, model.Provider.GetName())
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
