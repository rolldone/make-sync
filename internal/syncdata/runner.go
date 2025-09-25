package syncdata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"make-sync/internal/config"
	"make-sync/internal/sshclient"
	"make-sync/internal/util"
)

// RemoteAgentConfig represents the configuration sent to remote agent
// This mirrors the struct from internal/devsync/types.go
type RemoteAgentConfig struct {
	Devsync struct {
		Ignores        []string `json:"ignores"`
		AgentWatchs    []string `json:"agent_watchs"`
		ManualTransfer []string `json:"manual_transfer"`
		WorkingDir     string   `json:"working_dir"`
	} `json:"devsync"`
}

// generateRemoteConfig creates a config for the remote agent based on local config
func generateRemoteConfig(cfg *config.Config) *RemoteAgentConfig {
	remoteConfig := &RemoteAgentConfig{}
	remoteConfig.Devsync.Ignores = cfg.Devsync.Ignores
	remoteConfig.Devsync.AgentWatchs = cfg.Devsync.AgentWatchs
	remoteConfig.Devsync.ManualTransfer = cfg.Devsync.ManualTransfer
	remoteConfig.Devsync.WorkingDir = cfg.Devsync.Auth.RemotePath

	return remoteConfig
}

// uploadConfigToRemote creates and uploads config.json to remote .sync_temp directory
func uploadConfigToRemote(client *sshclient.SSHClient, cfg *config.Config, remoteSyncTemp, osTarget string) error {
	// Generate remote config
	remoteConfig := generateRemoteConfig(cfg)

	// Convert to JSON
	configJSON, err := json.MarshalIndent(remoteConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config to JSON: %v", err)
	}

	util.Default.Printf("📤 Uploading config.json with working_dir: %s\n", remoteConfig.Devsync.WorkingDir)
	util.Default.Printf("📄 Config content:\n%s\n", string(configJSON))

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

	// Always use absolute remotePath for .sync_temp/config.json, but avoid double .sync_temp
	remoteBase := cfg.Devsync.Auth.RemotePath
	var remoteConfigPath string
	if strings.Contains(strings.ToLower(osTarget), "win") {
		base := strings.ReplaceAll(remoteBase, "/", "\\")
		if strings.HasSuffix(base, ".sync_temp") {
			remoteConfigPath = base + "\\config.json"
		} else {
			remoteConfigPath = base + "\\.sync_temp\\config.json"
		}
	} else {
		base := remoteBase
		if strings.HasSuffix(base, ".sync_temp") {
			remoteConfigPath = filepath.Join(base, "config.json")
		} else {
			remoteConfigPath = filepath.Join(base, ".sync_temp", "config.json")
		}
	}

	// Upload config to remote
	if err := client.SyncFile(tempFile.Name(), remoteConfigPath); err != nil {
		return fmt.Errorf("failed to upload config: %v", err)
	}

	util.Default.Printf("✅ Config uploaded to: %s\n", remoteConfigPath)
	return nil
}

// ConnectSSH creates and connects an SSH client using values from cfg.Devsync.Auth
func ConnectSSH(cfg *config.Config) (*sshclient.SSHClient, error) {
	auth := cfg.Devsync.Auth
	username := auth.Username
	privateKey := auth.PrivateKey
	password := auth.Password
	host := auth.Host
	port := auth.Port
	if port == "" {
		port = "22"
	}

	client, err := sshclient.NewPersistentSSHClient(username, privateKey, password, host, port)
	if err != nil {
		return nil, fmt.Errorf("failed to create ssh client: %v", err)
	}
	if err := client.Connect(); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to connect ssh client: %v", err)
	}
	return client, nil
}

