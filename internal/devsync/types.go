package devsync

import (
	"encoding/json"
	"fmt"
	"make-sync/internal/config"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"make-sync/internal/devsync/sshclient"

	"github.com/rjeczalik/notify"
)

// EventType represents the type of file system event
type EventType int

const (
	EventCreate EventType = iota
	EventWrite
	EventRemove
	EventRename
)

// executeLocalCommandWithOutput executes a command locally and returns output
func (w *Watcher) executeLocalCommandWithOutput(cmd string) (string, error) {
	fmt.Printf("🔧 Executing: %s\n", cmd)

	// Execute the command using bash -c for complex commands
	command := exec.Command("bash", "-c", cmd)
	command.Dir = "." // Execute in current directory

	output, err := command.CombinedOutput()
	if err != nil {
		fmt.Printf("❌ Command failed: %v\n", err)
		fmt.Printf("Output: %s\n", string(output))
		return "", err
	}

	return string(output), nil
}

// FileEvent represents a file system event with metadata
type FileEvent struct {
	Path      string
	EventType EventType
	IsDir     bool
	OldPath   string // For rename events
	Timestamp time.Time
}

// Watcher handles file system watching
type Watcher struct {
	config            *config.Config
	watchPath         string // Absolute path being watched
	watchChan         chan notify.EventInfo
	done              chan bool
	eventChan         chan FileEvent
	lastEvents        map[string]FileEvent // For debouncing
	sshClient         *sshclient.SSHClient
	extendedIgnores   []string   // Cached extended ignore patterns
	ignoreFileModTime time.Time  // Last modification time of .sync_ignore file
	fileCache         *FileCache // File metadata cache
}

// buildAndDeployAgent builds the agent for target OS and deploys it
func (w *Watcher) buildAndDeployAgent() error {
	if w.sshClient == nil {
		fmt.Println("⚠️  SSH client not available, skipping agent deployment")
		return nil
	}

	fmt.Println("🔨 Building sync agent for target OS...")

	// Determine target OS from config
	targetOS := w.config.Devsync.OSTarget
	if targetOS == "" {
		targetOS = "linux" // Default to linux
	}

	// Build agent for target platform
	agentPath, err := w.buildAgentForTarget(targetOS)
	if err != nil {
		fmt.Printf("⚠️  Build failed for agent: %v\n", err)

		// As fallback, check if a pre-built binary exists in project root
		projectRoot := filepath.Dir(w.watchPath)
		fallbackName := fmt.Sprintf("sync-agent-%s", targetOS)
		if targetOS == "windows" {
			fallbackName += ".exe"
		}
		fallbackPath := filepath.Join(projectRoot, fallbackName)
		if _, statErr := os.Stat(fallbackPath); statErr == nil {
			fmt.Printf("ℹ️  Found existing agent binary: %s - will use it as fallback\n", fallbackPath)
			// Use the fallback binary name (relative) to deploy
			agentPath = fallbackName
		} else {
			return fmt.Errorf("no fallback agent found at %s and build failed: %v", fallbackPath, err)
		}
	}

	fmt.Printf("📦 Agent built successfully: %s\n", agentPath)

	// Deploy agent to remote server
	if err := w.deployAgentToRemote(agentPath); err != nil {
		return fmt.Errorf("failed to deploy agent: %v", err)
	}

	fmt.Println("✅ Agent deployed successfully to remote server")
	return nil
}

// buildAgentForTarget builds the agent executable for the specified target OS
func (w *Watcher) buildAgentForTarget(targetOS string) (string, error) {
	// Get the project root directory (parent of current working directory)
	projectRoot := filepath.Dir(w.watchPath)
	agentSourceDir := filepath.Join(projectRoot, "sub_app", "agent")
	agentBinaryName := fmt.Sprintf("sync-agent-%s", targetOS)

	// Determine GOOS based on target OS
	var goos string
	switch targetOS {
	case "linux":
		goos = "linux"
	case "windows":
		goos = "windows"
		agentBinaryName += ".exe"
	case "darwin":
		goos = "darwin"
	default:
		goos = "linux"
	}

	// Build command with absolute path
	buildCmd := fmt.Sprintf("cd %s && GOOS=%s go build -o %s .", agentSourceDir, goos, filepath.Join(projectRoot, agentBinaryName))

	// Execute build
	if err := w.executeLocalCommand(buildCmd); err != nil {
		return "", fmt.Errorf("build failed: %v", err)
	}

	return agentBinaryName, nil
}

