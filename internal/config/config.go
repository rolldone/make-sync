package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const ConfigFileName = "make-sync.yaml"

type Config struct {
	ResetCache     bool                   `yaml:"reset_cache"`
	SyncCollection SyncCollection         `yaml:"sync_collection"`
	Var            map[string]interface{} `yaml:"var,omitempty"`
	ProjectName    string                 `yaml:"project_name"`
	Username       string                 `yaml:"username"`
	PrivateKey     string                 `yaml:"privateKey"`
	Host           string                 `yaml:"host"`
	Port           string                 `yaml:"port"`
	LocalPath      string                 `yaml:"localPath"`
	RemotePath     string                 `yaml:"remotePath"`
	Devsync        Devsync                `yaml:"devsync"`
	TriggerPerm    TriggerPermission      `yaml:"trigger_permission"`
	DirectAccess   DirectAccess           `yaml:"direct_access"`
}

type SyncCollection struct {
	Src   string   `yaml:"src"`
	Files []string `yaml:"files"`
}

type Devsync struct {
	OSTarget       string   `yaml:"os_target"`
	Auth           Auth     `yaml:"auth"`
	Ignores        []string `yaml:"ignores"`
	AgentWatchs    []string `yaml:"agent_watchs"`
	ManualTransfer []string `yaml:"manual_transfer"`
	Script         Script   `yaml:"script"`
}

type Auth struct {
	Username   string `yaml:"username"`
	PrivateKey string `yaml:"privateKey"`
	Host       string `yaml:"host"`
	Port       string `yaml:"port"`
	LocalPath  string `yaml:"localPath"`
	RemotePath string `yaml:"remotePath"`
}

type Script struct {
	Local  ScriptSection `yaml:"local"`
	Remote ScriptSection `yaml:"remote"`
}

type ScriptSection struct {
	OnReady  string   `yaml:"on_ready"`
	OnStop   string   `yaml:"on_stop"`
	Commands []string `yaml:"commands,omitempty"`
}

type TriggerPermission struct {
	UnlinkFolder bool `yaml:"unlink_folder"`
	Unlink       bool `yaml:"unlink"`
	Change       bool `yaml:"change"`
	Add          bool `yaml:"add"`
}

type DirectAccess struct {
	ConfigFile  string       `yaml:"config_file"`
	SSHConfigs  []SSHConfig  `yaml:"ssh_configs"`
	SSHCommands []SSHCommand `yaml:"ssh_commands"`
}

type SSHConfig struct {
	Host           string `yaml:"Host"`
	HostName       string `yaml:"HostName"`
	User           string `yaml:"User"`
	Port           string `yaml:"Port"`
	RequestTty     string `yaml:"RequestTty"`
	IdentityFile   string `yaml:"IdentityFile"`
	StrictHostKey  string `yaml:"StrictHostKeyChecking"`
	RemoteCommand  string `yaml:"RemoteCommand"`
	ProxyCommand   string `yaml:"ProxyCommand,omitempty"`
	ServerAliveInt string `yaml:"ServerAliveInterval"`
	ServerAliveCnt string `yaml:"ServerAliveCountMax"`
}

type SSHCommand struct {
	AccessName string `yaml:"access_name"`
	Command    string `yaml:"command"`
}

// TemplateConfig represents the structure of template.yaml
type TemplateConfig struct {
	ResetCache     bool                   `yaml:"reset_cache"`
	SyncCollection TemplateSyncCollection `yaml:"sync_collection"`
	ProjectName    string                 `yaml:"project_name"`
	Username       string                 `yaml:"username"`
	PrivateKey     string                 `yaml:"private_key"`
	Host           string                 `yaml:"host"`
	Port           string                 `yaml:"port"`
	LocalPath      string                 `yaml:"local_path"`
	RemotePath     string                 `yaml:"remote_path"`
	Devsync        TemplateDevsync        `yaml:"devsync"`
	TriggerPerm    TemplateTriggerPerm    `yaml:"trigger_permission"`
	DirectAccess   TemplateDirectAccess   `yaml:"direct_access"`
}