// UploadAgentBinary uploads a local agent binary to the remote directory using direct SyncFile like watcher.
// localBinaryPath: path to local binary (e.g., built sync-agent or sync-agent.exe)
// remoteDir: absolute remote directory where to place binary (usually <project>/.sync_temp)
// osTarget: string indicating remote OS (contains "win" for windows)
func UploadAgentBinary(client *sshclient.SSHClient, localBinaryPath, remoteDir, osTarget string) error {
	// Create .sync_temp directory on remote first (like watcher does)
	if strings.Contains(strings.ToLower(osTarget), "win") {
		// Use consistent backslash paths for Windows
		winRemoteDir := strings.ReplaceAll(remoteDir, "/", "\\")
		createCmd := fmt.Sprintf("cmd.exe /C if not exist \"%s\" mkdir \"%s\"", winRemoteDir, winRemoteDir)
		util.Default.Printf("🔧 DEBUG: Creating remote dir with cmd: %s\n", createCmd)
		if err := client.RunCommand(createCmd); err != nil {
			return fmt.Errorf("failed to create remote .sync_temp (windows): %v", err)
		}
	} else {
		remoteCmd := fmt.Sprintf("mkdir -p %s", remoteDir)
		if err := client.RunCommand(remoteCmd); err != nil {
			return fmt.Errorf("failed to create remote .sync_temp: %v", err)
		}
	}

	// Always use absolute remotePath for .sync_temp/[unique-agent-name]
	remoteBase := remoteDir

	// Get unique agent binary name from local config
	localConfig, err := config.GetOrCreateLocalConfig()
	if err != nil {
		return fmt.Errorf("failed to load local config: %v", err)
	}

	remoteExecName := localConfig.GetAgentBinaryName(osTarget)
	var remoteAgentPath string
	if strings.Contains(strings.ToLower(osTarget), "win") {
		base := strings.ReplaceAll(remoteBase, "/", "\\")
		if strings.HasSuffix(base, ".sync_temp") {
			remoteAgentPath = base + "\\" + remoteExecName
		} else {
			remoteAgentPath = base + "\\.sync_temp\\" + remoteExecName
		}
	} else {
		base := remoteBase
		if strings.HasSuffix(base, ".sync_temp") {
			remoteAgentPath = filepath.Join(base, remoteExecName)
		} else {
			remoteAgentPath = filepath.Join(base, ".sync_temp", remoteExecName)
		}
	}

	util.Default.Printf("🔧 DEBUG: Uploading %s to %s\n", localBinaryPath, remoteAgentPath)
	util.Default.Printf("🔧 DEBUG: File size check...\n")

	// Check file size first
	if stat, err := os.Stat(localBinaryPath); err == nil {
		util.Default.Printf("🔧 DEBUG: Local binary size: %d bytes\n", stat.Size())
	}

	// Use SyncFile with absolute path
	if err := client.SyncFile(localBinaryPath, remoteAgentPath); err != nil {
		return fmt.Errorf("failed to upload agent: %v", err)
	}

	util.Default.Printf("✅ Agent binary uploaded successfully\n")

	// Set permissions on Unix (like watcher does)
	if !strings.Contains(strings.ToLower(osTarget), "win") {
		chmodCmd := fmt.Sprintf("chmod +x %s", remoteAgentPath)
		if err := client.RunCommand(chmodCmd); err != nil {
			return fmt.Errorf("failed to make agent executable: %v", err)
		}
	}

	return nil
}

