package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"make-sync/internal/config"
	"make-sync/internal/syncdata"
	"make-sync/internal/util"
)

// pathinfoCmd represents the path-info command
var pathinfoCmd = &cobra.Command{
	Use:   "path-info",
	Short: "Display path information for debugging",
	Long: `Display comprehensive path information including:
- Current working directory
- Executable paths (original and resolved)
- Project root detection
- Agent paths and validation
- Configuration paths

This command is useful for debugging path resolution issues.`,
	Run: runPathInfo,
}

func init() {
	// This will be registered in root.go
}

func runPathInfo(cmd *cobra.Command, args []string) {
	fmt.Println("🔍 Make-Sync Path Information")
	fmt.Println("=" + strings.Repeat("=", 35))
	fmt.Println()

	// Current working directory
	wd, err := os.Getwd()
	if err != nil {
		fmt.Printf("❌ Failed to get working directory: %v\n", err)
		wd = "<unknown>"
	}
	fmt.Printf("📂 Current Working Directory: %s\n", wd)

	// Executable paths
	exePath, err := os.Executable()
	if err != nil {
		fmt.Printf("❌ Failed to get executable path: %v\n", err)
		exePath = "<unknown>"
	}
	fmt.Printf("🔧 Executable Path (original): %s\n", exePath)

	// Resolved executable path (symlinks)
	resolvedPath := exePath
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		resolvedPath = resolved
	}
	if resolvedPath != exePath {
		fmt.Printf("🔗 Executable Path (resolved): %s\n", resolvedPath)
		fmt.Printf("   └─ Symlink detected\n")
	} else {
		fmt.Printf("🔗 Executable Path (resolved): %s (no symlink)\n", resolvedPath)
	}

	// Development mode detection
	isDev := isDevelopmentMode(resolvedPath)
	fmt.Printf("🛠️  Development Mode: %v\n", isDev)

	fmt.Println()

	// Project root detection
	fmt.Println("📁 Project Root Detection:")
	fmt.Println(strings.Repeat("-", 25))

	projectRoot, err := util.GetProjectRoot()
	if err != nil {
		fmt.Printf("❌ Failed to detect project root: %v\n", err)
		projectRoot = "<failed>"
	} else {
		fmt.Printf("✅ Detected Project Root: %s\n", projectRoot)
	}

	// Validate project root
	if projectRoot != "<failed>" {
		goModPath := filepath.Join(projectRoot, "go.mod")
		if _, err := os.Stat(goModPath); err == nil {
			fmt.Printf("✅ go.mod found at: %s\n", goModPath)
		} else {
			fmt.Printf("❌ go.mod NOT found at: %s\n", goModPath)
		}
	}

	fmt.Println()

	// Agent paths
	fmt.Println("🤖 Agent Path Information:")
	fmt.Println(strings.Repeat("-", 25))

	// Load config to get agent info
	cfg, err := config.LoadAndValidateConfig()
	if err != nil {
		fmt.Printf("⚠️  Failed to load config: %v\n", err)
		fmt.Printf("   Using defaults for agent path calculation\n")
		cfg = &config.Config{} // Use empty config
	}

	// Get local config for agent binary name
	localConfig, err := config.GetOrCreateLocalConfig()
	if err != nil {
		fmt.Printf("⚠️  Failed to load local config: %v\n", err)
	}

	// Calculate agent paths
	var agentSourceDir, agentBinaryName, agentOutputPath string

	if projectRoot != "<failed>" {
		agentSourceDir = filepath.Join(projectRoot, "sub_app", "agent")
		agentOutputPath = projectRoot
	} else {
		agentSourceDir = "<unknown - project root failed>"
		agentOutputPath = "<unknown - project root failed>"
	}

	if localConfig != nil {
		agentBinaryName = localConfig.GetAgentBinaryName("linux") // Default to linux for display
	} else {
		agentBinaryName = "sync-agent-linux" // Fallback
	}

	fmt.Printf("📂 Agent Source Directory: %s\n", agentSourceDir)
	fmt.Printf("🔧 Agent Binary Name: %s\n", agentBinaryName)
	fmt.Printf("📁 Agent Output Directory: %s\n", agentOutputPath)

	if agentOutputPath != "<unknown - project root failed>" {
		fullAgentPath := filepath.Join(agentOutputPath, agentBinaryName)
		fmt.Printf("🎯 Full Agent Binary Path: %s\n", fullAgentPath)
	}

	fmt.Println()

	// Validation
	fmt.Println("✅ Path Validation:")
	fmt.Println(strings.Repeat("-", 18))

	// Check agent source directory
	if agentSourceDir != "<unknown - project root failed>" {
		if _, err := os.Stat(agentSourceDir); err == nil {
			fmt.Printf("✅ Agent source directory exists: %s\n", agentSourceDir)

			// Check for main.go in agent directory
			mainGoPath := filepath.Join(agentSourceDir, "main.go")
			if _, err := os.Stat(mainGoPath); err == nil {
				fmt.Printf("✅ Agent main.go found: %s\n", mainGoPath)
			} else {
				fmt.Printf("❌ Agent main.go NOT found: %s\n", mainGoPath)
			}

			// Check for go.mod in agent directory
			agentGoModPath := filepath.Join(agentSourceDir, "go.mod")
			if _, err := os.Stat(agentGoModPath); err == nil {
				fmt.Printf("✅ Agent go.mod found: %s\n", agentGoModPath)
			} else {
				fmt.Printf("❌ Agent go.mod NOT found: %s\n", agentGoModPath)
			}
		} else {
			fmt.Printf("❌ Agent source directory NOT found: %s\n", agentSourceDir)
		}
	}

	// Check for existing agent binary
	if agentOutputPath != "<unknown - project root failed>" {
		fullAgentPath := filepath.Join(agentOutputPath, agentBinaryName)
		if stat, err := os.Stat(fullAgentPath); err == nil {
			fmt.Printf("✅ Existing agent binary found: %s (size: %d bytes)\n", fullAgentPath, stat.Size())
		} else {
			fmt.Printf("ℹ️  No existing agent binary: %s\n", fullAgentPath)
		}
	}

	fmt.Println()

	// Configuration info
	fmt.Println("⚙️  Configuration:")
	fmt.Println(strings.Repeat("-", 14))

	if cfg != nil && cfg.Devsync.OSTarget != "" {
		fmt.Printf("🎯 Target OS: %s\n", cfg.Devsync.OSTarget)
	} else {
		fmt.Printf("🎯 Target OS: linux (default)\n")
	}

	fmt.Printf("📊 Working Dir vs Project Root match: %v\n", wd == projectRoot)

	if wd != projectRoot {
		fmt.Printf("⚠️  Working directory differs from detected project root\n")
		fmt.Printf("   This might cause path resolution issues\n")
	}

	fmt.Println()

	// Ignore patterns simulation
	fmt.Println("🚫 Ignore Patterns Simulation:")
	fmt.Println(strings.Repeat("-", 30))
	runIgnorePatternsSimulation(projectRoot)

	fmt.Println()
	fmt.Println("💡 Tip: If paths look wrong, try running from the project root directory:")
	fmt.Printf("   cd %s\n", projectRoot)
	fmt.Printf("   ./make-sync path-info\n")
}

