package config

import (
	"errors"
	"fmt"
	"make-sync/internal/util"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
	"gopkg.in/yaml.v3"
)

var printer = util.Default

const ConfigFileName = "make-sync.yaml"

type Config struct {
	ResetCache     bool                   `yaml:"reset_cache"`
	SyncCollection SyncCollection         `yaml:"sync_collection"`
	Var            map[string]interface{} `yaml:"var,omitempty"`
	ProjectName    string                 `yaml:"project_name"`
	Username       string                 `yaml:"username"`
	PrivateKey     string                 `yaml:"privateKey"`
	Password       string                 `yaml:"password,omitempty"`
	Host           string                 `yaml:"host"`
	Port           string                 `yaml:"port"`
	LocalPath      string                 `yaml:"localPath"`
	RemotePath     string                 `yaml:"remotePath"`
	Devsync        Devsync                `yaml:"devsync"`
	DirectAccess   DirectAccess           `yaml:"direct_access"`
}

type SyncCollection struct {
	Src   string   `yaml:"src"`
	Files []string `yaml:"files"`
}

type Devsync struct {
	OSTarget       string            `yaml:"os_target"`
	AgentName      string            `yaml:"agent_name,omitempty"` // Unique identifier for agent process
	Auth           Auth              `yaml:"auth"`
	Ignores        []string          `yaml:"ignores"`
	AgentWatchs    []string          `yaml:"agent_watchs"`
	ManualTransfer []string          `yaml:"manual_transfer"`
	Script         Script            `yaml:"script"`
	TriggerPerm    TriggerPermission `yaml:"trigger_permission"`
}

type Auth struct {
	Username   string `yaml:"username"`
	PrivateKey string `yaml:"privateKey"`
	Password   string `yaml:"password,omitempty"`
	Host       string `yaml:"host"`
	Port       string `yaml:"port"`
	LocalPath  string `yaml:"localPath,omitempty"`
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
	ConfigFile  string                   `yaml:"config_file"`
	SSHConfigs  []map[string]interface{} `yaml:"ssh_configs"`
	SSHCommands []SSHCommand             `yaml:"ssh_commands"`
}

