package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/AamindMandragora/pragma/internal/db"
	"github.com/AamindMandragora/pragma/internal/llm"
	"github.com/AamindMandragora/pragma/internal/llm/catalog"
	"github.com/AamindMandragora/pragma/internal/process"
	"github.com/AamindMandragora/pragma/internal/tools"
	"github.com/google/uuid"
)

var logger *log.Logger

// starts up a logger that writes to .agent/pragma.log if it can be opened or stderr otherwise
func init() {
	os.MkdirAll(".agent", 0755)
	f, err := os.OpenFile(".agent/pragma.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		logger = log.New(os.Stderr, "pragma: ", log.LstdFlags)
	} else {
		logger = log.New(f, "", log.LstdFlags|log.Lshortfile)
	}
}

type AgentEvent struct {
	Type    string
	Name    string
	Args    string
	Content string
}

// agents hold a current model, the model tiers, tool registry, process manager, message history, OnEvent function, taskStart message pointer, input/output token counters, task/session cost, prev model name, a budget, and the session id
type Agent struct {
	CurrentModel     *llm.Model
	ModelTiers       []llm.ModelTier
	Registry         *tools.Registry
	Manager          *process.Manager
	History          []llm.Message
	OnEvent          func(AgentEvent)
	taskStart        int
	TaskInputTokens  int
	TaskOutputTokens int
	TaskCost         float64
	SessionCost      float64
	LastModelUsed    string
	Budget           float64
	SessionID        uuid.UUID
	// ContextMaxTokens bounds the input sent to the provider. The full history
	// remains available for persistence and is compacted only for requests.
	ContextMaxTokens int
	ContextSummary   string
	contextSummaryAt int
	compactions      int
	forceAskUser   bool
	forceAskReason string
}

