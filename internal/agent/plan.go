package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/AamindMandragora/pragma/internal/llm"
)

type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepRunning StepStatus = "running"
	StepPassed  StepStatus = "passed"
	StepFailed  StepStatus = "failed"
	StepBlocked StepStatus = "blocked"
)

type Step struct {
	ID          string     `json:"id"`
	Description string     `json:"description"`
	Scope       []string   `json:"scope"`
	Validate    string     `json:"validate"`
	Fallback    string     `json:"fallback"`
	MaxRetries  int        `json:"max_retries"`
	Estimate    int        `json:"estimate"`
	Status      StepStatus `json:"status"`
}

type Plan struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Steps       []Step `json:"steps"`
}

type StepResult struct {
	Passed     bool
	Response   string
	ScopeWarns []string
	Retries    int
	Err        error
}

func ParsePlan(text string) (*Plan, error) {
	jsonStr := extractJSON(text)
	if jsonStr == "" {
		return nil, fmt.Errorf("no JSON plan found in response")
	}
	var plan Plan
	if err := json.Unmarshal([]byte(jsonStr), &plan); err != nil {
		return nil, fmt.Errorf("invalid plan JSON: %w", err)
	}
	if plan.ID == "" {
		plan.ID = "plan-1"
	}
	for i := range plan.Steps {
		if plan.Steps[i].ID == "" {
			plan.Steps[i].ID = fmt.Sprintf("step-%d", i+1)
		}
		if plan.Steps[i].MaxRetries == 0 {
			plan.Steps[i].MaxRetries = 2
		}
		if plan.Steps[i].MaxRetries < 0 {
			plan.Steps[i].MaxRetries = 0
		}
		if plan.Steps[i].Status == "" {
			plan.Steps[i].Status = StepPending
		}
	}
	if len(plan.Steps) == 0 {
		return nil, fmt.Errorf("plan has no steps")
	}
	return &plan, nil
}

func extractJSON(text string) string {
	lower := strings.ToLower(text)
	if idx := strings.Index(lower, "```json"); idx != -1 {
		start := idx + len("```json")
		if end := strings.Index(text[start:], "```"); end != -1 {
			return strings.TrimSpace(text[start : start+end])
		}
	}
	if idx := strings.Index(text, "```"); idx != -1 {
		start := idx + len("```")
		if end := strings.Index(text[start:], "```"); end != -1 {
			candidate := strings.TrimSpace(text[start : start+end])
			if strings.HasPrefix(candidate, "{") {
				return candidate
			}
		}
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "{") {
		return text
	}
	// Models occasionally add a short sentence before an otherwise valid
	// object despite the raw-JSON instruction. Decode from each opening brace
	// so that the plan flow remains usable with those responses and with
	// fenced blocks that use an unusual language-case.
	for i := 0; i < len(text); i++ {
		if text[i] != '{' {
			continue
		}
		var object json.RawMessage
		decoder := json.NewDecoder(strings.NewReader(text[i:]))
		if err := decoder.Decode(&object); err == nil && len(object) > 0 {
			return string(object)
		}
	}
	return ""
}

func (p *Plan) Format() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Plan: %s\n", p.Description))
	b.WriteString(strings.Repeat("─", 40))
	b.WriteString("\n")
	for i, step := range p.Steps {
		sym := stepStatusSymbol(step.Status)
		b.WriteString(fmt.Sprintf("\n%s Step %d: %s\n", sym, i+1, step.Description))
		if len(step.Scope) > 0 {
			b.WriteString(fmt.Sprintf("  Scope: %s\n", strings.Join(step.Scope, ", ")))
		}
		if step.Validate != "" {
			b.WriteString(fmt.Sprintf("  Validate: %s\n", step.Validate))
		}
		if step.Fallback != "" {
			b.WriteString(fmt.Sprintf("  Fallback: %s\n", step.Fallback))
		}
		b.WriteString(fmt.Sprintf("  Max retries: %d\n", step.MaxRetries))
		if step.Estimate > 0 {
			b.WriteString(fmt.Sprintf("  Estimate: ~%d tokens\n", step.Estimate))
		}
	}
	return b.String()
}

func stepStatusSymbol(s StepStatus) string {
	switch s {
	case StepRunning:
		return "◉"
	case StepPassed:
		return "✓"
	case StepFailed:
		return "✕"
	case StepBlocked:
		return "!"
	default:
		return "○"
	}
}

func (p *Plan) Summary() string {
	passed, failed := 0, 0
	for _, step := range p.Steps {
		switch step.Status {
		case StepPassed:
			passed++
		case StepFailed, StepBlocked:
			failed++
		}
	}
	return fmt.Sprintf("%d/%d steps passed, %d failed", passed, len(p.Steps), failed)
}

func PlanGenerationPrompt(userPrompt string) string {
	return fmt.Sprintf(`You are in plan mode. Instead of immediately acting on the following task, generate a structured execution plan as JSON.

Task: %s

Output ONLY a JSON object with this exact schema (no code fences, no commentary):
{
  "id": "plan-1",
  "description": "Brief description of the overall plan",
  "steps": [
    {
      "id": "step-1",
      "description": "What this step accomplishes",
      "scope": ["file1.go", "file2.go"],
      "validate": "go test ./...",
      "fallback": "Instruction if validation fails",
      "max_retries": 2,
      "estimate": 1000
    }
  ]
}

Rules:
- Each step should be a discrete, independently verifiable unit of work
- scope lists the files the step may need to read or modify
- validate is a shell command that verifies the step succeeded (empty string if none needed)
- fallback is an instruction for retrying if validation fails (empty string if none)
- max_retries defaults to 2
- estimate is an approximate token budget hint for the step (0 if unknown)
- Keep the number of steps manageable (typically 3-8)
- Output raw JSON only`, userPrompt)
}

