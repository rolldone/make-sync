package devsync

import (
	"fmt"
)

// LocalCommandManager handles local command operations
type LocalCommandManager struct {
	watcher *Watcher
}

// NewLocalCommandManager creates a new local command manager
func NewLocalCommandManager(w *Watcher) *LocalCommandManager {
	return &LocalCommandManager{
		watcher: w,
	}
}

// ExecuteLocalScripts executes configured local scripts for file events
func (lcm *LocalCommandManager) ExecuteLocalScripts(event FileEvent) {
	// Execute local commands if configured
	for _, cmd := range lcm.watcher.config.Devsync.Script.Local.Commands {
		if cmd != "" {
			fmt.Printf("🔧 Executing local: %s\n", cmd)
			// TODO: Implement actual command execution
		}
	}
}

// ExecuteLocalCommands executes local commands from session selection
func (lcm *LocalCommandManager) ExecuteLocalCommands(sessionType string) []string {
	if sessionType == "local" {
		return lcm.watcher.config.Devsync.Script.Local.Commands
	}
	return []string{}
}
