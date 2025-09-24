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
	// Load configuration. If not present, don't exit — fall back to polling mode
	config, err := loadConfig()
	if err != nil {
		fmt.Printf("⚠️  Failed to load config: %v\n", err)
		fmt.Println("🔄 Falling back to polling for config in .sync_temp/config.json (agent will stay running)")
		// Initialize empty config so later logic will poll and wait for config to appear
		config = &AgentConfig{}
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
	// Load configuration. If missing, do not exit — fall back to polling for config
	config, err := loadConfig()
	if err != nil {
		fmt.Printf("⚠️  Failed to load config: %v\n", err)
		fmt.Println("🔄 Falling back to watching current directory and polling for .sync_temp/config.json")
		// Initialize an empty config so we continue and poll for configuration
		config = &AgentConfig{}
	}

	// (redundant error check removed — `err` already handled after loadConfig)
	// Change to working directory if specified
	workingDir := cwd
	fmt.Printf("🔧 DEBUG: workingDir = '%s'\n", workingDir)
	fmt.Printf("🔧 DEBUG: len(AgentWatchs) = %d\n", len(config.Devsync.AgentWatchs))
	if err := os.Chdir(workingDir); err != nil {
		fmt.Printf("⚠️  Failed to change to working directory: %v\n", err)
		fmt.Println("🔄 Continuing with current directory")
	} else {
		fmt.Printf("✅ Successfully changed to working directory: %s\n", workingDir)
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
		fmt.Println("⚠️  No agent_watchs configured — agent will remain running and poll for config changes")
		// Keep agent running and poll .sync_temp/config.json until watch paths are provided
		go func() {
			for {
				time.Sleep(5 * time.Second)
				cfg, err := loadConfig()
				if err != nil {
					// still no config, continue polling
					fmt.Printf("🔍 Polling for config: %v\n", err)
					continue
				}
				if cfg != nil && len(cfg.Devsync.AgentWatchs) > 0 {
					// Resolve newly discovered watch paths relative to workingDir
					newPaths := make([]string, len(cfg.Devsync.AgentWatchs))
					for i, wp := range cfg.Devsync.AgentWatchs {
						if workingDir != "" && !filepath.IsAbs(wp) {
							newPaths[i] = filepath.Join(workingDir, wp)
						} else {
							newPaths[i] = wp
						}
					}
					fmt.Printf("✅ Detected new watch paths: %v — starting watcher\n", newPaths)
					setupWatcher(newPaths)
					return
				}
			}
		}()

		// Block main goroutine so agent stays alive even when not watching
		select {}
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
	fmt.Println("🔍 Starting notify-based file watching (async, will retry missing paths)...")

	// Channel to receive events from notify
	c := make(chan notify.EventInfo, 100)

	// Start event handler goroutine immediately (it will block until events arrive)
	go func() {
		for e := range c {
			if e == nil {
				continue
			}
			handleFileEvent(e)
		}
	}()

	// Registration goroutine: try to register each path independently
	go func() {
		// Track which paths have been successfully registered
		registered := make([]bool, len(watchPaths))

		for {
			allRegistered := true

			for i, p := range watchPaths {
				if registered[i] {
					continue // already registered
				}

				// Check path existence; if missing, we'll retry later but continue
				if _, err := os.Stat(p); os.IsNotExist(err) {
					fmt.Printf("⚠️  Watch path does not exist yet: %s\n", p)
					allRegistered = false
					continue
				}

				// Attempt to register this individual path
				pattern := filepath.Join(p, "...")
				fmt.Printf("📋 Registering watch: %s\n", pattern)
				if err := notify.Watch(pattern, c, notify.All); err != nil {
					// Log error and try again later for this path; do not stop other registrations
					fmt.Printf("❌ Failed to register watch for %s: %v\n", p, err)
					allRegistered = false
					continue
				}

				// Mark as registered
				fmt.Printf("✅ Registered watch for: %s\n", p)
				registered[i] = true
			}

			// Check if everything is registered; if not, sleep then retry only unregistered ones
			for _, v := range registered {
				if !v {
					allRegistered = false
					break
				}
			}

			if allRegistered {
				fmt.Println("✅ All notify-based watches registered and active")
				return
			}

			// Wait before next retry iteration for unregistered paths
			fmt.Println("⏳ Some watches not ready yet, retrying in 5s...")
			time.Sleep(5 * time.Second)
		}
	}()

	// Registration goroutine started and event handler running.
	// Block here so the agent process stays alive (as previous behavior) —
	// registration still runs asynchronously in background.
	fmt.Println("⏳ Waiting for events (agent will keep running)...")
	select {}
}

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
