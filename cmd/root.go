package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"

	"make-sync/internal/config"
	"make-sync/internal/devsync"
	"make-sync/internal/history"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

var rootCmd = &cobra.Command{
	Use:   "make-sync",
	Short: "Remote server sync tool",
	Long: `A CLI tool for remote SSH, file synchronization, and development workflow management.
Supports SSH connections, file sync operations, and interactive configuration management.`,
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		cwd, _ := os.Getwd()
		fmt.Printf("You are in: %s\n", cwd)
		fmt.Println("Initialize Bootstrap Is Done!")
		fmt.Printf("process.execPath :: %s\n", os.Args[0])
		fmt.Printf("process.execPath dirname :: %s\n", filepath.Dir(os.Args[0]))
		fmt.Printf("process.execPath basename :: %s\n", filepath.Base(os.Args[0]))

		if config.ConfigExists() {
			cfg, err := config.LoadAndRenderConfig()
			if err != nil {
				fmt.Printf("❌ Configuration validation/rendering failed:\n%v\n", err)
				fmt.Println("💡 Please fix the configuration issues or run 'make-sync init' to recreate the config")
				return
			}
			fmt.Println("✅ Configuration is valid and rendered!")

			// Ensure local config exists with agent name (will be created automatically when needed)
			_, err = config.GetOrCreateLocalConfig()
			if err != nil {
				fmt.Printf("⚠️  Failed to initialize local config: %v\n", err)
			}

			// Main menu loop - return to menu after SSH sessions
			for {
				select {
				case <-ctx.Done():
					fmt.Println("⏹ Cancelled")
					return
				default:
				}
				if continueMenu, newCfg := showDirectAccessMenu(ctx, cfg); !continueMenu {
					// User chose to exit
					break
				} else if newCfg != nil {
					// Config was reloaded, update it
					cfg = newCfg
				}
				// Continue to show menu again after SSH session ends
			}
		} else {
			fmt.Println("Config file not found")
			fmt.Println("USAGE:")
			fmt.Println("Make sure you have the config file by running.")
			fmt.Println("make-sync init")
			fmt.Println("------------------------------")
			fmt.Println("For more details please visit. https://github.com/rolldone/ngi-sync")
			showRecentWorkspacesMenu()
		}
	},
}

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize config file",
	Long:  `Generate a default make-sync.yaml config file in the current directory.`,
	Run: func(cmd *cobra.Command, args []string) {
		cwd, _ := os.Getwd()
		fmt.Printf("You are in: %s\n", cwd)
		fmt.Println("Initialize Bootstrap Is Done!")
		fmt.Printf("process.execPath :: %s\n", os.Args[0])
		fmt.Printf("process.execPath dirname :: %s\n", filepath.Dir(os.Args[0]))
		fmt.Printf("process.execPath basename :: %s\n", filepath.Base(os.Args[0]))

		if config.ConfigExists() {
			fmt.Println("Config file already exists.")
			return
		}

		// Check if template.yaml exists in the actual executable directory
		// Use os.Executable() to get absolute path of the running binary (robust vs os.Args[0])
		exePath, err := os.Executable()
		if err != nil {
			fmt.Printf("❌ Error: failed to resolve executable path: %v\n", err)
			// Fallback to os.Args[0] directory as a last resort
			exePath = os.Args[0]
		}
		if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
			exePath = resolved
		}
		execDir := filepath.Dir(exePath)
		templateFile := filepath.Join(execDir, "template.yaml")

		// Try template.yaml next to the executable first; fallback to current working directory
		if _, err := os.Stat(templateFile); err == nil {
			// Load and parse template.yaml using struct mapping
			data, err := os.ReadFile(templateFile)
			if err != nil {
				fmt.Printf("Error reading template.yaml: %v\n", err)
				return
			}

			// Unmarshal template.yaml to TemplateConfig struct
			var templateConfig config.TemplateConfig
			err = yaml.Unmarshal(data, &templateConfig)
			if err != nil {
				fmt.Printf("Error parsing template.yaml: %v\n", err)
				return
			}

			// Map TemplateConfig to Config
			cfg := config.MapTemplateToConfig(templateConfig)

			// Marshal Config to YAML
			outputData, err := yaml.Marshal(&cfg)
			if err != nil {
				fmt.Printf("Error generating config: %v\n", err)
				return
			}

			// Write to make-sync.yaml
			err = os.WriteFile("make-sync.yaml", outputData, 0644)
			if err != nil {
				fmt.Printf("Error writing make-sync.yaml: %v\n", err)
				return
			}

			fmt.Printf("Config loaded from template.yaml (exec dir: %s) to %s\n", execDir, config.GetConfigPath())

			// Create .sync_ignore file with default extended ignores (gitignore style)
			syncIgnoreContent := `# Development files
.git
.DS_Store
Thumbs.db

# Dependencies
node_modules

# IDE files
.vscode

# Log files
*.log

# Temporary files
*.tmp
*.swp
*.bak

# SSH
.ssh`

			err = os.WriteFile(".sync_ignore", []byte(syncIgnoreContent), 0644)
			if err != nil {
				fmt.Printf("⚠️  Warning: Failed to create .sync_ignore file: %v\n", err)
			} else {
				fmt.Println("✅ Created .sync_ignore file with default ignore patterns")
			}

			// Add to history
			cwd, _ := os.Getwd()
			history.AddPath(cwd)
		} else if cwdTemplate := filepath.Join(".", "template.yaml"); func() bool { _, e := os.Stat(cwdTemplate); return e == nil }() {
			// Fallback: allow reading template.yaml from current working directory (useful during development/go run)
			data, err := os.ReadFile(cwdTemplate)
			if err != nil {
				fmt.Printf("Error reading template.yaml from current directory: %v\n", err)
				return
			}

			var templateConfig config.TemplateConfig
			if err := yaml.Unmarshal(data, &templateConfig); err != nil {
				fmt.Printf("Error parsing template.yaml: %v\n", err)
				return
			}

			cfg := config.MapTemplateToConfig(templateConfig)
			outputData, err := yaml.Marshal(&cfg)
			if err != nil {
				fmt.Printf("Error generating config: %v\n", err)
				return
			}

			if err := os.WriteFile("make-sync.yaml", outputData, 0644); err != nil {
				fmt.Printf("Error writing make-sync.yaml: %v\n", err)
				return
			}

			fmt.Printf("Config loaded from template.yaml (cwd) to %s\n", config.GetConfigPath())

			// Create .sync_ignore file with default extended ignores (gitignore style)
			syncIgnoreContent := `# Development files
.git
.DS_Store
Thumbs.db

# Dependencies
node_modules

# IDE files
.vscode

# Log files
*.log

# Temporary files
*.tmp
*.swp
*.bak

# SSH
.ssh`

			if err := os.WriteFile(".sync_ignore", []byte(syncIgnoreContent), 0644); err != nil {
				fmt.Printf("⚠️  Warning: Failed to create .sync_ignore file: %v\n", err)
			} else {
				fmt.Println("✅ Created .sync_ignore file with default ignore patterns")
			}

			cwd, _ := os.Getwd()
			history.AddPath(cwd)
		} else {
			// Template.yaml not found next to executable or in current working directory
			fmt.Printf("❌ Error: template.yaml not found in executable directory (%s)\n", execDir)
			fmt.Println("📝 Please ensure template.yaml exists in the same directory as the make-sync executable")
			fmt.Println("💡 Tip: Place template.yaml alongside the make-sync binary")
			return
		}
	},
}

