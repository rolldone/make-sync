package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/cespare/xxhash/v2"
	"github.com/rjeczalik/notify"

	"runtime"
	"sync-agent/internal/indexer"
)

// AgentConfig represents the agent configuration
type AgentConfig struct {
	Devsync struct {
		AgentWatchs []string `json:"agent_watchs"`
		WorkingDir  string   `json:"working_dir"`
	} `json:"devsync"`
}

// Global context for coordinating graceful shutdown
var (
	mainCtx    context.Context
	mainCancel context.CancelFunc
	shutdownMu sync.Mutex
)

// loadConfig loads configuration from .sync_temp/config.json in current dir or executable dir
func loadConfig() (*AgentConfig, error) {
	exePath, err := os.Executable()
	if err == nil {
		exeDir := filepath.Dir(exePath)
		exeBase := filepath.Base(exeDir)
		var configPaths []string
		if exeBase == ".sync_temp" {
			// If agent is in .sync_temp, look for config.json in same dir
			configPaths = append(configPaths, filepath.Join(exeDir, "config.json"))
		} else {
			// Otherwise, look for .sync_temp/config.json in exeDir
			configPaths = append(configPaths, filepath.Join(exeDir, ".sync_temp", "config.json"))
		}
		// Also try current working directory for legacy support
		configPaths = append(configPaths, ".sync_temp/config.json")

		for _, configPath := range configPaths {
			fmt.Printf("🔍 Trying config: %s\n", configPath)
			if _, err := os.Stat(configPath); err == nil {
				data, err := os.ReadFile(configPath)
				if err != nil {
					return nil, fmt.Errorf("failed to read config file: %v", err)
				}
				var config AgentConfig
				if err := json.Unmarshal(data, &config); err != nil {
					return nil, fmt.Errorf("failed to parse config file: %v", err)
				}
				fmt.Printf("✅ Loaded config from: %s\n", configPath)
				return &config, nil
			}
		}
	}

	return nil, fmt.Errorf("config file .sync_temp/config.json not found in .sync_temp, executable dir, or current dir")
}

// loadConfigAndChangeDir loads config and changes working directory if specified
func loadConfigAndChangeDir() error {
	// Load configuration - REQUIRED, no fallback
	config, err := loadConfig()
	if err != nil {
		return fmt.Errorf("config is required for indexing: %v", err)
	}

	// Working directory is required from config
	if config.Devsync.WorkingDir == "" {
		return fmt.Errorf("working_dir must be specified in config for indexing")
	}

	workingDir := config.Devsync.WorkingDir
	fmt.Printf("🔧 Using working directory from config: %s\n", workingDir)
	fmt.Printf("🔧 DEBUG: workingDir = '%s'\n", workingDir)

	if err := os.Chdir(workingDir); err != nil {
		return fmt.Errorf("failed to change to working directory '%s': %v", workingDir, err)
	}

	// Print actual working directory after chdir
	cwd, err := os.Getwd()
	if err == nil {
		fmt.Printf("📍 Current working directory: %s\n", cwd)
	}
	fmt.Printf("✅ Successfully changed to working directory: %s\n", workingDir)
	return nil
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
	// Setup context for coordinated shutdown
	mainCtx, mainCancel = context.WithCancel(context.Background())
	defer mainCancel()

	// Setup signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		sig := <-sigChan
		fmt.Printf("🔔 Received signal: %v, initiating shutdown...\n", sig)
		gracefulShutdown()
	}()

	// On Windows, start parent watcher to auto-exit if parent dies
	if runtime.GOOS == "windows" {
		startParentWatcher()
	}

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
		case "indexing":
			// perform indexing now and write .sync_temp/indexing_files.db
			// Load config and change working directory first, then perform indexing
			if err := loadConfigAndChangeDir(); err != nil {
				fmt.Printf("⚠️  Config setup failed: %v\n", err)
				fmt.Println("❌ Cannot proceed with indexing without proper config. Exiting.")
				os.Exit(1)
			}
			performIndexing()
			return
		}
	}

	fmt.Println("🚀 Sync Agent Started")
	fmt.Printf("📅 Started at: %s\n", time.Now().Format("2006-01-02 15:04:05"))

	startWatching()
}