// isDevelopmentMode checks if the executable path indicates we're running via "go run"
// This is a simplified version of the function in util package for this display
func isDevelopmentMode(exePath string) bool {
	tempDir := os.TempDir()
	tempDir = filepath.Clean(tempDir)
	exePath = filepath.Clean(exePath)

	// Check if executable is in temp directory
	if strings.HasPrefix(exePath, tempDir) {
		return true
	}

	// Check for Go build cache
	homeDir, err := os.UserHomeDir()
	if err == nil {
		goBuildCache := filepath.Join(homeDir, ".cache", "go-build")
		goBuildCache = filepath.Clean(goBuildCache)
		if strings.HasPrefix(exePath, goBuildCache) {
			return true
		}
	}

	// Check for go-build in path
	if strings.Contains(exePath, "go-build") {
		return true
	}

	return false
}

// runIgnorePatternsSimulation tests ignore patterns against sample files
func runIgnorePatternsSimulation(projectRoot string) {
	// Use current working directory for ignore simulation to test local .sync_ignore files
	wd, err := os.Getwd()
	if err != nil {
		fmt.Printf("❌ Cannot run simulation: Failed to get working directory: %v\n", err)
		return
	}

	fmt.Printf("📁 Testing files in current directory: %s\n", wd)

	// Initialize IgnoreCache for current working directory
	ignoreCache := syncdata.NewIgnoreCache(wd)

	// Test files for simulation
	testFiles := []struct {
		path  string
		isDir bool
		desc  string
	}{
		{"docker-compose.yml", false, "Docker Compose (common negation target)"},
		{"package.json", false, "Package JSON"},
		{"node_modules/react/index.js", false, "Node modules file"},
		{".env", false, "Environment file"},
		{".git/config", false, "Git config"},
		{"src/main.go", false, "Source file"},
		{"dist/app.js", false, "Build output"},
		{"logs/watcher.log", false, "Log file"},
		{".sync_temp/cache.db", false, "Sync temp file"},
		{"test.txt", false, "Test file"},
		{"README.md", false, "Readme file"},
		{"build", true, "Build directory"},
		{"vendor", true, "Vendor directory"},
	}

	// Check if .sync_ignore exists in current directory
	syncIgnorePath := filepath.Join(wd, ".sync_ignore")
	if _, err := os.Stat(syncIgnorePath); err == nil {
		fmt.Printf("✅ Found .sync_ignore file\n")

		// Show patterns being used
		// allPatterns := ignoreCache.GetAllPatterns()
		// if len(allPatterns) > 0 {
		fmt.Printf("📋 Active patterns (unknown total):\n")
		// for i, pattern := range allPatterns {
		// 	if i < 10 { // Show first 10
		// 		status := "🔸"
		// 		if strings.HasPrefix(pattern, "!") {
		// 			status = "🔹" // Different icon for negation
		// 		}
		// 		fmt.Printf("   %s %s\n", status, pattern)
		// 	} else if i == 10 {
		// 		fmt.Printf("   ... and %d more patterns\n", len(allPatterns)-10)
		// 		break
		// 	}
		// }
		// }
	} else {
		fmt.Printf("ℹ️  No .sync_ignore file found, using defaults only\n")
	}

	fmt.Println()

	// Test each file
	var passCount, failCount int
	for _, test := range testFiles {
		fullPath := filepath.Join(wd, test.path)
		isIgnored := ignoreCache.Match(fullPath, test.isDir)

		status := "✅ PASS"
		if isIgnored {
			status = "❌ IGNORED"
			failCount++
		} else {
			passCount++
		}

		fmt.Printf("%-15s %-40s %s\n", status, test.path, test.desc)
	}

	fmt.Println()
	fmt.Printf("📊 Summary: %d files would sync, %d files would be ignored\n", passCount, failCount)

	// Additional info about negation patterns
	allPatterns := ignoreCache.GetAllPatterns()
	negationCount := 0
	for _, pattern := range allPatterns {
		if strings.HasPrefix(pattern, "!") {
			negationCount++
		}
	}

	if negationCount > 0 {
		fmt.Printf("🔹 Found %d negation patterns (!) for priority inclusion\n", negationCount)
	}

	fmt.Println()

	// Agent availability check
	fmt.Println("🤖 Agent Availability Check:")
	fmt.Println(strings.Repeat("-", 25))
	checkAgentAvailability(projectRoot)
}

