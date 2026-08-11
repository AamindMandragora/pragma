package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/AamindMandragora/pragma/internal/agent"
)

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

func (m *TUIModel) requestApproval(request ApprovalRequestedMsg) tea.Cmd {
	if request.Response == nil {
		request.Response = m.confirmChan
	}
	return func() tea.Msg { return request }
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