type TemplateSyncCollection struct {
	Src   string   `yaml:"src"`
	Files []string `yaml:"files"`
}

type TemplateDevsync struct {
	OSTarget       string         `yaml:"os_target"`
	Auth           TemplateAuth   `yaml:"auth"`
	Ignores        []string       `yaml:"ignores"`
	AgentWatchs    []string       `yaml:"agent_watchs"`
	ManualTransfer []string       `yaml:"manual_transfer"`
	Script         TemplateScript `yaml:"script"`
}

type TemplateAuth struct {
	Username   string `yaml:"username"`
	PrivateKey string `yaml:"privateKey"`
	Host       string `yaml:"host"`
	Port       string `yaml:"port"`
	LocalPath  string `yaml:"localPath"`
	RemotePath string `yaml:"remotePath"`
}

type TemplateScript struct {
	Local  TemplateScriptSection `yaml:"local"`
	Remote TemplateScriptSection `yaml:"remote"`
}

type TemplateScriptSection struct {
	OnReady  string   `yaml:"on_ready"`
	OnStop   string   `yaml:"on_stop"`
	Commands []string `yaml:"commands,omitempty"`
}

type TemplateTriggerPerm struct {
	UnlinkFolder bool `yaml:"unlink_folder"`
	Unlink       bool `yaml:"unlink"`
	Change       bool `yaml:"change"`
	Add          bool `yaml:"add"`
}

type TemplateSSHConfig struct {
	Host           string `yaml:"Host"`
	HostName       string `yaml:"HostName"`
	User           string `yaml:"User"`
	Port           string `yaml:"Port"`
	RequestTty     string `yaml:"RequestTty"`
	IdentityFile   string `yaml:"IdentityFile"`
	StrictHostKey  string `yaml:"StrictHostKeyChecking"`
	RemoteCommand  string `yaml:"RemoteCommand"`
	ProxyCommand   string `yaml:"ProxyCommand,omitempty"`
	ServerAliveInt string `yaml:"ServerAliveInterval"`
	ServerAliveCnt string `yaml:"ServerAliveCountMax"`
}

type TemplateDirectAccess struct {
	ConfigFile  string              `yaml:"config_file"`
	SSHConfigs  []TemplateSSHConfig `yaml:"ssh_configs"`
	SSHCommands []SSHCommand        `yaml:"ssh_commands"`
}

// convertTemplateSSHToSSH converts TemplateSSHConfig to SSHConfig
func convertTemplateSSHToSSH(templateSSH TemplateSSHConfig) SSHConfig {
	var sshConfig SSHConfig
	sshConfig.Host = templateSSH.Host
	sshConfig.HostName = templateSSH.HostName
	sshConfig.User = templateSSH.User
	sshConfig.Port = templateSSH.Port
	sshConfig.RequestTty = templateSSH.RequestTty
	sshConfig.IdentityFile = templateSSH.IdentityFile
	sshConfig.StrictHostKey = templateSSH.StrictHostKey
	sshConfig.RemoteCommand = templateSSH.RemoteCommand
	sshConfig.ProxyCommand = templateSSH.ProxyCommand
	sshConfig.ServerAliveInt = templateSSH.ServerAliveInt
	sshConfig.ServerAliveCnt = templateSSH.ServerAliveCnt
	return sshConfig
}

