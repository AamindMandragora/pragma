// Package tui_test contains helpers used only by TUI tests.
package tui_test

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// FakeEventSource supplies deterministic Bubble Tea messages without knowing
// anything about the TUI package that consumes them.
type FakeEventSource struct {
	Events []tea.Msg
	Delay  time.Duration
	index  int
}

func NewFakeEventSource(events []tea.Msg) *FakeEventSource {
	return &FakeEventSource{Events: events}
}

func (s *FakeEventSource) Next() tea.Cmd {
	if s == nil || s.index >= len(s.Events) {
		return nil
	}
	msg := s.Events[s.index]
	s.index++
	return func() tea.Msg {
		if s.Delay > 0 {
			time.Sleep(s.Delay)
		}
		return msg
	}
}
