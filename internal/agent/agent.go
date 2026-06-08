package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/AamindMandragora/pragma/internal/llm"
	"github.com/AamindMandragora/pragma/internal/tools"
)

type AgentEvent struct {
	Type    string
	Name    string
	Args    string
	Content string
}

type Agent struct {
	CurrentModel     *llm.Model
	ModelTiers       []llm.ModelTier
	Registry         *tools.Registry
	History          []llm.Message
	OnEvent          func(AgentEvent)
	taskStart        int
	TaskInputTokens  int
	TaskOutputTokens int
	TaskCost         float64
	SessionCost      float64
	LastModelUsed    string
	Budget           float64
}

func NewAgent(model *llm.Model, registry *tools.Registry) *Agent {
	a := &Agent{CurrentModel: model, Registry: registry}
	prompt := a.buildSystemPrompt()
	arch := LoadArchitecture()
	if arch != "" {
		prompt += "\n# Project Context\n\n" + arch + "\n"
	}
	recent := LoadRecentDocs(5)
	if recent != "" {
		prompt += "\n# Recent Changes\n\n" + recent + "\n"
	}
	a.History = []llm.Message{{Role: "system", Content: prompt}}
	return a
}

func (a *Agent) buildSystemPrompt() string {
	prompt := `You are Pragma, a coding agent that runs in the terminal.

You help users with software engineering tasks: fixing bugs, writing features, refactoring, explaining code, and running commands.

# Rules

- Be concise. Your output is displayed in a terminal. No preamble, no sign-offs, no filler.
- Never apologize for mistakes. Fix them and move on.
- Never explain what you're about to do. Just do it.
- Never generate or guess URLs unless they are directly relevant to a programming task.
- Never create files unless absolutely necessary. Prefer editing existing files.
- If you cannot help with something, say so briefly without lecturing about why.

# Prioritize correctness

Prioritize technical accuracy over validating the user's assumptions. If the user is wrong, say so directly. Respectful correction is more valuable than false agreement. When uncertain, investigate before confirming.

# Tool usage

You have tools available to read files, write files, and run commands. Use them to accomplish tasks — do not ask the user to run commands for you.

- Use read_file to inspect code before editing it.
- Use write_file to create or overwrite files.
- When using write_file to create a markdown file, do not write it in the form of a code block. ALWAYS output raw markdown directly. You may still have code blocks within the raw markdown for different languages.
- Use edit_file to make targeted changes to existing files. Provide the exact text to replace and the new text.
- NEVER use write_file to modify an existing file. Always use edit_file. If edit_file fails because of a text mismatch, use read_file to see the exact content, then retry edit_file with the correct text.
- Use run_command to execute shell commands, run tests, or build the project.
- When running a non-trivial command, briefly explain what it does in one line.
- Use web_fetch to read web pages, documentation, API references, or any URL. the user may give you URLs to read, or you can fetch documentation pages you know about.
- Never use run_command to communicate with the user. All communication goes in your response text.
- If you need to run multiple independent commands, call them all rather than waiting between them.
- If you need to reason about a tool's output before deciding what to do next, make only ONE tool call. You will see the result and can then decide your next action. Only make multiple tool calls in one response if they are independent of each other.

# Code references

When referencing specific code, include the file path and line number (e.g. src/main.go:42) so the user can navigate directly.

# Approach to tasks

1. Understand what the user is asking.
2. Read the relevant code to build context.
3. Make the change or answer the question.
4. If you changed code, run tests or build to verify.
5. Report the result concisely.

Do not skip steps. Do not guess at code you haven't read. Do not make changes without verifying them.
`
	tools := a.Registry.List()
	if len(tools) > 0 && a.CurrentModel.ToolMode == "text" {
		prompt += "\n# Available tools\n\n"
		for _, t := range tools {
			prompt += fmt.Sprintf("- %s: %s\n  Parameters: %s\n\n", t.Name, t.Description, string(t.InputSchema))
		}
		prompt += `To use a tool, output EXACTLY this format:

<tool_call>
{"name": "tool_name", "args": {"param": "value"}}
</tool_call>

You may include text before and after tool calls. You may make multiple tool calls in one response. After you make a tool call, wait for the result before continuing.
`
	}
	return prompt
}