// MapTemplateToConfig converts TemplateConfig to Config
func MapTemplateToConfig(template TemplateConfig) Config {
	config := Config{
		ResetCache:  template.ResetCache,
		ProjectName: template.ProjectName,
		Username:    template.Username,
		PrivateKey:  template.PrivateKey,
		Host:        template.Host,
		Port:        template.Port,
		LocalPath:   template.LocalPath,
		RemotePath:  template.RemotePath,
	}

	// Map SyncCollection
	config.SyncCollection = SyncCollection{
		Src:   template.SyncCollection.Src,
		Files: template.SyncCollection.Files,
	}

	// Map Devsync
	config.Devsync = Devsync{
		OSTarget: template.Devsync.OSTarget,
		Auth: Auth{
			Username:   template.Devsync.Auth.Username,
			PrivateKey: template.Devsync.Auth.PrivateKey,
			Host:       template.Devsync.Auth.Host,
			Port:       template.Devsync.Auth.Port,
			LocalPath:  template.Devsync.Auth.LocalPath,
			RemotePath: template.Devsync.Auth.RemotePath,
		},
		Ignores:        template.Devsync.Ignores,
		AgentWatchs:    template.Devsync.AgentWatchs,
		ManualTransfer: template.Devsync.ManualTransfer,
	}

	// Map Script
	config.Devsync.Script = Script{
		Local: ScriptSection{
			OnReady:  template.Devsync.Script.Local.OnReady,
			OnStop:   template.Devsync.Script.Local.OnStop,
			Commands: template.Devsync.Script.Local.Commands,
		},
		Remote: ScriptSection{
			OnReady:  template.Devsync.Script.Remote.OnReady,
			OnStop:   template.Devsync.Script.Remote.OnStop,
			Commands: template.Devsync.Script.Remote.Commands,
		},
	}

	// Map TriggerPerm
	config.TriggerPerm = TriggerPermission{
		UnlinkFolder: template.TriggerPerm.UnlinkFolder,
		Unlink:       template.TriggerPerm.Unlink,
		Change:       template.TriggerPerm.Change,
		Add:          template.TriggerPerm.Add,
	}

	// Map DirectAccess
	config.DirectAccess = DirectAccess{
		ConfigFile:  template.DirectAccess.ConfigFile,
		SSHCommands: template.DirectAccess.SSHCommands,
	}

	// Map SSHConfigs
	config.DirectAccess.SSHConfigs = make([]SSHConfig, len(template.DirectAccess.SSHConfigs))
	for i, templateSSH := range template.DirectAccess.SSHConfigs {
		config.DirectAccess.SSHConfigs[i] = convertTemplateSSHToSSH(templateSSH)
	}

	return config
}

