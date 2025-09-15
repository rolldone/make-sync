package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/rjeczalik/notify"
)

// AgentConfig represents the agent configuration
type AgentConfig struct {
	Devsync struct {
		AgentWatchs []string `json:"agent_watchs"`
		WorkingDir  string   `json:"working_dir"`
	} `json:"devsync"`
}

// loadConfig loads configuration from .sync_temp/config.json
func loadConfig() (*AgentConfig, error) {
	// Look for config file in .sync_temp directory
	configPath := ".sync_temp/config.json"
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("config file .sync_temp/config.json not found")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %v", err)
	}

	var config AgentConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %v", err)
	}

	return &config, nil
}

func displayConfig() {
	// Load configuration
	config, err := loadConfig()
	if err != nil {
		fmt.Printf("❌ Failed to load config: %v\n", err)
		os.Exit(1)
	}

	// Display configuration in JSON format
	fmt.Println("📋 Current Agent Configuration:")
	fmt.Printf("👀 Watch Paths: %v\n", config.Devsync.AgentWatchs)
	fmt.Printf("� Working Directory: %s\n", config.Devsync.WorkingDir)

	// Display current working directory
	if cwd, err := os.Getwd(); err == nil {
		fmt.Printf("📍 Current Working Directory: %s\n", cwd)
	}

	// Display config file location
	fmt.Printf("📄 Config File: .sync_temp/config.json\n")

	// Display raw config file content
	fmt.Println("\n📄 Raw Config Content:")
	data, err := os.ReadFile(".sync_temp/config.json")
	if err != nil {
		fmt.Printf("❌ Failed to read config file: %v\n", err)
	} else {
		fmt.Println(string(data))
	}
}

func main() {
	// Check for command line arguments
	if len(os.Args) > 1 {
		command := os.Args[1]
		switch command {
		case "identity":
			printIdentity()
			return
		case "version":
			fmt.Println("Sync Agent v1.0.0")
			return
		case "config":
			displayConfig()
			return
		case "watch":
			startWatching()
			return
		}
	}

	fmt.Println("🚀 Sync Agent Started")
	fmt.Printf("📅 Started at: %s\n", time.Now().Format("2006-01-02 15:04:05"))

	startWatching()
}

