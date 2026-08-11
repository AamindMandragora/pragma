package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Editor is a small multiline editor used by both onboarding and the chat
// composer. It intentionally has no I/O of its own; the parent model decides
// what Enter means.
type Editor struct {
	value       []rune
	cursor      int
	Width       int
	Placeholder string
	Focused     bool
}

// keyName preserves modifier information for printable keys. Bubble Tea's
// String method intentionally returns only the text (for example, "p" for
// Alt+P when Text is populated), so modified shortcuts must use Keystroke.
func keyName(key tea.KeyPressMsg) string {
	if key.Mod != 0 {
		return key.Keystroke()
	}
	return key.String()
}

func (e *Editor) Value() string { return string(e.value) }

func (e *Editor) SetValue(value string) {
	e.value = []rune(value)
	e.cursor = len(e.value)
}

func (e *Editor) Reset() {
	e.value = nil
	e.cursor = 0
}

func (e *Editor) insert(value string) {
	runes := []rune(value)
	if len(runes) == 0 {
		return
	}
	e.value = append(e.value[:e.cursor], append(runes, e.value[e.cursor:]...)...)
	e.cursor += len(runes)
}

func (e *Editor) Update(msg tea.Msg) {
	if !e.Focused {
		return
	}
	if paste, ok := msg.(tea.PasteMsg); ok {
		e.insert(strings.ReplaceAll(paste.Content, "\r\n", "\n"))
		return
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return
	}
	switch keyName(key) {
	case "backspace":
		if e.cursor > 0 {
			e.value = append(e.value[:e.cursor-1], e.value[e.cursor:]...)
			e.cursor--
		}
	case "delete":
		if e.cursor < len(e.value) {
			e.value = append(e.value[:e.cursor], e.value[e.cursor+1:]...)
		}
	case "left":
		if e.cursor > 0 {
			e.cursor--
		}
	case "right":
		if e.cursor < len(e.value) {
			e.cursor++
		}
	case "home":
		for e.cursor > 0 && e.value[e.cursor-1] != '\n' {
			e.cursor--
		}
	case "end":
		for e.cursor < len(e.value) && e.value[e.cursor] != '\n' {
			e.cursor++
		}
	case "alt+a":
		e.cursor = 0
	case "alt+e":
		for e.cursor < len(e.value) {
			if e.value[e.cursor] == '\n' {
				break
			}
			e.cursor++
		}
	case "alt+u":
		e.Reset()
	case "alt+w":
		for e.cursor > 0 && e.value[e.cursor-1] == ' ' {
			e.cursor--
		}
		for e.cursor > 0 && e.value[e.cursor-1] != ' ' && e.value[e.cursor-1] != '\n' {
			e.cursor--
		}
	case "shift+enter", "alt+enter":
		e.insert("\n")
	default:
		if key.Text != "" && key.Mod == 0 {
			e.insert(key.Text)
		}
	}
}

func (e *Editor) View() string {
	width := max(e.Width, 8)
	if len(e.value) == 0 {
		cursor := "▌"
		if !e.Focused {
			cursor = ""
		}
		return mutedStyle.Render(e.Placeholder) + cursor
	}
	before := string(e.value[:e.cursor])
	after := string(e.value[e.cursor:])
	text := before + "▌" + after
	return lipgloss.Wrap(text, width, " ")
}