// ValidateConfig validates the configuration for required fields and file paths
func ValidateConfig(cfg *Config) error {
	var validationErrors []string

	// Validate required string fields
	if strings.TrimSpace(cfg.ProjectName) == "" {
		validationErrors = append(validationErrors, "project_name cannot be empty")
	}

	if strings.TrimSpace(cfg.Username) == "" {
		validationErrors = append(validationErrors, "username cannot be empty")
	}

	if strings.TrimSpace(cfg.Host) == "" {
		validationErrors = append(validationErrors, "host cannot be empty")
	}

	if strings.TrimSpace(cfg.Port) == "" {
		validationErrors = append(validationErrors, "port cannot be empty")
	} else {
		// Validate port is a valid number
		if port, err := strconv.Atoi(cfg.Port); err != nil || port <= 0 || port > 65535 {
			validationErrors = append(validationErrors, "port must be a valid number between 1-65535")
		}
	}

	if strings.TrimSpace(cfg.RemotePath) == "" {
		validationErrors = append(validationErrors, "remotePath cannot be empty")
	}

	// Validate private key file exists (if not empty)
	if strings.TrimSpace(cfg.PrivateKey) != "" {
		if _, err := os.Stat(cfg.PrivateKey); os.IsNotExist(err) {
			validationErrors = append(validationErrors, fmt.Sprintf("private key file does not exist: %s", cfg.PrivateKey))
		}
	}

	// Validate local path exists
	if strings.TrimSpace(cfg.LocalPath) != "" {
		if _, err := os.Stat(cfg.LocalPath); os.IsNotExist(err) {
			validationErrors = append(validationErrors, fmt.Sprintf("local path does not exist: %s", cfg.LocalPath))
		}
	}

	// Validate sync collection path
	if strings.TrimSpace(cfg.SyncCollection.Src) != "" {
		// Create directory if it doesn't exist
		if err := os.MkdirAll(cfg.SyncCollection.Src, 0755); err != nil {
			validationErrors = append(validationErrors, fmt.Sprintf("cannot create sync collection directory: %s", cfg.SyncCollection.Src))
		}
	}

	// Validate SSH configs
	for i, sshConfig := range cfg.DirectAccess.SSHConfigs {
		if strings.TrimSpace(sshConfig.Host) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: Host cannot be empty", i+1))
		}

		if strings.TrimSpace(sshConfig.HostName) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: HostName cannot be empty", i+1))
		}

		if strings.TrimSpace(sshConfig.User) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: User cannot be empty", i+1))
		}

		// Validate SSH port (skip validation for references starting with =)
		if strings.TrimSpace(sshConfig.Port) != "" && !strings.HasPrefix(sshConfig.Port, "=") {
			if port, err := strconv.Atoi(sshConfig.Port); err != nil || port <= 0 || port > 65535 {
				validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: Port must be a valid number between 1-65535", i+1))
			}
		}

		// Validate identity file exists (if specified and not a reference)
		if strings.TrimSpace(sshConfig.IdentityFile) != "" && !strings.HasPrefix(sshConfig.IdentityFile, "=") {
			if _, err := os.Stat(sshConfig.IdentityFile); os.IsNotExist(err) {
				validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: Identity file does not exist: %s", i+1, sshConfig.IdentityFile))
			}
		}

		// Validate HostName (skip validation for references starting with =)
		if !strings.HasPrefix(sshConfig.HostName, "=") && strings.TrimSpace(sshConfig.HostName) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: HostName cannot be empty", i+1))
		}

		// Validate User (skip validation for references starting with =)
		if !strings.HasPrefix(sshConfig.User, "=") && strings.TrimSpace(sshConfig.User) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: User cannot be empty", i+1))
		}
	}

	// Validate SSH commands
	for i, sshCmd := range cfg.DirectAccess.SSHCommands {
		if strings.TrimSpace(sshCmd.AccessName) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH command %d: access_name cannot be empty", i+1))
		}

		if strings.TrimSpace(sshCmd.Command) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH command %d: command cannot be empty", i+1))
		}
	}

	// Validate devsync settings
	if strings.TrimSpace(cfg.Devsync.OSTarget) == "" {
		validationErrors = append(validationErrors, "devsync.os_target cannot be empty")
	}

	// If there are validation errors, return them
	if len(validationErrors) > 0 {
		return fmt.Errorf("configuration validation failed:\n%s", strings.Join(validationErrors, "\n"))
	}

	return nil
}

// LoadAndValidateConfig loads and validates the configuration
func LoadAndValidateConfig() (*Config, error) {
	if !ConfigExists() {
		return nil, errors.New("make-sync.yaml not found. Please run 'make-sync init' first")
	}

	data, err := os.ReadFile(ConfigFileName)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %v", err)
	}

	var cfg Config
	err = yaml.Unmarshal(data, &cfg)
	if err != nil {
		return nil, fmt.Errorf("error parsing config file: %v", err)
	}

	// Validate the configuration
	err = ValidateConfig(&cfg)
	if err != nil {
		return nil, err
	}

	return &cfg, nil
}

