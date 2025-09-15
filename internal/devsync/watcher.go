package devsync

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rjeczalik/notify"
	"gopkg.in/yaml.v3"
)

// Start begins watching files in the configured directory
func (w *Watcher) Start() error {
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

	fmt.Printf("🔍 Starting file watcher on: %s\n", absWatchPath)
	fmt.Printf("📋 Watch permissions - Add: %v, Change: %v, Unlink: %v, UnlinkFolder: %v\n",
		w.config.TriggerPerm.Add,
		w.config.TriggerPerm.Change,
		w.config.TriggerPerm.Unlink,
		w.config.TriggerPerm.UnlinkFolder)

	// Setup recursive watching
	watchPattern := filepath.Join(absWatchPath, "...")
	err = notify.Watch(watchPattern, w.watchChan, notify.All)
	if err != nil {
		return fmt.Errorf("failed to setup file watcher: %v", err)
	}

	// Start keyboard input handler goroutine
	go w.handleKeyboardInput()

	// Start event processing goroutine
	go w.processEvents()

	fmt.Printf("✅ File watcher started successfully\n")
	fmt.Printf("💡 Press Ctrl+C to stop watching, R+Enter to reload .sync_ignore, S+Enter to show cache stats, A+Enter to deploy agent\n\n")

	// Wait for done signal
	<-w.done

	return nil
}

// Stop stops the file watcher
func (w *Watcher) Stop() {
	// Close file cache if exists
	if w.fileCache != nil {
		if totalFiles, totalSize, err := w.fileCache.GetFileStats(); err == nil {
			fmt.Printf("💾 Cache stats: %d files, %.2f MB\n", totalFiles, float64(totalSize)/(1024*1024))
		}

		if err := w.fileCache.Close(); err != nil {
			fmt.Printf("⚠️  Failed to close file cache: %v\n", err)
		} else {
			fmt.Printf("💾 File cache closed\n")
		}
	}

	// Close SSH connection if exists
	if w.sshClient != nil {
		if err := w.sshClient.Close(); err != nil {
			fmt.Printf("⚠️  Failed to close SSH connection: %v\n", err)
		} else {
			fmt.Printf("🔌 SSH connection closed\n")
		}
	}

	select {
	case w.done <- true:
	default:
		// Channel already has value or is closed
	}
}

// processEvents processes file system events
func (w *Watcher) processEvents() {
	for {
		select {
		case event := <-w.watchChan:
			w.handleEvent(event)
		case <-w.done:
			return
		}
	}
}