// deployAgentToRemote deploys the agent binary to the remote server
func (w *Watcher) deployAgentToRemote(agentPath string) error {
	// Get absolute path for the agent binary
	projectRoot := filepath.Dir(w.watchPath)
	absAgentPath := filepath.Join(projectRoot, agentPath)

	// Get the remote base path from config
	remoteBase := w.config.Devsync.Auth.RemotePath
	if remoteBase == "" {
		remoteBase = "." // fallback to current directory
	}
	// Create .sync_temp directory on remote using full path (always use / for remote)
	remoteSyncTemp := w.joinRemotePath(remoteBase, ".sync_temp")
	remoteCmd := fmt.Sprintf("mkdir -p %s", remoteSyncTemp)
	if err := w.sshClient.RunCommand(remoteCmd); err != nil {
		return fmt.Errorf("failed to create remote .sync_temp: %v", err)
	}

	fmt.Printf("📁 Created .sync_temp directory on remote server: %s\n", remoteSyncTemp)

	// Upload agent binary to remote .sync_temp directory
	remoteAgentPath := w.joinRemotePath(remoteSyncTemp, "sync-agent")
	fmt.Printf("📦 Deploying agent to remote path: %s\n", remoteAgentPath)
	// Check if agent already exists and compare identity
	if w.shouldSkipAgentUpload(absAgentPath, remoteAgentPath) {
		fmt.Println("⏭️  Agent already up-to-date, skipping upload")
		// return nil
	} else {
		fmt.Println("📦 Uploading new agent to remote server...")
		// fmt.Println("absolute agent path:", absAgentPath)
		// fmt.Println("remote agent path:", remoteAgentPath)
		if err := w.sshClient.SyncFile(absAgentPath, remoteAgentPath); err != nil {
			fmt.Println("err:", err)
			return fmt.Errorf("failed to upload agent: %v", err)
		}
	}
	// Make agent executable on remote
	remoteCmd = fmt.Sprintf("chmod +x %s", remoteAgentPath)
	if err := w.sshClient.RunCommand(remoteCmd); err != nil {
		return fmt.Errorf("failed to make agent executable: %v", err)
	}

	// Final verification - check agent permissions
	if output, err := w.sshClient.RunCommandWithOutput(fmt.Sprintf("ls -la %s", remoteAgentPath)); err != nil {
		fmt.Printf("⚠️  Could not verify agent permissions: %v\n", err)
	} else {
		fmt.Printf("✅ Agent permissions verified:\n%s", output)
	}

	return nil
}

// shouldSkipAgentUpload checks if agent upload should be skipped by comparing identities
func (w *Watcher) shouldSkipAgentUpload(localAgentPath, remoteAgentPath string) bool {
	fmt.Println("🔍 Checking if agent upload can be skipped...")

	// Check if remote agent exists
	checkCmd := fmt.Sprintf("test -f %s && echo 'exists' || echo 'not_exists'", remoteAgentPath)
	if output, err := w.sshClient.RunCommandWithOutput(checkCmd); err != nil {
		fmt.Printf("⚠️  Could not check remote agent existence: %v\n", err)
		return false // Don't skip if we can't check
	} else if strings.TrimSpace(output) != "exists" {
		fmt.Println("📦 Remote agent doesn't exist, upload required")
		return false // Agent doesn't exist, need to upload
	}

	fmt.Println("📦 Remote agent exists, checking identity...")

	// Get local agent identity
	localIdentity, err := w.getLocalAgentIdentity(localAgentPath)
	if err != nil {
		fmt.Printf("⚠️  Could not get local agent identity: %v\n", err)
		return false // Don't skip if we can't get local identity
	}

	// Get remote agent identity
	remoteIdentityCmd := fmt.Sprintf("cd %s && chmod +x sync-agent && ./sync-agent identity", filepath.Dir(remoteAgentPath))
	if output, err := w.sshClient.RunCommandWithOutput(remoteIdentityCmd); err != nil {
		fmt.Printf("⚠️  Could not get remote agent identity: %v\n", err)
		return false // Don't skip if we can't get remote identity
	} else {
		remoteIdentity := strings.TrimSpace(output)

		fmt.Printf("🔢 Local agent identity:  %s\n", localIdentity)
		fmt.Printf("🔢 Remote agent identity: %s\n", remoteIdentity)

		if localIdentity == remoteIdentity {
			// fmt.Println("✅ Agent identities match, skipping upload")
			return true // Skip upload
		} else {
			fmt.Println("🔄 Agent identities differ, upload required")
			return false // Need to upload
		}
	}
}