// RenderTemplateVariables renders template variables in the configuration
// Replaces references like =username, =host, =port, etc. with actual values
func RenderTemplateVariables(cfg *Config) error {
	// Read the current config file as raw text
	data, err := os.ReadFile(ConfigFileName)
	if err != nil {
		return fmt.Errorf("error reading config file for template rendering: %v", err)
	}

	// Convert to string for processing
	configText := string(data)

	// Create a map of template variables
	templateVars := map[string]string{
		"username":     cfg.Username,
		"host":         cfg.Host,
		"port":         cfg.Port,
		"privateKey":   cfg.PrivateKey,
		"remotePath":   cfg.RemotePath,
		"localPath":    cfg.LocalPath,
		"project_name": cfg.ProjectName,
	}

	// Replace all template variables
	for key, value := range templateVars {
		// Replace =key with actual value
		pattern := fmt.Sprintf("=%s", key)
		configText = strings.ReplaceAll(configText, pattern, value)
	}

	// Write back the rendered config
	err = os.WriteFile(ConfigFileName, []byte(configText), 0644)
	if err != nil {
		return fmt.Errorf("error writing rendered config: %v", err)
	}

	return nil
}

// LoadAndRenderConfig loads config, validates it, and renders template variables in memory
func LoadAndRenderConfig() (*Config, error) {
	// First load and validate
	cfg, err := LoadAndValidateConfig()
	if err != nil {
		return nil, err
	}

	// Then render template variables in memory (advanced)
	renderedCfg, err := RenderTemplateVariablesInMemory(cfg)
	if err != nil {
		return nil, fmt.Errorf("template rendering failed: %v", err)
	}

	return renderedCfg, nil
}

