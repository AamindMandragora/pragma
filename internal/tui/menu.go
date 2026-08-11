package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// MenuOption is an entry in the command palette or help menu.
type MenuOption struct {
	Label       string
	Description string
	OnSelect    func() tea.Cmd
}

// Menu is deliberately small: it is a modal child model owned by the shell.
// Keeping it independent means the command palette can later be replaced
// without changing the timeline or agent lifecycle.
type Menu struct {
	Title       string
	Options     []MenuOption
	Cursor      int
	Offset      int
	VisibleRows int
}

var (
	selectedStyle = lipgloss.NewStyle().Foreground(colorPink).Bold(true)
	normalStyle   = lipgloss.NewStyle().Foreground(colorText)
	descStyle     = lipgloss.NewStyle().Foreground(colorMuted).Italic(true)
)

func (m *Menu) Move(delta int) {
	if len(m.Options) == 0 {
		return
	}
	m.Cursor = (m.Cursor + delta + len(m.Options)) % len(m.Options)
	m.ensureCursorVisible()
}

func (m *Menu) SetVisibleRows(rows int) {
	m.VisibleRows = max(1, rows)
	m.ensureCursorVisible()
}

func (m *Menu) ensureCursorVisible() {
	if len(m.Options) == 0 || m.VisibleRows <= 0 {
		return
	}
	if m.Cursor < m.Offset {
		m.Offset = m.Cursor
	}
	if m.Cursor >= m.Offset+m.VisibleRows {
		m.Offset = m.Cursor - m.VisibleRows + 1
	}
	m.Offset = max(0, min(m.Offset, max(0, len(m.Options)-m.VisibleRows)))
}

func (m *Menu) Selected() *MenuOption {
	if len(m.Options) == 0 || m.Cursor < 0 || m.Cursor >= len(m.Options) {
		return nil
	}
	return &m.Options[m.Cursor]
}

func (m *Menu) HandleKey(msg tea.KeyPressMsg) (done bool, cmd tea.Cmd) {
	switch keyName(msg) {
	case "up", "alt+k":
		m.Move(-1)
	case "down", "alt+j":
		m.Move(1)
	case "pgup":
		m.Move(-max(1, m.VisibleRows))
	case "pgdown":
		m.Move(max(1, m.VisibleRows))
	case "home":
		m.Cursor = 0
		m.ensureCursorVisible()
	case "end":
		m.Cursor = len(m.Options) - 1
		m.ensureCursorVisible()
	case "enter":
		if option := m.Selected(); option != nil && option.OnSelect != nil {
			return true, option.OnSelect()
		}
		return true, nil
	case "esc", "alt+c":
		return true, nil
	}
	return false, nil
}

func (m *Menu) View() string {
	return m.ViewWindow(0)
}

// ViewWindow renders the menu's current window. A zero row count renders all
// options, which keeps the small standalone Menu useful in tests and callers
// that do not need a bounded view.
func (m *Menu) ViewWindow(rows int) string {
	var b strings.Builder
	if m.Title != "" {
		b.WriteString(lipgloss.NewStyle().Foreground(colorPurple).Bold(true).Render(m.Title))
		b.WriteString("\n\n")
	}
	start, end := 0, len(m.Options)
	if rows > 0 && len(m.Options) > rows {
		m.SetVisibleRows(rows)
		start = m.Offset
		end = min(len(m.Options), start+rows)
		if start > 0 {
			b.WriteString(subtleStyle.Render("↑ more"))
			b.WriteString("\n")
		}
	}
	for i := start; i < end; i++ {
		option := m.Options[i]
		line := "  " + option.Label
		if i == m.Cursor {
			line = "› " + option.Label
			b.WriteString(selectedStyle.Render(line))
			if option.Description != "" {
				b.WriteString("  ")
				b.WriteString(descStyle.Render(option.Description))
			}
		} else {
			b.WriteString(normalStyle.Render(line))
		}
		b.WriteString("\n")
	}
	if rows > 0 && end < len(m.Options) {
		b.WriteString(subtleStyle.Render("↓ more"))
		b.WriteString("\n")
	}
	return b.String()
}
