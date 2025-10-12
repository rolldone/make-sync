package cmd

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"make-sync/internal/config"
	"make-sync/internal/devsync"
	"make-sync/internal/history"
	"make-sync/internal/pipeline/executor"
	"make-sync/internal/pipeline/parser"
	"make-sync/internal/pipeline/types"
	"make-sync/internal/util"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
	"gopkg.in/yaml.v3"
)

var oldStateRoot *term.State
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
			// No config found - enter directory navigation mode
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

		// Find make-sync-sample.yaml in project root
		var sampleFile string
		var foundSample bool

		// Get project root and look for make-sync-sample.yaml there
		if projectRoot, err := util.GetProjectRoot(); err == nil {
			projectSample := filepath.Join(projectRoot, "make-sync-sample.yaml")
			if _, err := os.Stat(projectSample); err == nil {
				sampleFile = projectSample
				foundSample = true
			}
		}

		if !foundSample {
			fmt.Println("❌ Error: make-sync-sample.yaml not found in project root")
			fmt.Println("📝 Please ensure make-sync-sample.yaml exists in the project root directory")
			fmt.Println("💡 You can copy from an existing make-sync.yaml:")
			fmt.Println("   cp make-sync.yaml make-sync-sample.yaml")
			return
		}

		fmt.Printf("📄 Using make-sync-sample.yaml as template from: %s\n", sampleFile)

		// Generate unique filenames to avoid overwriting existing files
		var configFileName, ignoreFileName string
		for {
			suffix := generateRandomSuffix(4)
			configFileName = fmt.Sprintf("make-sync-%s.yaml", suffix)
			ignoreFileName = fmt.Sprintf(".sync_ignore_%s", suffix)

			// Check if both files don't exist
			if _, err := os.Stat(configFileName); os.IsNotExist(err) {
				if _, err := os.Stat(ignoreFileName); os.IsNotExist(err) {
					break // Found unique names
				}
			}
			// If collision, loop will continue with new suffix
		}

		data, err := os.ReadFile(sampleFile)
		if err != nil {
			fmt.Printf("Error reading make-sync-sample.yaml: %v\n", err)
			return
		}

		// Parse as regular config (make-sync-sample.yaml is already in final format)
		var cfg config.Config
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			fmt.Printf("Error parsing make-sync-sample.yaml: %v\n", err)
			return
		}

		// Write config file with unique name
		outputData, err := yaml.Marshal(&cfg)
		if err != nil {
			fmt.Printf("Error generating config: %v\n", err)
			return
		}

		if err := os.WriteFile(configFileName, outputData, 0644); err != nil {
			fmt.Printf("Error writing %s: %v\n", configFileName, err)
			return
		}

		fmt.Printf("✅ Config created: %s\n", configFileName)

		// Create .sync_ignore file with unique name
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

		if err := os.WriteFile(ignoreFileName, []byte(syncIgnoreContent), 0644); err != nil {
			fmt.Printf("⚠️  Warning: Failed to create %s file: %v\n", ignoreFileName, err)
		} else {
			fmt.Printf("✅ Ignore file created: %s\n", ignoreFileName)
		}

		// Show usage instructions
		fmt.Println("💡 To use this config:")
		fmt.Printf("   cp %s make-sync.yaml\n", configFileName)
		fmt.Printf("   cp %s .sync_ignore\n", ignoreFileName)

		// Add to history
		history.AddPath(cwd)
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
	oldState, _ := util.GetState()
	oldStateRoot = oldState
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(NewBackupRestoreCmd())
	// register exec command
	rootCmd.AddCommand(execCmd)
	// register devsync command
	rootCmd.AddCommand(devsyncCmd)
	// register path-info command
	rootCmd.AddCommand(pathinfoCmd)
	// register pipeline command
	rootCmd.AddCommand(NewPipelineCmd())
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
		// Update history with current access time
		if err := history.AddPath(path); err != nil {
			fmt.Printf("⚠️  Failed to update history: %v\n", err)
		}
		// Spawn new shell in the target directory
		var cmd *exec.Cmd
		quotedPath := fmt.Sprintf("\"%s\"", path) // Quote path to handle spaces
		if runtime.GOOS == "windows" {
			cmd = exec.Command("cmd", "/c", fmt.Sprintf("cd /d %s && cmd", quotedPath))
		} else {
			// Linux, macOS, etc.
			cmd = exec.Command("bash", "-c", fmt.Sprintf("cd %s && exec bash", quotedPath))
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		err := cmd.Run()
		if err != nil {
			fmt.Printf("❌ Failed to start shell in directory: %v\n", err)
		}
		// After shell exits, return to menu
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
		util.ResetRaw(oldStateRoot)
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
		if host, ok := sc["Host"].(string); ok && host == hostName {
			hasRequested = true
			break
		}
	}
	if !hasRequested {
		return fmt.Errorf("no SSH config found for host: %s", hostName)
	}

	for idx, sc := range renderedCfg.DirectAccess.SSHConfigs {
		host, ok := sc["Host"].(string)
		if !ok || host == "" {
			continue
		}
		if idx > 0 {
			configLines = append(configLines, "")
		}
		configLines = append(configLines, fmt.Sprintf("Host %s", host))

		// Iterate over map and write non-empty values
		for key, val := range sc {
			// Skip Host field as it's already written above
			if key == "Host" {
				continue
			}

			if valStr := fmt.Sprintf("%v", val); valStr != "" {
				// Khusus untuk RemoteCommand: jangan quote agar tidak ada petik ganda
				if key == "RemoteCommand" {
					configLines = append(configLines, fmt.Sprintf("    %s %s", key, valStr))
				} else {
					configLines = append(configLines, fmt.Sprintf("    %s %s", key, quoteIfNeeded(valStr)))
				}
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

// generateRandomSuffix generates a random 4-character alphanumeric string
func generateRandomSuffix(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, length)
	for i := range result {
		num, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		result[i] = charset[num.Int64()]
	}
	return string(result)
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// NewPipelineCmd creates the pipeline command
func NewPipelineCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pipeline",
		Short: "Manage pipeline executions",
		Long:  `Run, list, and manage pipeline executions for automated workflows.`,
	}

	cmd.AddCommand(
		newPipelineRunCmd(),
		newPipelineListCmd(),
		newPipelineCreateCmd(),
	)

	return cmd
}

// newPipelineRunCmd creates the pipeline run subcommand
func newPipelineRunCmd() *cobra.Command {
	var varOverrides map[string]string

	cmd := &cobra.Command{
		Use:   "run [execution_key]",
		Short: "Run a pipeline execution",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			executionKey := args[0]

			// Load config
			cfg, err := config.LoadAndRenderConfig()
			if err != nil {
				fmt.Printf("❌ Failed to load config: %v\n", err)
				os.Exit(1)
			}

			// Find execution
			var execution *types.Execution
			for i, exec := range cfg.DirectAccess.Executions {
				if exec.Key == executionKey {
					execution = &cfg.DirectAccess.Executions[i]
					break
				}
			}
			if execution == nil {
				fmt.Printf("❌ Execution '%s' not found\n", executionKey)
				os.Exit(1)
			}

			// Load pipeline
			pipelinePath := filepath.Join(cfg.DirectAccess.PipelineDir, execution.Pipeline)
			pipeline, err := parser.ParsePipeline(pipelinePath)
			if err != nil {
				fmt.Printf("❌ Failed to parse pipeline: %v\n", err)
				os.Exit(1)
			}

			// Load vars with priority system:
			// 1. Start with empty vars
			vars := make(types.Vars)

			// 2. Load from vars.yaml if execution.Var is specified (lowest priority)
			if execution.Var != "" {
				varsPath := parser.ResolveVarsPath(cfg.DirectAccess.PipelineDir)
				fileVars, err := parser.ParseVarsSafe(varsPath, execution.Var)
				if err != nil {
					fmt.Printf("❌ Failed to parse vars: %v\n", err)
					os.Exit(1)
				}
				// Merge fileVars into vars
				for k, v := range fileVars {
					vars[k] = v
				}
			}

			// 3. Merge execution.Variables (higher priority than vars.yaml)
			if execution.Variables != nil {
				for k, v := range execution.Variables {
					vars[k] = v
				}
			}

			// 4. Apply CLI overrides (highest priority)
			for k, v := range varOverrides {
				vars[k] = v
			}

			// Execute
			executor := executor.NewExecutor()
			if err := executor.Execute(pipeline, execution, vars, execution.Hosts, cfg); err != nil {
				fmt.Printf("❌ Execution failed: %v\n", err)
				os.Exit(1)
			}

			fmt.Println("✅ Pipeline executed successfully")
		},
	}

	cmd.Flags().StringToStringVar(&varOverrides, "var", nil, "Override variables (key=value)")

	return cmd
}