func startWatching() {
	// Get current working directory for debugging
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Printf("⚠️  Failed to get current working directory: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("📍 Current working directory: %s\n", cwd)
	// Load configuration
	config, err := loadConfig()
	if err != nil {
		fmt.Printf("⚠️  Failed to load config: %v\n", err)
		fmt.Println("🔄 Falling back to watching current directory")
		// Fallback to current directory if config not found
		// watchPaths := []string{"."}
		// setupWatcher(watchPaths)
		fmt.Println("❌ Exiting due to configuration error")
		os.Exit(1)
		return
	}

	// (redundant error check removed — `err` already handled after loadConfig)
	// Change to working directory if specified
	workingDir := cwd
	fmt.Printf("🔧 DEBUG: workingDir = '%s'\n", workingDir)
	fmt.Printf("🔧 DEBUG: len(AgentWatchs) = %d\n", len(config.Devsync.AgentWatchs))
	currentDir := "."

	if err := os.Chdir(workingDir); err != nil {
		fmt.Printf("⚠️  Failed to change to working directory: %v\n", err)
		fmt.Println("🔄 Continuing with current directory")
		currentDir = workingDir // Still use working dir for path resolution
	} else {
		fmt.Printf("✅ Successfully changed to working directory: %s\n", workingDir)
		currentDir = workingDir
	}

	// Resolve watch paths relative to working directory
	fmt.Printf("🔧 Working dir: '%s'\n", workingDir)
	fmt.Printf("🔧 Raw watch paths: %v\n", config.Devsync.AgentWatchs)

	watchPaths := make([]string, len(config.Devsync.AgentWatchs))
	for i, watchPath := range config.Devsync.AgentWatchs {
		fmt.Printf("🔍 Processing watch path: '%s'\n", watchPath)
		if workingDir != "" && !filepath.IsAbs(watchPath) {
			// Combine working directory with relative watch path
			resolvedPath := filepath.Join(workingDir, watchPath)
			watchPaths[i] = resolvedPath
			fmt.Printf("🔗 Resolved watch path: %s -> %s\n", watchPath, resolvedPath)
		} else {
			watchPaths[i] = watchPath
			fmt.Printf("📁 Using watch path as-is: %s\n", watchPath)
		}
	}

	fmt.Printf("📋 Final watch paths: %v\n", watchPaths)

	if len(watchPaths) == 0 {
		fmt.Println("⚠️  No agent_watchs configured, watching current directory")
		watchPaths = []string{currentDir}
	}

	fmt.Printf("📋 Loaded config with %d watch paths\n", len(watchPaths))
	setupWatcher(watchPaths)
}

func setupWatcher(watchPaths []string) {
	// Create .sync_temp directory if it doesn't exist
	syncTempDir := ".sync_temp"
	if err := os.MkdirAll(syncTempDir, 0755); err != nil {
		fmt.Printf("❌ Failed to create .sync_temp directory: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("👀 Watching directories: %v\n", watchPaths)
	fmt.Printf("📁 Agent location: %s\n", syncTempDir)

	// Try notify-based watching first
	if tryNotifyWatcher(watchPaths) {
		return // Success with notify
	}

	// Fallback to polling-based watching
	fmt.Println("🔄 Falling back to polling-based file watching...")
	// pollingWatcher(watchPaths)
}

func tryNotifyWatcher(watchPaths []string) bool {
	fmt.Println("🔍 Testing notify-based file watching...")

	// Create a channel to receive events
	c := make(chan notify.EventInfo, 10)

	// Register each watch path. Use recursive pattern by appending "..." when supported.
	for _, p := range watchPaths {
		// Ensure path exists
		if _, err := os.Stat(p); os.IsNotExist(err) {
			fmt.Printf("⚠️  Watch path does not exist: %s\n", p)
			// Unregister any watches we may have created
			notify.Stop(c)
			return false
		}

		// For recursive watching, notify package uses "..." suffix on the path
		pattern := filepath.Join(p, "...")
		fmt.Printf("📋 Registering watch: %s\n", pattern)
		if err := notify.Watch(pattern, c, notify.All); err != nil {
			fmt.Printf("❌ Failed to register watch for %s: %v\n", p, err)
			notify.Stop(c)
			return false
		}
	}

	// Start a goroutine to handle events from the channel
	go func() {
		for e := range c {
			// Some events may be nil if the channel is closed; guard against it
			// fmt.Println("🔔 DEBUG: Received event:", e) // Debug line to trace events
			if e == nil {
				continue
			}
			handleFileEvent(e)
		}
	}()

	fmt.Println("✅ Notify-based file watching is ready — listening for events...")

	// Block here; notify events will be delivered to the goroutine above.
	// Return true so caller doesn't fall back to polling.
	select {}
}

// func pollingWatcher(watchPaths []string) {
// 	fmt.Println("✅ Agent ready and watching for file changes (polling)...")
// 	fmt.Printf("🎯 Total watch paths: %d\n", len(watchPaths))
// 	fmt.Println("💡 Press Ctrl+C to stop")

// 	// Create context for cancellation
// 	ctx, cancel := context.WithCancel(context.Background())
// 	defer cancel()

// 	// Simple polling implementation
// 	ticker := time.NewTicker(5 * time.Second) // Check every 1 second for testing
// 	defer ticker.Stop()

// 	fileHashes := make(map[string]string)

// 	// Initial scan
// 	for _, watchPath := range watchPaths {
// 		checkDirectoryChanges(watchPath, fileHashes)
// 	}

// 	// Continuous polling with context
// 	for {
// 		select {
// 		case <-ctx.Done():
// 			return
// 		case <-ticker.C:
// 			for _, watchPath := range watchPaths {
// 				checkDirectoryChanges(watchPath, fileHashes)
// 			}
// 		}
// 	}
// }

// func checkDirectoryChanges(dirPath string, fileHashes map[string]string) {
// 	fmt.Println("Checking directory:", dirPath)
// 	filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
// 		if err != nil {
// 			return nil
// 		}

// 		// Skip directories and hidden files
// 		if info.IsDir() || strings.HasPrefix(info.Name(), ".") {
// 			return nil
// 		}
// 		// Calculate current hash
// 		currentHash, err := calculateFileHash(path)
// 		if err != nil {
// 			return nil
// 		}

// 		// Check if file changed
// 		lastHash, exists := fileHashes[path]
// 		if !exists {
// 			// New file
// 			timestamp := time.Now().Format("2006-01-02 15:04:05")
// 			fmt.Printf("[%s] EVENT|Create|%s\n", timestamp, path)
// 			fmt.Printf("[%s] HASH|%s|%s\n", timestamp, path, currentHash)
// 			fileHashes[path] = currentHash
// 		} else if lastHash != currentHash {
// 			// Modified file
// 			timestamp := time.Now().Format("2006-01-02 15:04:05")
// 			fmt.Printf("[%s] EVENT|Write|%s\n", timestamp, path)
// 			fmt.Printf("[%s] HASH|%s|%s\n", timestamp, path, currentHash)
// 			fileHashes[path] = currentHash
// 		}

// 		return nil
// 	})
// }

func handleFileEvent(event notify.EventInfo) {
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	// Format output for easy parsing by make-sync
	fmt.Printf("[%s] EVENT|%s|%s\n", timestamp, event.Event().String(), event.Path())

	// Calculate file hash using xxHash (only for files that exist)
	if info, err := os.Stat(event.Path()); err == nil && !info.IsDir() {
		if hash, err := calculateFileHash(event.Path()); err == nil {
			fmt.Printf("[%s] HASH|%s|%s\n", timestamp, event.Path(), hash)
		} else {
			fmt.Printf("[%s] ERROR|hash_failed|%s|%v\n", timestamp, event.Path(), err)
		}
	} else if err != nil {
		fmt.Printf("[%s] ERROR|stat_failed|%s|%v\n", timestamp, event.Path(), err)
	}

	// Flush output immediately
	os.Stdout.Sync()
}

func printIdentity() {
	// Get the current executable path
	execPath, err := os.Executable()
	if err != nil {
		fmt.Printf("Error getting executable path: %v\n", err)
		os.Exit(1)
	}

	// Calculate hash of the agent binary itself
	hash, err := calculateFileHash(execPath)
	if err != nil {
		fmt.Printf("Error calculating identity hash: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("%s\n", hash)
}

func calculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := xxhash.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}