// getLocalAgentIdentity gets the identity hash of the local agent binary
func (w *Watcher) getLocalAgentIdentity(agentPath string) (string, error) {
	// Execute local agent with identity command
	identityCmd := fmt.Sprintf("%s identity", agentPath)
	output, err := w.executeLocalCommandWithOutput(identityCmd)
	if err != nil {
		return "", fmt.Errorf("failed to get local agent identity: %v", err)
	}

	// The output should be just the hash
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return "", fmt.Errorf("no identity output from local agent")
	}

	// Get the last line which should contain the hash
	return strings.TrimSpace(lines[len(lines)-1]), nil
}

// executeLocalCommand executes a command locally
func (w *Watcher) executeLocalCommand(cmd string) error {
	fmt.Printf("🔧 Executing: %s\n", cmd)

	// Execute the command using bash -c for complex commands
	command := exec.Command("bash", "-c", cmd)
	command.Dir = "." // Execute in current directory

	output, err := command.CombinedOutput()
	if err != nil {
		fmt.Printf("❌ Command failed: %v\n", err)
		fmt.Printf("Output: %s\n", string(output))
		return err
	}

	if len(output) > 0 {
		fmt.Printf("✅ Command output: %s\n", string(output))
	}

	return nil
}

// NewWatcher creates a new file watcher instance
func NewWatcher(cfg *config.Config) *Watcher {
	// Calculate watch path
	watchPath := cfg.LocalPath
	if watchPath == "" {
		watchPath = "."
	}
	absWatchPath, err := filepath.Abs(watchPath)
	if err != nil {
		fmt.Printf("⚠️  Failed to get absolute watch path: %v\n", err)
		absWatchPath = watchPath
	}

	// Initialize SSH client if auth is configured
	var sshClient *sshclient.SSHClient
	if cfg.Devsync.Auth.Username != "" && cfg.Devsync.Auth.PrivateKey != "" {
		var err error
		// Use persistent SSH client for better performance
		sshClient, err = sshclient.NewPersistentSSHClient(
			cfg.Devsync.Auth.Username,
			cfg.Devsync.Auth.PrivateKey,
			cfg.Devsync.Auth.Host,
			cfg.Devsync.Auth.Port,
		)
		if err != nil {
			fmt.Printf("⚠️  Failed to initialize SSH client: %v\n", err)
		} else {
			// Connect to SSH server
			if err := sshClient.Connect(); err != nil {
				fmt.Printf("⚠️  Failed to connect SSH server: %v\n", err)
				sshClient = nil
			} else {
				fmt.Printf("🔗 SSH client connected successfully\n")

				// Start persistent session for continuous monitoring
				if err := sshClient.StartPersistentSession(); err != nil {
					fmt.Printf("⚠️  Failed to start persistent session: %v\n", err)
					sshClient = nil
				} else {
					fmt.Printf("🔄 Persistent SSH session started\n")
				}
			}
		}
	}

	// Initialize file cache
	var fileCache *FileCache
	if sshClient != nil {
		// Create .sync_temp directory if it doesn't exist
		syncTempDir := ".sync_temp"
		if err := os.MkdirAll(syncTempDir, 0755); err != nil {
			fmt.Printf("⚠️  Failed to create .sync_temp directory: %v\n", err)
		} else {
			dbPath := filepath.Join(syncTempDir, "file_cache.db")
			var err error
			fileCache, err = NewFileCache(dbPath, absWatchPath)
			if err != nil {
				fmt.Printf("⚠️  Failed to initialize file cache: %v\n", err)
			} else {
				fmt.Printf("💾 File cache initialized: %s\n", dbPath)

				// Reset cache if configured
				if cfg.ResetCache {
					if err := fileCache.ResetCache(); err != nil {
						fmt.Printf("⚠️  Failed to reset cache: %v\n", err)
					}
				}
			}
		}
	}

	// Build and deploy agent if SSH client is available
	watcher := &Watcher{
		config:     cfg,
		watchPath:  absWatchPath,
		watchChan:  make(chan notify.EventInfo, 100),
		done:       make(chan bool),
		eventChan:  make(chan FileEvent, 100),
		lastEvents: make(map[string]FileEvent),
		sshClient:  sshClient,
		fileCache:  fileCache,
	}

	// Build and deploy agent to remote server
	if sshClient != nil {
		if err := watcher.buildAndDeployAgent(); err != nil {
			fmt.Printf("⚠️  Failed to build/deploy agent: %v\n", err)
		}

		// Sync configuration to remote
		if err := watcher.syncConfigToRemote(); err != nil {
			fmt.Printf("⚠️  Failed to sync config to remote: %v\n", err)
		}

		// Start continuous agent monitoring
		if err := watcher.startAgentMonitoring(); err != nil {
			fmt.Printf("⚠️  Failed to start agent monitoring: %v\n", err)
		}
	}

	return watcher
}