type SSHCommand struct {
	AccessName string `yaml:"access_name"`
	Command    string `yaml:"command"`
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
	// if strings.TrimSpace(cfg.LocalPath) != "" {
	// 	if _, err := os.Stat(cfg.LocalPath); os.IsNotExist(err) {
	// 		validationErrors = append(validationErrors, fmt.Sprintf("local path does not exist: %s", cfg.LocalPath))
	// 	}
	// }

	// Validate sync collection path
	if strings.TrimSpace(cfg.SyncCollection.Src) != "" {
		// Create directory if it doesn't exist
		if err := os.MkdirAll(cfg.SyncCollection.Src, 0755); err != nil {
			validationErrors = append(validationErrors, fmt.Sprintf("cannot create sync collection directory: %s", cfg.SyncCollection.Src))
		}
	}

	// Validate SSH configs
	for i, sshConfig := range cfg.DirectAccess.SSHConfigs {
		if host, ok := sshConfig["Host"].(string); !ok || strings.TrimSpace(host) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: Host cannot be empty", i+1))
		}

		if hostName, ok := sshConfig["HostName"].(string); !ok || strings.TrimSpace(hostName) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: HostName cannot be empty", i+1))
		}

		if user, ok := sshConfig["User"].(string); !ok || strings.TrimSpace(user) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: User cannot be empty", i+1))
		}

		// Validate SSH port (skip validation for references starting with =)
		if port, ok := sshConfig["Port"].(string); ok && strings.TrimSpace(port) != "" && !strings.HasPrefix(port, "=") {
			if p, err := strconv.Atoi(port); err != nil || p <= 0 || p > 65535 {
				validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: Port must be a valid number between 1-65535", i+1))
			}
		}

		// Validate identity file exists (if specified and not a reference)
		if identityFile, ok := sshConfig["IdentityFile"].(string); ok && strings.TrimSpace(identityFile) != "" && !strings.HasPrefix(identityFile, "=") {
			if _, err := os.Stat(identityFile); os.IsNotExist(err) {
				validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: Identity file does not exist: %s", i+1, identityFile))
			}
		}

		// Validate HostName (skip validation for references starting with =)
		if hostName, ok := sshConfig["HostName"].(string); ok && !strings.HasPrefix(hostName, "=") && strings.TrimSpace(hostName) == "" {
			validationErrors = append(validationErrors, fmt.Sprintf("SSH config %d: HostName cannot be empty", i+1))
		}

		// Validate User (skip validation for references starting with =)
		if user, ok := sshConfig["User"].(string); ok && !strings.HasPrefix(user, "=") && strings.TrimSpace(user) == "" {
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

	// Read raw config file
	data, err := os.ReadFile(ConfigFileName)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %v", err)
	}

	// Load .env if exists (same dir as config file)
	cfgDir := filepath.Dir(ConfigFileName)
	envMap, _ := loadDotEnvIfExists(cfgDir)

	// Interpolate ${VAR} using OS env first, then .env values
	rendered := interpolateEnv(string(data), envMap)

	var cfg Config
	err = yaml.Unmarshal([]byte(rendered), &cfg)
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
		"password":     cfg.Password,
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

	basPath, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("error getting current working directory: %v", err)
	}
	realPath, err := filepath.EvalSymlinks(basPath)
	if err != nil {
		printer.Println("Error:", err)
		os.Exit(1)
	}
	absWatchPath, err := filepath.Abs(realPath)
	if err != nil {
		return nil, fmt.Errorf("error resolving absolute path: %v", err)
	}
	renderedCfg.Devsync.Auth.LocalPath = absWatchPath
	renderedCfg.LocalPath = absWatchPath

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
		if hostName, ok := (*sshConfig)["HostName"].(string); ok && strings.HasPrefix(hostName, "=") {
			oldValue := hostName
			(*sshConfig)["HostName"] = renderer.RenderComplexTemplates(hostName)
			printer.Printf("🔧 Rendered SSH config[%d].HostName: %s → %s\n", i, oldValue, (*sshConfig)["HostName"])
			renderCount++
		}
		if user, ok := (*sshConfig)["User"].(string); ok && strings.HasPrefix(user, "=") {
			oldValue := user
			(*sshConfig)["User"] = renderer.RenderComplexTemplates(user)
			printer.Printf("🔧 Rendered SSH config[%d].User: %s → %s\n", i, oldValue, (*sshConfig)["User"])
			renderCount++
		}
		if port, ok := (*sshConfig)["Port"].(string); ok && strings.HasPrefix(port, "=") {
			oldValue := port
			(*sshConfig)["Port"] = renderer.RenderComplexTemplates(port)
			printer.Printf("🔧 Rendered SSH config[%d].Port: %s → %s\n", i, oldValue, (*sshConfig)["Port"])
			renderCount++
		}
		if identityFile, ok := (*sshConfig)["IdentityFile"].(string); ok && strings.HasPrefix(identityFile, "=") {
			oldValue := identityFile
			(*sshConfig)["IdentityFile"] = renderer.RenderComplexTemplates(identityFile)
			printer.Printf("🔧 Rendered SSH config[%d].IdentityFile: %s → %s\n", i, oldValue, (*sshConfig)["IdentityFile"])
			renderCount++
		}
		if remoteCommand, ok := (*sshConfig)["RemoteCommand"].(string); ok && strings.Contains(remoteCommand, "=") {
			oldValue := remoteCommand
			(*sshConfig)["RemoteCommand"] = renderer.RenderComplexTemplates(remoteCommand)
			printer.Printf("🔧 Rendered SSH config[%d].RemoteCommand: %s → %s\n", i, oldValue, (*sshConfig)["RemoteCommand"])
			renderCount++
		}
		if proxyCommand, ok := (*sshConfig)["ProxyCommand"].(string); ok && strings.Contains(proxyCommand, "=") {
			oldValue := proxyCommand
			(*sshConfig)["ProxyCommand"] = renderer.RenderComplexTemplates(proxyCommand)
			printer.Printf("🔧 Rendered SSH config[%d].ProxyCommand: %s → %s\n", i, oldValue, (*sshConfig)["ProxyCommand"])
			renderCount++
		}
	}

	// Render Devsync Auth fields
	if strings.HasPrefix(renderedCfg.Devsync.Auth.Username, "=") {
		oldValue := renderedCfg.Devsync.Auth.Username
		renderedCfg.Devsync.Auth.Username = renderer.RenderComplexTemplates(renderedCfg.Devsync.Auth.Username)
		printer.Printf("🔧 Rendered Devsync.Auth.Username: %s → %s\n", oldValue, renderedCfg.Devsync.Auth.Username)
		renderCount++
	}
	if strings.HasPrefix(renderedCfg.Devsync.Auth.PrivateKey, "=") {
		oldValue := renderedCfg.Devsync.Auth.PrivateKey
		renderedCfg.Devsync.Auth.PrivateKey = renderer.RenderComplexTemplates(renderedCfg.Devsync.Auth.PrivateKey)
		printer.Printf("🔧 Rendered Devsync.Auth.PrivateKey: %s → %s\n", oldValue, renderedCfg.Devsync.Auth.PrivateKey)
		renderCount++
	}
	if strings.HasPrefix(renderedCfg.Devsync.Auth.Password, "=") {
		oldValue := renderedCfg.Devsync.Auth.PrivateKey
		renderedCfg.Devsync.Auth.Password = renderer.RenderComplexTemplates(renderedCfg.Devsync.Auth.Password)
		printer.Printf("🔧 Rendered Devsync.Auth.PrivateKey: %s → %s\n", oldValue, renderedCfg.Devsync.Auth.Password)
		renderCount++
	}
	if strings.HasPrefix(renderedCfg.Devsync.Auth.Host, "=") {
		oldValue := renderedCfg.Devsync.Auth.Host
		renderedCfg.Devsync.Auth.Host = renderer.RenderComplexTemplates(renderedCfg.Devsync.Auth.Host)
		printer.Printf("🔧 Rendered Devsync.Auth.Host: %s → %s\n", oldValue, renderedCfg.Devsync.Auth.Host)
		renderCount++
	}
	if strings.HasPrefix(renderedCfg.Devsync.Auth.Port, "=") {
		oldValue := renderedCfg.Devsync.Auth.Port
		renderedCfg.Devsync.Auth.Port = renderer.RenderComplexTemplates(renderedCfg.Devsync.Auth.Port)
		printer.Printf("🔧 Rendered Devsync.Auth.Port: %s → %s\n", oldValue, renderedCfg.Devsync.Auth.Port)
		renderCount++
	}
	if strings.HasPrefix(renderedCfg.Devsync.Auth.LocalPath, "=") {
		oldValue := renderedCfg.Devsync.Auth.LocalPath
		renderedCfg.Devsync.Auth.LocalPath = renderer.RenderComplexTemplates(renderedCfg.Devsync.Auth.LocalPath)
		printer.Printf("🔧 Rendered Devsync.Auth.LocalPath: %s → %s\n", oldValue, renderedCfg.Devsync.Auth.LocalPath)
		renderCount++
	}
	if strings.HasPrefix(renderedCfg.Devsync.Auth.RemotePath, "=") {
		oldValue := renderedCfg.Devsync.Auth.RemotePath
		renderedCfg.Devsync.Auth.RemotePath = renderer.RenderComplexTemplates(renderedCfg.Devsync.Auth.RemotePath)
		printer.Printf("🔧 Rendered Devsync.Auth.RemotePath: %s → %s\n", oldValue, renderedCfg.Devsync.Auth.RemotePath)
		renderCount++
	}

	// Render SSH commands
	for i := range renderedCfg.DirectAccess.SSHCommands {
		sshCmd := &renderedCfg.DirectAccess.SSHCommands[i]

		// Render access_name and command
		if strings.Contains(sshCmd.AccessName, "=") {
			oldValue := sshCmd.AccessName
			sshCmd.AccessName = renderer.RenderComplexTemplates(sshCmd.AccessName)
			printer.Printf("🔧 Rendered SSH command[%d].AccessName: %s → %s\n", i, oldValue, sshCmd.AccessName)
			renderCount++
		}
		if strings.Contains(sshCmd.Command, "=") {
			oldValue := sshCmd.Command
			sshCmd.Command = renderer.RenderComplexTemplates(sshCmd.Command)
			printer.Printf("🔧 Rendered SSH command[%d].Command: %s → %s\n", i, oldValue, sshCmd.Command)
			renderCount++
		}
	}

	if renderCount > 0 {
		printer.Printf("✅ Template rendering completed: %d references resolved\n", renderCount)
	} else {
		printer.Println("ℹ️  No template references found in configuration")
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

// loadDotEnvIfExists attempts to load a .env file from the directory of config
// and returns a map of key->value. If no .env exists or parsing fails, an empty map is returned.
func loadDotEnvIfExists(dir string) (map[string]string, error) {
	envPath := filepath.Join(dir, ".env")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		return map[string]string{}, nil
	}

	m, err := godotenv.Read(envPath)
	if err != nil {
		printer.Printf("⚠️  Failed to parse .env at %s: %v\n", envPath, err)
		return map[string]string{}, err
	}
	return m, nil
}

// interpolateEnv replaces ${VAR} occurrences in the input text. Precedence: OS env > envMap.
// Missing variables are replaced with empty string and a warning is emitted.
func interpolateEnv(input string, envMap map[string]string) string {
	lookup := func(varName string) string {
		if v := os.Getenv(varName); v != "" {
			return v
		}
		if v, ok := envMap[varName]; ok {
			return v
		}
		// warn about missing but return empty
		printer.Printf("⚠️  Environment variable %s not set; using empty string\n", varName)
		return ""
	}

	// Use os.Expand to replace ${VAR} and $VAR
	return os.Expand(input, func(name string) string {
		// os.Expand passes the variable name without braces
		return lookup(name)
	})
}
