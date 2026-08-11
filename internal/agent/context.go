package agent

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/AamindMandragora/pragma/internal/llm"
)

const (
	// DefaultMaxInputTokens leaves room for an 8192-token response on a typical
	// 32k context window.
	DefaultMaxInputTokens  = 24000
	contextCompactionAt    = 0.75
	maxModelToolChars      = 12000
	maxContextSummaryChars = 12000
	maxLatestUserChars     = 6000
)

// Compact replaces the old part of the model-facing context with a compact
// local summary. The full history remains in memory and in the session DB.
func (a *Agent) Compact() string {
	if len(a.History) <= 1 {
		return "Nothing to compact."
	}

	before := len(a.History) - 1
	a.ContextSummary = summarizeHistory(a.History[1:])
	a.contextSummaryAt = len(a.History)
	a.compactions++
	return fmt.Sprintf("Context compacted: summarized %d earlier messages; raw session history was preserved.", before)
}

// ClearContext removes the model's current in-memory conversation while
// leaving the persisted session history untouched.
func (a *Agent) ClearContext() {
	a.ContextSummary = ""
	a.contextSummaryAt = 0
	a.compactions = 0
	if len(a.History) > 1 {
		a.History = a.History[:1]
	}
}

// HistoryForModel returns a bounded copy of the history for one provider
// request. History stored on Agent is deliberately left untouched so a task
// can still be resumed and audited later.
func (a *Agent) HistoryForModel() []llm.Message {
	if len(a.History) == 0 {
		return nil
	}

	bounded := modelMessages(a.History)
	limit := a.ContextMaxTokens
	if limit <= 0 {
		limit = DefaultMaxInputTokens
	}

	threshold := int(float64(limit) * contextCompactionAt)
	if a.ContextSummary == "" && estimateMessages(bounded) <= threshold {
		return bounded
	}

	if a.ContextSummary == "" || len(a.History) != a.contextSummaryAt {
		a.ContextSummary = summarizeHistory(a.History[1:])
		a.contextSummaryAt = len(a.History)
		a.compactions++
		a.emit(AgentEvent{
			Type:    "context",
			Content: fmt.Sprintf("Context is large; compacting before the next model call (%d/%d estimated tokens).", estimateMessages(bounded), limit),
		})
	}

	return compactedHistory(a.History, a.ContextSummary)
}

// compactedHistory keeps the original system prompt and replaces every prior
// conversation/tool message with one synthetic user message. This deliberately
// removes function-call protocol items from the next provider request while
// preserving the useful facts from the old conversation.
func compactedHistory(history []llm.Message, summary string) []llm.Message {
	if len(history) == 0 {
		return nil
	}

	system := modelMessage(history[0])
	context := "Compacted historical context. All previous tool calls are complete; no previous tool call is pending. Treat earlier activity as background facts and do not replay those calls. The section labeled 'Latest user direction' is the active request; follow it even though the earlier history has been compacted.\n\n" + summary
	return []llm.Message{system, {Role: "user", Content: context}}
}

func modelMessages(history []llm.Message) []llm.Message {
	result := make([]llm.Message, 0, len(history))
	for _, msg := range history {
		result = append(result, modelMessage(msg))
	}
	return result
}

func modelMessage(msg llm.Message) llm.Message {
	limit := maxModelToolChars
	if msg.Role != "tool" {
		limit = maxModelToolChars * 2
	}
	if len(msg.Content) > limit {
		msg.Content = truncate(msg.Content, limit) + fmt.Sprintf("\n\n[tool output truncated in model context; original length %d bytes]", len(msg.Content))
	}
	return msg
}

func estimateMessages(messages []llm.Message) int {
	chars := 0
	for _, msg := range messages {
		chars += len(msg.Role) + len(msg.Content) + len(msg.TCID) + 32
		for _, tc := range msg.TCs {
			chars += len(tc.Id) + len(tc.Name) + len(tc.Args) + 32
		}
	}
	// Four characters per token is intentionally conservative for source code.
	return (chars + 3) / 4
}

func summarizeHistory(history []llm.Message) string {
	latestUserIndex := -1
	latestUser := ""
	for i, msg := range history {
		if msg.Role == "user" {
			latestUserIndex = i
			latestUser = strings.TrimSpace(msg.Content)
		}
	}

	latestSection := ""
	if latestUser != "" {
		latestSection = "\n\nLatest user direction (preserved verbatim):\n" + truncate(latestUser, maxLatestUserChars)
	}
	summaryLimit := maxContextSummaryChars - len(latestSection)
	if summaryLimit < 1000 {
		summaryLimit = 1000
	}

	var b strings.Builder
	b.WriteString("Earlier conversation and tool activity (details may be abbreviated; the latest user direction is preserved below):\n")

	for i, msg := range history {
		if b.Len() >= summaryLimit {
			break
		}
		if i == latestUserIndex {
			continue
		}

		switch msg.Role {
		case "user":
			writeSummaryLine(&b, "User", msg.Content, 700)
		case "assistant":
			if msg.Content != "" {
				writeSummaryLine(&b, "Assistant", msg.Content, 700)
			}
			if len(msg.TCs) > 0 {
				var calls []string
				for _, tc := range msg.TCs {
					calls = append(calls, tc.Name+"("+truncate(string(tc.Args), 240)+")")
				}
				writeSummaryLine(&b, "Tools", strings.Join(calls, ", "), 1200)
			}
		case "tool":
			writeSummaryLine(&b, "Tool result", msg.Content, 400)
		case "system":
			writeSummaryLine(&b, "System", msg.Content, 400)
		}
	}

	return truncate(strings.TrimSpace(b.String()), summaryLimit) + latestSection
}

func writeSummaryLine(b *strings.Builder, label, content string, limit int) {
	content = strings.Join(strings.Fields(content), " ")
	if content == "" {
		return
	}
	b.WriteString("- ")
	b.WriteString(label)
	b.WriteString(": ")
	b.WriteString(truncate(content, limit))
	b.WriteByte('\n')
}

func truncate(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "..."
}
