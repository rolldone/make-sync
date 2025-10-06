package deployagent

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"make-sync/internal/config"
	"make-sync/internal/sshclient"
	"make-sync/internal/util"
)

// UnifiedDeployOptions contains all parameters for a complete agent deployment
type UnifiedDeployOptions struct {
	Config         *config.Config
	ProjectRoot    string
	SSHClient      *sshclient.SSHClient
	TargetOS       string
	BuildIfMissing bool // Whether to build agent if not found
	UploadAgent    bool // Whether to upload agent binary
	UploadConfig   bool // Whether to upload config.json
}

// RemoteAgentConfig represents the configuration sent to remote agent
type RemoteAgentConfig struct {
	Devsync struct {
		SizeLimit      int      `json:"size_limit"`
		Ignores        []string `json:"ignores"`
		AgentWatchs    []string `json:"agent_watchs"`
		ManualTransfer []string `json:"manual_transfer"`
		WorkingDir     string   `json:"working_dir"`
	} `json:"devsync"`
}

// DeployAgentAndConfig is the unified function that handles complete agent deployment:
// 1. Build agent (if needed/requested)
// 2. Upload agent binary to remote
// 3. Upload config.json to remote
// Returns the remote agent path for execution
func DeployAgentAndConfig(opts UnifiedDeployOptions) (remoteAgentPath string, err error) {
	if opts.Config == nil {
		return "", fmt.Errorf("config is required")
	}
	if opts.SSHClient == nil {
		return "", fmt.Errorf("SSH client is required")
	}

	// Determine target OS
	targetOS := opts.TargetOS
	if targetOS == "" {
		targetOS = opts.Config.Devsync.OSTarget
		if targetOS == "" {
			targetOS = "linux" // Default
		}
	}

	// Get remote base path
	remoteBase := opts.Config.Devsync.Auth.RemotePath
	if remoteBase == "" {
		return "", fmt.Errorf("remote path is required in config")
	}

	// Determine remote .sync_temp directory path
	var remoteSyncTemp string
	if strings.Contains(strings.ToLower(targetOS), "win") {
		remoteSyncTemp = strings.ReplaceAll(remoteBase, "/", "\\") + "\\.sync_temp"
	} else {
		remoteSyncTemp = filepath.Join(remoteBase, ".sync_temp")
		remoteSyncTemp = filepath.ToSlash(remoteSyncTemp)
	}

	// Create .sync_temp directory on remote
	if err := createRemoteSyncTempDir(opts.SSHClient, remoteSyncTemp, targetOS); err != nil {
		return "", fmt.Errorf("failed to create remote .sync_temp directory: %v", err)
	}

	// Kill existing agent with same name before deployment
	localConfig, err := config.GetOrCreateLocalConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load local config: %v", err)
	}
	if err := killExistingAgent(opts.SSHClient, localConfig.Devsync.AgentName, targetOS); err != nil {
		util.Default.Printf("⚠️  Failed to kill existing agent: %v\n", err)
		// Continue with deployment even if kill fails
	}

	var agentPath string

	// Build agent if requested
	if opts.BuildIfMissing || opts.UploadAgent {
		buildOpts := BuildOptions{
			ProjectRoot: opts.ProjectRoot,
			TargetOS:    targetOS,
			SSHClient:   NewSSHClientAdapter(opts.SSHClient),
			Config:      opts.Config, // Pass config for unique agent naming
		}

		builtPath, err := BuildAgentForTarget(buildOpts)
		if err != nil {
			// Try fallback
			fallbackPath := FindFallbackAgent(opts.ProjectRoot, targetOS)
			if fallbackPath != "" {
				util.Default.Printf("ℹ️  Using fallback agent binary: %s\n", fallbackPath)
				agentPath = fallbackPath
			} else {
				return "", fmt.Errorf("failed to build agent and no fallback found: %v", err)
			}
		} else {
			agentPath = builtPath
		}
	}

	// Upload agent binary if requested
	if opts.UploadAgent && agentPath != "" {
		if err := uploadAgentBinary(opts.SSHClient, agentPath, remoteSyncTemp, targetOS); err != nil {
			return "", fmt.Errorf("failed to upload agent binary: %v", err)
		}
	}

	// Upload config if requested
	if opts.UploadConfig {
		if err := uploadConfigJSON(opts.SSHClient, opts.Config, remoteSyncTemp, targetOS); err != nil {
			return "", fmt.Errorf("failed to upload config: %v", err)
		}
	}

	// Determine remote agent path for execution - using unique agent name from local config
	localConfig, err = config.GetOrCreateLocalConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load local config: %v", err)
	}

	agentBinaryName := localConfig.GetAgentBinaryName(targetOS)
	remoteExecName := agentBinaryName

	if strings.Contains(strings.ToLower(targetOS), "win") {
		remoteAgentPath = remoteSyncTemp + "\\" + remoteExecName
	} else {
		remoteAgentPath = filepath.Join(remoteSyncTemp, remoteExecName)
	}

	return remoteAgentPath, nil
}

