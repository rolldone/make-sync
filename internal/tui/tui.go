package tui

import (
	"fmt"
	"strings"
	"sync"

	"make-sync/internal/util"

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
	// printing/log area
	logMu    sync.Mutex
	logLines []string

	// PTY output buffer (raw bytes converted to strings)
	ptyMu    sync.Mutex
	ptyLines []string
}

// ptyOut is an optional channel that PTY bridge can write raw bytes to.
// When non-nil, ShowMenuWithPrints will subscribe and forward these into the UI.
var ptyOut chan []byte

// SetPTYOutputChannel registers a channel to receive PTY output bytes. Pass
// nil to unregister.
func SetPTYOutputChannel(ch chan []byte) {
	ptyOut = ch
}

// message type used to transport printed strings into Bubble Tea update loop
type printMsg string

// ShowMenuWithPrints runs the menu program while subscribing to util.PrintChan so
// printed messages are delivered into the TUI model. It restores any previous
// print channel on exit.
func ShowMenuWithPrints(items []string, title string) (string, error) {
	// create a buffered channel for print messages
	ch := make(chan string, 256)
	// preserve previous channel
	prevCh := util.PrintChan
	util.SetPrintChannel(ch)

	// start program
	m := NewMenu(items, title)
	p := tea.NewProgram(m)

	// forward ch into program
	done := make(chan struct{})
	go func() {
		for s := range ch {
			// sanitize common sequences used by SafePrinter or other writers
			sClean := strings.ReplaceAll(s, "\r\x1b[K", "")
			sClean = strings.ReplaceAll(sClean, "\x1b[2J\x1b[1;1H", "")

			// split multi-line blocks into individual trimmed lines so the
			// TUI's log area doesn't receive huge blocks with leading spaces.
			parts := strings.Split(sClean, "\n")
			for _, part := range parts {
				line := strings.TrimSpace(part)
				if line == "" {
					continue
				}
				p.Send(printMsg(line + "\n"))
			}
		}
		close(done)
	}()

	// subscribe to PTY output channel if present
	if ptyOut != nil {
		go func() {
			for b := range ptyOut {
				p.Send(printMsg(string(b)))
			}
		}()
	}

	if _, err := p.Run(); err != nil {
		// restore previous channel and TUI flag
		util.SetPrintChannel(prevCh)
		return "", err
	}

	// restore previous channel and TUI flag
	util.SetPrintChannel(prevCh)
	close(ch)
	<-done
	return m.choice, nil
}

func (m *menuModel) Init() tea.Cmd { return nil }

// ...existing code...
func (m *menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// handle incoming print messages
	switch msg := msg.(type) {
	case printMsg:
		m.logMu.Lock()
		m.logLines = append(m.logLines, string(msg))
		// keep last 200 lines to avoid unbounded memory
		if len(m.logLines) > 200 {
			m.logLines = m.logLines[len(m.logLines)-200:]
		}
		m.logMu.Unlock()
		return m, nil
	}

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
	// render menu and below it a small log area showing last 8 lines
	menuView := m.list.View()
	m.logMu.Lock()
	defer m.logMu.Unlock()
	n := len(m.logLines)
	start := 0
	if n > 8 {
		start = n - 8
	}
	logBlock := ""
	for _, l := range m.logLines[start:] {
		logBlock += l
		// ensure newline
		if len(l) == 0 || l[len(l)-1] != '\n' {
			logBlock += "\n"
		}
	}
	// append recent PTY lines after prints
	m.ptyMu.Lock()
	if len(m.ptyLines) > 0 {
		// show last 8 PTY lines merged with logBlock
		pstart := 0
		if len(m.ptyLines) > 8 {
			pstart = len(m.ptyLines) - 8
		}
		for _, l := range m.ptyLines[pstart:] {
			logBlock += l
			if len(l) == 0 || l[len(l)-1] != '\n' {
				logBlock += "\n"
			}
		}
	}
	m.ptyMu.Unlock()
	if logBlock != "" {
		return menuView + "\n--- recent ---\n" + logBlock
	}
	return menuView
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
