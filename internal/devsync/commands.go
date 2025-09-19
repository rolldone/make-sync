package devsync

// CommandManager handles all command-related operations
type CommandManager struct {
	watcher       *Watcher
	localManager  *LocalCommandManager
	remoteManager *RemoteCommandManager
}

// NewCommandManager creates a new command manager
func NewCommandManager(w *Watcher) *CommandManager {
	return &CommandManager{
		watcher:       w,
		localManager:  NewLocalCommandManager(w),
		remoteManager: NewRemoteCommandManager(w),
	}
}

// HandleDeployAgentCommand handles the deploy agent command
func (cm *CommandManager) HandleDeployAgentCommand() {
	cm.remoteManager.HandleDeployAgentCommand()
}
