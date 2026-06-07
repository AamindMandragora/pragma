package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/AamindMandragora/pragma/internal/llm"
)

func (a *Agent) generateDoc(ctx context.Context) (string, error) {
	taskHistory := a.History[a.taskStart:]
	history := make([]llm.Message, len(taskHistory))
	copy(history, taskHistory)
	docsConfig := a.ProviderConfig
	docsConfig.MaxTokens = 500
	history = append(history, llm.Message{Role: "user", Content: `Write a task summary for what just happened. Follow this format exactly:

WHAT: One sentence on what was done.
WHY: One sentence on why it was needed.
HOW: 2-3 sentences on the approach taken and key implementation details.
FILES: Already tracked separately, do not list them.
OPEN: Any unresolved questions, deferred decisions, or follow-up work. Write "None" if there are none.

Be specific. Reference function names, file paths, and technical details. Do not pad with filler. Under 150 words total.`})
	ch, err := a.Provider.Chat(ctx, history, nil, docsConfig)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for event := range ch {
		switch event.Type {
		case "text":
			text.WriteString(event.Text)
		case "error":
			return "", event.Err
		}
	}
	return text.String(), nil
}

func (a *Agent) saveDoc(summary string) error {
	err := os.MkdirAll(".agent/docs/commits/", 0755)
	if err != nil {
		return err
	}
	timestamp := time.Now().Format("2006-01-02_150405")

	var files []string
	for _, msg := range a.History[a.taskStart:] {
		for _, tc := range msg.TCs {
			if tc.Name == "edit_file" || tc.Name == "write_file" {
				var argsMap map[string]interface{}
				if err := json.Unmarshal(tc.Args, &argsMap); err == nil {
					if pathVal, ok := argsMap["path"]; ok {
						f := fmt.Sprintf("%v", pathVal)
						files = append(files, f)
					}
				}
			}
		}
	}

	var doc strings.Builder
	doc.WriteString(fmt.Sprintf("# Task Summary — %s\n\n", timestamp))
	if a.taskStart < len(a.History) {
		doc.WriteString(fmt.Sprintf("**User request:** %s\n\n", a.History[a.taskStart].Content))
	}
	doc.WriteString(fmt.Sprintf("**Summary:**\n%s\n\n", summary))
	if len(files) > 0 {
		doc.WriteString("**Files touched:**\n\n")
		for _, f := range files {
			doc.WriteString(fmt.Sprintf("- %s\n", f))
		}
	}

	return os.WriteFile(".agent/docs/commits/"+timestamp+".md", []byte(doc.String()), 0644)
}

func LoadArchitecture() string {
	data, err := os.ReadFile(".agent/docs/ARCHITECTURE.md")
	if err != nil {
		return ""
	}
	return string(data)
}

func LoadRecentDocs(n int) string {
	entries, err := os.ReadDir(".agent/docs/commits/")
	if err != nil {
		return ""
	}
	var docs []string
	start := len(entries) - n
	if start < 0 {
		start = 0
	}
	for _, entry := range entries[start:] {
		data, err := os.ReadFile(".agent/docs/commits/" + entry.Name())
		if err != nil {
			continue
		}
		docs = append(docs, string(data))
	}
	return strings.Join(docs, "\n---\n\n")
}

func (a *Agent) updateArchitecture(ctx context.Context, summary string) {
	existing := LoadArchitecture()
	prompt := `You maintain a living architecture document for a software project. It has three sections:

# Current State
A module-by-module map of the codebase. For each module: its purpose in one line, the key files, and any important types or interfaces it exports. Update this when new modules are added, existing ones change responsibility, or files are created/deleted/renamed.

# Key Decisions
A running log of architectural choices. Each entry is one line: what was decided, what alternative was rejected, and why. Only add entries when the task summary mentions a deliberate choice between alternatives. Do not fabricate decisions that aren't evidenced in the summary.

# Roadmap
Three subsections:
- NEXT: Work explicitly identified as the immediate next step
- DEFERRED: Work mentioned but explicitly postponed, with the reason
- BLOCKED: Work that can't proceed until something else is done, with the dependency

When a task completes something from the roadmap, move it to Current State and remove it from here. When a task reveals new work, add it to the appropriate subsection. Never remove roadmap items silently — either complete them or note why they were dropped.

Rules:
- Preserve all existing content unless the task summary directly contradicts or completes it
- Do not invent information not present in the task summary or existing doc
- Do not wrap output in code fences
- Output only the raw markdown
`
	if existing == "" {
		prompt += "No architecture doc exists yet. Create one from scratch based on this task summary.\n\n"
	} else {
		prompt += "Here is the current ARCHITECTURE.md:\n\n" + existing + "\n\n"
	}
	prompt += "Task summary:\n" + summary

	config := a.ProviderConfig
	config.MaxTokens = 1000
	ch, err := a.Provider.Chat(ctx, []llm.Message{
		{Role: "user", Content: prompt},
	}, nil, config)
	if err != nil {
		return
	}
	var text strings.Builder
	for event := range ch {
		if event.Type == "text" {
			text.WriteString(event.Text)
		}
	}
	text.WriteString("\n")
	if text.Len() > 0 {
		os.MkdirAll(".agent/docs/", 0755)
		os.WriteFile(".agent/docs/ARCHITECTURE.md", []byte(text.String()), 0644)
	}
}