// checkAgentAvailability tests if agent binaries exist and can be executed
func checkAgentAvailability(projectRoot string) {
	if projectRoot == "<failed>" {
		fmt.Printf("❌ Cannot check agent: Project root not detected\n")
		return
	}

	// Get local config for agent binary names
	localConfig, err := config.GetOrCreateLocalConfig()
	if err != nil {
		fmt.Printf("⚠️  Failed to load local config: %v\n", err)
		return
	}

	// Test different target OS agents
	targetOSList := []struct {
		os   string
		desc string
	}{
		{"linux", "Linux Agent"},
		{"windows", "Windows Agent"},
		{"darwin", "macOS Agent"},
	}

	for _, target := range targetOSList {
		agentBinaryName := localConfig.GetAgentBinaryName(target.os)
		agentPath := filepath.Join(projectRoot, agentBinaryName)

		// Check if agent binary exists
		if stat, err := os.Stat(agentPath); err == nil {
			fmt.Printf("✅ %s: Found at %s\n", target.desc, agentPath)
			fmt.Printf("   📊 Size: %d bytes, Modified: %s\n",
				stat.Size(),
				stat.ModTime().Format("2006-01-02 15:04:05"))

			// Try to get agent identity if it's for current OS
			if target.os == "linux" { // Assuming we're running on Linux
				if identity := testAgentIdentity(agentPath); identity != "" {
					fmt.Printf("   🔢 Identity: %s\n", identity)
				} else {
					fmt.Printf("   ⚠️  Cannot get identity (may not be executable)\n")
				}
			}
		} else {
			fmt.Printf("❌ %s: Not found at %s\n", target.desc, agentPath)
		}
	}

	// Check agent source directory
	agentSourceDir := filepath.Join(projectRoot, "sub_app", "agent")
	if stat, err := os.Stat(agentSourceDir); err == nil && stat.IsDir() {
		fmt.Printf("✅ Agent source: Found at %s\n", agentSourceDir)

		// Check go.mod in agent directory
		agentGoMod := filepath.Join(agentSourceDir, "go.mod")
		if _, err := os.Stat(agentGoMod); err == nil {
			fmt.Printf("   📄 go.mod: Present\n")
		} else {
			fmt.Printf("   ❌ go.mod: Missing\n")
		}

		// Check main.go
		agentMain := filepath.Join(agentSourceDir, "main.go")
		if _, err := os.Stat(agentMain); err == nil {
			fmt.Printf("   📄 main.go: Present\n")
		} else {
			fmt.Printf("   ❌ main.go: Missing\n")
		}
	} else {
		fmt.Printf("❌ Agent source: Not found at %s\n", agentSourceDir)
	}

	fmt.Println()
	fmt.Printf("💡 To build missing agents, run: ./make-sync deploy-agent\n")
}

// testAgentIdentity tries to run agent identity command
func testAgentIdentity(agentPath string) string {
	// This is a simple test - in real deployment you'd use exec.Command
	// For now, just check if file is executable
	if stat, err := os.Stat(agentPath); err == nil {
		mode := stat.Mode()
		if mode&0111 != 0 { // Check if any execute bit is set
			return "Executable binary (identity test skipped)"
		}
		return "File exists but not executable"
	}
	return ""
}
