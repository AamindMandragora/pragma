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

// sends a message to the provider asking it to generate a document summarizing what it did in response to the user's request
func (a *Agent) generateDoc(ctx context.Context) (string, error) {
	// starts history at the user's chat
	taskHistory := a.History[a.taskStart:]
	history := make([]llm.Message, len(taskHistory))
	copy(history, taskHistory)
	history = append(history, llm.Message{Role: "user", Content: `Write a task summary for what just happened. Follow this format exactly:

WHAT: One sentence on what was done.
WHY: One sentence on why it was needed.
HOW: 2-3 sentences on the approach taken and key implementation details.
FILES: Already tracked separately, do not list them.
OPEN: Any unresolved questions, deferred decisions, or follow-up work. Write "None" if there are none.

Be specific. Reference function names, file paths, and technical details. Do not pad with filler. Under 150 words total.`})
	ch, err := a.CurrentModel.Provider.Chat(ctx, history, nil, *a.CurrentModel)
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

// writes the summary string to a document
func (a *Agent) saveDoc(summary string) error {
	// creates the tasks folder
	err := os.MkdirAll(".agent/docs/tasks/", 0755)
	if err != nil {
		return err
	}

	timestamp := time.Now().Format("2006-01-02_150405")

	// loops through all tool calls and finds the ones that edited a file, then records the files they wrote
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

	// combines all the info together
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

	// writes to the file
	return os.WriteFile(".agent/docs/tasks/" + timestamp + ".md", []byte(doc.String()), 0644)
}

// reads from ARCHITECTURE.md
func LoadArchitecture() string {
	data, err := os.ReadFile(".agent/docs/ARCHITECTURE.md")
	if err != nil {
		return ""
	}
	return string(data)
}

// gets the n most recent task docs
func LoadRecentDocs(n int) string {
	entries, err := os.ReadDir(".agent/docs/tasks/")
	if err != nil {
		return ""
	}
	var docs []string
	start := len(entries) - n
	if start < 0 {
		start = 0
	}
	for _, entry := range entries[start:] {
		data, err := os.ReadFile(".agent/docs/tasks/" + entry.Name())
		if err != nil {
			continue
		}
		docs = append(docs, string(data))
	}
	return strings.Join(docs, "\n---\n\n")
}

// prompts the agent to update the architecture based on the task summary
func (a *Agent) updateArchitecture(ctx context.Context, summary string) {
	existing := LoadArchitecture()

	prompt := `Based on this task summary, output ONLY the changes needed to the architecture doc. Use this exact format:

SECTION: Current State
ADD: <new line to add>
REMOVE: <exact line to remove>

SECTION: Key Decisions
ADD: <new line to add>

SECTION: Roadmap > NEXT
ADD: <new item>
REMOVE: <completed item>

SECTION: Roadmap > DEFERRED
ADD: <new item>

SECTION: Roadmap > BLOCKED
ADD: <new item>

Rules:
- Only output sections that need changes. Omit unchanged sections entirely.
- Each ADD or REMOVE is one line.
- REMOVE must match existing text exactly.
- If no changes are needed, output: NO CHANGES
- Do not output the full document. Only output the diff.
`
	// if we don't have a doc, this will create it and write to the file
	if existing == "" {
		prompt = `Create a brief architecture document with three sections: Current State, Key Decisions, and Roadmap (with subsections NEXT, DEFERRED, BLOCKED). Base it on this task summary. Keep each entry to one line. Do not wrap in code fences. Output raw markdown only.

Task summary:
` + summary
		ch, err := a.CurrentModel.Provider.Chat(ctx, []llm.Message{{Role: "user", Content: prompt}}, nil, *a.CurrentModel)
		if err != nil {
			return
		}
		var text strings.Builder
		for event := range ch {
			if event.Type == "text" {
				text.WriteString(event.Text)
			}
		}
		if text.Len() > 0 {
			os.MkdirAll(".agent/docs/", 0755)
			os.WriteFile(".agent/docs/ARCHITECTURE.md", []byte(text.String()+"\n"), 0644)
		}
		return
	}

	// if there's already a doc, then we prompt the agent to compile the necessary changes
	prompt += "\nCurrent ARCHITECTURE.md:\n" + existing + "\n\nTask summary:\n" + summary

	ch, err := a.CurrentModel.Provider.Chat(ctx, []llm.Message{{Role: "user", Content: prompt}}, nil, *a.CurrentModel)
	if err != nil {
		return
	}
	var text strings.Builder
	for event := range ch {
		if event.Type == "text" {
			text.WriteString(event.Text)
		}
	}
	response := strings.TrimSpace(text.String())
	if response == "NO CHANGES" || response == "" {
		return
	}

	applyArchitectureChanges(existing, response)
}

// given the existing doc and the changes, applies them and writes back to ARCHITECTURE.md
func applyArchitectureChanges(existing string, changes string) {
	doc := existing
	lines := strings.Split(changes, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ADD: ") {
			entry := strings.TrimPrefix(line, "ADD: ")
			doc = strings.TrimRight(doc, "\n") + "\n" + entry + "\n"
		} else if strings.HasPrefix(line, "REMOVE: ") {
			entry := strings.TrimPrefix(line, "REMOVE: ")
			doc = strings.Replace(doc, entry+"\n", "", 1)
		}
	}
	os.MkdirAll(".agent/docs/", 0755)
	os.WriteFile(".agent/docs/ARCHITECTURE.md", []byte(doc), 0644)
}