// joinRemotePath joins path components using forward slashes (/) for remote paths
// This ensures compatibility with remote servers (typically Linux/Unix)
func (w *Watcher) joinRemotePath(elem ...string) string {
	if len(elem) == 0 {
		return ""
	}
	if len(elem) == 1 {
		return elem[0]
	}

	// Start with first element
	result := elem[0]

	// Join remaining elements with forward slash
	for i := 1; i < len(elem); i++ {
		if result != "" && !strings.HasSuffix(result, "/") {
			result += "/"
		}
		elem[i] = strings.TrimPrefix(elem[i], "/")
		result += elem[i]
	}

	return result
}

// startAgentMonitoring starts continuous monitoring of the remote agent
func (w *Watcher) startAgentMonitoring() error {
	if w.sshClient == nil || !w.sshClient.IsPersistent() {
		return fmt.Errorf("persistent SSH client not available")
	}

	fmt.Println("👀 Starting continuous agent monitoring...")

	// Get the remote agent path
	remoteBase := w.config.Devsync.Auth.RemotePath
	if remoteBase == "" {
		return fmt.Errorf("remote base path is empty or not configured")
	}
	remoteSyncTemp := w.joinRemotePath(remoteBase, ".sync_temp")
	remoteAgentPath := w.joinRemotePath(remoteSyncTemp, "sync-agent")

	// Start agent watch command in background - run once and keep it running
	watchCmd := fmt.Sprintf("cd %s && chmod +x %s && %s watch", remoteBase, remoteAgentPath, remoteAgentPath)

	// fmt.Printf("🚀 Starting agent with command: %s\n", watchCmd)

	// Start the agent in a goroutine and keep it running
	go func() {
		for {
			if w.sshClient.GetSession() == nil {
				fmt.Println("⚠️  SSH session lost, attempting to restart...")
				if err := w.sshClient.StartPersistentSession(); err != nil {
					fmt.Printf("❌ Failed to restart SSH session: %v\n", err)
					time.Sleep(5 * time.Second)
					continue
				}
			}

			// fmt.Println("🔄 Starting agent watch command...")

			// Execute the watch command - this should run continuously
			if err := w.runAgentWatchCommand(watchCmd); err != nil {
				fmt.Printf("⚠️  Agent watch command failed: %v\n", err)

				// If session failed, try to restart
				if strings.Contains(err.Error(), "session") || strings.Contains(err.Error(), "broken pipe") {
					fmt.Println("🔌 SSH session broken, stopping current session...")
					w.sshClient.StopAgentSession()
					time.Sleep(3 * time.Second)
					continue
				}
			}

			// If we reach here, the command completed (which it shouldn't for a watch command)
			// This means the agent stopped unexpectedly
			fmt.Println("⚠️  Agent watch command completed unexpectedly, restarting in 5 seconds...")
			time.Sleep(5 * time.Second)
		}
	}()

	return nil
}

