package tui_test

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/AamindMandragora/pragma/internal/agent"
	"github.com/AamindMandragora/pragma/internal/tools"
	tui "github.com/AamindMandragora/pragma/internal/tui"
)

func key(text string) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Text: text, Code: []rune(text)[0]})
}

func modifiedKey(text string, mod tea.KeyMod) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Text: text, Code: []rune(text)[0], Mod: mod})
}

func chatModel() *tui.TUIModel {
	// A non-nil agent selects the chat shell without starting any work.
	return tui.NewModel(&agent.Agent{})
}

func TestShellRendersResponsiveLayouts(t *testing.T) {
	tests := []struct {
		name    string
		width   int
		want    []string
		notWant []string
	}{
		{name: "narrow", width: 64, want: []string{"CONVERSATION", "FOCUS COMPOSER"}, notWant: []string{"INSPECTOR"}},
		{name: "medium", width: 100, want: []string{"CONVERSATION", "INSPECTOR"}},
		{name: "wide", width: 140, want: []string{"RUN / PLAN", "CONVERSATION", "INSPECTOR"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := chatModel()
			m.Update(tea.WindowSizeMsg{Width: tt.width, Height: 30})
			view := m.View()
			if view.BackgroundColor != nil {
				t.Fatalf("view at %d columns overrides the terminal background", tt.width)
			}
			for _, want := range tt.want {
				if !strings.Contains(view.Content, want) {
					t.Fatalf("view at %d columns does not contain %q", tt.width, want)
				}
			}
			for _, notWant := range tt.notWant {
				if strings.Contains(view.Content, notWant) {
					t.Fatalf("view at %d columns unexpectedly contains %q", tt.width, notWant)
				}
			}
			for _, line := range strings.Split(view.Content, "\n") {
				if lipgloss.Width(line) > tt.width+2 {
					t.Errorf("line is wider than terminal (%d > %d): %q", lipgloss.Width(line), tt.width, line)
				}
			}
		})
	}
}

func TestTypedTimelineMessagesAndOutputCap(t *testing.T) {
	m := chatModel()
	m.Update(tui.ToolStartedMsg{ID: "tool-1", Name: "go test", Summary: "go test ./..."})
	m.Update(tui.ToolOutputMsg{ID: "tool-1", Chunk: "ok package\n"})
	m.Update(tui.ToolFinishedMsg{ID: "tool-1", Name: "go test", Output: "ok package", ExitCode: 0})

	view := m.View().Content
	if !strings.Contains(view, "DONE") || !strings.Contains(view, "ok package") {
		t.Fatalf("tool lifecycle was not retained in the rendered timeline: %q", view)
	}

	m.Update(tui.AgentTokenMsg{EventID: "agent-1", Token: "hello "})
	m.Update(tui.AgentTokenMsg{EventID: "agent-1", Token: "world"})
	m.Update(tui.AgentTurnFinishedMsg{Response: "done"})
	view = m.View().Content
	if !strings.Contains(view, "hello world") {
		t.Fatalf("streaming agent event was not coalesced: %q", view)
	}

	m.Update(tui.ToolStartedMsg{ID: "tool-2", Name: "flood"})
	m.Update(tui.ToolOutputMsg{ID: "tool-2", Chunk: strings.Repeat("x", 10000)})
	if !strings.Contains(m.View().Content, "output capped") {
		t.Fatal("tool output was not capped")
	}
}

func TestNavigationAndFakeEvents(t *testing.T) {
	event := tui.TimelineEventMsg{Event: tui.TimelineEvent{ID: "fake-user", Type: tui.EventUser, Status: tui.StatusDone, Title: "YOU", Body: "Revamp the Pragma workspace."}}
	source := NewFakeEventSource([]tea.Msg{event})
	m := tui.NewModel(&agent.Agent{})
	m.Update(tea.WindowSizeMsg{Width: 64, Height: 24})
	cmd := source.Next()
	if cmd == nil {
		t.Fatal("fake source did not produce a command")
	}
	m.Update(cmd())
	if !strings.Contains(m.View().Content, "Revamp the Pragma workspace.") {
		t.Fatalf("fake event was not appended: %q", m.View().Content)
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyDown}))
	m.Update(key("p"))
	if strings.Contains(m.View().Content, "INSPECTOR · PLAN") {
		t.Fatalf("plain p unexpectedly activated an inspector shortcut: %q", m.View().Content)
	}
	m.Update(modifiedKey("p", tea.ModAlt|tea.ModShift))
	if !strings.Contains(m.View().Content, "INSPECTOR · PLAN") {
		t.Fatalf("Alt+Shift+P did not activate the plan inspector: %q", m.View().Content)
	}
}

func TestComposerAcceptsShortcutLetters(t *testing.T) {
	m := chatModel()
	for _, text := range []string{"p", "f", "t", "g", "s", "?"} {
		m.Update(key(text))
	}
	if !strings.Contains(m.View().Content, "pftgs?") {
		t.Fatalf("composer treated ordinary text as a shortcut: %q", m.View().Content)
	}
}

func TestPlanModeShortcutAndApproval(t *testing.T) {
	m := chatModel()
	m.Update(modifiedKey("p", tea.ModAlt))
	if !strings.Contains(m.View().Content, "PLAN") {
		t.Fatalf("Alt+P did not enable plan mode: %q", m.View().Content)
	}

	_, cmd := m.Update(tui.PlanGeneratedMsg{Plan: &agent.Plan{
		Description: "Test plan",
		Steps:       []agent.Step{{Description: "Run the test step"}},
	}})
	if cmd != nil {
		t.Fatal("receiving a generated plan unexpectedly started work")
	}
	if !strings.Contains(m.View().Content, "PLAN APPROVAL") {
		t.Fatalf("generated plan did not request approval: %q", m.View().Content)
	}
	m.Update(key("y"))
	if !strings.Contains(m.View().Content, "PLAN APPROVAL") {
		t.Fatalf("plain y unexpectedly approved a plan: %q", m.View().Content)
	}
	_, cmd = m.Update(modifiedKey("y", tea.ModAlt))
	if cmd == nil || !strings.Contains(m.View().Content, "PLAN APPROVED") {
		t.Fatalf("approving a plan did not start execution: view=%q cmd=%v", m.View().Content, cmd != nil)
	}
}

