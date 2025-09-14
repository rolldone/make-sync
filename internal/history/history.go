package history

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const HistoryDir = ".make-sync"
const HistoryFile = "history.json"

type History struct {
	Paths []string `json:"paths"`
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
		return &History{Paths: []string{}}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var h History
	err = json.Unmarshal(data, &h)
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
	for _, p := range h.Paths {
		if p == path {
			return nil
		}
	}
	h.Paths = append(h.Paths, path)
	return SaveHistory(h)
}

func RemovePath(path string) error {
	h, err := LoadHistory()
	if err != nil {
		return err
	}
	for i, p := range h.Paths {
		if p == path {
			h.Paths = append(h.Paths[:i], h.Paths[i+1:]...)
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
	for _, p := range h.Paths {
		if strings.Contains(strings.ToLower(p), strings.ToLower(query)) {
			results = append(results, p)
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
	return h.Paths
}