// RenderTemplateVariablesInMemory renders template variables without writing to file
func RenderTemplateVariablesInMemory(cfg *Config) (*Config, error) {
	// Create a copy of the config for rendering
	renderedCfg := *cfg

	// Create advanced renderer
	renderer := NewAdvancedTemplateRenderer(&renderedCfg)

	renderCount := 0

	// Render SSH configs
	for i := range renderedCfg.DirectAccess.SSHConfigs {
		sshConfig := &renderedCfg.DirectAccess.SSHConfigs[i]

		// Render each field that might contain template variables
		if strings.HasPrefix(sshConfig.HostName, "=") {
			oldValue := sshConfig.HostName
			sshConfig.HostName = renderer.RenderComplexTemplates(sshConfig.HostName)
			fmt.Printf("🔧 Rendered SSH config[%d].HostName: %s → %s\n", i, oldValue, sshConfig.HostName)
			renderCount++
		}
		if strings.HasPrefix(sshConfig.User, "=") {
			oldValue := sshConfig.User
			sshConfig.User = renderer.RenderComplexTemplates(sshConfig.User)
			fmt.Printf("🔧 Rendered SSH config[%d].User: %s → %s\n", i, oldValue, sshConfig.User)
			renderCount++
		}
		if strings.HasPrefix(sshConfig.Port, "=") {
			oldValue := sshConfig.Port
			sshConfig.Port = renderer.RenderComplexTemplates(sshConfig.Port)
			fmt.Printf("🔧 Rendered SSH config[%d].Port: %s → %s\n", i, oldValue, sshConfig.Port)
			renderCount++
		}
		if strings.HasPrefix(sshConfig.IdentityFile, "=") {
			oldValue := sshConfig.IdentityFile
			sshConfig.IdentityFile = renderer.RenderComplexTemplates(sshConfig.IdentityFile)
			fmt.Printf("🔧 Rendered SSH config[%d].IdentityFile: %s → %s\n", i, oldValue, sshConfig.IdentityFile)
			renderCount++
		}
		if strings.Contains(sshConfig.RemoteCommand, "=") {
			oldValue := sshConfig.RemoteCommand
			sshConfig.RemoteCommand = renderer.RenderComplexTemplates(sshConfig.RemoteCommand)
			fmt.Printf("🔧 Rendered SSH config[%d].RemoteCommand: %s → %s\n", i, oldValue, sshConfig.RemoteCommand)
			renderCount++
		}
		if strings.Contains(sshConfig.ProxyCommand, "=") {
			oldValue := sshConfig.ProxyCommand
			sshConfig.ProxyCommand = renderer.RenderComplexTemplates(sshConfig.ProxyCommand)
			fmt.Printf("🔧 Rendered SSH config[%d].ProxyCommand: %s → %s\n", i, oldValue, sshConfig.ProxyCommand)
			renderCount++
		}
	}

	// Render Devsync Auth fields
	if strings.HasPrefix(renderedCfg.Devsync.Auth.Username, "=") {
		oldValue := renderedCfg.Devsync.Auth.Username
		renderedCfg.Devsync.Auth.Username = renderer.RenderComplexTemplates(renderedCfg.Devsync.Auth.Username)
		fmt.Printf("🔧 Rendered Devsync.Auth.Username: %s → %s\n", oldValue, renderedCfg.Devsync.Auth.Username)
		renderCount++
	}
	if strings.HasPrefix(renderedCfg.Devsync.Auth.PrivateKey, "=") {
		oldValue := renderedCfg.Devsync.Auth.PrivateKey
		renderedCfg.Devsync.Auth.PrivateKey = renderer.RenderComplexTemplates(renderedCfg.Devsync.Auth.PrivateKey)
		fmt.Printf("🔧 Rendered Devsync.Auth.PrivateKey: %s → %s\n", oldValue, renderedCfg.Devsync.Auth.PrivateKey)
		renderCount++
	}
	if strings.HasPrefix(renderedCfg.Devsync.Auth.Host, "=") {
		oldValue := renderedCfg.Devsync.Auth.Host
		renderedCfg.Devsync.Auth.Host = renderer.RenderComplexTemplates(renderedCfg.Devsync.Auth.Host)
		fmt.Printf("🔧 Rendered Devsync.Auth.Host: %s → %s\n", oldValue, renderedCfg.Devsync.Auth.Host)
		renderCount++
	}
	if strings.HasPrefix(renderedCfg.Devsync.Auth.Port, "=") {
		oldValue := renderedCfg.Devsync.Auth.Port
		renderedCfg.Devsync.Auth.Port = renderer.RenderComplexTemplates(renderedCfg.Devsync.Auth.Port)
		fmt.Printf("🔧 Rendered Devsync.Auth.Port: %s → %s\n", oldValue, renderedCfg.Devsync.Auth.Port)
		renderCount++
	}
	if strings.HasPrefix(renderedCfg.Devsync.Auth.LocalPath, "=") {
		oldValue := renderedCfg.Devsync.Auth.LocalPath
		renderedCfg.Devsync.Auth.LocalPath = renderer.RenderComplexTemplates(renderedCfg.Devsync.Auth.LocalPath)
		fmt.Printf("🔧 Rendered Devsync.Auth.LocalPath: %s → %s\n", oldValue, renderedCfg.Devsync.Auth.LocalPath)
		renderCount++
	}
	if strings.HasPrefix(renderedCfg.Devsync.Auth.RemotePath, "=") {
		oldValue := renderedCfg.Devsync.Auth.RemotePath
		renderedCfg.Devsync.Auth.RemotePath = renderer.RenderComplexTemplates(renderedCfg.Devsync.Auth.RemotePath)
		fmt.Printf("🔧 Rendered Devsync.Auth.RemotePath: %s → %s\n", oldValue, renderedCfg.Devsync.Auth.RemotePath)
		renderCount++
	}

	// Render SSH commands
	for i := range renderedCfg.DirectAccess.SSHCommands {
		sshCmd := &renderedCfg.DirectAccess.SSHCommands[i]

		// Render access_name and command
		if strings.Contains(sshCmd.AccessName, "=") {
			oldValue := sshCmd.AccessName
			sshCmd.AccessName = renderer.RenderComplexTemplates(sshCmd.AccessName)
			fmt.Printf("🔧 Rendered SSH command[%d].AccessName: %s → %s\n", i, oldValue, sshCmd.AccessName)
			renderCount++
		}
		if strings.Contains(sshCmd.Command, "=") {
			oldValue := sshCmd.Command
			sshCmd.Command = renderer.RenderComplexTemplates(sshCmd.Command)
			fmt.Printf("🔧 Rendered SSH command[%d].Command: %s → %s\n", i, oldValue, sshCmd.Command)
			renderCount++
		}
	}

	if renderCount > 0 {
		fmt.Printf("✅ Template rendering completed: %d references resolved\n", renderCount)
	} else {
		fmt.Println("ℹ️  No template references found in configuration")
	}

	return &renderedCfg, nil
} // AdvancedTemplateRenderer handles complex template rendering with nested properties
type AdvancedTemplateRenderer struct {
	config *Config
}

