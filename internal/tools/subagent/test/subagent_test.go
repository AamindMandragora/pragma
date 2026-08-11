package subagent_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/AamindMandragora/pragma/internal/tools/subagent"
)

func TestToolDelegatesPromptAndBudget(t *testing.T) {
	var gotPrompt string
	var gotBudget float64
	tool := &subagent.Tool{Run: func(prompt string, budget float64) (string, error) {
		gotPrompt = prompt
		gotBudget = budget
		return "child result", nil
	}}

	got, err := tool.Execute(json.RawMessage(`{"prompt":"  inspect the parser  ","budget":0.25}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "child result" || gotPrompt != "inspect the parser" || gotBudget != 0.25 {
		t.Fatalf("unexpected delegation: result=%q prompt=%q budget=%v", got, gotPrompt, gotBudget)
	}
}

func TestToolValidatesPromptAndBudget(t *testing.T) {
	tool := &subagent.Tool{Run: func(string, float64) (string, error) { return "", nil }}
	for name, args := range map[string]string{
		"missing prompt":  `{"prompt":" "}`,
		"negative budget": `{"prompt":"work","budget":-1}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := tool.Execute(json.RawMessage(args)); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestToolReturnsRunnerError(t *testing.T) {
	want := errors.New("child failed")
	tool := &subagent.Tool{Run: func(string, float64) (string, error) { return "", want }}
	_, err := tool.Execute(json.RawMessage(`{"prompt":"work"}`))
	if !errors.Is(err, want) {
		t.Fatalf("expected runner error %v, got %v", want, err)
	}
}
