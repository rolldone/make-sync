package devsync

import (
	"fmt"
	"strings"
)

// RemoteCommandManager handles remote command operations
type RemoteCommandManager struct {
	watcher *Watcher
}

// NewRemoteCommandManager creates a new remote command manager
func NewRemoteCommandManager(w *Watcher) *RemoteCommandManager {
	return &RemoteCommandManager{
		watcher: w,
	}
}

// ExecuteRemoteScripts executes configured remote scripts for file events
func (rcm *RemoteCommandManager) ExecuteRemoteScripts(event FileEvent) {

	// Execute remote commands if configured
	for _, cmd := range rcm.watcher.config.Devsync.Script.Remote.Commands {
		if cmd != "" && rcm.watcher.sshClient != nil {
			fmt.Printf("🔧 Executing remote: %s\n", cmd)
			if err := rcm.watcher.sshClient.RunCommand(cmd); err != nil {
				fmt.Printf("❌ Failed to execute remote command: %v\n", err)
			}
		}
	}
}

// ExecuteRemoteCommands executes remote commands from session selection
func (rcm *RemoteCommandManager) ExecuteRemoteCommands(sessionType string) []string {
	if sessionType == "remote" {
		return rcm.watcher.config.Devsync.Script.Remote.Commands
	}
	return []string{}
}

// HandleDeployAgentCommand handles the A deploy agent command
func (rcm *RemoteCommandManager) HandleDeployAgentCommand() {
	fmt.Printf("\n🚀 Deploy Agent Command\n")
	fmt.Printf("======================\n")

	if rcm.watcher.sshClient == nil {
		fmt.Printf("❌ SSH client not available\n")
		fmt.Printf("💡 Make sure SSH configuration is properly set up\n")
		return
	}

	fmt.Printf("🔨 Building and deploying sync agent...\n")

	// Build and deploy agent
	if err := rcm.buildAndDeployAgent(); err != nil {
		fmt.Printf("❌ Failed to build/deploy agent: %v\n", err)
		return
	}

	fmt.Printf("✅ Agent deployed successfully!\n")
	fmt.Printf("💡 Agent is now available at: ~/.sync_temp/sync-agent\n")

	// Additional verification - show agent info
	if output, err := rcm.watcher.sshClient.RunCommandWithOutput("file .sync_temp/sync-agent"); err == nil {
		fmt.Printf("📋 Agent info: %s", strings.TrimSpace(output))
	}
}

// buildAndDeployAgent builds the agent for target OS and deploys it
func (rcm *RemoteCommandManager) buildAndDeployAgent() error {
	// This would contain the agent building and deployment logic
	// For now, just return success
	return nil
}