// NewAdvancedTemplateRenderer creates a new template renderer
func NewAdvancedTemplateRenderer(cfg *Config) *AdvancedTemplateRenderer {
	return &AdvancedTemplateRenderer{config: cfg}
}

// RenderComplexTemplates renders complex template expressions like =field.nested.value
func (r *AdvancedTemplateRenderer) RenderComplexTemplates(text string) string {
	// Find all template expressions starting with =
	re := regexp.MustCompile(`=([a-zA-Z_][a-zA-Z0-9_.[\]]*)`)
	return re.ReplaceAllStringFunc(text, func(match string) string {
		// Remove the = prefix to get the path
		path := strings.TrimPrefix(match, "=")

		// Resolve the nested property
		value, err := r.resolveNestedProperty(path)
		if err != nil {
			// Return original if resolution fails
			return match
		}

		return value
	})
}

// resolveNestedProperty resolves nested properties like "devsync.os_target" or "var.one.two"
func (r *AdvancedTemplateRenderer) resolveNestedProperty(path string) (string, error) {
	parts := strings.Split(path, ".")

	// Start with the root config
	current := reflect.ValueOf(r.config)

	// Handle special case for 'var' field
	if parts[0] == "var" && len(parts) > 1 {
		return r.resolveVarProperty(parts[1:])
	}

	// Handle array access like ssh_configs[0]
	if strings.Contains(parts[0], "[") {
		return r.resolveArrayProperty(parts[0])
	}

	// Navigate through nested properties
	for _, part := range parts {
		if strings.Contains(part, "[") {
			// Handle array access in nested property
			currentIndex := len(parts) - 1
			for i, p := range parts {
				if p == part {
					currentIndex = i
					break
				}
			}
			arrayPath := strings.Join(parts[:currentIndex+1], ".")
			return r.resolveNestedArrayProperty(arrayPath)
		}

		// Get the field value
		current = r.getFieldValue(current, part)
		if !current.IsValid() {
			return "", fmt.Errorf("property not found: %s", part)
		}
	}

	// Convert final value to string
	return r.valueToString(current), nil
}

// resolveVarProperty resolves var.* properties like var.one.two
func (r *AdvancedTemplateRenderer) resolveVarProperty(parts []string) (string, error) {
	if r.config.Var == nil {
		return "", fmt.Errorf("var section not found in config")
	}

	current := r.config.Var

	for i, part := range parts {
		if value, exists := current[part]; exists {
			if i == len(parts)-1 {
				// Last part, return the value
				switch v := value.(type) {
				case string:
					return v, nil
				case int:
					return strconv.Itoa(v), nil
				case float64:
					return strconv.FormatFloat(v, 'f', -1, 64), nil
				case bool:
					return strconv.FormatBool(v), nil
				default:
					return fmt.Sprintf("%v", v), nil
				}
			} else {
				// Not the last part, continue navigating
				if nextMap, ok := value.(map[string]interface{}); ok {
					current = nextMap
				} else {
					return "", fmt.Errorf("cannot navigate into non-map value at %s", strings.Join(parts[:i+1], "."))
				}
			}
		} else {
			return "", fmt.Errorf("var property not found: %s", strings.Join(parts[:i+1], "."))
		}
	}

	return "", fmt.Errorf("unexpected end of var resolution")
}

