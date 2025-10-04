package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const HistoryDir = ".make-sync"
const HistoryFile = "history.json"

type HistoryEntry struct {
	Path       string    `json:"path"`
	LastAccess time.Time `json:"last_access"`
}

type History struct {
	Entries []HistoryEntry `json:"entries"`
}

func GetHistoryDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, HistoryDir)
}

func GetHistoryPath() string {
	return filepath.Join(GetHistoryDir(), HistoryFile)
}

func LoadHistory() (*History, error) {
	path := GetHistoryPath()
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return &History{Entries: []HistoryEntry{}}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var h History
	err = json.Unmarshal(data, &h)
	if err != nil || len(h.Entries) == 0 {
		// Try old format migration
		var oldHistory struct {
			Paths []string `json:"paths"`
		}
		if oldErr := json.Unmarshal(data, &oldHistory); oldErr == nil && len(oldHistory.Paths) > 0 {
			// Migrate old format to new format
			h.Entries = make([]HistoryEntry, len(oldHistory.Paths))
			now := time.Now()
			for i, p := range oldHistory.Paths {
				h.Entries[i] = HistoryEntry{
					Path:       p,
					LastAccess: now, // Set all to now for migration
				}
			}
			// Save migrated format
			_ = SaveHistory(&h) // Ignore error for now
			return &h, nil
		}
		if err != nil {
			return nil, err
		}
	}

	return &h, err
}

func SaveHistory(h *History) error {
	dir := GetHistoryDir()
	os.MkdirAll(dir, 0755)
	path := GetHistoryPath()
	data, err := json.MarshalIndent(h, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

func AddPath(path string) error {
	h, err := LoadHistory()
	if err != nil {
		return err
	}
	// Avoid duplicates
	for i, entry := range h.Entries {
		if entry.Path == path {
			// Update last access time
			h.Entries[i].LastAccess = time.Now()
			return SaveHistory(h)
		}
	}
	// Add new entry
	h.Entries = append(h.Entries, HistoryEntry{
		Path:       path,
		LastAccess: time.Now(),
	})
	return SaveHistory(h)
}

func RemovePath(path string) error {
	h, err := LoadHistory()
	if err != nil {
		return err
	}
	for i, entry := range h.Entries {
		if entry.Path == path {
			h.Entries = append(h.Entries[:i], h.Entries[i+1:]...)
			break
		}
	}
	return SaveHistory(h)
}

func SearchPaths(query string) []string {
	h, err := LoadHistory()
	if err != nil {
		return []string{}
	}
	var results []string
	for _, entry := range h.Entries {
		if strings.Contains(strings.ToLower(entry.Path), strings.ToLower(query)) {
			results = append(results, entry.Path)
		}
	}
	sort.Strings(results)
	return results
}

func GetAllPaths() []string {
	h, err := LoadHistory()
	if err != nil {
		return []string{}
	}
	if len(h.Entries) == 0 {
		return []string{}
	}

	// Sort by last access time (most recent first)
	sort.Slice(h.Entries, func(i, j int) bool {
		return h.Entries[i].LastAccess.After(h.Entries[j].LastAccess)
	})

	var result []string
	for _, entry := range h.Entries {
		result = append(result, entry.Path)
	}

	return result
}