// handleEvent processes a single file system event
func (w *Watcher) handleEvent(event notify.EventInfo) {
	path := event.Path()

	// Check if this is the .sync_ignore file being modified
	if filepath.Base(path) == ".sync_ignore" {
		// Clear the cache so it will be reloaded on next shouldIgnore call
		w.extendedIgnores = nil
		w.ignoreFileModTime = time.Time{}
	}

	// Check if path should be ignored
	if w.shouldIgnore(path) {
		return
	}

	// Map notify event to our EventType
	eventType := w.mapNotifyEvent(event.Event())

	// Check if this event type is allowed by permissions
	if !w.isEventAllowed(eventType) {
		return
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

	// Handle rename events (they come as two separate events)
	if eventType == EventRename {
		// For rename, we might need to track old path
		// This is simplified - in production you might want to track rename pairs
	}

	// Display the event
	w.displayEvent(fileEvent)

	// Execute scripts if configured
	w.executeScripts(fileEvent)
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

// shouldIgnore checks if a path should be ignored based on ignore patterns
func (w *Watcher) shouldIgnore(path string) bool {
	// Core ignores that are ALWAYS applied (cannot be overridden)
	coreIgnores := []string{
		".sync_collections", // Sync collections folder
		".sync_temp",        // Temporary files folder
		"make-sync.yaml",    // Main config file
		".sync_ignore",      // Ignore configuration file
	}

	// Check core ignores first (these cannot be overridden)
	for _, ignore := range coreIgnores {
		if w.matchesPattern(path, ignore) {
			return true
		}
	}

	// Additional check for .sync_temp folder (failsafe)
	if strings.Contains(path, ".sync_temp") {
		return true
	}

	// Extended ignores from .sync_ignore file
	extendedIgnores := w.loadExtendedIgnores()
	for _, ignore := range extendedIgnores {
		if w.matchesPattern(path, ignore) {
			return true
		}
	}

	// Check user-configured ignores from YAML
	for _, ignore := range w.config.Devsync.Ignores {
		if w.matchesPattern(path, ignore) {
			return true
		}
	}
	return false
}

// loadExtendedIgnores loads ignore patterns from .sync_ignore file with caching
func (w *Watcher) loadExtendedIgnores() []string {
	// Check if we have cached patterns and if .sync_ignore file hasn't changed
	syncIgnorePath := ".sync_ignore"
	if len(w.extendedIgnores) > 0 {
		if info, err := os.Stat(syncIgnorePath); err == nil {
			if info.ModTime().Equal(w.ignoreFileModTime) {
				// File hasn't changed, return cached patterns
				return w.extendedIgnores
			}
		}
	}

	// Default extended ignores
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

	// Try to read .sync_ignore file
	content, err := os.ReadFile(syncIgnorePath)
	if err != nil {
		// File doesn't exist, cache defaults
		w.extendedIgnores = defaultExtendedIgnores
		w.ignoreFileModTime = time.Time{} // Reset mod time
		return defaultExtendedIgnores
	}

	// Get file modification time
	info, err := os.Stat(syncIgnorePath)
	if err != nil {
		w.extendedIgnores = defaultExtendedIgnores
		w.ignoreFileModTime = time.Time{}
		return defaultExtendedIgnores
	}

	// Try to parse as YAML first
	var yamlIgnores []string
	if err := yaml.Unmarshal(content, &yamlIgnores); err == nil && len(yamlIgnores) > 0 {
		fmt.Printf("📄 Loaded .sync_ignore as YAML format (%d patterns)\n", len(yamlIgnores))
		w.extendedIgnores = yamlIgnores
		w.ignoreFileModTime = info.ModTime()
		return yamlIgnores
	}

	// If YAML parsing fails, try to parse as .gitignore style (plain text)
	lines := strings.Split(string(content), "\n")
	var gitignoreIgnores []string

	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Skip lines that start with YAML dash (old format)
		if strings.HasPrefix(line, "- ") {
			continue
		}
		gitignoreIgnores = append(gitignoreIgnores, line)
	}

	if len(gitignoreIgnores) > 0 {
		fmt.Printf("📄 Loaded .sync_ignore as .gitignore format (%d patterns)\n", len(gitignoreIgnores))
		w.extendedIgnores = gitignoreIgnores
		w.ignoreFileModTime = info.ModTime()
		return gitignoreIgnores
	}

	// If both parsing methods fail, fall back to default
	fmt.Printf("⚠️  Failed to parse .sync_ignore file, using defaults\n")
	w.extendedIgnores = defaultExtendedIgnores
	w.ignoreFileModTime = info.ModTime()
	return defaultExtendedIgnores
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

// isEventAllowed checks if an event type is allowed by trigger permissions
func (w *Watcher) isEventAllowed(eventType EventType) bool {
	switch eventType {
	case EventCreate:
		return w.config.TriggerPerm.Add
	case EventWrite:
		return w.config.TriggerPerm.Change
	case EventRemove:
		return w.config.TriggerPerm.Unlink
	case EventRename:
		return w.config.TriggerPerm.Unlink // For rename, we use unlink permission
	default:
		return false
	}
}

// displayEvent displays a file event to the console
func (w *Watcher) displayEvent(event FileEvent) {
	timestamp := time.Now().Format("15:04:05")
	relativePath := w.getRelativePath(event.Path)

	var emoji, action string
	switch event.EventType {
	case EventCreate:
		emoji = "🆕"
		action = "Created"
	case EventWrite:
		emoji = "📝"
		action = "Modified"
	case EventRemove:
		emoji = "🗑️"
		action = "Deleted"
	case EventRename:
		emoji = "📋"
		action = "Renamed"
	}

	if event.IsDir {
		fmt.Printf("%s %s %s [DIR] %s\n", timestamp, emoji, action, relativePath)
	} else {
		fmt.Printf("%s %s %s %s\n", timestamp, emoji, action, relativePath)
	}
}

// getRelativePath gets the relative path from watch directory
func (w *Watcher) getRelativePath(absPath string) string {
	// For remote sync, we want just the filename to sync to remote root
	// But for cache lookup, we need the relative path from watch directory
	return filepath.Base(absPath)
}

// executeScripts executes configured scripts for file events
func (w *Watcher) executeScripts(event FileEvent) {
	// Execute local commands if configured
	for _, cmd := range w.config.Devsync.Script.Local.Commands {
		if cmd != "" {
			fmt.Printf("🔧 Executing local: %s\n", cmd)
			// TODO: Implement actual command execution
		}
	}

	// Handle SSH sync if SSH client is available and event is not delete
	if w.sshClient != nil && event.EventType != EventRemove {
		w.syncFileViaSSH(event)
	}

	// Execute remote commands if configured
	for _, cmd := range w.config.Devsync.Script.Remote.Commands {
		if cmd != "" && w.sshClient != nil {
			fmt.Printf("🔧 Executing remote: %s\n", cmd)
			if err := w.sshClient.RunCommand(cmd); err != nil {
				fmt.Printf("❌ Failed to execute remote command: %v\n", err)
			}
		}
	}
}

// isDuplicateEvent checks if this event is a duplicate of a recent event
func (w *Watcher) isDuplicateEvent(event FileEvent) bool {
	key := event.Path + string(rune(event.EventType))
	if lastEvent, exists := w.lastEvents[key]; exists {
		// If same event type for same path within 100ms, consider it duplicate
		if time.Since(lastEvent.Timestamp) < 100*time.Millisecond {
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

// syncFileViaSSH syncs a file to remote server via SSH
func (w *Watcher) syncFileViaSSH(event FileEvent) {
	if w.sshClient == nil {
		return
	}

	// Skip directories for now
	if event.IsDir {
		return
	}

	fmt.Println("event.Path:", event.Path)
	// FINAL FAILSAFE: Never sync files in .sync_temp folder
	if strings.Contains(event.Path, ".sync_temp") {
		fmt.Printf("🚫 BLOCKED: File in .sync_temp folder: %s\n", event.Path)
		return
	}

	// Get relative path from watch directory
	relativePath := w.getRelativePath(event.Path)

	// Skip if file is outside watch directory
	if relativePath == "" {
		fmt.Printf("⚠️  Skipping file outside watch directory: %s\n", event.Path)
		return
	}

	// Check file cache to see if file has changed
	fmt.Println("w.fileCache:", w.fileCache)

	time.Sleep(300 * time.Millisecond)

	if w.fileCache != nil {
		shouldSync, err := w.fileCache.ShouldSyncFile(event.Path)
		if err != nil {
			fmt.Printf("⚠️  Failed to check file cache for %s: %v\n", relativePath, err)
			// Continue with sync on cache error
		} else if !shouldSync {
			fmt.Printf("⏭️  Skipping unchanged file: %s\n", relativePath)
			return
		}
	}

	// Construct remote path - ensure it's relative to remote working directory
	remoteBase := w.config.Devsync.Auth.RemotePath
	if remoteBase == "" {
		remoteBase = "."
	}

	// Join remote base with relative path
	remotePath := filepath.Join(remoteBase, relativePath)

	// Ensure the remote path uses forward slashes for remote server
	remotePath = filepath.ToSlash(remotePath)

	fmt.Printf("📤 Syncing file: %s -> %s\n", event.Path, remotePath)
	fmt.Printf("   Relative path: %s\n", relativePath)
	fmt.Printf("   Remote base: %s\n", remoteBase)

	// Upload file via SSH
	if err := w.sshClient.SyncFile(event.Path, remotePath); err != nil {
		fmt.Printf("❌ Faiqqqqled to sync file %s: %v\n", relativePath, err)
	} else {
		fmt.Printf("✅ Successfully synced: %s\n", relativePath)

		// Update file cache after successful sync
		if w.fileCache != nil {
			if err := w.fileCache.UpdateFileMetadata(event.Path); err != nil {
				fmt.Printf("⚠️  Failed to update file cache for %s: %v\n", relativePath, err)
			}
		}
	}
}

// handleKeyboardInput handles keyboard input for manual commands
func (w *Watcher) handleKeyboardInput() {
	buffer := make([]byte, 1)
	for {
		n, err := os.Stdin.Read(buffer)
		if err != nil {
			break
		}
		if n > 0 {
			input := string(buffer[:n])
			// Check for R key (reload command)
			if input == "R" || input == "r" {
				w.handleReloadCommand()
			}
			// Check for S key (show stats command)
			if input == "S" || input == "s" {
				w.handleShowStatsCommand()
			}
			// Check for A key (deploy agent command)
			if input == "A" || input == "a" {
				w.handleDeployAgentCommand()
			}
		}
	}
}

// handleReloadCommand handles the R reload command
func (w *Watcher) handleReloadCommand() {
	fmt.Printf("\n🔄 Manual reload requested (R key)\n")
	fmt.Printf("=================================\n")

	// Validate .sync_ignore file first
	if err := w.validateSyncIgnore(); err != nil {
		fmt.Printf("❌ .sync_ignore validation failed: %v\n", err)
		fmt.Printf("💡 Please fix the .sync_ignore file and try again with R\n")
		return
	}

	// If validation passes, reload the watch patterns
	w.reloadWatchPatterns()
	fmt.Printf("✅ .sync_ignore reloaded successfully\n")
}

// reloadWatchPatterns reloads the ignore patterns by clearing cache and reloading
func (w *Watcher) reloadWatchPatterns() {
	// Clear the cached ignore patterns and modification time
	w.extendedIgnores = nil
	w.ignoreFileModTime = time.Time{}

	// Reload the patterns
	_ = w.loadExtendedIgnores()

	fmt.Printf("🔄 Ignore patterns reloaded from .sync_ignore\n")
}

// validateSyncIgnore validates the .sync_ignore file format
func (w *Watcher) validateSyncIgnore() error {
	syncIgnorePath := ".sync_ignore"

	// Check if file exists
	if _, err := os.Stat(syncIgnorePath); os.IsNotExist(err) {
		return fmt.Errorf("file does not exist")
	}

	// Read file content
	content, err := os.ReadFile(syncIgnorePath)
	if err != nil {
		return fmt.Errorf("cannot read file: %v", err)
	}

	// Try to parse as YAML first
	var yamlIgnores []string
	if err := yaml.Unmarshal(content, &yamlIgnores); err == nil && len(yamlIgnores) > 0 {
		// Valid YAML format
		return nil
	}

	// Try to parse as .gitignore style (plain text)
	lines := strings.Split(string(content), "\n")
	validPatterns := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		// Skip empty lines and comments
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Skip lines that start with YAML dash (old format)
		if strings.HasPrefix(line, "- ") {
			continue
		}

		// Basic validation for pattern format
		if len(line) > 0 {
			validPatterns++
		}
	}

	if validPatterns == 0 {
		return fmt.Errorf("no valid patterns found in file")
	}

	// Valid .gitignore format
	return nil
}

// handleShowStatsCommand handles the S show stats command
func (w *Watcher) handleShowStatsCommand() {
	fmt.Printf("\n📊 Cache Statistics\n")
	fmt.Printf("==================\n")

	if w.fileCache == nil {
		fmt.Printf("❌ File cache not initialized\n")
		return
	}

	totalFiles, totalSize, err := w.fileCache.GetFileStats()
	if err != nil {
		fmt.Printf("❌ Failed to get cache stats: %v\n", err)
		return
	}

	fmt.Printf("📁 Total cached files: %d\n", totalFiles)
	fmt.Printf("💾 Total cached size: %.2f MB\n", float64(totalSize)/(1024*1024))
	fmt.Printf("📂 Cache location: .sync_temp/file_cache.db\n")
}

// handleDeployAgentCommand handles the A deploy agent command
func (w *Watcher) handleDeployAgentCommand() {
	fmt.Printf("\n🚀 Deploy Agent Command\n")
	fmt.Printf("======================\n")

	if w.sshClient == nil {
		fmt.Printf("❌ SSH client not available\n")
		fmt.Printf("💡 Make sure SSH configuration is properly set up\n")
		return
	}

	fmt.Printf("🔨 Building and deploying sync agent...\n")

	// Build and deploy agent
	if err := w.buildAndDeployAgent(); err != nil {
		fmt.Printf("❌ Failed to build/deploy agent: %v\n", err)
		return
	}

	fmt.Printf("✅ Agent deployed successfully!\n")
	fmt.Printf("💡 Agent is now available at: ~/.sync_temp/sync-agent\n")

	// Additional verification - show agent info
	if output, err := w.sshClient.RunCommandWithOutput("file .sync_temp/sync-agent"); err == nil {
		fmt.Printf("📋 Agent info: %s", strings.TrimSpace(output))
	}
}