func TestHashHelpListsShortcutsAndCommands(t *testing.T) {
	m := chatModel()
	for _, r := range "#help" {
		m.Update(key(string(r)))
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	view := m.View().Content
	for _, want := range []string{"shortcuts and hash commands", "Alt+P", "HASH COMMANDS", "#plan", "#budget [amount]"} {
		if !strings.Contains(view, want) {
			t.Fatalf("#help output missing %q: %q", want, view)
		}
	}
	if strings.Contains(view, "Ctrl+") {
		t.Fatalf("#help output still advertises Control shortcuts: %q", view)
	}
}

func TestHelpAndPlanViewsCanScrollWithinTerminal(t *testing.T) {
	m := chatModel()
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, r := range "#help" {
		m.Update(key(string(r)))
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter}))
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}))
	view := m.View().Content
	if !strings.Contains(view, "#quit") {
		t.Fatalf("help menu did not page to its final entries: %q", view)
	}
	if lipgloss.Height(view) > 24 {
		t.Fatalf("help menu overflowed terminal height: %d", lipgloss.Height(view))
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEsc}))

	steps := make([]agent.Step, 20)
	for i := range steps {
		steps[i] = agent.Step{Description: "Plan step " + string(rune('A'+i))}
	}
	m.Update(tui.PlanGeneratedMsg{Plan: &agent.Plan{Description: "Long plan", Steps: steps}})
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}))
	view = m.View().Content
	if !strings.Contains(view, "Plan step T") {
		t.Fatalf("plan approval view did not scroll to its final step: %q", view)
	}
	if lipgloss.Height(view) > 24 {
		t.Fatalf("plan approval view overflowed terminal height: %d", lipgloss.Height(view))
	}
}

func TestConversationTimelineScrollsFromStartToTail(t *testing.T) {
	m := chatModel()
	m.Update(tea.WindowSizeMsg{Width: 64, Height: 24})
	for i := 0; i < 16; i++ {
		m.Update(tui.TimelineEventMsg{Event: tui.TimelineEvent{
			Type:   tui.EventAgent,
			Title:  "AGENT " + string(rune('A'+i)),
			Status: tui.StatusDone,
			Body:   "chat-entry-" + string(rune('A'+i)) + " " + strings.Repeat("detail ", 30),
		}})
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyTab}))
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	view := m.View().Content
	if !strings.Contains(view, "chat-entry-A") {
		t.Fatalf("Home did not reach the beginning of the conversation: %q", view)
	}
	if lipgloss.Height(view) > 24 {
		t.Fatalf("conversation overflowed terminal height at its start: %d", lipgloss.Height(view))
	}
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}))
	view = m.View().Content
	if !strings.Contains(view, "chat-entry-P") {
		t.Fatalf("End did not return to the conversation tail: %q", view)
	}
	if lipgloss.Height(view) > 24 {
		t.Fatalf("conversation overflowed terminal height at its tail: %d", lipgloss.Height(view))
	}
	m.Update(tui.ApprovalRequestedMsg{Command: "go test ./...", Response: make(chan tools.ConfirmResponse, 1)})
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	view = m.View().Content
	if !strings.Contains(view, "AGENT A") {
		t.Fatalf("conversation could not scroll while approval was open: %q", view)
	}
}

func TestApprovalModalAndSanitization(t *testing.T) {
	m := chatModel()
	m.Update(tui.ApprovalRequestedMsg{Command: "rm -rf ./build", Directory: "/tmp/project", Risk: "destructive", Reason: "cleanup requested", Response: make(chan tools.ConfirmResponse, 1)})
	view := m.View().Content
	for _, want := range []string{"APPROVAL REQUIRED", "rm -rf ./build", "destructive", "[Alt+Y] approve once", "[Alt+N]", "reject"} {
		if !strings.Contains(view, want) {
			t.Fatalf("approval view missing %q", want)
		}
	}
	m = chatModel()
	m.Update(tui.TimelineEventMsg{Event: tui.TimelineEvent{Type: tui.EventSystem, Title: "SANITIZED", Body: "safe\x1b[31mdanger\x1b[0m"}})
	view = m.View().Content
	if !strings.Contains(view, "safedanger") || strings.Contains(view, "\x1b[31m") {
		t.Fatalf("terminal escape sequence was not removed: %q", view)
	}
	m.Update(tui.ToolStartedMsg{ID: "capped", Name: "large output"})
	m.Update(tui.ToolOutputMsg{ID: "capped", Chunk: strings.Repeat("x", 9000)})
	if !strings.Contains(m.View().Content, "output capped") {
		t.Fatal("large output did not expose its tail/cap notice")
	}
}

func TestEditorPasteAndMultiline(t *testing.T) {
	e := tui.Editor{Width: 20, Focused: true}
	e.Update(tea.PasteMsg{Content: "one\ntwo"})
	if e.Value() != "one\ntwo" {
		t.Fatalf("paste was not preserved: %q", e.Value())
	}
	e.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyLeft}))
	e.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnter, Mod: tea.ModShift}))
	if !strings.Contains(e.Value(), "\n") {
		t.Fatal("multiline editor did not retain a newline")
	}
}