// resolveArrayProperty resolves array properties like "ssh_configs[0]"
func (r *AdvancedTemplateRenderer) resolveArrayProperty(path string) (string, error) {
	re := regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_]*)\[(\d+)\]$`)
	matches := re.FindStringSubmatch(path)

	if len(matches) != 3 {
		return "", fmt.Errorf("invalid array syntax: %s", path)
	}

	arrayName := matches[1]
	index, _ := strconv.Atoi(matches[2])

	// Get the array field
	arrayValue := r.getFieldValue(reflect.ValueOf(r.config), arrayName)
	if !arrayValue.IsValid() || arrayValue.Kind() != reflect.Slice {
		return "", fmt.Errorf("array not found: %s", arrayName)
	}

	// Check bounds
	if index < 0 || index >= arrayValue.Len() {
		return "", fmt.Errorf("array index out of bounds: %s[%d]", arrayName, index)
	}

	// Get the element
	element := arrayValue.Index(index)
	return r.valueToString(element), nil
}

// resolveNestedArrayProperty resolves nested array properties like "direct_access.ssh_configs[0].host"
func (r *AdvancedTemplateRenderer) resolveNestedArrayProperty(path string) (string, error) {
	// This is a simplified implementation - in production you'd want more robust parsing
	parts := strings.Split(path, ".")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid nested array path: %s", path)
	}

	// For now, return the original path if it's too complex
	return "=" + path, nil
}

// getFieldValue gets a field value from a struct using reflection
func (r *AdvancedTemplateRenderer) getFieldValue(v reflect.Value, fieldName string) reflect.Value {
	if v.Kind() == reflect.Ptr {
		v = v.Elem()
	}

	if v.Kind() != reflect.Struct {
		return reflect.Value{}
	}

	// Try with title case first (for exported fields)
	titleField := strings.ToUpper(fieldName[:1]) + fieldName[1:]
	field := v.FieldByName(titleField)
	if field.IsValid() {
		return field
	}

	// Try with exact case
	field = v.FieldByName(fieldName)
	return field
}

// valueToString converts a reflect.Value to string
func (r *AdvancedTemplateRenderer) valueToString(v reflect.Value) string {
	if !v.IsValid() {
		return ""
	}

	switch v.Kind() {
	case reflect.String:
		return v.String()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(v.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(v.Uint(), 10)
	case reflect.Float32, reflect.Float64:
		return strconv.FormatFloat(v.Float(), 'f', -1, 64)
	case reflect.Bool:
		return strconv.FormatBool(v.Bool())
	default:
		// For complex types, convert to string representation
		if v.CanInterface() {
			return fmt.Sprintf("%v", v.Interface())
		}
		return ""
	}
}

// RenderTemplateVariablesAdvanced renders template variables with advanced nested property support
func RenderTemplateVariablesAdvanced(cfg *Config) error {
	// Read the current config file as raw text
	data, err := os.ReadFile(ConfigFileName)
	if err != nil {
		return fmt.Errorf("error reading config file for template rendering: %v", err)
	}

	// Create advanced renderer
	renderer := NewAdvancedTemplateRenderer(cfg)

	// Render complex templates
	configText := string(data)
	configText = renderer.RenderComplexTemplates(configText)

	// Write back the rendered config
	err = os.WriteFile(ConfigFileName, []byte(configText), 0644)
	if err != nil {
		return fmt.Errorf("error writing rendered config: %v", err)
	}

	return nil
}

func ConfigExists() bool {
	_, err := os.Stat(ConfigFileName)
	return !os.IsNotExist(err)
}

func GetConfigPath() string {
	cwd, _ := os.Getwd()
	return filepath.Join(cwd, ConfigFileName)
}
