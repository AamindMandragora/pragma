package interaction

import (
	"encoding/json"
	"errors"
)

// AskUserTool pauses the agent and asks the user a structured question.
type AskUserTool struct{}

func (t *AskUserTool) Name() string {
	return "ask_user"
}

func (t *AskUserTool) Description() string {
	return "Pause and ask the user a structured question when stuck or when a design decision is needed. Prefer this over guessing or repeatedly retrying failing approaches."
}

func (t *AskUserTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"tried": {
				"type": "array",
				"items": {"type": "string"},
				"description": "Approaches already attempted"
			},
			"problem": {
				"type": "string",
				"description": "What is going wrong"
			},
			"question": {
				"type": "string",
				"description": "What you need from the user"
			}
		},
		"required": ["tried", "problem", "question"]
	}`)
}

func (t *AskUserTool) Execute(args json.RawMessage) (string, error) {
	return "", errors.New("ask_user must be dispatched via Registry.AskUser")
}