// gracefulShutdown initiates coordinated shutdown
func gracefulShutdown() {
	shutdownMu.Lock()
	defer shutdownMu.Unlock()

	fmt.Println("🔄 Initiating graceful shutdown...")
	mainCancel() // Cancel main context to signal all goroutines

	// Give goroutines a moment to clean up
	time.Sleep(100 * time.Millisecond)

	fmt.Println("✅ Agent shutdown complete")
	os.Exit(0)
}

func startWatching() {
	// Load config and change working directory
	if err := loadConfigAndChangeDir(); err != nil {
		fmt.Printf("⚠️  Config setup failed: %v\n", err)
		os.Exit(1)
	}

	// Load configuration again for watch paths (after chdir)
	config, err := loadConfig()
	if err != nil {
		fmt.Printf("⚠️  Failed to load config: %v\n", err)
		fmt.Println("🔄 Falling back to watching current directory and polling for .sync_temp/config.json")
		// Initialize an empty config so we continue and poll for configuration
		config = &AgentConfig{}
	}

	// Get current working directory after config loading
	workingDir, err := os.Getwd()
	if err != nil {
		fmt.Printf("⚠️  Failed to get current working directory: %v\n", err)
		workingDir = ""
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
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()

			for {
				select {
				case <-mainCtx.Done():
					fmt.Println("🔄 Config polling stopped (context cancelled)")
					return
				case <-ticker.C:
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
			}
		}()

		// Block main goroutine but watch for context cancellation
		<-mainCtx.Done()
		fmt.Println("⏹️  Agent shutting down (no watch paths configured)")
		return
	}

	fmt.Printf("📋 Loaded config with %d watch paths\n", len(watchPaths))
	setupWatcher(watchPaths)
}

func performIndexing() {
	// Use current working dir as root for indexing
	root, err := os.Getwd()
	if err != nil {
		fmt.Printf("❌ Failed to get working dir: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("🔍 Building index for: %s\n", root)

	// Decide where to place .sync_temp/indexing_files.db.
	// If the agent executable itself is located inside a directory named ".sync_temp",
	// prefer that directory (this supports running as `.sync_temp/sync-agent indexing`).
	exePath, err := os.Executable()
	exeDir := ""
	if err == nil {
		if ed, err2 := filepath.Abs(filepath.Dir(exePath)); err2 == nil {
			exeDir = ed
		}
	}

	// default .sync_temp inside cwd
	absSyncTemp := filepath.Join(root, ".sync_temp")
	// if exeDir ends with .sync_temp, prefer exeDir
	if exeDir != "" && filepath.Base(exeDir) == ".sync_temp" {
		absSyncTemp = exeDir
		fmt.Printf("ℹ️  Detected agent executable in .sync_temp, using %s for DB storage\n", absSyncTemp)
	} else {
		fmt.Printf("ℹ️  Using %s for DB storage\n", absSyncTemp)
	}

	if err := os.MkdirAll(absSyncTemp, 0755); err != nil {
		fmt.Printf("❌ Failed to create %s directory: %v\n", absSyncTemp, err)
		os.Exit(1)
	}

	idx, err := indexer.BuildIndex(root)
	if err != nil {
		fmt.Printf("❌ Indexing failed: %v\n", err)
		os.Exit(1)
	}
	dbPath := filepath.Join(absSyncTemp, "indexing_files.db")
	if err := indexer.SaveIndexDB(dbPath, idx); err != nil {
		fmt.Printf("❌ Failed to save index DB: %v\n", err)
		os.Exit(1)
	}

	// Print a brief summary
	added, modified, removed := indexer.CompareIndices(nil, idx)
	fmt.Printf("✅ Index saved: %s (entries=%d)\n", dbPath, len(idx))
	fmt.Printf("Summary: added=%d modified=%d removed=%d\n", len(added), len(modified), len(removed))
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
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-mainCtx.Done():
				fmt.Println("🔄 Watch registration stopped (context cancelled)")
				return
			case <-ticker.C:
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

				// Check if everything is registered
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
			}
		}
	}()

	// Registration goroutine started and event handler running.
	// Block here so the agent process stays alive (as previous behavior) —
	// registration still runs asynchronously in background.
	fmt.Println("⏳ Waiting for events (agent will keep running)...")
	<-mainCtx.Done()

	// Clean up notify watchers
	notify.Stop(c)
	close(c)
	fmt.Println("✅ File watcher stopped gracefully")
	return true
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
