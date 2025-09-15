package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	Long: `A CLI tool for remote SSH, fi		// Valid		// Validate and render configuration before proceeding
		fmt.Println("🔍 Validating and rendering configuration...")
		_, err := config.LoadAndRenderConfig()
		if err != nil {
			fmt.Printf("❌ Configuration validation/rendering failed:\n%v\n", err)
			fmt.Println("💡 Please fix the configuration issues or run 'make-sync init' to recreate the config")
			return
		}
		fmt.Println("✅ Configuration is valid and rendered!")

		for {der configuration before proceeding
		fmt.Println("🔍 Validating and rendering configuration...")
		_, err := config.LoadAndRenderConfig()
		if err != nil {
			fmt.Printf("❌ Configuration validation/rendering failed:\n%v\n", err)
			fmt.Println("💡 Please fix the configuration issues or run 'make-sync init' to recreate the config")
			return
		}
		fmt.Println("✅ Configuration is valid and rendered!")

		for {d command execution.`,
	Run: func(cmd *cobra.Command, args []string) {
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

			// Main menu loop - return to menu after SSH sessions
			for {
				if !showDirectAccessMenu(cfg) {
					// User chose to exit
					break
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
			fmt.Println("-----------------------------------------------------------------------------")
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

		for {
			prompt := promptui.Select{
				Label: "You have sync collection config data",
				Items: []string{"1) Load saved config", "2) Create New"},
			}

			_, result, err := prompt.Run()
			if err != nil {
				fmt.Printf("Prompt failed %v\n", err)
				return
			}

			if result == "1) Load saved config" {
				err := showLoadMenu()
				if err != nil {
					fmt.Printf("Error loading config: %v\n", err)
					continue // Stay in loop on error
				} else {
					// Load successful, exit loop
					break
				}
			} else if result == "2) Create New" {
				// Check if template.yaml exists in executable directory
				execDir := filepath.Dir(os.Args[0])
				templateFile := filepath.Join(execDir, "template.yaml")
				if _, err := os.Stat(templateFile); !os.IsNotExist(err) {
					// Load and parse template.yaml using struct mapping
					data, err := os.ReadFile(templateFile)
					if err != nil {
						fmt.Printf("Error reading template.yaml: %v\n", err)
						continue
					}

					// Unmarshal template.yaml to TemplateConfig struct
					var templateConfig config.TemplateConfig
					err = yaml.Unmarshal(data, &templateConfig)
					if err != nil {
						fmt.Printf("Error parsing template.yaml: %v\n", err)
						continue
					}

					// Map TemplateConfig to Config
					cfg := config.MapTemplateToConfig(templateConfig)

					// Marshal Config to YAML
					outputData, err := yaml.Marshal(&cfg)
					if err != nil {
						fmt.Printf("Error generating config: %v\n", err)
						continue
					}

					// Write to make-sync.yaml
					err = os.WriteFile("make-sync.yaml", outputData, 0644)
					if err != nil {
						fmt.Printf("Error writing make-sync.yaml: %v\n", err)
						continue
					}

					fmt.Printf("Config loaded from template.yaml to %s\n", config.GetConfigPath())

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
					break
				} else {
					// Template.yaml not found in executable directory
					fmt.Printf("❌ Error: template.yaml not found in executable directory (%s)\n", execDir)
					fmt.Println("📝 Please ensure template.yaml exists in the same directory as the make-sync executable")
					fmt.Println("💡 Tip: Place template.yaml alongside the make-sync binary")
					continue
				}
			} else {
				fmt.Println("Invalid selection.")
				continue
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(dataCmd)
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
			fmt.Printf("No matches found for '%s'\n", result)
			return
		}
		// Show search results
		searchPrompt := promptui.Select{
			Label: fmt.Sprintf("Search results for '%s'", result),
			Items: results,
		}
		_, searchResult, err := searchPrompt.Run()
		if err != nil {
			fmt.Printf("Prompt failed %v\n", err)
			return
		}
		result = searchResult
	}

	// Submenu
	subPrompt := promptui.Select{
		Label: fmt.Sprintf("Selected: %s", result),
		Items: []string{"Enter Directory", "Delete Directory"},
	}

	_, subResult, err := subPrompt.Run()
	if err != nil {
		fmt.Printf("Prompt failed %v\n", err)
		return
	}

	if subResult == "Enter Directory" {
		fmt.Printf("Changing to directory: %s\n", result)
		// Note: In real CLI, this would change shell directory, but for demo we print
	} else if subResult == "Delete Directory" {
		history.RemovePath(result)
		fmt.Printf("Removed from history: %s\n", result)
	}
}

func showLoadMenu() error {
	fmt.Println("? Action Mode : load")

	syncDir := ".sync_collections"
	if _, err := os.Stat(syncDir); os.IsNotExist(err) {
		os.MkdirAll(syncDir, 0755)
	}

	entries, err := os.ReadDir(syncDir)
	if err != nil {
		return fmt.Errorf("error reading sync_collections: %v", err)
	}

	if len(entries) == 0 {
		fmt.Println("No saved configs found in .sync_collections")
		// Give option to go back
		backPrompt := promptui.Select{
			Label: "What would you like to do?",
			Items: []string{"Back to main menu"},
		}
		_, backResult, err := backPrompt.Run()
		if err != nil {
			return err
		}
		if backResult == "Back to main menu" {
			return fmt.Errorf("back_selected")
		}
		return nil
	}

	var items []string
	for i, entry := range entries {
		if entry.IsDir() {
			items = append(items, fmt.Sprintf("%d) %s", i+1, entry.Name()))
		}
	}
	items = append(items, "Back")

	prompt := promptui.Select{
		Label: "? Display data saved",
		Items: items,
	}

	_, result, err := prompt.Run()
	if err != nil {
		return err
	}

	if result == "Back" {
		return fmt.Errorf("back_selected") // Special error for back selection
	}

	// Extract folder name
	parts := strings.Split(result, ") ")
	if len(parts) < 2 {
		return fmt.Errorf("invalid selection")
	}
	folderName := parts[1]

	// Check if make-sync.yaml exists in subfolder
	configPath := filepath.Join(syncDir, folderName, "make-sync.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("config file not found in %s", folderName)
	}

	// Copy to current dir
	destPath := "make-sync.yaml"
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("error reading config: %v", err)
	}

	err = os.WriteFile(destPath, data, 0644)
	if err != nil {
		return fmt.Errorf("error writing config: %v", err)
	}

	fmt.Printf("Config loaded from %s\n", configPath)

	// Add to history
	cwd, _ := os.Getwd()
	history.AddPath(cwd)

	return nil
}

func showDirectAccessMenu(loadedCfg *config.Config) bool {
	// Use the already loaded configuration
	cfg := loadedCfg

	// Create menu items from ssh_commands
	var items []string
	for _, sshCmd := range cfg.DirectAccess.SSHCommands {
		items = append(items, sshCmd.AccessName)
	}

	// Add additional static options
	items = append(items, "console :: Open Console")
	items = append(items, "devsync :: Open Devsync")
	items = append(items, "clean :: Git clean up")
	items = append(items, "Restart")

	prompt := promptui.Select{
		Label: "Direct Access List",
		Items: items,
	}

	_, result, err := prompt.Run()
	if err != nil {
		fmt.Printf("Prompt failed %v\n", err)
		return false
	}

	fmt.Printf("Selected: %s\n", result)

	// Handle SSH command execution
	for _, sshCmd := range cfg.DirectAccess.SSHCommands {
		if sshCmd.AccessName == result {
			fmt.Printf("Executing: %s\n", sshCmd.Command)

			// Parse SSH command to get host name
			hostName, err := parseSSHCommand(sshCmd.Command)
			if err != nil {
				fmt.Printf("❌ Error parsing SSH command: %v\n", err)
				return true // Continue to menu on error
			}

			fmt.Printf("🔍 SSH Host: %s\n", hostName)

			// Generate temporary SSH config
			err = generateSSHTempConfig(cfg, hostName)
			if err != nil {
				fmt.Printf("❌ Error generating SSH temp config: %v\n", err)
				return true // Continue to menu on error
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
			return true // Continue to menu after SSH session
		}
	}

	// Handle other static options
	switch result {
	case "console :: Open Console":
		fmt.Println("Opening console...")
		// TODO: Implement console opening
		return true
	case "devsync :: Open Devsync":
		fmt.Println("Opening devsync...")
		err := devsync.RunDevSync(cfg)
		if err != nil {
			fmt.Printf("❌ DevSync error: %v\n", err)
		}
		return true
	case "clean :: Git clean up":
		fmt.Println("Running git clean up...")
		// TODO: Implement git clean up
		return true
	case "Restart":
		fmt.Println("Restarting...")
		// TODO: Implement restart
		return true
	}

	// Default: continue to menu
	return true
}

var dataCmd = &cobra.Command{
	Use:   "data",
	Short: "Manage saved config data",
	Long:  `Save, load, or delete saved configuration data.`,
	Run: func(cmd *cobra.Command, args []string) {
		cwd, _ := os.Getwd()
		fmt.Printf("You are in: %s\n", cwd)
		fmt.Println("Initialize Bootstrap Is Done!")
		fmt.Printf("process.execPath :: %s\n", os.Args[0])
		fmt.Printf("process.execPath dirname :: %s\n", filepath.Dir(os.Args[0]))
		fmt.Printf("process.execPath basename :: %s\n", filepath.Base(os.Args[0]))

		// Validate and render configuration before proceeding
		fmt.Println("🔍 Validating and rendering configuration...")
		_, err := config.LoadAndRenderConfig()
		if err != nil {
			fmt.Printf("❌ Configuration validation/rendering failed:\n%v\n", err)
			fmt.Println("💡 Please fix the configuration issues or run 'make-sync init' to recreate the config")
			return
		}
		fmt.Println("✅ Configuration is valid and rendered!")

		for {
			prompt := promptui.Select{
				Label: "? Action Mode",
				Items: []string{"save", "load", "delete", "exit"},
			}

			_, result, err := prompt.Run()
			if err != nil {
				fmt.Printf("Prompt failed %v\n", err)
				return
			}

			if result == "save" {
				err := showSaveMenu()
				if err != nil {
					if err.Error() == "back_selected" {
						// Back selected, continue to main menu
						continue
					}
					fmt.Printf("Error saving config: %v\n", err)
					continue
				} else {
					// Save successful, exit
					return
				}
			} else if result == "load" {
				err := showLoadMenu()
				if err != nil {
					if err.Error() == "back_selected" {
						// Back selected, continue to main menu
						continue
					}
					fmt.Printf("Error loading config: %v\n", err)
					continue
				} else {
					// Load successful, exit
					return
				}
			} else if result == "delete" {
				err := showDeleteMenu()
				if err != nil {
					if err.Error() == "back_selected" {
						// Back selected, continue to main menu
						continue
					}
					fmt.Printf("Error deleting config: %v\n", err)
					continue
				}
			} else if result == "exit" {
				fmt.Println("Goodbye!")
				return
			}
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

		// Run devsync
		err = devsync.RunDevSync(cfg)
		if err != nil {
			fmt.Printf("❌ DevSync error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(dataCmd)
	rootCmd.AddCommand(devsyncCmd)
}

func showSaveMenu() error {
	fmt.Println("? Display data saved")

	syncDir := ".sync_collections"
	if _, err := os.Stat(syncDir); os.IsNotExist(err) {
		os.MkdirAll(syncDir, 0755)
	}

	entries, err := os.ReadDir(syncDir)
	if err != nil {
		return fmt.Errorf("error reading sync_collections: %v", err)
	}

	var items []string
	for i, entry := range entries {
		if entry.IsDir() {
			items = append(items, fmt.Sprintf("%d) %s", i+1, entry.Name()))
		}
	}
	items = append(items, "New file")
	items = append(items, "Back")

	prompt := promptui.Select{
		Label: "? Display data saved",
		Items: items,
	}

	_, result, err := prompt.Run()
	if err != nil {
		return err
	}

	if result == "Back" {
		return fmt.Errorf("back_selected") // Special error for back selection
	}

	if result == "New file" {
		// Create new config
		namePrompt := promptui.Prompt{
			Label: "Enter config name",
		}

		configName, err := namePrompt.Run()
		if err != nil {
			return err
		}

		if configName == "" {
			return fmt.Errorf("config name cannot be empty")
		}

		// Check if config already exists
		configPath := filepath.Join(syncDir, configName)
		if _, err := os.Stat(configPath); !os.IsNotExist(err) {
			return fmt.Errorf("config '%s' already exists", configName)
		}

		// Create directory
		err = os.MkdirAll(configPath, 0755)
		if err != nil {
			return fmt.Errorf("error creating config directory: %v", err)
		}

		// Copy current make-sync.yaml if it exists
		srcPath := "make-sync.yaml"
		destPath := filepath.Join(configPath, "make-sync.yaml")

		if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return fmt.Errorf("error reading current config: %v", err)
			}

			err = os.WriteFile(destPath, data, 0644)
			if err != nil {
				return fmt.Errorf("error saving config: %v", err)
			}
		} else {
			// No current config to save
			return fmt.Errorf("no make-sync.yaml file found in current directory - please create config first")
		}

		fmt.Printf("Config saved as '%s'\n", configName)
		return nil
	}

	// Update existing config
	parts := strings.Split(result, ") ")
	if len(parts) < 2 {
		return fmt.Errorf("invalid selection")
	}
	folderName := parts[1]

	configPath := filepath.Join(syncDir, folderName)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("config '%s' does not exist", folderName)
	}

	// Copy current make-sync.yaml
	srcPath := "make-sync.yaml"
	destPath := filepath.Join(configPath, "make-sync.yaml")

	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		return fmt.Errorf("no current config file to save")
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("error reading current config: %v", err)
	}

	err = os.WriteFile(destPath, data, 0644)
	if err != nil {
		return fmt.Errorf("error updating config: %v", err)
	}

	fmt.Printf("Config '%s' updated\n", folderName)
	return nil
}

func showDeleteMenu() error {
	fmt.Println("? Display data saved")

	syncDir := ".sync_collections"
	if _, err := os.Stat(syncDir); os.IsNotExist(err) {
		os.MkdirAll(syncDir, 0755)
	}

	entries, err := os.ReadDir(syncDir)
	if err != nil {
		return fmt.Errorf("error reading sync_collections: %v", err)
	}

	if len(entries) == 0 {
		fmt.Println("No saved configs found")
		return nil
	}

	var items []string
	for i, entry := range entries {
		if entry.IsDir() {
			items = append(items, fmt.Sprintf("%d) %s", i+1, entry.Name()))
		}
	}
	items = append(items, "Back")

	prompt := promptui.Select{
		Label: "? Display data saved",
		Items: items,
	}

	_, result, err := prompt.Run()
	if err != nil {
		return err
	}

	if result == "Back" {
		return fmt.Errorf("back_selected") // Special error for back selection
	}

	parts := strings.Split(result, ") ")
	if len(parts) < 2 {
		return fmt.Errorf("invalid selection")
	}
	folderName := parts[1]

	configPath := filepath.Join(syncDir, folderName)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("config '%s' does not exist", folderName)
	}

	// Confirm deletion
	confirmPrompt := promptui.Prompt{
		Label: fmt.Sprintf("Delete config '%s'? (y/N)", folderName),
	}

	confirm, err := confirmPrompt.Run()
	if err != nil {
		return err
	}

	if strings.ToLower(confirm) != "y" && strings.ToLower(confirm) != "yes" {
		fmt.Println("Deletion cancelled")
		return nil
	}

	err = os.RemoveAll(configPath)
	if err != nil {
		return fmt.Errorf("error deleting config: %v", err)
	}

	fmt.Printf("Config '%s' deleted\n", folderName)
	return nil
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
			os.Remove(syncTempDir)
		}
	}

	// Find matching SSH config
	var matchedConfig *config.SSHConfig
	for _, sshCfg := range cfg.DirectAccess.SSHConfigs {
		if sshCfg.Host == hostName {
			matchedConfig = &sshCfg
			break
		}
	}

	if matchedConfig == nil {
		return fmt.Errorf("SSH config not found for host: %s", hostName)
	}

	// Create .sync_temp/.ssh directory
	err := os.MkdirAll(sshDir, 0755)
	if err != nil {
		return fmt.Errorf("error creating SSH temp directory: %v", err)
	}

	// Generate SSH config content
	var configLines []string

	configLines = append(configLines, fmt.Sprintf("Host %s", matchedConfig.Host))

	if matchedConfig.HostName != "" {
		configLines = append(configLines, fmt.Sprintf("    HostName %s", matchedConfig.HostName))
	}

	if matchedConfig.User != "" {
		configLines = append(configLines, fmt.Sprintf("    User %s", matchedConfig.User))
	}

	if matchedConfig.Port != "" {
		configLines = append(configLines, fmt.Sprintf("    Port %s", matchedConfig.Port))
	}

	if matchedConfig.IdentityFile != "" {
		configLines = append(configLines, fmt.Sprintf("    IdentityFile %s", matchedConfig.IdentityFile))
	}

	if matchedConfig.RequestTty != "" {
		configLines = append(configLines, fmt.Sprintf("    RequestTTY %s", matchedConfig.RequestTty))
	}

	if matchedConfig.StrictHostKey != "" {
		configLines = append(configLines, fmt.Sprintf("    StrictHostKeyChecking %s", matchedConfig.StrictHostKey))
	}

	if matchedConfig.RemoteCommand != "" {
		configLines = append(configLines, fmt.Sprintf("    RemoteCommand %s", matchedConfig.RemoteCommand))
	}

	if matchedConfig.ProxyCommand != "" {
		configLines = append(configLines, fmt.Sprintf("    ProxyCommand %s", matchedConfig.ProxyCommand))
	}

	if matchedConfig.ServerAliveInt != "" {
		configLines = append(configLines, fmt.Sprintf("    ServerAliveInterval %s", matchedConfig.ServerAliveInt))
	}

	if matchedConfig.ServerAliveCnt != "" {
		configLines = append(configLines, fmt.Sprintf("    ServerAliveCountMax %s", matchedConfig.ServerAliveCnt))
	}

	// Write to .sync_temp/.ssh/config file
	content := strings.Join(configLines, "\n") + "\n"
	err = os.WriteFile(configPath, []byte(content), 0644)
	if err != nil {
		return fmt.Errorf("error writing SSH temp config: %v", err)
	}

	fmt.Printf("✅ Generated SSH temp config: %s\n", configPath)
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
