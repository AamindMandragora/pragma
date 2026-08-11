package agent_test

import (
	"strings"
	"testing"

	"github.com/AamindMandragora/pragma/internal/agent"
)

func TestParsePlan_RawJSON(t *testing.T) {
	input := `{"id":"p1","description":"Refactor auth","steps":[{"id":"s1","description":"Extract middleware","scope":["auth.go"],"validate":"go test ./...","fallback":"fix compilation errors","max_retries":3,"estimate":500}]}`
	plan, err := agent.ParsePlan(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.ID != "p1" {
		t.Errorf("expected id p1, got %s", plan.ID)
	}
	if plan.Description != "Refactor auth" {
		t.Errorf("expected description 'Refactor auth', got %s", plan.Description)
	}
	if len(plan.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(plan.Steps))
	}
	if plan.Steps[0].MaxRetries != 3 {
		t.Errorf("expected max_retries 3, got %d", plan.Steps[0].MaxRetries)
	}
	if plan.Steps[0].Validate != "go test ./..." {
		t.Errorf("expected validate 'go test ./...', got %s", plan.Steps[0].Validate)
	}
}

func TestParsePlan_CodeFences(t *testing.T) {
	input := "Here is the plan:\n```json\n{\"description\":\"test plan\",\"steps\":[{\"description\":\"step one\"}]}\n```\n"
	plan, err := agent.ParsePlan(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Description != "test plan" {
		t.Errorf("expected 'test plan', got %s", plan.Description)
	}
	if plan.Steps[0].ID != "step-1" {
		t.Errorf("expected auto-generated id step-1, got %s", plan.Steps[0].ID)
	}
	if plan.Steps[0].MaxRetries != 2 {
		t.Errorf("expected default max_retries 2, got %d", plan.Steps[0].MaxRetries)
	}
}

func TestParsePlan_NoSteps(t *testing.T) {
	input := `{"description":"empty","steps":[]}`
	_, err := agent.ParsePlan(input)
	if err == nil {
		t.Fatal("expected error for plan with no steps")
	}
}

func TestParsePlan_InvalidJSON(t *testing.T) {
	_, err := agent.ParsePlan("this is not json")
	if err == nil {
		t.Fatal("expected error for non-JSON input")
	}
}

func TestPlanFormat(t *testing.T) {
	plan := &agent.Plan{
		Description: "Test plan",
		Steps: []agent.Step{
			{ID: "s1", Description: "First step", Scope: []string{"a.go"}, Validate: "go vet", Status: agent.StepPassed, MaxRetries: 2},
			{ID: "s2", Description: "Second step", Status: agent.StepPending, MaxRetries: 2},
		},
	}
	output := plan.Format()
	if output == "" {
		t.Fatal("Format returned empty string")
	}
	if !strings.Contains(output, "Test plan") {
		t.Error("missing plan description")
	}
	if !strings.Contains(output, "First step") {
		t.Error("missing first step")
	}
	if !strings.Contains(output, "Scope: a.go") {
		t.Error("missing scope")
	}
	if !strings.Contains(output, "Validate: go vet") {
		t.Error("missing validate")
	}
}

func TestPlanSummary(t *testing.T) {
	plan := &agent.Plan{
		Steps: []agent.Step{
			{Status: agent.StepPassed},
			{Status: agent.StepPassed},
			{Status: agent.StepFailed},
		},
	}
	summary := plan.Summary()
	if summary != "2/3 steps passed, 1 failed" {
		t.Errorf("unexpected summary: %s", summary)
	}
}

func TestExtractJSON_PlainJSON(t *testing.T) {
	plan, err := agent.ParsePlan(`  {"description":"plain","steps":[{"description":"step"}]}  `)
	if err != nil || plan.Description != "plain" {
		t.Fatalf("plain JSON plan was not parsed: plan=%#v err=%v", plan, err)
	}
}

func TestExtractJSON_WithPreamble(t *testing.T) {
	plan, err := agent.ParsePlan("Here is the plan:\n{\"description\":\"preamble\",\"steps\":[{\"description\":\"step\"}]}\n")
	if err != nil || plan.Description != "preamble" {
		t.Fatalf("plan with a short model preamble was not parsed: plan=%#v err=%v", plan, err)
	}
}