var devsyncCmd = &cobra.Command{
	Use:   "devsync",
	Short: "Start file watching in devsync mode",
	Long:  `Watch files for changes and display real-time notifications based on configuration.`,
	Run: func(cmd *cobra.Command, args []string) {
		cwd, _ := os.Getwd()

		// Create .sync_temp directory if it doesn't exist
		syncTempDir := filepath.Join(cwd, ".sync_temp")
		if err := os.MkdirAll(syncTempDir, 0755); err != nil {
			fmt.Printf("❌ Failed to create .sync_temp directory: %v\n", err)
			return
		}

		fmt.Printf("📁 Log directory: %s\n", syncTempDir)
		fmt.Printf("You are in: %s\n", cwd)
		fmt.Println("Initialize Bootstrap Is Done!")
		fmt.Printf("process.execPath :: %s\n", os.Args[0])
		fmt.Printf("process.execPath dirname :: %s\n", filepath.Dir(os.Args[0]))
		fmt.Printf("process.execPath basename :: %s\n", filepath.Base(os.Args[0]))

		// Validate and render configuration before proceeding
		fmt.Println("🔍 Validating and rendering configuration...")
		cfg, err := config.LoadAndRenderConfig() // Use LoadAndRenderConfig to render templates
		if err != nil {
			fmt.Printf("❌ Configuration validation/rendering failed:\n%v\n", err)
			fmt.Println("💡 Please fix the configuration issues or run 'make-sync init' to recreate the config")
			return
		}
		fmt.Println("✅ Configuration is valid and rendered!")

		// Run devsync mode menu
		devsync.ShowDevSyncModeMenu(cmd.Context(), cfg)
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(NewBackupRestoreCmd())
	// register exec command
	rootCmd.AddCommand(execCmd)
	// register devsync command
	rootCmd.AddCommand(devsyncCmd)
}

func showRecentWorkspacesMenu() {
	paths := history.GetAllPaths()
	if len(paths) == 0 {
		fmt.Println("No recent workspaces found.")
		return
	}

	prompt := promptui.SelectWithAdd{
		Label:    "Display recent workspaces (type to search)",
		Items:    paths,
		AddLabel: "Search",
	}

	idx, result, err := prompt.Run()
	if err != nil {
		fmt.Printf("Prompt failed %v\n", err)
		return
	}

	if idx == -1 {
		// Search mode
		results := history.SearchPaths(result)
		if len(results) == 0 {
			fmt.Printf("No workspaces found matching '%s'\n", result)
			return
		}

		// Show search results
		searchPrompt := promptui.Select{
			Label: "Search results",
			Items: results,
		}

		_, selected, err := searchPrompt.Run()
		if err != nil {
			fmt.Printf("Prompt failed %v\n", err)
			return
		}
		result = selected
	}

	// Handle selection
	if subResult := showWorkspaceSubMenu(result); subResult == "exit" {
		return
	}
}

func showWorkspaceSubMenu(path string) string {
	items := []string{
		"Enter Directory",
		"Delete Directory",
		"Back",
	}

	prompt := promptui.Select{
		Label: fmt.Sprintf("Selected: %s", path),
		Items: items,
	}

	_, subResult, err := prompt.Run()
	if err != nil {
		fmt.Printf("Prompt failed %v\n", err)
		return "back"
	}

	if subResult == "Enter Directory" {
		fmt.Printf("Changing to directory: %s\n", path)
		// Note: In real CLI, this would change shell directory, but for demo we print
	} else if subResult == "Delete Directory" {
		history.RemovePath(path)
		fmt.Printf("Removed from history: %s\n", path)
	}

	// Default: continue to menu
	return "continue"
}

func showDirectAccessMenu(ctx context.Context, loadedCfg *config.Config) (bool, *config.Config) {
	// Use the already loaded configuration
	cfg := loadedCfg

	// Create menu items from ssh_commands
	var items []string
	for _, sshCmd := range cfg.DirectAccess.SSHCommands {
		items = append(items, sshCmd.AccessName)
	}

	// Add static menu items
	items = append(items, "devsync :: Open Devsync")
	items = append(items, "clean :: Git clean up")
	items = append(items, "Restart")
	items = append(items, "Exit")

	prompt := promptui.Select{
		Label: "Select an option",
		Items: items,
	}

	_, result, err := prompt.Run()
	if err != nil {
		fmt.Printf("Prompt failed %v\n", err)
		return false, nil
	}

	// Handle SSH command execution
	for _, sshCmd := range cfg.DirectAccess.SSHCommands {
		if sshCmd.AccessName == result {
			fmt.Printf("Executing: %s\n", sshCmd.Command)

			// Parse SSH command to get host name
			hostName, err := parseSSHCommand(sshCmd.Command)
			if err != nil {
				fmt.Printf("❌ Error parsing SSH command: %v\n", err)
				return true, nil // Continue to menu on error
			}

			fmt.Printf("🔍 SSH Host: %s\n", hostName)

			// Generate temporary SSH config
			err = generateSSHTempConfig(cfg, hostName)
			if err != nil {
				fmt.Printf("❌ Error generating SSH temp config: %v\n", err)
				return true, nil // Continue to menu on error
			}

			// Execute the SSH command with custom config using -F option
			modifiedCommand := strings.Replace(sshCmd.Command, "ssh ", "ssh -F .sync_temp/.ssh/config ", 1)
			fmt.Printf("🔧 Modified command: %s\n", modifiedCommand)

			cmd := exec.Command("bash", "-c", modifiedCommand)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin

			err = cmd.Run()
			if err != nil {
				fmt.Printf("❌ Error executing command: %v\n", err)
			}
			return true, nil // Continue to menu after SSH session
		}
	}

	// Handle other static options
	switch result {
	case "devsync :: Open Devsync":
		fmt.Println("Opening devsync...")
		devsync.ShowDevSyncModeMenu(ctx, cfg)

		return true, nil
	case "clean :: Git clean up":
		fmt.Println("Running git clean up (local)...")
		// Run the sequence of git commands locally:
		cmds := [][]string{
			{"git", "config", "core.filemode", "false"},
			{"git", "config", "core.autocrlf", "true"},
			{"git", "add", "--renormalize", "."},
			{"git", "reset"},
		}
		for _, parts := range cmds {
			c := exec.Command(parts[0], parts[1:]...)
			c.Stdout = os.Stdout
			c.Stderr = os.Stderr
			c.Stdin = os.Stdin
			if err := c.Run(); err != nil {
				fmt.Printf("⚠️  Command failed: %v\n", err)
				// continue to next, but inform user
			}
		}
		fmt.Println("Git clean up finished.")
		return true, nil
	case "Restart":
		fmt.Println("🔄 Reloading configuration...")
		newCfg, err := config.LoadAndRenderConfig()
		if err != nil {
			fmt.Printf("❌ Failed to reload configuration: %v\n", err)
			fmt.Println("💡 Continuing with current configuration")
			return true, nil
		}
		fmt.Println("✅ Configuration reloaded successfully!")
		return true, newCfg
	case "Exit":
		fmt.Println("Exiting...")
		return false, nil
	}

	// Default: continue to menu
	return true, nil
}

// generateSSHTempConfig generates temporary SSH config folder (.sync_temp/.ssh/config)
func generateSSHTempConfig(cfg *config.Config, hostName string) error {
	syncTempDir := ".sync_temp"
	sshDir := filepath.Join(syncTempDir, ".ssh")
	configPath := filepath.Join(sshDir, "config")

	// Clean up old .sync_temp file if it exists (from previous version)
	if _, err := os.Stat(syncTempDir); err == nil {
		// Check if it's a file, not a directory
		if info, err := os.Stat(syncTempDir); err == nil && !info.IsDir() {
			fmt.Printf("🔄 Removing old .sync_temp file...\n")
			if err := os.Remove(syncTempDir); err != nil {
				return fmt.Errorf("error removing old .sync_temp file: %v", err)
			}
		}
	}

	// Create .sync_temp directory if it doesn't exist
	if err := os.MkdirAll(sshDir, 0755); err != nil {
		return fmt.Errorf("error creating .sync_temp directory: %v", err)
	}

	// Render template variables first so =host, =remotePath, etc. are concrete
	renderedCfg, rerr := config.RenderTemplateVariablesInMemory(cfg)
	if rerr != nil {
		return fmt.Errorf("error rendering template variables: %v", rerr)
	}

	// helper to quote values with spaces
	quoteIfNeeded := func(s string) string {
		if s == "" {
			return s
		}
		if strings.ContainsAny(s, " \t\"'") {
			// prefer double quotes; escape existing double quotes
			s = strings.ReplaceAll(s, "\"", "\\\"")
			return "\"" + s + "\""
		}
		return s
	}

	// Generate SSH config content for ALL entries (multi-host support)
	var configLines []string
	configLines = append(configLines, "# Temporary SSH config generated by make-sync")
	configLines = append(configLines, "")

	// Optionally: ensure the requested host exists, but still write all
	hasRequested := false
	for _, sc := range renderedCfg.DirectAccess.SSHConfigs {
		if sc.Host == hostName {
			hasRequested = true
			break
		}
	}
	if !hasRequested {
		return fmt.Errorf("no SSH config found for host: %s", hostName)
	}

	for idx, sc := range renderedCfg.DirectAccess.SSHConfigs {
		if sc.Host == "" {
			continue
		}
		if idx > 0 {
			configLines = append(configLines, "")
		}
		configLines = append(configLines, fmt.Sprintf("Host %s", sc.Host))

		// Dynamically scan struct fields and write non-empty ones
		scValue := reflect.ValueOf(sc)
		scType := scValue.Type()
		for i := 0; i < scValue.NumField(); i++ {
			field := scValue.Field(i)
			fieldType := scType.Field(i)
			fieldName := fieldType.Name

			// Skip Host field as it's already written above
			if fieldName == "Host" {
				continue
			}

			// Get value as string, handling different types
			var value string
			switch field.Kind() {
			case reflect.String:
				value = field.String()
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				value = fmt.Sprintf("%d", field.Int())
			case reflect.Bool:
				value = fmt.Sprintf("%t", field.Bool())
			default:
				// Skip unsupported types
				continue
			}

			if value != "" {
				configLines = append(configLines, fmt.Sprintf("    %s %s", fieldName, quoteIfNeeded(value)))
			}
		}
	}

	// Write to .sync_temp/.ssh/config file
	content := strings.Join(configLines, "\n") + "\n"
	err := os.WriteFile(configPath, []byte(content), 0644)
	if err != nil {
		return fmt.Errorf("error writing SSH temp config: %v", err)
	}

	fmt.Printf("✅ Generated SSH temp config with %d host entries: %s\n", len(renderedCfg.DirectAccess.SSHConfigs), configPath)
	return nil
}

// parseSSHCommand parses SSH command to extract host name
func parseSSHCommand(command string) (string, error) {
	// Simple parsing for "ssh [options] host"
	parts := strings.Fields(command)
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid SSH command format")
	}

	// Find the host (usually the last part, ignoring options)
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		// Skip options (starting with -)
		if !strings.HasPrefix(part, "-") {
			return part, nil
		}
	}

	return "", fmt.Errorf("could not find host in SSH command")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// ExecuteContext allows running the root command with a supplied context for cancellation.
func ExecuteContext(ctx context.Context) error {
	rootCmd.SetContext(ctx)
	if err := rootCmd.Execute(); err != nil {
		return err
	}
	return nil
}