// RemoteRunAgentIndexing runs the agent with the 'indexing' command on the remote.
// remoteDir must be the directory where the agent binary is located (absolute path).
// Returns output (stdout/stderr combined) or error.
func RemoteRunAgentIndexing(client *sshclient.SSHClient, remoteDir, osTarget string) (string, error) {
	isWin := strings.Contains(strings.ToLower(osTarget), "win")

	// Get unique agent binary name from local config
	localConfig, err := config.GetOrCreateLocalConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load local config: %v", err)
	}

	// Build the binary path based on OS using unique agent name
	binaryName := localConfig.GetAgentBinaryName(osTarget)
	var cmd string
	remoteBase := remoteDir
	if isWin {
		winRemoteDir := strings.ReplaceAll(remoteBase, "/", "\\")
		var agentPath, cdDir string
		if strings.HasSuffix(winRemoteDir, ".sync_temp") {
			agentPath = winRemoteDir + "\\" + binaryName
			cdDir = winRemoteDir[:len(winRemoteDir)-len(".sync_temp")]
			if strings.HasSuffix(cdDir, "\\") {
				cdDir = cdDir[:len(cdDir)-1]
			}
			if cdDir == "" {
				cdDir = winRemoteDir
			} // fallback
		} else {
			agentPath = winRemoteDir + "\\.sync_temp\\" + binaryName
			cdDir = winRemoteDir
		}
		cmd = fmt.Sprintf("cmd.exe /C cd /d \"%s\" && \"%s\" indexing", cdDir, agentPath)
	} else {
		var agentPath, cdDir string
		if strings.HasSuffix(remoteBase, ".sync_temp") {
			agentPath = filepath.Join(remoteBase, binaryName)
			cdDir = strings.TrimSuffix(remoteBase, ".sync_temp")
			if cdDir == "" {
				cdDir = remoteBase
			}
		} else {
			agentPath = filepath.Join(remoteBase, ".sync_temp", binaryName)
			cdDir = remoteBase
		}
		cmd = fmt.Sprintf("cd %s && %s indexing", shellQuote(cdDir), shellQuote(agentPath))
	}

	fmt.Printf("🔍 DEBUG: Executing remote command: %s\n", cmd)

	// Add timeout context for debugging
	start := time.Now()
	fmt.Printf("🔍 DEBUG: Starting remote command execution at %s\n", start.Format("15:04:05"))

	// Try using RunCommandWithStream for better responsiveness like watcher does
	outputChan, errorChan, err := client.RunCommandWithStream(cmd, false)
	if err != nil {
		return "", fmt.Errorf("failed to start remote indexing command: %v", err)
	}

	var output strings.Builder
	timeout := time.After(60 * time.Second) // 60 second timeout for indexing

	fmt.Printf("🔍 DEBUG: Streaming command output...\n")

	for {
		select {
		case out, ok := <-outputChan:
			if !ok {
				// Command finished
				duration := time.Since(start)
				fmt.Printf("🔍 DEBUG: Command completed in %v\n", duration)

				result := output.String()
				fmt.Printf("🔍 DEBUG: Final output (%d bytes): %s\n", len(result), result)
				return result, nil
			}
			output.WriteString(out)
			fmt.Printf("🔍 DEBUG: Output chunk: %s", out) // Real-time output

		case err := <-errorChan:
			if err != nil {
				duration := time.Since(start)
				fmt.Printf("🔍 DEBUG: Command failed after %v: %v\n", duration, err)
				return output.String(), fmt.Errorf("remote indexing failed: %v", err)
			}

		case <-timeout:
			duration := time.Since(start)
			fmt.Printf("🔍 DEBUG: Command timed out after %v\n", duration)
			return output.String(), fmt.Errorf("remote indexing timed out after 60 seconds")
		}
	}
} // shellQuote quotes a POSIX path using single quotes, escaping existing single quotes
func shellQuote(s string) string {
	// simple implementation: replace ' with '\'' and wrap with '
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// RunAgentIndexingFlow encapsulates the full remote indexing orchestration:
// - locate local agent binary from provided candidates
// - connect SSH using cfg
// - upload agent into remote .sync_temp
// - run remote indexing command
// - download remote indexing_files.db into a local temp file
// Returns the local path of the downloaded DB, the remote output, or an error.
func RunAgentIndexingFlow(cfg *config.Config, localCandidates []string) (string, string, error) {
	// find local binary
	var localBinary string
	for _, c := range localCandidates {
		if _, err := os.Stat(c); err == nil {
			localBinary = c
			break
		}
	}
	if localBinary == "" {
		return "", "", fmt.Errorf("agent binary not found in candidates: %v", localCandidates)
	}

	// determine remote base path
	remotePath := cfg.Devsync.Auth.RemotePath
	if remotePath == "" {
		remotePath = cfg.RemotePath
	}
	osTarget := strings.ToLower(strings.TrimSpace(cfg.Devsync.OSTarget))
	var remoteSyncTemp string
	if remotePath == "" {
		if strings.Contains(osTarget, "win") {
			remoteSyncTemp = "%TEMP%"
		} else {
			remoteSyncTemp = "/tmp/.sync_temp"
		}
	} else {
		if strings.Contains(osTarget, "win") {
			remoteSyncTemp = filepath.ToSlash(filepath.Join(remotePath, ".sync_temp"))
		} else {
			remoteSyncTemp = filepath.Join(remotePath, ".sync_temp")
		}
	}

	util.Default.Printf("ℹ️  Local agent binary: %s\n", localBinary)
	util.Default.Printf("ℹ️  Remote target .sync_temp: %s (os_target=%s)\n", remoteSyncTemp, osTarget)

	// Connect SSH
	sshCli, err := ConnectSSH(cfg)
	if err != nil {
		return "", "", fmt.Errorf("ssh connect failed: %v", err)
	}
	defer sshCli.Close()

	// Upload agent binary into remoteSyncTemp
	if err := UploadAgentBinary(sshCli, localBinary, remoteSyncTemp, osTarget); err != nil {
		return "", "", fmt.Errorf("upload agent binary failed: %v", err)
	}
	util.Default.Println("✅ Agent binary uploaded")

	// Upload config.json to remote .sync_temp (needed for agent to know working directory)
	if err := uploadConfigToRemote(sshCli, cfg, remoteSyncTemp, osTarget); err != nil {
		return "", "", fmt.Errorf("upload config failed: %v", err)
	}
	util.Default.Println("✅ Config uploaded")

	// Run remote indexing
	util.Default.Println("🔍 Running remote agent indexing...")
	out, err := RemoteRunAgentIndexing(sshCli, remoteSyncTemp, osTarget)
	if err != nil {
		return "", out, fmt.Errorf("remote indexing failed: %v", err)
	}
	util.Default.Printf("✅ Remote indexing finished. Remote outaput:\n%s\n", out)
	return "", out, nil
}