func ExecuteStep(ctx context.Context, a *Agent, plan *Plan, stepIndex int) StepResult {
	step := &plan.Steps[stepIndex]
	step.Status = StepRunning
	stepHistoryStart := len(a.History)

	prompt := buildStepPrompt(step, stepIndex, len(plan.Steps))
	var allScopeWarns []string
	warnedPaths := make(map[string]bool)

	for retry := 0; retry <= step.MaxRetries; retry++ {
		response, err := a.Run(ctx, prompt)
		scopeWarns := checkScopeViolations(a.History[stepHistoryStart:], step)
		for _, warning := range scopeWarns {
			path := warning
			if !warnedPaths[path] {
				warnedPaths[path] = true
				allScopeWarns = append(allScopeWarns, warning)
			}
		}
		if err != nil {
			step.Status = StepFailed
			return StepResult{Passed: false, Err: err, Response: response, ScopeWarns: allScopeWarns, Retries: retry}
		}

		if step.Validate == "" {
			step.Status = StepPassed
			return StepResult{Passed: true, Response: response, ScopeWarns: allScopeWarns, Retries: retry}
		}

		if runValidateCommand(ctx, step.Validate) {
			step.Status = StepPassed
			return StepResult{Passed: true, Response: response, ScopeWarns: allScopeWarns, Retries: retry}
		}

		a.emit(AgentEvent{Type: "plan_validate_fail", Name: step.ID, Content: fmt.Sprintf("Validation failed: %s", step.Validate)})

		if retry < step.MaxRetries {
			if step.Fallback != "" {
				prompt = fmt.Sprintf("%s\n\nThe validation command '%s' failed. This is retry %d/%d.", step.Fallback, step.Validate, retry+1, step.MaxRetries)
			} else {
				prompt = fmt.Sprintf("The validation command '%s' failed. Please investigate and fix the issue. This is retry %d/%d.", step.Validate, retry+1, step.MaxRetries)
			}
		} else {
			a.RequireAskUser(fmt.Sprintf("Step %d (%s) failed validation after %d retries. Command: %s", stepIndex+1, step.Description, step.MaxRetries, step.Validate))
			askResponse, askErr := a.Run(ctx, "The validation for this step has failed after all retries. Ask the user how to proceed.")
			if askErr != nil {
				step.Status = StepFailed
				return StepResult{Passed: false, Err: askErr, Response: askResponse, ScopeWarns: allScopeWarns, Retries: step.MaxRetries}
			}
			if runValidateCommand(ctx, step.Validate) {
				step.Status = StepPassed
				return StepResult{Passed: true, Response: askResponse, ScopeWarns: allScopeWarns, Retries: step.MaxRetries}
			}
			step.Status = StepFailed
			return StepResult{Passed: false, Response: "Validation still failing after user intervention", ScopeWarns: allScopeWarns, Retries: step.MaxRetries}
		}
	}
	step.Status = StepFailed
	return StepResult{Passed: false, Response: "Unexpected end of retry loop", ScopeWarns: allScopeWarns}
}

func buildStepPrompt(step *Step, index, total int) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Execute step %d of %d: %s", index+1, total, step.Description))
	if len(step.Scope) > 0 {
		b.WriteString(fmt.Sprintf("\n\nThe primary files for this step are: %s. You may read other files for context, but modifications should be limited to these files when possible.", strings.Join(step.Scope, ", ")))
	}
	if step.Validate != "" {
		b.WriteString(fmt.Sprintf("\n\nAfter completing the work, the following command will be used to validate success: %s", step.Validate))
	}
	b.WriteString("\n\nComplete this step now.")
	return b.String()
}

func checkScopeViolations(messages []llm.Message, step *Step) []string {
	if len(step.Scope) == 0 {
		return nil
	}
	scopeSet := make(map[string]bool)
	for _, f := range step.Scope {
		scopeSet[canonicalPath(f)] = true
	}
	seen := make(map[string]bool)
	var warnings []string
	for _, msg := range messages {
		for _, tc := range msg.TCs {
			if tc.Name == "edit_file" || tc.Name == "write_file" || tc.Name == "delete_file" || tc.Name == "move_file" {
				for _, path := range extractFilePaths(tc.Args) {
					canonical := canonicalPath(path)
					if canonical != "" && !scopeContains(scopeSet, canonical) && !seen[canonical] {
						seen[canonical] = true
						warnings = append(warnings, fmt.Sprintf("Touched %s (outside step scope)", path))
					}
				}
			}
		}
	}
	return warnings
}

func extractFilePath(args json.RawMessage) string {
	paths := extractFilePaths(args)
	if len(paths) > 0 {
		return paths[0]
	}
	return ""
}

func extractFilePaths(args json.RawMessage) []string {
	var parsed map[string]interface{}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return nil
	}
	var paths []string
	for _, key := range []string{"path", "from", "to"} {
		if path, ok := parsed[key].(string); ok && strings.TrimSpace(path) != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

func canonicalPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return filepath.Clean(filepath.ToSlash(value))
	}
	return filepath.Clean(filepath.ToSlash(absolute))
}

func scopeContains(scopeSet map[string]bool, path string) bool {
	if scopeSet[path] {
		return true
	}
	for scope := range scopeSet {
		if strings.HasPrefix(path, scope+"/") {
			return true
		}
	}
	return false
}

func runValidateCommand(ctx context.Context, command string) bool {
	if command == "" {
		return true
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	return cmd.Run() == nil
}