// runAgentWatchCommand runs the agent watch command and handles its output stream
func (w *Watcher) runAgentWatchCommand(watchCmd string) error {
	fmt.Printf("🚀 Running agent watch command: %s\n", watchCmd)

	// Use streaming output for continuous monitoring
	outputChan, errorChan, err := w.sshClient.RunCommandWithStream(watchCmd)
	if err != nil {
		return fmt.Errorf("failed to start agent watch command: %v", err)
	}

	fmt.Println("📡 Agent watch command started, monitoring output stream...")

	// Process output in real-time
	for {
		select {
		case output, ok := <-outputChan:
			if !ok {
				// Channel closed, agent command finished
				fmt.Println("📡 Agent output channel closed")
				return nil
			}
			if output == "" {
				continue
			}
			w.processAgentOutput(output)

		case err, ok := <-errorChan:
			if !ok {
				// Channel closed
				fmt.Println("📡 Agent error channel closed")
				return nil
			}
			if err != nil {
				fmt.Printf("⚠️  Agent output error: %v\n", err)
				return err
			}

		case <-time.After(3000 * time.Second):
			fmt.Println("⏰ Agent monitoring timeout - command may still be running")
			return fmt.Errorf("agent monitoring timeout")
		}
	}
}

// processAgentOutput processes the JSON output from the remote agent
func (w *Watcher) processAgentOutput(output string) {
	if strings.TrimSpace(output) == "" {
		return
	}

	// fmt.Printf("📨 Agent output: %s\n", output)

	// Parse structured output from agent
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}

		// Parse line format: [timestamp] TYPE|data|path
		if strings.HasPrefix(line, "[") && strings.Contains(line, "]") {
			parts := strings.SplitN(line, "]", 2)
			if len(parts) != 2 {
				continue
			}

			content := strings.TrimSpace(parts[1])
			if strings.HasPrefix(content, "EVENT|") {
				// Parse event: EVENT|EventType|path
				eventParts := strings.Split(content, "|")
				if len(eventParts) >= 3 {
					eventType := eventParts[1]
					filePath := eventParts[2]
					// Handle file events
					w.handleFileDownloadEvent(eventType, filePath)
				}
			} else if strings.HasPrefix(content, "HASH|") {
				// Parse hash: HASH|path|hash_value
				hashParts := strings.Split(content, "|")
				if len(hashParts) >= 3 {
					filePath := hashParts[1]
					hashValue := hashParts[2]

					// Determine remote base and try to compute relative path
					localPath := strings.ReplaceAll(filePath, w.config.Devsync.Auth.RemotePath, w.config.Devsync.Auth.LocalPath)

					// Map to local path under watchPath
					var err error
					// Compute local hash
					var localHash string
					if w.fileCache != nil {
						localHash, err = w.fileCache.CalculateFileHash(localPath)
						if err != nil {
							localHash = ""
						}
					}

					// fmt.Println("🧾 Received hash info - Remote:", filePath, "Local:", localPath, "Remote Hash:", hashValue, "Local Hash:", localHash)

					if localHash == hashValue {
						// If hashes match, no action needed
						return
					} else {
						// Try to download the file from remote to local
						remoteBase := w.config.Devsync.Auth.RemotePath
						if remoteBase == "" {
							remoteBase = "."
						}
						currentHash, _ := w.fileCache.CalculateFileHash(filePath)

						if currentHash == hashValue {
							fmt.Printf("✅ Remote file %s already in cache with matching hash, skipping download\n", filePath)
							continue
						}

						fmt.Println("💾 Downloading remote file to local:", filePath, "->", localPath)
						if err := w.fileCache.UpdateMetaDataFromDownload(localPath, hashValue); err != nil {
							fmt.Printf("⚠️  Failed to update cache for %s: %v\n", localPath, err)
						}
						if err := w.sshClient.DownloadFile(localPath, filePath); err != nil {
							fmt.Printf("❌ Failed to download file %s from remote %s: \n", localPath, err)
						}
					}
				}
			}
		}
	}
}

// handleFileEvent handles individual file events from agent
func (w *Watcher) handleFileDownloadEvent(eventType, filePath string) {

	// Only sync for Create and Write events
	// Handle delete events explicitly - accept common variants from agent
	if eventType == "Delete" || eventType == "Remove" || strings.HasSuffix(eventType, ".Remove") || strings.Contains(eventType, "Delete") {
		fmt.Printf("🗑️  Received delete event for %s\n", filePath)

		// Map remote path to local path
		relPath := strings.ReplaceAll(filePath, w.config.Devsync.Auth.RemotePath, w.config.Devsync.Auth.LocalPath)

		// Attempt to remove local file or directory
		if err := os.RemoveAll(relPath); err != nil {
			fmt.Printf("❌ Failed to delete local path %s: %v\n", relPath, err)
		} else {
			fmt.Printf("✅ Deleted local path: %s\n", relPath)

			// Remove metadata from cache if available
			if w.fileCache != nil {
				if err := w.fileCache.DeleteFileMetadata(relPath); err != nil {
					fmt.Printf("⚠️  Failed to delete metadata for %s: %v\n", relPath, err)
				}
			}
		}

		return
	}
}