// creates an agent with current model equal to the first one in tiers, builds the system prompt and puts it in the history
func NewAgent(tiers []llm.ModelTier, registry *tools.Registry, manager *process.Manager) *Agent {
	a := &Agent{CurrentModel: tiers[0].Model, Registry: registry, Manager: manager, SessionID: uuid.New(), ContextMaxTokens: DefaultMaxInputTokens}
	a.ModelTiers = tiers
	// gets the current working directory for the db session row
	var cwd, _ = os.Getwd()
	db.CreateSession(a.SessionID, "", cwd)
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

// does the same thing as new agent except tries to fetch stored messages from the db and updates the history accordingly
func ResumeAgent(sessionID string, tiers []llm.ModelTier, registry *tools.Registry, manager *process.Manager) *Agent {
	parsedId, err := uuid.Parse(sessionID)
	if err != nil {
		logger.Printf("Invalid session ID %q: %v", sessionID, err)
	}
	a := &Agent{CurrentModel: tiers[0].Model, Registry: registry, Manager: manager, SessionID: parsedId, ContextMaxTokens: DefaultMaxInputTokens}
	a.ModelTiers = tiers
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
	history, err := db.LoadSessionMessages(parsedId)
	if err != nil {
		logger.Printf("Error when querying db: %v", err)
	}
	a.History = append(a.History, history...)
	return a
}

// builds the base system prompt
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

You have tools available to inspect files, edit files, run commands, fetch web pages, and work with git. Use them to accomplish tasks — do not ask the user to run commands for you.

- Use read_file to inspect code, configs, prompts, and other repository files before editing them.
- For large files or outputs, compose the summarize_output filter directly, for example {"filters":[{"name":"summarize_output"}]}; this keeps the full source out of the main conversation.
- Tool results are complete by default. When only part of a result is useful, compose ordered output filters in the call, for example {"filters":[{"name":"find_issues"}]} or {"filters":[{"name":"tail","args":{"n":50}}]}. Available filters include summarize_output, find_issues, grep, head, tail, line_range, compact_output, dedupe_output, json_pretty, and output_stats.
- Use write_file to create new files or overwrite files that do not already exist.
- Use edit_file to make targeted changes to existing files. Provide the exact text to replace and the new text.
- NEVER use write_file to modify an existing file. Always use edit_file. If edit_file fails because of a text mismatch, use read_file to see the exact content, then retry edit_file with the correct text.
- Use move_file to rename or relocate files when the contents should stay the same.
- Use delete_file to remove files that are no longer needed.
- Use run_command to execute shell commands, run tests, build the project, or inspect the working tree.
- When running a non-trivial command, briefly explain what it does in one line.
- If you need to run something too complex to do in the shell, write a short python script and use run_python to execute.
- Use web_fetch to read web pages, documentation, API references, or any URL. The user may give you URLs to read, or you can fetch documentation pages you know about.
- Use git_status to inspect the working tree, git_diff to review changes, git_log to inspect recent commits, git_branch to list/create/check out branches, git_stash to push or pop stash entries, and git_commit to stage and commit changes.
- Never use run_command to communicate with the user. Ordinary replies go in your response text; structured questions that need a decision go through ask_user.
- If you need to run multiple independent commands, call them all rather than waiting between them.
- If you need to reason about a tool's output before deciding what to do next, make only ONE tool call. You will see the result and can then decide your next action. Only make multiple tool calls in one response if they are independent of each other.
- Use ask_user to pause and ask the user when you are stuck or need a design decision. Pass what you tried, what is going wrong, and a clear question.

# Escalation

If you have attempted multiple approaches and none are working, or if you need a design decision you cannot make yourself, call ask_user. This is preferred over guessing or continuing to burn tokens on failing approaches.

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
	// if there are tools and the model doesn't support native tool calls, then add them to the system prompt
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

// parses the first text-based tool call in the given text and returns a before/after tool call string as well
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

// runs the OnEvent if one exists
func (a *Agent) emit(event AgentEvent) {
	if a.OnEvent != nil {
		a.OnEvent(event)
	}
}

// RequireAskUser forces the next agent turn to call ask_user before any other tools.
// Used when plan-mode step retries are exhausted.
func (a *Agent) RequireAskUser(reason string) {
	a.forceAskUser = true
	a.forceAskReason = reason
	msg := "Forced escalation: you must call ask_user now. Do not call any other tools until you have asked the user."
	if reason != "" {
		msg += "\nReason: " + reason
	}
	a.History = append(a.History, llm.Message{Role: "system", Content: msg})
	if err := db.SaveMessage(a.SessionID, a.History[len(a.History)-1]); err != nil {
		logger.Printf("SaveMessage failed: %v", err)
	}
}

func (a *Agent) askUserOnlyTools() []llm.ToolDef {
	for _, t := range a.Registry.List() {
		if t.Name == "ask_user" {
			return []llm.ToolDef{t}
		}
	}
	return nil
}

// calculates the cost based on the token usage using the embedded model catalog
func calculateCost(usage *llm.TokenUsage) float64 {
	inputPrice, cachedPrice, outputPrice, ok := catalog.Price(usage.Model)
	if !ok {
		return 0
	}
	uncached := usage.InputTokens - usage.CachedInputTokens
	if uncached < 0 {
		uncached = 0
	}
	inputCost := float64(uncached) / 1_000_000 * inputPrice
	cachedCost := float64(usage.CachedInputTokens) / 1_000_000 * cachedPrice
	outputCost := float64(usage.OutputTokens) / 1_000_000 * outputPrice
	return inputCost + cachedCost + outputCost
}

// runs the agent given a context and an input message
func (a *Agent) Run(ctx context.Context, message string) (string, error) {
	// each message is a new task
	a.taskStart = len(a.History)
	a.TaskInputTokens = 0
	a.TaskOutputTokens = 0
	a.TaskCost = 0

	// checks if we're inside a git repo
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	inWorkTree, err := cmd.Output()
	if err != nil {
		return "", err
	}
	// if so, push a checkpoint onto the stash stack and re-apply to preserve working tree
	if strings.TrimSpace(string(inWorkTree)) == "true" {
		ctim := time.Now()
		cmd = exec.Command("git", "stash", "push", "-u", "-m", fmt.Sprintf("pragma-checkpoint-%d", ctim.Unix()))
		if err := cmd.Run(); err == nil {
			cmd = exec.Command("git", "stash", "apply")
			cmd.Run()
		}
	}

	// if this is the first message, use it to create the session's title
	if a.taskStart == 1 {
		title := message
		if len(title) > 50 {
			title = title[:50]
		}
		err := db.UpdateSessionTitle(a.SessionID, title)
		if err != nil {
			return "", err
		}
	}

	// adds user message to history, haven't used a tool yet
	a.History = append(a.History, llm.Message{Role: "user", Content: message})
	callIndex := 0
	usedTools := false

	// tries to save the message to the db
	err = db.SaveMessage(a.SessionID, a.History[len(a.History)-1])
	if err != nil {
		logger.Printf("SaveMessage failed: %v", err)
	}

	for range 20 {
		// guard against going over budget
		if a.Budget > 0 && a.SessionCost >= a.Budget {
			return "", errors.New("budget exceeded")
		}

		// loops through model tiers backwards and find the first one whose threshold is less than the percent of budget that's been spent, then switches to it
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

		// if we have native tools, then we can pass in a list of ToolDef to the chat
		var toolDefs []llm.ToolDef
		if a.CurrentModel.ToolMode == "native" {
			if a.forceAskUser {
				toolDefs = a.askUserOnlyTools()
			} else {
				toolDefs = a.Registry.List()
			}
		}
		ch, err := a.CurrentModel.Provider.Chat(ctx, a.HistoryForModel(), toolDefs, *a.CurrentModel)
		if err != nil {
			return "", err
		}

		var text strings.Builder
		// since native tool calls are their own events, they get stored in the list now
		var nativeToolCalls []llm.ToolCall
		budgetViolatedDuringStream := false

		for event := range ch {
			switch event.Type {
			case "text":
				text.WriteString(event.Text)
			case "tool_call":
				nativeToolCalls = append(nativeToolCalls, *event.TC)
				callIndex++
			// calculates the cost of this iteration of run, checks whether we've gone over budget
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

		// creates the toolCalls array based on whether we use native or text-based tools
		var toolCalls []llm.ToolCall
		var cleanText string
		if a.CurrentModel.ToolMode == "native" {
			toolCalls = nativeToolCalls
			cleanText = text.String()
		} else {
			// repeatedly loop through the remaining text and parse the first tool call until there are none
			remainingText := text.String()
			for {
				var toolCall *llm.ToolCall
				var parsedText string
				parsedText, _, toolCall = parseFirstToolCall(remainingText, callIndex)
				if toolCall == nil {
					cleanText = remainingText
					break
				}
				toolCalls = append(toolCalls, *toolCall)
				remainingText = parsedText
				callIndex++
			}
		}

		for i := range toolCalls {
			if len(strings.TrimSpace(string(toolCalls[i].Args))) == 0 {
				toolCalls[i].Args = json.RawMessage(`{}`)
				continue
			}
			if !json.Valid(toolCalls[i].Args) {
				return "", fmt.Errorf("model returned incomplete arguments for %s; context or output limits may have been reached. Try #compact and retry", toolCalls[i].Name)
			}
		}

		// add all the tool calls to the message history once we're done calling
		if len(toolCalls) == 0 {
			if strings.TrimSpace(cleanText) == "" {
				return "", errors.New("model returned no visible response; context or output limits may have been reached. Try #compact and retry")
			}
			a.History = append(a.History, llm.Message{Role: "assistant", Content: cleanText})
			// tries to save the message to the db
			err := db.SaveMessage(a.SessionID, a.History[len(a.History)-1])
			if err != nil {
				logger.Printf("SaveMessage failed: %v", err)
			}
			// if we have ever used tools then we need to generate a task doc and update the architecture
			if usedTools {
				if summary, err := a.generateDoc(ctx); err == nil {
					a.saveDoc(summary)
					a.updateArchitecture(ctx, summary)
				}
			}
			// send an event about how much we've spent
			a.emit(AgentEvent{
				Type:    "cost",
				Content: fmt.Sprintf("Tokens: %d in / %d out | Task Cost: $%.4f | Session Cost: $%.4f | Model used: '%s'", a.TaskInputTokens, a.TaskOutputTokens, a.TaskCost, a.SessionCost, a.LastModelUsed),
			})
			return strings.TrimSpace(cleanText), nil
		}

		// don't run tools if we're over budget
		if a.Budget > 0 && a.SessionCost >= a.Budget {
			return "", errors.New("budget exceeded; freezing tool execution")
		}

		// loop through all the tools that were called and dispatch them through the registry, then send an event containing the result
		usedTools = true
		a.History = append(a.History, llm.Message{Role: "assistant", Content: text.String(), TCs: toolCalls})
		// tries to save the message to the db
		err = db.SaveMessage(a.SessionID, a.History[len(a.History)-1])
		if err != nil {
			logger.Printf("SaveMessage failed: %v", err)
		}
		for _, tc := range toolCalls {
			a.emit(AgentEvent{Type: "tool_call", Name: tc.Name, Args: string(tc.Args)})
			var res string
			askedUser := false
			if a.forceAskUser && tc.Name != "ask_user" {
				res = "forced escalation: call ask_user only"
			} else {
				var dispatchErr error
				res, dispatchErr = a.Registry.Dispatch(tc.Name, tc.Args)
				if dispatchErr != nil {
					res = "tool error: " + dispatchErr.Error()
				} else if tc.Name == "ask_user" {
					askedUser = true
					a.forceAskUser = false
					a.forceAskReason = ""
				}
			}
			// remove files in .agentignore from output
			res = process.ScrubOutput(res)
			a.emit(AgentEvent{Type: "tool_result", Name: tc.Name, Content: res})
			if a.CurrentModel.ToolMode == "native" {
				a.History = append(a.History, llm.Message{Role: "tool", Content: res, TCID: tc.Id})
			} else {
				a.History = append(a.History, llm.Message{Role: "tool", Content: fmt.Sprintf("Tool result for %s:\n%s", tc.Name, res), TCID: tc.Id})
			}
			// tries to save the message to the db
			err = db.SaveMessage(a.SessionID, a.History[len(a.History)-1])
			if err != nil {
				logger.Printf("SaveMessage failed: %v", err)
			}

			if askedUser {
				userMsg := llm.Message{Role: "user", Content: "User response to ask_user:\n" + res}
				a.History = append(a.History, userMsg)
				if saveErr := db.SaveMessage(a.SessionID, userMsg); saveErr != nil {
					logger.Printf("SaveMessage failed: %v", saveErr)
				}
			}

			// if we run out of budget after calling a tool then stop running them
			if a.Budget > 0 && a.SessionCost >= a.Budget {
				return "", errors.New("budget exceeded during tool execution sequence")
			}
		}
	}
	return "", errors.New("Max iterations exceeded")
}
