package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/AamindMandragora/pragma/internal/agent"
	"github.com/AamindMandragora/pragma/internal/tools"
)

// Start wires the shared agent into the shell. The callbacks only enqueue
// messages; they never mutate view state or render from a worker goroutine.
func Start(a *agent.Agent) {
	m := NewModel(a)
	p := tea.NewProgram(m)
	if a != nil && a.Registry != nil {
		a.Registry.Confirm = func(toolName, summary string) tools.ConfirmResponse {
			response := make(chan tools.ConfirmResponse, 1)
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
