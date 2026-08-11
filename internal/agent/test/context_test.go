package agent_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/AamindMandragora/pragma/internal/agent"
	"github.com/AamindMandragora/pragma/internal/llm"
)

func TestHistoryForModelCompactsWithoutChangingRawHistory(t *testing.T) {
	large := strings.Repeat("source code and command output ", 2000)
	history := []llm.Message{{Role: "system", Content: "system prompt"}}
	for i := 0; i < 8; i++ {
		history = append(history,
			llm.Message{Role: "user", Content: "inspect the project"},
			llm.Message{Role: "assistant", TCs: []llm.ToolCall{{Id: "call", Name: "read_file", Args: []byte(`{"path":"file.go"}`)}}},
			llm.Message{Role: "tool", TCID: "call", Content: large},
		)
	}

	a := &agent.Agent{History: history, ContextMaxTokens: 24000}
	modelHistory := a.HistoryForModel()

	if a.ContextSummary == "" {
		t.Fatal("expected automatic context compaction")
	}
	if len(a.History) != len(history) {
		t.Fatalf("raw history changed: got %d messages, want %d", len(a.History), len(history))
	}
	if len(modelHistory) >= len(history) {
		t.Fatalf("model history was not reduced: got %d messages, raw %d", len(modelHistory), len(history))
	}
	if len(modelHistory) != 2 || modelHistory[1].Role != "user" {
		t.Fatalf("compacted history should contain system plus one user summary, got %#v", modelHistory)
	}
	if strings.Contains(modelHistory[1].Content, "function_call") || strings.Contains(modelHistory[1].Content, "call_") {
		t.Fatal("compacted user summary should not contain function-call protocol identifiers")
	}
	if !strings.Contains(modelHistory[1].Content, "read_file") {
		t.Fatal("compacted model history is missing the summarized tool activity")
	}
}

func TestCompactKeepsRawHistory(t *testing.T) {
	a := &agent.Agent{History: []llm.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "please inspect this"},
	}}

	message := a.Compact()
	if !strings.Contains(message, "Context compacted") {
		t.Fatalf("unexpected compact result: %s", message)
	}
	if len(a.History) != 2 {
		t.Fatalf("compact removed raw history")
	}
	if a.ContextSummary == "" {
		t.Fatal("compact did not create a summary")
	}
}

func TestCompactionRemovesToolCallProtocol(t *testing.T) {
	history := []llm.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "inspect this file"},
		{Role: "assistant", TCs: []llm.ToolCall{{Id: "call-1", Name: "read_file", Args: []byte(`{"path":"large.go"}`)}}},
		{Role: "tool", TCID: "call-1", Content: strings.Repeat("source code ", 5000)},
	}
	a := &agent.Agent{History: history, ContextMaxTokens: 3000}
	modelHistory := a.HistoryForModel()

	if len(modelHistory) != 2 || modelHistory[1].Role != "user" {
		t.Fatalf("unexpected compacted history shape: %#v", modelHistory)
	}
	if modelHistory[1].TCs != nil || modelHistory[1].TCID != "" {
		t.Fatal("synthetic compacted user message contains tool-call metadata")
	}
	for _, message := range modelHistory {
		if message.Role == "tool" || len(message.TCs) > 0 {
			t.Fatal("compacted history still contains tool protocol messages")
		}
	}
	if !strings.Contains(modelHistory[1].Content, "read_file") {
		t.Fatal("compacted history omitted the completed tool activity")
	}
}

func TestCompactionRefreshesAfterNewHistory(t *testing.T) {
	a := &agent.Agent{History: []llm.Message{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "old direction"},
	}, ContextMaxTokens: 2000}
	a.Compact()
	a.History = append(a.History, llm.Message{Role: "user", Content: "new direction"})

	modelHistory := a.HistoryForModel()
	if len(modelHistory) != 2 || !strings.Contains(modelHistory[1].Content, "new direction") {
		t.Fatal("compaction reused a stale summary after history changed")
	}
}

func TestCompactionPreservesLatestUserDirection(t *testing.T) {
	history := []llm.Message{{Role: "system", Content: "system prompt"}}
	for i := 0; i < 40; i++ {
		history = append(history,
			llm.Message{Role: "user", Content: fmt.Sprintf("old direction %d", i)},
			llm.Message{Role: "assistant", TCs: []llm.ToolCall{{Id: fmt.Sprintf("call-%d", i), Name: "read_file", Args: []byte(`{"path":"old.go"}`)}}},
			llm.Message{Role: "tool", TCID: fmt.Sprintf("call-%d", i), Content: strings.Repeat("old tool output ", 500)},
		)
	}
	latest := "Stop reading files. Make the concrete TUI edit now and run the tests."
	history = append(history, llm.Message{Role: "user", Content: latest})

	a := &agent.Agent{History: history, ContextMaxTokens: 3000}
	modelHistory := a.HistoryForModel()
	if len(modelHistory) != 2 || !strings.Contains(modelHistory[1].Content, latest) {
		t.Fatal("compaction dropped the latest user direction")
	}
}