// newPipelineListCmd creates the pipeline list subcommand
func newPipelineListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List available pipeline executions",
		Run: func(cmd *cobra.Command, args []string) {
			cfg, err := config.LoadAndRenderConfig()
			if err != nil {
				fmt.Printf("❌ Failed to load config: %v\n", err)
				os.Exit(1)
			}

			fmt.Println("Available Executions:")
			for _, exec := range cfg.DirectAccess.Executions {
				fmt.Printf("- %s (%s): %s\n", exec.Name, exec.Key, exec.Pipeline)
			}
		},
	}

	return cmd
}

// newPipelineCreateCmd creates the pipeline create subcommand
func newPipelineCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create [name]",
		Short: "Create a new pipeline template",
		Long:  `Create a new pipeline YAML file with a Docker-focused template for CI/CD workflows.`,
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, args []string) {
			name := args[0]

			// Validate pipeline name - reject names that would conflict with system files
			reservedNames := []string{"vars", "scripts"}
			for _, reserved := range reservedNames {
				if name == reserved {
					fmt.Printf("❌ Pipeline name '%s' is not allowed as it would conflict with system files\n", name)
					os.Exit(1)
				}
			}

			filename := name + ".yaml"

			// Load config to get pipeline_dir
			cfg, err := config.LoadAndRenderConfig()
			if err != nil {
				fmt.Printf("❌ Failed to load config: %v\n", err)
				os.Exit(1)
			}

			// Determine where to save the pipeline file
			var outputPath string
			if cfg.DirectAccess.PipelineDir != "" {
				// Use configured pipeline directory
				outputPath = filepath.Join(cfg.DirectAccess.PipelineDir, filename)
				// Ensure pipeline directory exists
				if err := os.MkdirAll(cfg.DirectAccess.PipelineDir, 0755); err != nil {
					fmt.Printf("❌ Failed to create pipeline directory: %v\n", err)
					os.Exit(1)
				}
			} else {
				// Fallback to current working directory
				outputPath = filename
			}

			// Check if file already exists
			if _, err := os.Stat(outputPath); err == nil {
				fmt.Printf("❌ Pipeline file '%s' already exists\n", outputPath)
				fmt.Printf("💡 Use a different name or remove the existing file if you want to recreate it\n")
				os.Exit(1)
			}

			// Get template
			template := getDockerPipelineTemplate(name)

			// Write to file
			if err := os.WriteFile(outputPath, []byte(template), 0644); err != nil {
				fmt.Printf("❌ Failed to create pipeline file: %v\n", err)
				os.Exit(1)
			}

			fmt.Printf("✅ Created pipeline template: %s\n", outputPath)
			fmt.Println("📝 Edit the file to customize your pipeline configuration")
		},
	}

	return cmd
}

// getDockerPipelineTemplate returns a Docker-focused pipeline template
func getDockerPipelineTemplate(name string) string {
	// Get project root using the same method as other commands
	projectRoot, err := util.GetProjectRoot()
	if err != nil {
		fmt.Printf("❌ Failed to detect project root: %v\n", err)
		os.Exit(1)
	}

	// Read template from project root
	templatePath := filepath.Join(projectRoot, "pipeline-sample.yaml")
	templateBytes, err := os.ReadFile(templatePath)
	if err != nil {
		fmt.Printf("❌ Failed to read template file: %v\n", err)
		os.Exit(1)
	}

	template := string(templateBytes)

	// Replace placeholders
	template = strings.ReplaceAll(template, "{{PIPELINE_NAME}}", name)

	return template
}

// ExecuteContext allows running the root command with a supplied context for cancellation.
func ExecuteContext(ctx context.Context) error {
	rootCmd.SetContext(ctx)
	if err := rootCmd.Execute(); err != nil {
		return err
	}
	return nil
}
