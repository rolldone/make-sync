package devsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"make-sync/internal/config"
	"make-sync/internal/deployagent"
	"make-sync/internal/events"
	"make-sync/internal/sshclient"
	"make-sync/internal/util"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/asaskevich/EventBus"
	"github.com/rjeczalik/notify"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

func NewWatcherBasic(cfg *config.Config) (*Watcher, error) {
	// Calculate watch path
	watchPath := cfg.LocalPath
	if watchPath == "" {
		watchPath = "."
	}
	absWatchPath, err := filepath.Abs(watchPath)
	if err != nil {
		util.Default.Printf("⚠️  Failed to get absolute watch path: %v\n", err)
		absWatchPath = watchPath
	}

	// Initialize SSH client if auth is configured
	var sshClient *sshclient.SSHClient
	if cfg.Devsync.Auth.Username != "" && (cfg.Devsync.Auth.PrivateKey != "" || cfg.Devsync.Auth.Password != "") {
		var err error
		// Use persistent SSH client for better performance
		sshClient, err = sshclient.NewPersistentSSHClient(
			cfg.Devsync.Auth.Username,
			cfg.Devsync.Auth.PrivateKey,
			cfg.Devsync.Auth.Password,
			cfg.Devsync.Auth.Host,
			cfg.Devsync.Auth.Port,
		)
		if err != nil {
			util.Default.Printf("⚠️  Failed to initialize SSH client: %v\n", err)
		} else {
			// Connect to SSH server
			if err := sshClient.Connect(); err != nil {
				util.Default.Printf("⚠️  Failed to connect SSH server: %v\n", err)
				sshClient = nil
			} else {
				util.Default.Printf("🔗 SSH client connected successfully\n")
				util.Default.ClearLine()

				// Start persistent session for continuous monitoring
				if err := sshClient.StartPersistentSession(); err != nil {
					util.Default.Printf("⚠️  Failed to start persistent session: %v\n", err)
					sshClient = nil
				} else {
					util.Default.Printf("🔄 Persistent SSH session started\n")
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
			util.Default.Printf("⚠️  Failed to create .sync_temp directory: %v\n", err)
		} else {
			dbPath := filepath.Join(syncTempDir, "file_cache.db")
			var err error
			fileCache, err = NewFileCache(dbPath, absWatchPath)
			if err != nil {
				util.Default.Printf("⚠️  Failed to initialize file cache: %v\n", err)
			} else {
				util.Default.Printf("💾 File cache initialized: %s\n", dbPath)

				// Reset cache if configured
				if cfg.ResetCache {
					if err := fileCache.ResetCache(); err != nil {
						util.Default.Printf("⚠️  Failed to reset cache: %v\n", err)
					}
				}
			}
		}
	}

	// Build and deploy agent if SSH client is available
	// Create context for coordinated shutdown
	ctx, cancel := context.WithCancel(context.Background())

	watcher := &Watcher{
		ready:      make(chan struct{}),
		config:     cfg,
		watchPath:  absWatchPath,
		watchChan:  make(chan notify.EventInfo, 100),
		done:       make(chan bool),
		eventChan:  make(chan FileEvent, 100),
		lastEvents: make(map[string]FileEvent),
		sshClient:  sshClient,
		fileCache:  fileCache,
		// sessions:        make(map[string]*Session),
		sessionCounter:  0,
		keyboardStop:    make(chan bool, 1),
		keyboardRestart: make(chan bool, 1),
		keyboardStopped: make(chan struct{}, 1),
		eventBus:        EventBus.New(),
		// printing centralized via util.Default
		Slot:           nil,
		notifyStopped:  make(chan struct{}),
		configMu:       sync.RWMutex{},
		ignoresMu:      sync.RWMutex{},
		KeyboardEvents: make(chan string, 8),
		TUIActive:      false,
		ctx:            ctx,
		cancelFunc:     cancel,
	}

	return watcher, nil
}

// NewWatcher creates a new file watcher instance
func NewWatcher(cfg *config.Config) (*Watcher, error) {

	watcher, err := NewWatcherBasic(cfg)

	// Initialize command manager after watcher is created
	watcher.commands = NewCommandManager(watcher)

	// Initialize PTY manager for persistent remote sessions (Alt+3..9)
	watcher.ptyMgr = NewPTYManager(watcher)

	// build & deploy
	if err := watcher.buildAndDeployAgent(); err != nil {
		util.Default.Printf("⚠️  Failed to build/deploy agent: %v\n", err)
	}

	// sync config
	if err := watcher.syncConfigToRemote(); err != nil {
		util.Default.Printf("⚠️  Failed to sync config to remote: %v\n", err)
	}

	// start monitoring
	if err := watcher.startAgentMonitoring(); err != nil {
		util.Default.Printf("⚠️  Failed to start agent monitoring: %v\n", err)
	}

	// Setup persistent EventBus subscription for cleanup (survives Start/Stop cycles)
	watcher.setupEventBusSubscriptions()

	// start goroutines here as before (they'll wait on ready)
	return watcher, err
}

// Start begins watching files in the configured directory
func (w *Watcher) Start() error {

	restore, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return fmt.Errorf("failed to enable raw mode: %w", err)
	}
	w.oldState = restore
	w.firstOld = restore

	// make Start idempotent for repeated UI navigation
	w.runningMu.Lock()
	if w.running {
		w.runningMu.Unlock()
		return nil // already running
	}
	w.running = true
	w.runningMu.Unlock()
	// ensure we reset running flag when Start returns
	defer func() {
		w.runningMu.Lock()
		w.running = false
		w.runningMu.Unlock()
	}()

	// Get the watch path from config
	watchPath := w.config.LocalPath
	if watchPath == "" {
		watchPath = "."
	}

	// Convert to absolute path
	absWatchPath, err := filepath.Abs(watchPath)
	if err != nil {
		return fmt.Errorf("failed to get absolute path for %s: %v", watchPath, err)
	}

	w.safePrintf("🔍 Starting file watcher on: %s\n", absWatchPath)
	util.Default.ClearLine()
	w.safePrintf("📋 Watch permissions - Add: %v, Change: %v, Unlink: %v, UnlinkFolder: %v\n",
		w.config.Devsync.TriggerPerm.Add,
		w.config.Devsync.TriggerPerm.Change,
		w.config.Devsync.TriggerPerm.Unlink,
		w.config.Devsync.TriggerPerm.UnlinkFolder)

	// Ensure we safely initialize watchChan if needed. Accesses to watchChan
	// race when other goroutines (StopNotify) set/close it, so take a small
	// snapshot under notifyMu and perform initialization while holding the lock.
	w.notifyMu.Lock()
	needInit := (w.watchChan == nil)
	w.notifyMu.Unlock()
	if needInit {
		// If a previous notify loop existed, wait for it to fully stop
		w.notifyMu.Lock()
		prevStopped := w.notifyStopped
		w.notifyMu.Unlock()
		if prevStopped != nil {
			select {
			case <-prevStopped:
				// previous notify stopped
			case <-time.After(5 * time.Second):
				w.safePrintf("⚠️  Start: timeout waiting for previous notifyStopped\n")
			}
		}

		// reset so StopNotify can be called again later and allocate watchChan
		w.notifyMu.Lock()
		w.notifyStopOnce = sync.Once{}
		w.notifyStopped = make(chan struct{})
		w.watchChan = make(chan notify.EventInfo, 100)
		w.notifyMu.Unlock()
	}

	// Setup recursive watching
	watchPattern := filepath.Join(absWatchPath, "...")
	err = notify.Watch(watchPattern, w.watchChan, notify.All)
	if err != nil {
		return fmt.Errorf("failed to setup file watcher: %v", err)
	}

	// close ready exactly once to signal goroutines the watcher init completed
	w.readyOnce.Do(func() {
		// ensure channel exists (created in NewWatcher)
		if w.ready == nil {
			w.ready = make(chan struct{})
		}
		close(w.ready)
	})

	// Start keyboard input handler goroutine
	// Start legacy keyboard handler only when TUI is not active
	go w.handleKeyboardInput()

	// Start session completion event handler
	go w.handleSessionCompletionEvents()

	// Start event processing goroutine
	go w.processEvents()

	w.safeStatus("✅ File watcher started successfully\n")
	w.safeStatus("💡 Press Ctrl+C to stop watching, R+Enter to reload .sync_ignore, S+Enter to show cache stats, A+Enter to deploy agent\n")

	select {
	case <-w.done:
		w.safePrintln("done:", w.notifyStopped)
		return nil
	case <-w.notifyStopped:
		w.safePrintln("notifyStopped:", w.notifyStopped)
		// notify subsystem stopped -> return to menu
		return nil
	}
}

// Stop stops the file watcher
func (w *Watcher) Stop() {
	// Close file cache if exists
	if w.fileCache != nil {
		if totalFiles, totalSize, err := w.fileCache.GetFileStats(); err == nil {
			w.safePrintf("💾 Cache stats: %d files, %.2f MB\n", totalFiles, float64(totalSize)/(1024*1024))
		}

		if err := w.fileCache.Close(); err != nil {
			w.safePrintf("⚠️  Failed to close file cache: %v\n", err)
		} else {
			w.safePrintln("💾 File cache closed")
		}
	}

	// Close SSH connection if exists
	if w.sshClient != nil {
		if err := w.sshClient.Close(); err != nil {
			w.safePrintf("⚠️  Failed to close SSH connection: %v\n", err)
		} else {
			w.safePrintln("🔌 SSH connection closed")
		}
	}

	select {
	case w.done <- true:
	default:
		// Channel already has value or is closed
	}
}

func (w *Watcher) generateRemoteConfig() *RemoteAgentConfig {
	// Load & render config (may be expensive) then swap under lock
	newCfg, err := config.LoadAndRenderConfig()
	if err != nil {
		return &RemoteAgentConfig{}
	}

	// atomically update watcher config
	w.configMu.Lock()
	w.config = newCfg
	w.configMu.Unlock()

	// Build remote config snapshot from the new config
	cfg := &RemoteAgentConfig{}
	cfg.Devsync.Ignores = newCfg.Devsync.Ignores
	cfg.Devsync.AgentWatchs = newCfg.Devsync.AgentWatchs
	cfg.Devsync.ManualTransfer = newCfg.Devsync.ManualTransfer
	cfg.Devsync.WorkingDir = newCfg.Devsync.Auth.RemotePath

	return cfg
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

// joinRemotePathOS joins remote path components using a separator appropriate
// for the target OS. For Windows it uses backslashes and preserves drive
// letters; otherwise it falls back to POSIX-style forward slashes.
func (w *Watcher) joinRemotePathOS(targetOS string, elem ...string) string {
	if len(elem) == 0 {
		return ""
	}
	if strings.Contains(strings.ToLower(targetOS), "win") {
		// Windows-style join using backslashes
		res := elem[0]
		for i := 1; i < len(elem); i++ {
			part := strings.Trim(elem[i], "\\/")
			if !strings.HasSuffix(res, "\\") {
				res += "\\"
			}
			res += part
		}
		return res
	}
	// default to POSIX join
	return w.joinRemotePath(elem...)
}

// shellEscape escapes single quotes for safe inclusion in single-quoted shell strings
func shellEscape(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// startAgentMonitoring starts continuous monitoring of the remote agent
func (w *Watcher) startAgentMonitoring() error {
	if w.sshClient == nil || !w.sshClient.IsPersistent() {
		return fmt.Errorf("persistent SSH client not available")
	}

	w.safePrintln("👀 Starting continuous agent monitoring...")

	// Get the remote agent path
	remoteBase := w.config.Devsync.Auth.RemotePath
	if remoteBase == "" {
		return fmt.Errorf("remote base path is empty or not configured")
	}
	// Use OS-aware joins for remote paths
	remoteSyncTemp := w.joinRemotePathOS(w.config.Devsync.OSTarget, remoteBase, ".sync_temp")

	// Get unique agent binary name from local config
	localConfig, err := config.GetOrCreateLocalConfig()
	if err != nil {
		w.safePrintf("⚠️  Failed to load local config: %v\n", err)
		return err // Can't proceed without agent name
	}

	targetOS := strings.ToLower(w.config.Devsync.OSTarget)
	remoteExecName := localConfig.GetAgentBinaryName(targetOS)
	remoteAgentPath := w.joinRemotePathOS(w.config.Devsync.OSTarget, remoteSyncTemp, remoteExecName)

	// Start agent watch command in background - run once and keep it running
	var watchCmd string
	if strings.Contains(strings.ToLower(w.config.Devsync.OSTarget), "win") {
		// Windows: capture PID and run agent
		watchCmd = fmt.Sprintf(`cmd.exe /C "cd /d "%s" && (type ".sync_temp\config.json" 2>nul || echo NO_REMOTE_CONFIG) && echo AGENT_STARTING && "%s" watch"`, remoteBase, remoteAgentPath)
	} else {
		// POSIX: capture PID and run agent
		watchCmd = fmt.Sprintf(`bash -c "(cat %s/.sync_temp/config.json || echo NO_REMOTE_CONFIG) && echo AGENT_STARTING && %s watch & echo AGENT_PID:$! && wait"`, remoteBase, remoteAgentPath)
	}

	// fmt.Printf("🚀 Starting agent with command: %s\n", watchCmd)

	// Start the agent in a goroutine and keep it running
	go func() {
		for {
			// Check if context cancelled
			select {
			case <-w.ctx.Done():
				w.safePrintln("🔄 Agent monitoring stopped (shutdown requested)")
				return
			default:
			}

			if w.sshClient.GetSession() == nil {
				w.safePrintln("⚠️  SSH session lost, attempting to restart...")
				if err := w.sshClient.StartPersistentSession(); err != nil {
					w.safePrintf("❌ Failed to restart SSH session: %v\n", err)

					// Use context-aware sleep
					select {
					case <-w.ctx.Done():
						w.safePrintln("🔄 Agent monitoring stopped during reconnect wait")
						return
					case <-time.After(5 * time.Second):
						continue
					}
				}
			}

			// fmt.Println("🔄 Starting agent watch command...")

			// Execute the watch command - this should run continuously
			// Before starting the long-running watch, run a quick identity check to verify the executable can run
			var identityCmd string
			if strings.Contains(strings.ToLower(w.config.Devsync.OSTarget), "win") {
				identityCmd = fmt.Sprintf("cmd.exe /C cd /d \"%s\" && \"%s\" identity", remoteBase, remoteAgentPath)
			} else {
				identityCmd = fmt.Sprintf("%s identity", remoteAgentPath)
			}
			if out, err := w.sshClient.RunCommandWithOutput(identityCmd); err != nil {
				w.safePrintf("⚠️  Agent identity check failed: %v\n", err)
				w.safePrintf("🔍 identity output: %s\n", strings.TrimSpace(out))
			} else {
				util.Default.ClearLine()
				w.safePrintf("🔢 Agent identity (pre-watch): %s\n", strings.TrimSpace(out))
				util.Default.ClearLine()
			}

			// Execute the watch command - this should run continuously
			if err := w.runAgentWatchCommand(watchCmd); err != nil {
				w.safePrintf("⚠️  Agent watch command failed: %v\n", err)

				// If shutdown requested, exit gracefully
				if strings.Contains(err.Error(), "shutdown requested") {
					w.safePrintln("🔄 Agent monitoring stopped (shutdown requested)")
					return
				}

				// If session failed, try to restart
				if strings.Contains(err.Error(), "session") || strings.Contains(err.Error(), "broken pipe") {
					w.safePrintln("🔌 SSH session broken, stopping current session...")
					w.sshClient.StopAgentSession()

					// Use context-aware sleep
					select {
					case <-w.ctx.Done():
						w.safePrintln("🔄 Agent monitoring stopped during session restart wait")
						return
					case <-time.After(3 * time.Second):
						continue
					}
				}
			}

			// If we reach here, the command completed (which it shouldn't for a watch command)
			// This means the agent stopped unexpectedly
			w.safeStatusln("⚠️  Agent watch command completed unexpectedly, restarting in 5 seconds...")

			// Use context-aware sleep
			select {
			case <-w.ctx.Done():
				w.safePrintln("🔄 Agent monitoring stopped during restart wait")
				return
			case <-time.After(5 * time.Second):
				// Continue to next iteration
			}
		}
	}()

	return nil
}

// runAgentWatchCommand runs the agent watch command and handles its output stream
func (w *Watcher) runAgentWatchCommand(watchCmd string) error {
	// Print the command with a single blank line before it to reduce visual clutter
	w.safeStatusln("🚀 Running agent watch command: %s", watchCmd)

	// Use streaming output for continuous monitoring
	var outputChan <-chan string
	var errorChan <-chan error
	var err error

	// Retry wrapper: if starting the remote watch command fails (e.g., SSH/TCP),
	// attempt to reconnect the SSH client and retry with exponential backoff.
	maxRetries := 6
	backoff := 1 * time.Second
	for attempt := 0; attempt < maxRetries; attempt++ {
		outputChan, errorChan, err = w.sshClient.RunCommandWithStream(watchCmd, false)
		if err == nil {
			break
		}
		w.safePrintf("⚠️  Agent watch command failed to start (attempt %d/%d): %v\n", attempt+1, maxRetries, err)

		// Try reconnecting SSH client if possible
		if w.sshClient != nil {
			_ = w.sshClient.Close()
			// small sleep before reconnect
			select {
			case <-w.ctx.Done():
				return fmt.Errorf("shutdown requested")
			case <-time.After(backoff):
			}
			if cerr := w.sshClient.Connect(); cerr != nil {
				w.safePrintf("⚠️  SSH client reconnect failed: %v\n", cerr)
			} else {
				// Try to start persistent session if configured
				_ = w.sshClient.StartPersistentSession()
			}
		}

		// Exponential backoff
		time.Sleep(backoff)
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
	if err != nil {
		return fmt.Errorf("failed to start agent watch command: %v", err)
	}

	// Hide cursor while streaming and print a one-line status
	// w.hideCursor()
	w.safeStatusln("📡 Agent watch command started, monitoring output stream...")

	// Process output in real-time
	for {
		select {
		case <-w.ctx.Done():
			// Context cancelled - shutdown requested
			w.showCursor()
			w.safePrintln("🔄 Agent watch command stopped (shutdown requested)")
			return fmt.Errorf("shutdown requested")

		case output, ok := <-outputChan:
			if !ok {
				// Channel closed, agent command finished
				// Restore cursor visibility before returning
				w.showCursor()
				w.safePrintln("📡 Agent output channel closed")
				return nil
			}
			if output == "" {
				continue
			}
			w.processAgentOutput(output)

		case err, ok := <-errorChan:
			if !ok {
				// Channel closed
				// Restore cursor visibility before returning
				w.showCursor()
				w.safeStatusln("📡 Agent error channel closed")
				return nil
			}
			if err != nil {
				// Restore cursor before propagating error
				w.showCursor()
				w.safePrintf("⚠️  Agent output error: %v\n", err)

				// Drain any remaining output from the output channel to capture last logs
				var buf strings.Builder
			drainLoop:
				for {
					select {
					case s, ok := <-outputChan:
						if !ok {
							break drainLoop
						}
						buf.WriteString(s)
					default:
						// no more immediate output
						break drainLoop
					}
				}
				if buf.Len() > 0 {
					w.safePrintf("📨 Agent final output:\n%s\n", buf.String())
				}

				return err
			}

		case <-time.After(3000 * time.Second):
			// Restore cursor before returning
			w.showCursor()
			w.safePrintln("⏰ Agent monitoring timeout - command may still be running")
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

	// Check for agent PID information first
	if strings.Contains(output, "AGENT_PID:") {
		lines := strings.Split(output, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "AGENT_PID:") {
				pid := strings.TrimPrefix(line, "AGENT_PID:")
				w.agentPID = strings.TrimSpace(pid)
				util.Default.ClearLine()
				w.safePrintf("📍 Agent PID captured: %s\n", w.agentPID)
				continue
			}
		}
	}

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
					localPath, lerr := util.RemoteToLocal(w.config.Devsync.Auth.RemotePath, w.config.Devsync.Auth.LocalPath, filePath)
					if lerr != nil {
						w.safePrintf("⚠️  Could not map remote path to local: %v\n", lerr)
						continue
					}

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
							w.safePrintf("✅ Remote file %s already in cache with matching hash, skipping download\n", filePath)
							continue
						}

						w.safePrintf("💾 Downloading remote file to local: %s -> %s\n", filePath, localPath)
						if err := w.fileCache.UpdateMetaDataFromDownload(localPath, hashValue); err != nil {
							w.safePrintf("⚠️  Failed to update cache for %s: %v\n", localPath, err)
						}
						if err := w.sshClient.DownloadFile(localPath, filePath); err != nil {
							w.safePrintf("❌ Failed to download file %s from remote: %v\n", localPath, err)
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
		w.safePrintf("🗑️  Received delete event for %s\n", filePath)

		// Map remote path to local path
		relPath, rerr := util.RemoteToLocal(w.config.Devsync.Auth.RemotePath, w.config.Devsync.Auth.LocalPath, filePath)
		if rerr != nil {
			w.safePrintf("⚠️  Could not map remote delete path to local: %v\n", rerr)
			return
		}

		// Attempt to remove local file or directory
		if err := os.RemoveAll(relPath); err != nil {
			w.safePrintf("❌ Failed to delete local path %s: %v\n", relPath, err)
		} else {
			w.safePrintf("✅ Deleted local path: %s\n", relPath)

			// Remove metadata from cache if available
			if w.fileCache != nil {
				if err := w.fileCache.DeleteFileMetadata(relPath); err != nil {
					w.safePrintf("⚠️  Failed to delete metadata for %s: %v\n", relPath, err)
				}
			}
		}

		return
	}
}

// StopAgentMonitoring stops the continuous agent monitoring
func (w *Watcher) StopAgentMonitoring() error {
	if w.sshClient != nil {
		// First, try to kill remote agent processes explicitly
		util.Default.ClearLine()
		w.safePrintln("🔄 Attempting to kill remote agent processes...")

		// Get unique agent binary name from local config
		localConfig, err := config.GetOrCreateLocalConfig()
		if err != nil {
			w.safePrintf("⚠️  Failed to load local config: %v\n", err)
			return w.sshClient.StopAgentSession()
		}

		targetOS := strings.ToLower(w.config.Devsync.OSTarget)
		agentBinaryName := localConfig.GetAgentBinaryName(targetOS)
		w.safePrintf("🎯 Looking for agent process: %s\n", agentBinaryName)

		var killCmd string

		if strings.Contains(targetOS, "win") {
			// Windows: Get PID first, then kill - using unique agent name
			getPIDCmd := fmt.Sprintf(`for /f "tokens=2 delims=," %%i in ('tasklist /fi "imagename eq %s" /fo csv /nh') do @echo %%i`, agentBinaryName)
			if pidOutput, err := w.sshClient.RunCommandWithOutput(getPIDCmd); err == nil {
				// Remove quotes from PID output
				pidOutput = strings.Trim(strings.TrimSpace(pidOutput), `"`)
				if pidOutput != "" && pidOutput != `""` {
					w.safePrintf("📍 Found agent PID: %s\n", pidOutput)
					killCmd = fmt.Sprintf(`taskkill /F /PID %s 2>nul || echo "Agent PID %s not found"`, pidOutput, pidOutput)
				} else {
					w.safePrintf("⚠️  No %s processes found\n", agentBinaryName)
					killCmd = fmt.Sprintf(`echo "No %s processes found"`, agentBinaryName)
				}
			} else {
				// Fallback to process name kill
				killCmd = fmt.Sprintf(`taskkill /F /IM %s 2>nul || echo "No %s processes found"`, agentBinaryName, agentBinaryName)
			}
		} else {
			// Unix/Linux: Get PID first, then kill - using unique agent name
			getPIDCmd := fmt.Sprintf(`pgrep -f "%s" | head -1`, agentBinaryName)
			if pidOutput, err := w.sshClient.RunCommandWithOutput(getPIDCmd); err == nil {
				pidOutput = strings.TrimSpace(pidOutput)
				if pidOutput != "" {
					w.safePrintf("📍 Found agent PID: %s\n", pidOutput)
					killCmd = fmt.Sprintf(`kill -TERM %s 2>/dev/null || echo "Agent PID %s not found"`, pidOutput, pidOutput)
				} else {
					w.safePrintf("⚠️  No %s processes found\n", agentBinaryName)
					killCmd = fmt.Sprintf(`echo "No %s processes found"`, agentBinaryName)
				}
			} else {
				// Fallback to process name kill
				killCmd = fmt.Sprintf(`pkill -f "%s" || echo "No %s processes found"`, agentBinaryName, agentBinaryName)
			}
		}

		// Run kill command (ignore errors since process might already be dead)
		if err := w.sshClient.RunCommand(killCmd); err != nil {
			w.safePrintf("⚠️  Kill command failed (this might be normal): %v\n", err)
		} else {
			w.safePrintln("✅ Remote agent kill command sent")
		}

		// Then close SSH agent session
		return w.sshClient.StopAgentSession()
	}
	return nil
} // syncConfigToRemote syncs the current configuration to remote .sync_temp/config.json
func (w *Watcher) syncConfigToRemote() error {
	if w.sshClient == nil {
		return nil
	}

	// generate remote config JSON
	cfg := w.generateRemoteConfig()
	configJSON, err := w.configToJSON(cfg)
	if err != nil {
		return fmt.Errorf("failed to convert config to JSON: %v", err)
	}

	// compute remote path for .sync_temp/config.json (OS-aware)
	remoteBase := w.config.Devsync.Auth.RemotePath
	if remoteBase == "" {
		remoteBase = "."
	}
	remoteSyncTemp := w.joinRemotePathOS(w.config.Devsync.OSTarget, remoteBase, ".sync_temp")
	remoteConfigPath := w.joinRemotePathOS(w.config.Devsync.OSTarget, remoteSyncTemp, "config.json")

	// Print remote path and dump config atomically. Add one blank line before the block
	util.Default.Printf("📤 Syncing config to: %s\n", remoteConfigPath)
	util.Default.Printf("📄 Config content:\n")
	// Use PrintBlock to atomically print multi-line JSON and clear any single-line status
	util.Default.PrintBlock(configJSON, true)
	// Upload config to remote via SSH
	if err := w.uploadConfigToRemote(configJSON, remoteConfigPath); err != nil {
		return fmt.Errorf("failed to upload config: %v", err)
	}

	w.safePrintln("✅ Config synced successfully to remote")
	return nil
}

// safePrintf prints using a mutex to avoid interleaving with other goroutines
func (w *Watcher) safePrintf(format string, a ...interface{}) {
	util.Default.ClearLine()
	util.Default.Printf(format, a...)
	util.Default.ClearLine()
}

// safePrintln prints a line using a mutex to avoid interleaving with other goroutines
func (w *Watcher) safePrintln(a ...interface{}) {
	util.Default.ClearLine()
	util.Default.Println(a...)
	util.Default.ClearLine()
}

// safeStatus writes a single-line status at the start of the line (clears remainder)
func (w *Watcher) safeStatus(format string, a ...interface{}) {
	// Print clear-line and formatted status as a single atomic block to
	util.Default.PrintBlock(fmt.Sprintf(format, a...), true)
}

// safeStatusln writes a single-line status and appends a newline
func (w *Watcher) safeStatusln(format string, a ...interface{}) {
	// Use PrintBlock to atomically clear the line and print the status with newline
	util.Default.PrintBlock(fmt.Sprintf(format, a...), true)
}

// hideCursor hides the terminal cursor (thread-safe)
func (w *Watcher) hideCursor() {
	util.Default.Print("\x1b[?25l")
}

// showCursor shows the terminal cursor (thread-safe)
func (w *Watcher) showCursor() {
	util.Default.Print("\x1b[?25h")
}

// buildAndDeployAgent builds the agent for target OS and deploys it
func (w *Watcher) buildAndDeployAgent() error {
	if w.sshClient == nil {
		w.safePrintln("⚠️  SSH client not available, skipping agent deployment")
		return nil
	}

	// Get project root
	projectRoot := filepath.Dir(w.watchPath)

	// Prepare unified deployment options
	deployOpts := deployagent.UnifiedDeployOptions{
		ProjectRoot:    projectRoot,
		TargetOS:       w.config.Devsync.OSTarget,
		Config:         w.config,
		SSHClient:      w.sshClient,
		BuildIfMissing: true,
		UploadAgent:    true,
		UploadConfig:   true,
	}

	// Use unified deployment API
	remoteAgentPath, err := deployagent.DeployAgentAndConfig(deployOpts)
	if err != nil {
		return fmt.Errorf("failed to deploy agent and config: %v", err)
	}

	w.safePrintf("✅ Agent and config deployed successfully at: %s\n", remoteAgentPath)
	return nil
}

// processEvents processes file system events
func (w *Watcher) processEvents() {

	// Wait until Watcher is fully initialized by Start()
	if w.ready != nil {
		<-w.ready
	}

	// Ensure notifyStopped is initialized so StopNotify can wait on it
	if w.notifyStopped == nil {
		w.notifyStopped = make(chan struct{})
	}

	defer func() {
		// signal that notify processing stopped. Closing a channel twice will
		// panic, so guard with recover to ensure we don't crash if something
		// else already closed it.
		defer func() {
			if r := recover(); r != nil {
				// already closed or other panic - log and continue
				w.safePrintf("⚠️  notifyStopped close recovered: %v\n", r)
			}
		}()
		if w.notifyStopped != nil {
			close(w.notifyStopped)
		}
	}()

	for {
		select {
		case event, ok := <-w.watchChan:
			if !ok {
				// watch channel closed - return
				return
			}
			w.handleEvent(event)
		case <-w.done:
			return
		}
	}
}

// handleSessionCompletionEvents handles session completion events via EventBus
func (w *Watcher) handleSessionCompletionEvents() {
	// Subscribe to session completion events
	if w.eventBus == nil {
		return
	}
	w.eventBus.Subscribe("session:completed", func(sessionName string) {
		// If no active sessions remain, clear screen and show main menu
		if !w.hasActiveSession() {
			util.Default.PrintBlock("\033[2J\033[1;1H", false)
			w.displayMainMenu()
		}
	})
}

// handleCleanupEvents handles cleanup events via GlobalBus
func (w *Watcher) handleCleanupEvents() {
	// Subscribe to cleanup events using GlobalBus
	events.GlobalBus.Subscribe(events.EventCleanupRequested, func() {
		util.Default.ClearLine()
		util.Default.Printf("🧹 Received cleanup event, stopping agents...\n")
		util.Default.ClearLine()
		if err := w.StopAgentMonitoring(); err != nil {
			util.Default.Printf("⚠️  Error during cleanup: %v\n", err)
		} else {
			util.Default.Printf("✅ Agent cleanup completed\n")
			util.Default.ClearLine()
		}
	})
}

// setupEventBusSubscriptions sets up persistent EventBus subscriptions
func (w *Watcher) setupEventBusSubscriptions() {
	// Subscribe to cleanup events - this survives Start/Stop cycles
	events.GlobalBus.Subscribe(events.EventCleanupRequested, func() {
		util.Default.ClearLine()
		util.Default.Printf("🧹 Received cleanup event, stopping agents...\n")
		util.Default.ClearLine()
		if err := w.StopAgentMonitoring(); err != nil {
			util.Default.Printf("⚠️  Error during cleanup: %v\n", err)
		} else {
			util.Default.Printf("✅ Agent cleanup completed\n")
		}
		util.Default.ClearLine()
	})
}

// handleEvent processes a single file system event
func (w *Watcher) handleEvent(event notify.EventInfo) {
	path := event.Path()

	// If .sync_ignore modified -> clear cache under lock
	if filepath.Base(path) == ".sync_ignore" {
		w.ignoresMu.Lock()
		w.extendedIgnores = nil
		w.ignoreFileModTime = time.Time{}
		w.ignoresMu.Unlock()
	}

	// Snapshot config for safe downstream use
	w.configMu.RLock()
	cfgSnapshot := w.config
	w.configMu.RUnlock()

	// Check if path should be ignored (uses loadExtendedIgnores which is locked internally)
	if w.shouldIgnore(path) {
		return
	}

	// Map notify event to our EventType
	eventType := w.mapNotifyEvent(event.Event())

	// Check permissions using snapshot
	if cfgSnapshot != nil {
		switch eventType {
		case EventCreate:
			if !cfgSnapshot.Devsync.TriggerPerm.Add {
				return
			}
		case EventWrite:
			if !cfgSnapshot.Devsync.TriggerPerm.Change {
				return
			}
		case EventRemove:
			if !cfgSnapshot.Devsync.TriggerPerm.Unlink {
				return
			}
		case EventRename:
			if !cfgSnapshot.Devsync.TriggerPerm.Unlink {
				return
			}
		}
	} else {
		// fallback: use existing isEventAllowed (reads w.config) but try to avoid races
		if !w.isEventAllowed(eventType) {
			return
		}
	}

	// Get file info
	info, err := os.Stat(path)
	isDir := err == nil && info.IsDir()

	// Create FileEvent
	fileEvent := FileEvent{
		Path:      path,
		EventType: eventType,
		IsDir:     isDir,
		Timestamp: time.Now(),
	}

	// Check for duplicate events (debouncing)
	if w.isDuplicateEvent(fileEvent) {
		return // Skip duplicate event
	}

	// Store this event for debouncing
	w.storeEvent(fileEvent)

	// Execute scripts if configured
	w.ExecuteScripts(fileEvent)
}

// ExecuteScripts executes configured scripts for file events
func (w *Watcher) ExecuteScripts(event FileEvent) {

	// Handle SSH sync / delete if SSH client is available
	if w.sshClient != nil {
		if event.EventType == EventRemove {
			// Map local path to remote path using POSIX join (preserve forward slashes)
			remoteBase := w.config.Devsync.Auth.RemotePath
			if remoteBase == "" {
				remoteBase = "."
			}
			var remotePath string
			if rel, rerr := filepath.Rel(w.config.Devsync.Auth.LocalPath, event.Path); rerr == nil {
				remotePath = w.joinRemotePath(remoteBase, filepath.ToSlash(rel))
			} else {
				// Try robust helper conversion instead of naive replace
				rp, merr := util.LocalToRemote(w.config.Devsync.Auth.LocalPath, remoteBase, event.Path)
				if merr != nil {
					w.safePrintf("⚠️  Could not map local path to remote: %v\n", merr)
					return
				}
				remotePath = rp
			}

			// Final failsafe: don't try to delete .sync_temp on remote
			if strings.Contains(remotePath, ".sync_temp") {
				w.safePrintf("🚫 BLOCKED: Remote delete blocked for path: %s\n", remotePath)
			} else {
				// Use rm -rf to remove files or directories on remote side
				cmd := fmt.Sprintf("rm -rf '%s'", remotePath)
				w.safePrintf("📤 Deleting remote path: %s\n", remotePath)
				if err := w.sshClient.RunCommand(cmd); err != nil {
					w.safePrintf("❌ Failed to delete remote path %s: %v\n", remotePath, err)
				} else {
					w.safePrintf("✅ Remote delete succeeded: %s\n", remotePath)
				}
			}

			// Remove metadata from file cache if available
			if w.fileCache != nil {
				if err := w.fileCache.DeleteFileMetadata(event.Path); err != nil {
					w.safePrintf("⚠️  Failed to delete metadata for %s: %v\n", event.Path, err)
				} else {
					w.safePrintf("🗑️  Deleted cache metadata for %s\n", event.Path)
				}
			}
		} else {
			// For create/write events, sync file normally
			isDifferent, err := w.fileCache.ShouldSyncFile(event.Path)
			if err != nil {
				w.safePrintf("⚠️  Cache check error for %s: %v\n", event.Path, err)
				return
			}
			if !isDifferent {
				w.safeStatus("ℹ️  File unchanged, skipping sync: %s\n", event.Path)
				return
			}
			w.syncFileViaSSH(event)
		}
	}
}

// StopNotify stops only the file-notify subsystem (file watching). It is idempotent
// and will signal when the notify loop has fully stopped by closing notifyStopped.
// ...existing code...
func (w *Watcher) StopNotify() {
	// idempotent stop via sync.Once
	// Hold notifyMu while invoking Do to prevent Start() from resetting the
	// sync.Once concurrently which would cause a data race on the Once internals.
	w.notifyMu.Lock()
	nsOnce := &w.notifyStopOnce

	nsOnce.Do(func() {
		// protect against panics from notify.Stop or close
		w.safeStatusln("\n🛑 Stopping file watcher...")
		time.Sleep(3 * time.Second)
		if w.watchChan != nil {
			// Stop notify library watchers (safe to call multiple times)
			func() {
				defer func() {
					if r := recover(); r != nil {
						w.safePrintf("⚠️  notify.Stop panic recovered: %v\n", r)
					}
				}()
				notify.Stop(w.watchChan)
			}()

			// Close our watch channel to signal processEvents to exit.
			func() {
				defer func() {
					if r := recover(); r != nil {
						// already closed by someone else — ignore
					}
				}()
				close(w.watchChan)
			}()
		}
	})
	w.notifyMu.Unlock()

	// Wait for processEvents() to acknowledge shutdown by closing notifyStopped
	w.notifyMu.Lock()
	ns := w.notifyStopped
	w.notifyMu.Unlock()
	if ns == nil {
		// if processEvents not started, nothing to wait for
		return
	}
	select {
	case <-ns:
		// stopped normally
	case <-time.After(5 * time.Second):
		w.safePrintf("⚠️  StopNotify: timeout waiting for notify to stop\n")
	}

	// Note: do not assign nil to w.watchChan here. The channel is closed
	// inside the sync.Once above and keeping the reference avoids a concurrent
	// write to the struct field that could race with Start(). If the channel
	// value is observed as closed elsewhere, code should handle that case.
	// Clear watchChan reference under notifyMu so Start() won't observe an
	// in-progress StopNotify and reinitialize concurrently which could lead
	// to processEvents running and closing notifyStopped again.
	w.notifyMu.Lock()
	w.watchChan = nil
	w.notifyMu.Unlock()
}

// syncFileViaSSH syncs a file to remote server via SSH
func (w *Watcher) syncFileViaSSH(event FileEvent) {
	if w.sshClient == nil {
		w.safePrintf("❌ SSH client not available for file sync\n")
		return
	}

	localPath := event.Path

	targetOS := strings.ToLower(w.config.Devsync.OSTarget)
	remoteBase := w.config.Devsync.Auth.RemotePath
	if remoteBase == "" {
		remoteBase = "."
	}

	// Derive relative path
	var relPart string
	if rel, rerr := filepath.Rel(w.config.Devsync.Auth.LocalPath, localPath); rerr == nil {
		relPart = filepath.ToSlash(rel)
	} else {
		rp, merr := util.LocalToRemote(w.config.Devsync.Auth.LocalPath, remoteBase, localPath)
		if merr != nil {
			w.safePrintf("⚠️  Could not map local path to remote for sync: %v\n", merr)
			return
		}
		// rp includes remoteBase already; treat full path
		relPart = strings.TrimPrefix(rp, remoteBase)
	}

	// Build remote path OS-aware
	var remotePath string
	if strings.Contains(targetOS, "win") {
		// Ensure remoteBase uses backslashes
		remotePath = w.joinRemotePathOS(targetOS, remoteBase, relPart)
		// Normalize any forward slashes that slipped through
		remotePath = strings.ReplaceAll(remotePath, "/", "\\")
	} else {
		remotePath = w.joinRemotePath(remoteBase, relPart)
	}

	// Determine remote directory
	var remoteDir string
	if strings.Contains(targetOS, "win") {
		idx := strings.LastIndexAny(remotePath, "\\/")
		if idx == -1 {
			remoteDir = "."
		} else {
			remoteDir = remotePath[:idx]
		}
		// Let UploadFile also attempt dir creation; do a lightweight mkdir best-effort (ignore failure)
		mkdirCmd := fmt.Sprintf("cmd.exe /C if not exist \"%s\" mkdir \"%s\"", remoteDir, remoteDir)
		if err := w.sshClient.RunCommand(mkdirCmd); err != nil {
			w.safePrintf("⚠️  Windows mkdir pre-step failed (continuing, UploadFile will retry): %v\n", err)
		}
	} else {
		remoteDir = path.Dir(remotePath)
		mkdirCmd := fmt.Sprintf("mkdir -p '%s'", remoteDir)
		if err := w.sshClient.RunCommand(mkdirCmd); err != nil {
			w.safePrintf("❌ Failed to create remote directory %s: %v\n", remoteDir, err)
			return
		}
	}

	w.safePrintf("🔄 Sync (OS=%s) local='%s' → remote='%s' dir='%s'\n", targetOS, localPath, remotePath, remoteDir)

	if err := w.sshClient.UploadFile(localPath, remotePath); err != nil {
		w.safePrintf("❌ Failed to sync file %s to %s: %v\n", localPath, remotePath, err)
		return
	}

	w.safeStatus("✅ File synced: %s → %s\n", localPath, remotePath)

	if w.fileCache != nil {
		if err := w.fileCache.UpdateFileMetadata(localPath); err != nil {
			w.safePrintf("⚠️  Failed to update cache metadata for %s: %v\n", localPath, err)
		}
	}
}

// mapNotifyEvent maps notify.Event to our EventType
func (w *Watcher) mapNotifyEvent(event notify.Event) EventType {
	switch {
	case event&notify.Create != 0:
		return EventCreate
	case event&notify.Write != 0:
		return EventWrite
	case event&notify.Remove != 0:
		return EventRemove
	case event&notify.Rename != 0:
		return EventRename
	default:
		return EventWrite // Default to write for unknown events
	}
}

func (w *Watcher) shouldIgnore(path string) bool {
	// Core ignores that are ALWAYS applied
	coreIgnores := []string{
		".sync_collections",
		".sync_temp",
		"make-sync.yaml",
		".sync_ignore",
	}

	for _, ignore := range coreIgnores {
		if w.matchesPattern(path, ignore) {
			return true
		}
	}

	if strings.Contains(path, ".sync_temp") {
		return true
	}

	// Extended ignores - loadExtendedIgnores is concurrency-safe and returns a copy
	extended := w.loadExtendedIgnores()
	for _, ignore := range extended {
		if w.matchesPattern(path, ignore) {
			return true
		}
	}

	// Snapshot config for user-configured ignores
	w.configMu.RLock()
	cfg := w.config
	w.configMu.RUnlock()

	if cfg != nil {
		for _, ignore := range cfg.Devsync.Ignores {
			if w.matchesPattern(path, ignore) {
				return true
			}
		}
	}

	return false
}

// filepath: /home/donny/workspaces/make-sync/internal/devsync/watcher.go
func (w *Watcher) ReloadWatchPatterns() error {
	// Build new patterns off-line if needed (not holding locks)
	// Invalidate cached extended ignores under write lock
	w.ignoresMu.Lock()
	w.extendedIgnores = nil
	w.ignoreFileModTime = time.Time{}
	w.ignoresMu.Unlock()

	// Preload extended ignores (will populate cache safely)
	_ = w.loadExtendedIgnores()

	w.safePrintln("✅ Watch patterns reloaded")
	return nil
}

func (w *Watcher) loadExtendedIgnores() []string {
	syncIgnorePath := ".sync_ignore"

	// Fast-path: return cached copy under RLock if file unchanged
	w.ignoresMu.RLock()
	if len(w.extendedIgnores) > 0 {
		cached := make([]string, len(w.extendedIgnores))
		copy(cached, w.extendedIgnores)
		cachedMod := w.ignoreFileModTime
		w.ignoresMu.RUnlock()

		if info, err := os.Stat(syncIgnorePath); err == nil {
			if info.ModTime().Equal(cachedMod) {
				return cached
			}
		} else {
			if cachedMod.IsZero() {
				return cached
			}
		}
	} else {
		w.ignoresMu.RUnlock()
	}

	// Defaults
	defaultExtendedIgnores := []string{
		".git",
		".DS_Store",
		"Thumbs.db",
		"node_modules",
		".vscode",
		"*.log",
		"*.tmp",
		"*.swp",
		"*.bak",
	}

	// Read file (no locks)
	content, err := os.ReadFile(syncIgnorePath)
	if err != nil {
		// cache defaults
		w.ignoresMu.Lock()
		w.extendedIgnores = make([]string, len(defaultExtendedIgnores))
		copy(w.extendedIgnores, defaultExtendedIgnores)
		w.ignoreFileModTime = time.Time{}
		w.ignoresMu.Unlock()
		out := make([]string, len(defaultExtendedIgnores))
		copy(out, defaultExtendedIgnores)
		return out
	}

	info, err := os.Stat(syncIgnorePath)
	if err != nil {
		w.ignoresMu.Lock()
		w.extendedIgnores = make([]string, len(defaultExtendedIgnores))
		copy(w.extendedIgnores, defaultExtendedIgnores)
		w.ignoreFileModTime = time.Time{}
		w.ignoresMu.Unlock()
		out := make([]string, len(defaultExtendedIgnores))
		copy(out, defaultExtendedIgnores)
		return out
	}

	// Try YAML
	var yamlIgnores []string
	if err := yaml.Unmarshal(content, &yamlIgnores); err == nil && len(yamlIgnores) > 0 {
		w.ignoresMu.Lock()
		w.extendedIgnores = make([]string, len(yamlIgnores))
		copy(w.extendedIgnores, yamlIgnores)
		w.ignoreFileModTime = info.ModTime()
		w.ignoresMu.Unlock()

		out := make([]string, len(yamlIgnores))
		copy(out, yamlIgnores)
		w.safeStatus("📄 Loaded .sync_ignore as YAML format (%d patterns)\n", len(yamlIgnores))
		return out
	}

	// Fallback: .gitignore style
	lines := strings.Split(string(content), "\n")
	var gitignoreIgnores []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "- ") {
			continue
		}
		gitignoreIgnores = append(gitignoreIgnores, line)
	}

	if len(gitignoreIgnores) > 0 {
		w.ignoresMu.Lock()
		w.extendedIgnores = make([]string, len(gitignoreIgnores))
		copy(w.extendedIgnores, gitignoreIgnores)
		w.ignoreFileModTime = info.ModTime()
		w.ignoresMu.Unlock()

		out := make([]string, len(gitignoreIgnores))
		copy(out, gitignoreIgnores)
		w.safeStatus("📄 Loaded .sync_ignore as .gitignore format (%d patterns)\n", len(gitignoreIgnores))
		return out
	}

	// Fallback to defaults
	w.safePrintf("⚠️  Failed to parse .sync_ignore file, using defaults\n")
	w.ignoresMu.Lock()
	w.extendedIgnores = make([]string, len(defaultExtendedIgnores))
	copy(w.extendedIgnores, defaultExtendedIgnores)
	w.ignoreFileModTime = info.ModTime()
	w.ignoresMu.Unlock()

	out := make([]string, len(defaultExtendedIgnores))
	copy(out, defaultExtendedIgnores)
	return out
}

// matchesPattern checks if a path matches a pattern (supports wildcards)
func (w *Watcher) matchesPattern(path, pattern string) bool {
	// Simple wildcard support
	if strings.Contains(pattern, "*") {
		// Convert wildcard to regex
		regexPattern := strings.ReplaceAll(pattern, "*", ".*")
		matched, _ := regexp.MatchString(regexPattern, path)
		return matched
	}
	// Simple substring match for non-wildcard patterns
	return strings.Contains(path, pattern)
}

// fileSHA256 computes the SHA256 hex digest of a local file
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// isEventAllowed checks if an event type is allowed by trigger permissions
func (w *Watcher) isEventAllowed(eventType EventType) bool {
	switch eventType {
	case EventCreate:
		return w.config.Devsync.TriggerPerm.Add
	case EventWrite:
		return w.config.Devsync.TriggerPerm.Change
	case EventRemove:
		return w.config.Devsync.TriggerPerm.Unlink
	case EventRename:
		return w.config.Devsync.TriggerPerm.Unlink // For rename, we use unlink permission
	default:
		return false
	}
}

// isDuplicateEvent checks if this event is a duplicate of a recent event

// isDuplicateEvent checks if this event is a duplicate of a recent event
func (w *Watcher) isDuplicateEvent(event FileEvent) bool {
	key := event.Path + string(rune(event.EventType))
	if lastEvent, exists := w.lastEvents[key]; exists {
		// If same event type for same path within 1000ms, consider it duplicate
		if time.Since(lastEvent.Timestamp) < 3*time.Second {
			return true
		}
	}
	return false
}

// storeEvent stores the event for debouncing purposes
func (w *Watcher) storeEvent(event FileEvent) {
	key := event.Path + string(rune(event.EventType))
	w.lastEvents[key] = event

	// Clean up old events (older than 1 second)
	for key, storedEvent := range w.lastEvents {
		if time.Since(storedEvent.Timestamp) > time.Second {
			delete(w.lastEvents, key)
		}
	}
}

// hasActiveSession checks if there are any active sessions
func (w *Watcher) hasActiveSession() bool {
	for _, session := range w.sessions {
		if session.isActive {
			return true
		}
	}
	return false
}

// HandleReloadCommand handles the reload command from user input
func (w *Watcher) HandleReloadCommand() {
	if err := w.ReloadConfiguration(); err != nil {
		w.safePrintf("❌ Failed to reload configuration: %v\n", err)
	}
}

// ReloadConfiguration reloads the configuration
func (w *Watcher) ReloadConfiguration() error {

	util.Default.PrintBlock("🔄 Reloading configuration...", true)

	// Load new config
	newCfg, err := config.LoadAndRenderConfig()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Update watcher's config
	oldConfig := w.config
	w.config = newCfg

	// Sync new config to remote if SSH is configured
	if err := w.syncConfigToRemote(); err != nil {
		// Restore old config on error
		w.config = oldConfig
		return fmt.Errorf("failed to sync config to remote: %w", err)
	}

	if err := w.StopAgentMonitoring(); err != nil {
		// Restore old config on error
		w.config = oldConfig
		return fmt.Errorf("failed to stop agent monitoring: %w", err)
	}

	// Reload watch patterns if watch path changed
	if err := w.ReloadWatchPatterns(); err != nil {
		// Restore old config on error
		w.config = oldConfig
		return fmt.Errorf("failed to reload watch patterns: %w", err)
	}

	w.safePrintln("✅ Configuration reloaded successfully")
	return nil
}

// HandleShowStatsCommand handles the show stats command
func (w *Watcher) HandleShowStatsCommand() {
	statsBlock := `
📊 File Cache Statistics
========================

`
	if w.fileCache == nil {
		statsBlock += "❌ File cache not available"
		util.Default.PrintBlock(statsBlock, true)
		return
	}

	totalFiles, totalSize, err := w.fileCache.GetFileStats()
	if err != nil {
		statsBlock += fmt.Sprintf("❌ Failed to get cache stats: %v", err)
		util.Default.PrintBlock(statsBlock, true)
		return
	}

	statsBlock += fmt.Sprintf(`📁 Total cached files: %d
💾 Total cached size: %.2f MB
🗄️  Cache location: .sync_temp/file_cache.db`,
		totalFiles,
		float64(totalSize)/(1024*1024))

	util.Default.PrintBlock(statsBlock, true)
}

// HandleDeployAgentCommand handles the deploy agent command
func (w *Watcher) HandleDeployAgentCommand() {
	w.commands.HandleDeployAgentCommand()
}

// gracefulShutdown initiates coordinated shutdown of the watcher
func (w *Watcher) gracefulShutdown() {
	w.shutdownMu.Lock()
	defer w.shutdownMu.Unlock()

	w.safePrintln("🔄 Initiating graceful shutdown...")

	// Cancel context to signal all goroutines
	if w.cancelFunc != nil {
		w.cancelFunc()
	}

	// Stop agent monitoring
	if err := w.StopAgentMonitoring(); err != nil {
		w.safePrintf("⚠️  Error stopping agent monitoring: %v\n", err)
	}

	// Stop file watching
	w.StopNotify()

	// Give goroutines a moment to clean up
	time.Sleep(500 * time.Millisecond)

	// Close SSH connections
	if w.sshClient != nil {
		if err := w.sshClient.Close(); err != nil {
			w.safePrintf("⚠️  Error closing SSH client: %v\n", err)
		}
	}

	// Close file cache
	if w.fileCache != nil {
		if err := w.fileCache.Close(); err != nil {
			w.safePrintf("⚠️  Error closing file cache: %v\n", err)
		}
	}

	w.safePrintln("✅ Watcher shutdown complete")

	// Publish shutdown complete event
	events.GlobalBus.Publish(events.EventWatcherStopped, "graceful")

	// Signal done channel
	select {
	case w.done <- true:
	default:
		// Channel already has value or is closed
	}
}