func parseFirstToolCall(text string, callIndex int) (string, string, *llm.ToolCall) {
	start := strings.Index(text, "<tool_call>")
	if start == -1 {
		return text, "", nil
	}
	end := strings.Index(text, "</tool_call>")
	if end == -1 {
		return text, "", nil
	}
	before := text[:start]
	block := strings.TrimSpace(text[start+len("<tool_call>") : end])
	after := text[end+len("<tool_call>"):]
	var parsed struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	}
	if err := json.Unmarshal([]byte(block), &parsed); err != nil {
		return text, "", nil
	}
	tc := &llm.ToolCall{Id: fmt.Sprintf("call_%d", callIndex), Name: parsed.Name, Args: parsed.Args}
	return strings.TrimSpace(before), strings.TrimSpace(after), tc
}

func (a *Agent) emit(event AgentEvent) {
	if a.OnEvent != nil {
		a.OnEvent(event)
	}
}

var pricing = map[string][2]float64{
	"gpt-4.1-nano":             {0.10, 0.40},
	"gpt-4.1-mini":             {0.40, 1.60},
	"gpt-4o-mini":              {0.15, 0.60},
	"gpt-4o":                   {2.50, 10.00},
	"gpt-4-turbo":              {10.00, 30.00},
	"gpt-4":                    {30.00, 60.00},
	"gpt-4.5":                  {75.00, 150.00},
	"gpt-5-nano":               {0.05, 0.40},
	"gpt-5-mini":               {0.25, 2.00},
	"gpt-5":                    {1.25, 10.00},
	"gpt-5-pro":                {21.00, 168.00},
	"gpt-5.3-codex":            {1.25, 10.00},
	"gpt-5.4-mini":             {0.75, 4.50},
	"gpt-5.4":                  {2.50, 15.00},
	"gpt-5.4-pro":              {30.00, 180.00},
	"gpt-5.5":                  {5.00, 30.00},
	"gpt-5.5-pro":              {30.00, 180.00},
	"qwen/qwen3-coder":         {0.22, 1.80},
	"qwen/qwen3.6-plus":        {0.325, 1.95},
	"deepseek/deepseek-coder":  {0.14, 0.28},
	"deepseek/deepseek-chat":   {0.14, 0.28},
	"meta-llama/llama-3.3-70b": {0.70, 0.90},
	"meta-llama/llama-3-405b":  {2.66, 2.66},
	"mistralai/mistral-large":  {2.00, 6.00},
	"google/gemini-2.5-flash":  {0.15, 0.60},
	"google/gemini-2.5-pro":    {1.25, 5.00},
	"cohere/command-r-plus":    {2.50, 10.00},
}

func calculateCost(usage *llm.TokenUsage) float64 {
	prices, ok := pricing[usage.Model]
	if !ok {
		for key, p := range pricing {
			if strings.HasPrefix(usage.Model, key) || strings.HasSuffix(usage.Model, key) {
				prices = p
				ok = true
				break
			}
		}
	}
	if !ok {
		return 0
	}
	inputCost := float64(usage.InputTokens) / 1_000_000 * prices[0]
	outputCost := float64(usage.OutputTokens) / 1_000_000 * prices[1]
	return inputCost + outputCost
}