// StopAgentMonitoring stops the continuous agent monitoring
func (w *Watcher) StopAgentMonitoring() error {
	if w.sshClient != nil {
		return w.sshClient.StopAgentSession()
	}
	return nil
}

// syncConfigToRemote syncs the current configuration to remote .sync_temp/config.json
func (w *Watcher) syncConfigToRemote() error {
	if w.sshClient == nil {
		return fmt.Errorf("SSH client not available")
	}

	fmt.Println("📋 Syncing configuration to remote...")

	// Get remote base path
	remoteBase := w.config.Devsync.Auth.RemotePath
	if remoteBase == "" {
		remoteBase = "."
	}

	// Create remote .sync_temp directory
	remoteSyncTemp := w.joinRemotePath(remoteBase, ".sync_temp")
	mkdirCmd := fmt.Sprintf("mkdir -p %s", remoteSyncTemp)
	if err := w.sshClient.RunCommand(mkdirCmd); err != nil {
		return fmt.Errorf("failed to create remote .sync_temp: %v", err)
	}

	// Create remote config path
	remoteConfigPath := w.joinRemotePath(remoteSyncTemp, "config.json")

	// Generate config content for remote agent
	remoteConfig := w.generateRemoteConfig()

	// Convert config to JSON
	configJSON, err := w.configToJSON(remoteConfig)
	if err != nil {
		return fmt.Errorf("failed to convert config to JSON: %v", err)
	}

	fmt.Printf("📤 Syncing config to: %s\n", remoteConfigPath)
	fmt.Printf("📄 Config content:\n%s\n", configJSON)

	// Upload config to remote via SSH
	if err := w.uploadConfigToRemote(configJSON, remoteConfigPath); err != nil {
		return fmt.Errorf("failed to upload config: %v", err)
	}

	fmt.Printf("✅ Config synced successfully to remote\n")
	return nil
}

// RemoteAgentConfig represents the configuration sent to remote agent
type RemoteAgentConfig struct {
	Devsync struct {
		Ignores        []string `json:"ignores"`
		AgentWatchs    []string `json:"agent_watchs"`
		ManualTransfer []string `json:"manual_transfer"`
		WorkingDir     string   `json:"working_dir"`
	} `json:"devsync"`
}

// generateRemoteConfig generates minimal configuration for remote agent
func (w *Watcher) generateRemoteConfig() *RemoteAgentConfig {
	newCfg, err := config.LoadAndRenderConfig()
	if err != nil {
		return &RemoteAgentConfig{}
	}

	// Update current config reference
	w.config = newCfg

	// Create remote config with necessary fields
	config := &RemoteAgentConfig{}

	config.Devsync.Ignores = w.config.Devsync.Ignores
	config.Devsync.AgentWatchs = w.config.Devsync.AgentWatchs
	config.Devsync.ManualTransfer = w.config.Devsync.ManualTransfer
	config.Devsync.WorkingDir = w.config.Devsync.Auth.RemotePath

	return config
}

// configToJSON converts config struct to JSON string
func (w *Watcher) configToJSON(cfg *RemoteAgentConfig) (string, error) {
	// Convert struct to JSON with indentation
	jsonBytes, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal config to JSON: %v", err)
	}

	return string(jsonBytes), nil
}

// uploadConfigToRemote uploads config content to remote file
func (w *Watcher) uploadConfigToRemote(configContent, remotePath string) error {
	// Create temporary local file
	tempFile, err := os.CreateTemp("", "remote-config-*.json")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Write config content to temp file
	if _, err := tempFile.WriteString(configContent); err != nil {
		return fmt.Errorf("failed to write config to temp file: %v", err)
	}
	tempFile.Close()

	// Upload temp file to remote
	if err := w.sshClient.SyncFile(tempFile.Name(), remotePath); err != nil {
		return fmt.Errorf("failed to upload config file: %v", err)
	}

	return nil
}
