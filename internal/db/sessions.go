package db

import (
	"encoding/json"
	"time"

	"github.com/AamindMandragora/pragma/internal/llm"
	"github.com/google/uuid"
)

// holds the relevant information for a session (collection of chats)
type SessionInfo struct {
	Id        uuid.UUID
	Title     string
	Cwd       string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// gets the current time and inserts a row into the sessions table given the id, title, and cwd
func CreateSession(id uuid.UUID, title string, cwd string) error {
	var ctim = time.Now()
	var _, err = db.Exec("INSERT INTO sessions (id, title, cwd, created_at, updated_at) VALUES (?, ?, ?, ?, ?)", id.String(), title, cwd, ctim.Unix(), ctim.Unix())
	return err
}

// updates the title of the entry with the same id and the updated_at time
func UpdateSessionTitle(id uuid.UUID, title string) error {
	var mtim = time.Now()
	var _, err = db.Exec("UPDATE sessions SET title = ?, updated_at = ? WHERE id = ?", title, mtim.Unix(), id.String())
	return err
}

// creates a new message, inserts it into the messages table, and updates the session's updated_at time
func SaveMessage(sessionId uuid.UUID, msg llm.Message) error {
	var id, err = uuid.NewUUID()
	if err != nil {
		return err
	}
	var toolCalls *string
	if msg.TCs != nil {
		raw, err := json.Marshal(msg.TCs)
		if err != nil {
			return err
		}
		s := string(raw)
		toolCalls = &s
	} else {
		toolCalls = nil
	}
	var toolCallId *string
	if msg.TCID != "" {
		toolCallId = &msg.TCID
	} else {
		toolCallId = nil
	}
	var ctim = time.Now()
	_, err = db.Exec("INSERT INTO messages (id, session_id, role, content, created_at, tool_calls, tool_call_id) VALUES (?, ?, ?, ?, ?, ?, ?)", id.String(), sessionId.String(), msg.Role, msg.Content, ctim.Unix(), toolCalls, toolCallId)
	if err != nil {
		return err
	}
	_, err = db.Exec("UPDATE sessions SET updated_at = ? WHERE id = ?", ctim.Unix(), sessionId.String())
	return err
}

// queries the db for all messages belonging to a given session, then converts them to llm.Message and returns a list
func LoadSessionMessages(sessionId uuid.UUID) ([]llm.Message, error) {
	rows, err := db.Query("SELECT role, content, tool_calls, tool_call_id FROM messages WHERE session_id = ? ORDER BY created_at", sessionId.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []llm.Message
	for rows.Next() {
		var toolCalls, toolCallId []byte
		var msg llm.Message
		err = rows.Scan(&msg.Role, &msg.Content, &toolCalls, &toolCallId)
		if err != nil {
			return nil, err
		}
		if toolCalls == nil {
			msg.TCs = nil
		} else {
			err = json.Unmarshal(toolCalls, &msg.TCs)
			if err != nil {
				return nil, err
			}
		}
		if toolCallId == nil {
			msg.TCID = ""
		} else {
			msg.TCID = string(toolCallId)
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

// queries the most recent sessions, converts to SessionInfo, returns list
func ListSessions(limit int) ([]SessionInfo, error) {
	rows, err := db.Query("SELECT id, title, cwd, created_at, updated_at FROM sessions ORDER BY updated_at DESC LIMIT ?", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var sessions []SessionInfo
	for rows.Next() {
		var id string
		var created_at, updated_at int64
		var session SessionInfo
		err = rows.Scan(&id, &session.Title, &session.Cwd, &created_at, &updated_at)
		if err != nil {
			return nil, err
		}
		session.Id, err = uuid.Parse(id)
		if err != nil {
			return nil, err
		}
		session.CreatedAt = time.Unix(created_at, 0)
		session.UpdatedAt = time.Unix(updated_at, 0)
		sessions = append(sessions, session)
	}
	return sessions, nil
}