func (a *Agent) Run(ctx context.Context, message string) (string, error) {
	a.taskStart = len(a.History)
	a.TaskInputTokens = 0
	a.TaskOutputTokens = 0
	a.TaskCost = 0

	a.History = append(a.History, llm.Message{Role: "user", Content: message})
	callIndex := 0
	usedTools := false

	for range 20 {
		if a.Budget > 0 && a.SessionCost >= a.Budget {
			return "", errors.New("budget exceeded")
		}

		if a.Budget > 0 {
			for i := len(a.ModelTiers) - 1; i >= 0; i-- {
				tier := a.ModelTiers[i]
				if a.SessionCost >= a.Budget*tier.Threshold {
					if a.CurrentModel.Name != tier.Model.Name {
						a.emit(AgentEvent{
							Type:    "cost",
							Content: fmt.Sprintf("Budget %.0f%% used, switching to %s", (a.SessionCost/a.Budget)*100, tier.Model.Name),
						})
						a.CurrentModel = tier.Model
					}
					break
				}
			}
		}

		var toolDefs []llm.ToolDef
		if a.CurrentModel.ToolMode == "native" {
			toolDefs = a.Registry.List()
		}
		ch, err := a.CurrentModel.Provider.Chat(ctx, a.History, toolDefs, *a.CurrentModel)
		if err != nil {
			return "", err
		}

		var text strings.Builder
		var nativeToolCalls []llm.ToolCall
		budgetViolatedDuringStream := false

		for event := range ch {
			switch event.Type {
			case "text":
				text.WriteString(event.Text)
			case "tool_call":
				nativeToolCalls = append(nativeToolCalls, *event.TC)
			case "usage":
				cost := calculateCost(event.Usage)
				a.TaskInputTokens += event.Usage.InputTokens
				a.TaskOutputTokens += event.Usage.OutputTokens
				a.TaskCost += cost
				a.SessionCost += cost
				a.LastModelUsed = event.Usage.Model

				if a.Budget > 0 && a.SessionCost >= a.Budget {
					budgetViolatedDuringStream = true
				}
			case "error":
				return "", event.Err
			}
		}

		if budgetViolatedDuringStream {
			return "", errors.New("budget exceeded during LLM response generation")
		}

		var toolCalls []llm.ToolCall
		var cleanText string
		if a.CurrentModel.ToolMode == "native" {
			toolCalls = nativeToolCalls
			cleanText = text.String()
		} else {
			var toolCall *llm.ToolCall
			cleanText, _, toolCall = parseFirstToolCall(text.String(), callIndex)
			if toolCall != nil {
				toolCalls = []llm.ToolCall{*toolCall}
			}
		}

		if len(toolCalls) == 0 {
			a.History = append(a.History, llm.Message{Role: "assistant", Content: cleanText})
			if usedTools {
				if summary, err := a.generateDoc(ctx); err == nil {
					a.saveDoc(summary)
					a.updateArchitecture(ctx, summary)
				}
			}
			a.emit(AgentEvent{
				Type:    "cost",
				Content: fmt.Sprintf("Tokens: %d in / %d out | Task Cost: $%.4f | Session Cost: $%.4f | Model used: '%s'", a.TaskInputTokens, a.TaskOutputTokens, a.TaskCost, a.SessionCost, a.LastModelUsed),
			})
			return strings.TrimSpace(cleanText), nil
		}

		if a.Budget > 0 && a.SessionCost >= a.Budget {
			return "", errors.New("budget exceeded; freezing tool execution")
		}

		usedTools = true
		a.History = append(a.History, llm.Message{Role: "assistant", Content: text.String(), TCs: toolCalls})
		for _, tc := range toolCalls {
			a.emit(AgentEvent{Type: "tool_call", Name: tc.Name, Args: string(tc.Args)})
			res, err := a.Registry.Dispatch(tc.Name, tc.Args)
			if err != nil {
				res = "tool error: " + err.Error()
			}
			a.emit(AgentEvent{Type: "tool_result", Name: tc.Name, Content: res})
			if a.CurrentModel.ToolMode == "native" {
				a.History = append(a.History, llm.Message{Role: "tool", Content: res, TCID: tc.Id})
			} else {
				a.History = append(a.History, llm.Message{Role: "tool", Content: fmt.Sprintf("Tool result for %s:\n%s", tc.Name, res), TCID: tc.Id})
			}
			callIndex++

			if a.Budget > 0 && a.SessionCost >= a.Budget {
				return "", errors.New("budget exceeded during tool execution sequence")
			}
		}
	}
	return "", errors.New("Max iterations exceeded")
}
