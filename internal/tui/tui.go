package tui

import (
	"fmt"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
)

type menuItem string

func (m menuItem) Title() string       { return string(m) }
func (m menuItem) Description() string { return "" }
func (m menuItem) FilterValue() string { return string(m) }

type menuModel struct {
	list   list.Model
	choice string
}

func (m *menuModel) Init() tea.Cmd { return nil }

// ...existing code...
func (m *menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// explicit handle cursor movement to ensure up/down work with compact delegate
		switch msg.String() {
		case "enter":
			if itm := m.list.SelectedItem(); itm != nil {
				m.choice = itm.(menuItem).Title()
			}
			return m, tea.Quit
		case "esc", "q":
			m.choice = "cancelled"
			return m, tea.Quit
		case "up", "k":
			m.list.CursorUp()
			return m, nil
		case "down", "j":
			m.list.CursorDown()
			return m, nil
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

// ...existing code...

func (m *menuModel) View() string {
	if m.choice != "" {
		return fmt.Sprintf("Selected: %s\n", m.choice)
	}
	return m.list.View()
}

// ShowMenu blocks and returns the selected item (or "cancelled")
func ShowMenu(items []string, title string) (string, error) {
	m := NewMenu(items, title)
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		return "", err
	}
	return m.choice, nil
}