// createRemoteSyncTempDir creates the .sync_temp directory on remote server
func createRemoteSyncTempDir(client *sshclient.SSHClient, remoteSyncTemp, targetOS string) error {
	if strings.Contains(strings.ToLower(targetOS), "win") {
		winRemoteDir := strings.ReplaceAll(remoteSyncTemp, "/", "\\")
		createCmd := fmt.Sprintf("cmd.exe /C if not exist \"%s\" mkdir \"%s\"", winRemoteDir, winRemoteDir)
		if err := client.RunCommand(createCmd); err != nil {
			return fmt.Errorf("failed to create remote .sync_temp (windows): %v", err)
		}
	} else {
		createCmd := fmt.Sprintf("mkdir -p %s", remoteSyncTemp)
		if err := client.RunCommand(createCmd); err != nil {
			return fmt.Errorf("failed to create remote .sync_temp (unix): %v", err)
		}
	}
	return nil
}

// uploadAgentBinary uploads the agent binary to remote .sync_temp directory
func uploadAgentBinary(client *sshclient.SSHClient, localAgentPath, remoteSyncTemp, targetOS string) error {
	// Get absolute path if relative
	var absAgentPath string
	if filepath.IsAbs(localAgentPath) {
		absAgentPath = localAgentPath
	} else {
		var err error
		absAgentPath, err = filepath.Abs(localAgentPath)
		if err != nil {
			return fmt.Errorf("failed to get absolute path for agent: %v", err)
		}
	}

	// Note: This function receives localAgentPath that should already have unique naming
	// The unique naming is handled at build/deployment level, not here
	localFileName := filepath.Base(localAgentPath)

	var remoteAgentPath string
	if strings.Contains(strings.ToLower(targetOS), "win") {
		remoteAgentPath = remoteSyncTemp + "\\" + localFileName
	} else {
		remoteAgentPath = filepath.Join(remoteSyncTemp, localFileName)
	}

	util.Default.Printf("📦 Uploading agent: %s -> %s\n", absAgentPath, remoteAgentPath)

	// Upload agent binary
	if err := client.SyncFile(absAgentPath, remoteAgentPath); err != nil {
		return fmt.Errorf("failed to upload agent: %v", err)
	}

	// Set execute permission on Unix
	if !strings.Contains(strings.ToLower(targetOS), "win") {
		chmodCmd := fmt.Sprintf("chmod +x %s", remoteAgentPath)
		log.Println("[deployagent_unified.go] DEBUG: Running chmod command:", chmodCmd)
		if err := client.RunCommand(chmodCmd); err != nil {
			log.Println("[deployagent_unified.go] DEBUG: chmod command failed:", err)
			return fmt.Errorf("failed to make agent executable: %v", err)
		}
		log.Println("[deployagent_unified.go] DEBUG: chmod command succeeded")
	}

	util.Default.Printf("✅ Agent uploaded successfully\n")
	return nil
}

// uploadConfigJSON uploads the config.json to remote .sync_temp directory
func uploadConfigJSON(client *sshclient.SSHClient, cfg *config.Config, remoteSyncTemp, targetOS string) error {
	// Generate remote config
	remoteConfig := &RemoteAgentConfig{}
	remoteConfig.Devsync.Ignores = cfg.Devsync.Ignores
	remoteConfig.Devsync.AgentWatchs = cfg.Devsync.AgentWatchs
	remoteConfig.Devsync.ManualTransfer = cfg.Devsync.ManualTransfer
	remoteConfig.Devsync.WorkingDir = cfg.Devsync.Auth.RemotePath

	// Convert to JSON
	configJSON, err := json.MarshalIndent(remoteConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config to JSON: %v", err)
	}

	util.Default.Printf("📤 Uploading config.json with working_dir: %s\n", remoteConfig.Devsync.WorkingDir)

	// Create temporary local file
	tempFile, err := os.CreateTemp("", "remote-config-*.json")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Write config to temp file
	if _, err := tempFile.Write(configJSON); err != nil {
		return fmt.Errorf("failed to write config to temp file: %v", err)
	}
	tempFile.Close()

	// Determine remote config path
	var remoteConfigPath string
	if strings.Contains(strings.ToLower(targetOS), "win") {
		remoteConfigPath = remoteSyncTemp + "\\config.json"
	} else {
		remoteConfigPath = filepath.Join(remoteSyncTemp, "config.json")
	}

	// Upload config to remote
	if err := client.SyncFile(tempFile.Name(), remoteConfigPath); err != nil {
		return fmt.Errorf("failed to upload config: %v", err)
	}

	util.Default.Printf("✅ Config uploaded to: %s\n", remoteConfigPath)
	return nil
}

// killExistingAgent kills any running agent with the same agent name before deployment
func killExistingAgent(client *sshclient.SSHClient, agentName, targetOS string) error {
	if agentName == "" {
		return nil // No agent name to kill
	}

	binaryName := fmt.Sprintf("sync-agent-%s", agentName)
	var killCmd string

	if strings.Contains(strings.ToLower(targetOS), "win") {
		binaryName += ".exe"
		killCmd = fmt.Sprintf("taskkill /F /IM %s 2>nul", binaryName)
	} else {
		killCmd = fmt.Sprintf("pkill -f %s", binaryName)
	}

	util.Default.Printf("🔪 Killing existing agent: %s\n", binaryName)

	// Try to kill the agent - ignore errors if process not found
	if err := client.RunCommand(killCmd); err != nil {
		util.Default.Printf("⚠️  Kill command failed (agent may not be running): %v\n", err)
	} else {
		util.Default.Printf("✅ Successfully killed existing agent\n")
	}

	return nil
}
