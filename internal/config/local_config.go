package config

import (
	"encoding/json"
	"fmt"
	"make-sync/internal/util"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalConfig represents the local .sync_temp/config.json structure
type LocalConfig struct {
	Devsync LocalDevsync `json:"devsync"`
}

type LocalDevsync struct {
	Ignores        []string `json:"ignores"`
	AgentWatchs    []string `json:"agent_watchs"`
	ManualTransfer []string `json:"manual_transfer"`
	WorkingDir     string   `json:"working_dir"`
	AgentName      string   `json:"agent_name,omitempty"` // Unique agent identifier
}

// LocalConfigPath returns the path to .sync_temp/config.json
func LocalConfigPath() string {
	return filepath.Join(".sync_temp", "config.json")
}

// LoadLocalConfig loads the local config from .sync_temp/config.json
func LoadLocalConfig() (*LocalConfig, error) {
	configPath := LocalConfigPath()

	// If config doesn't exist, return empty config
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return &LocalConfig{}, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read local config: %w", err)
	}

	var config LocalConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse local config: %w", err)
	}

	return &config, nil
}

// SaveLocalConfig saves the local config to .sync_temp/config.json
func (lc *LocalConfig) Save() error {
	configPath := LocalConfigPath()

	// Ensure .sync_temp directory exists
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		return fmt.Errorf("failed to create .sync_temp directory: %w", err)
	}

	data, err := json.MarshalIndent(lc, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal local config: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0644); err != nil {
		return fmt.Errorf("failed to write local config: %w", err)
	}

	return nil
}

// generateAgentName creates a unique agent identifier
func generateLocalAgentName() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return fmt.Sprintf("%d%d", time.Now().Unix(), r.Intn(1000))
}

// EnsureAgentName ensures the local config has an agent name, generating one if needed
func (lc *LocalConfig) EnsureAgentName() bool {
	if lc.Devsync.AgentName == "" {
		lc.Devsync.AgentName = generateLocalAgentName()
		util.Default.Printf("🆔 Generated unique agent name: %s\n", lc.Devsync.AgentName)
		return true // Config was modified
	}
	return false // Config unchanged
}

// GetAgentBinaryName returns the unique agent binary name based on local config
func (lc *LocalConfig) GetAgentBinaryName(targetOS string) string {
	if lc.Devsync.AgentName == "" {
		lc.EnsureAgentName()
	}

	if strings.Contains(strings.ToLower(targetOS), "win") {
		return fmt.Sprintf("sync-agent-%s.exe", lc.Devsync.AgentName)
	}
	return fmt.Sprintf("sync-agent-%s", lc.Devsync.AgentName)
}

// GetOrCreateLocalConfig loads existing local config or creates new one with agent name
func GetOrCreateLocalConfig() (*LocalConfig, error) {
	config, err := LoadLocalConfig()
	if err != nil {
		return nil, err
	}

	// Ensure agent name exists
	if config.EnsureAgentName() {
		if err := config.Save(); err != nil {
			return nil, fmt.Errorf("failed to save local config: %w", err)
		}
	}

	return config, nil
}
