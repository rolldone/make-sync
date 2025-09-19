package tui

import (
	"io"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/lipgloss"
)

// compactDelegate reduces per-item height to 1 line to make list dense
type compactDelegate struct{ list.DefaultDelegate }

func (d compactDelegate) Height() int { return 1 }

// remove extra spacing between rows
func (d compactDelegate) Spacing() int { return 0 }

// Render only the title (with a simple selected marker) to avoid extra desc/lines
func (d compactDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	// write directly to the provided writer (newer bubbles.List expects void Render)
	it := listItem.(menuItem)
	title := it.Title()
	prefix := "  "
	if index == m.Index() {
		prefix = "> "
		_, _ = io.WriteString(w, d.Styles.SelectedTitle.Render(prefix+title))
		return
	}
	_, _ = io.WriteString(w, d.Styles.NormalTitle.Render(prefix+title))
}

func NewMenu(items []string, title string) *menuModel {
	var lItems []list.Item
	for _, it := range items {
		lItems = append(lItems, menuItem(it))
	}

	// compact delegate (one-line height) that also carries styled colors
	delegate := compactDelegate{list.NewDefaultDelegate()}

	// tweak styles for better visibility (safe to call on zero-value)
	delegate.Styles.SelectedTitle = lipgloss.NewStyle().Foreground(lipgloss.Color("#ff79c6")).Bold(true)
	delegate.Styles.SelectedDesc = lipgloss.NewStyle().Foreground(lipgloss.Color("#8be9fd"))
	delegate.Styles.NormalTitle = lipgloss.NewStyle().Foreground(lipgloss.Color("#f8f8f2"))
	delegate.Styles.NormalDesc = lipgloss.NewStyle().Foreground(lipgloss.Color("#6272a4"))

	// smaller width/height to reduce outer padding
	l := list.New(lItems, delegate, 40, 10)
	l.Title = title
	l.SetShowHelp(false)
	l.SetFilteringEnabled(false)
	l.SetShowStatusBar(false)
	l.SetShowPagination(false)

	return &menuModel{list: l}
}

// ...existing code...
