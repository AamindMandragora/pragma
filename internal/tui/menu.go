package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// menuoption struct for each option
type MenuOption struct {
	Label       string
	Description string
	OnSelect    func() tea.Cmd
}

// the menu struct, containing all menu options and cursor location
type Menu struct {
	Title   string
	Options []MenuOption
	Cursor  int
}

var (
	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("212")). // pinkish
			Bold(true)
	normalStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")) // gray
	descStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Italic(true)
)

// function for moving the cursor for each option in the menu
func (m *Menu) Move(delta int) {
	if len(m.Options) == 0 {
		return
	}
	n := len(m.Options)
	m.Cursor = (m.Cursor + delta + n) % n
}

// returns selected option
func (m *Menu) Selected() *MenuOption {
	if len(m.Options) == 0 {
		return nil
	}
	return &m.Options[m.Cursor]
}

func (m *Menu) HandleKey(msg tea.KeyMsg) (done bool, cmd tea.Cmd) {
	switch msg.String() {
	case "up", "left":
		m.Move(-1)
	case "down", "right":
		m.Move(1)
	case "enter":
		if opt := m.Selected(); opt != nil && opt.OnSelect != nil {
			return true, opt.OnSelect()
		}
		return true, nil
	case "esc":
		return true, nil
	}
	return false, nil
}

func (m *Menu) View() string {
	var b strings.Builder
	if m.Title != "" {
		b.WriteString(lipgloss.NewStyle().Bold(true).Render(m.Title))
		b.WriteString("\n\n")
	}
	for i, opt := range m.Options {
		line := "  " + opt.Label
		if i == m.Cursor {
			line = "> " + opt.Label
			b.WriteString(selectedStyle.Render(line))
			if opt.Description != "" {
				b.WriteString(" - " + descStyle.Render(opt.Description))
			}
		} else {
			b.WriteString(normalStyle.Render(line))
		}
		b.WriteString("\n")
	}
	return b.String()
}
