package util

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// GetProjectRoot returns the project root directory, handling both development mode
// (go run) and production mode (compiled executable).
//
// In development mode (go run), os.Executable() returns a temporary path, so we
// need to find the actual project root by looking for go.mod or other indicators.
//
// In production mode, we use the directory containing the executable.
func GetProjectRoot() (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}

	// Resolve symlinks if any
	if resolved, err := filepath.EvalSymlinks(exePath); err == nil {
		exePath = resolved
	}

	// Check if we're running in development mode (go run)
	// go run creates temporary executables in system temp directory
	if isDevelopmentMode(exePath) {
		// In development mode, find project root by looking for go.mod
		root, err := findProjectRootFromWorkingDir()
		// Debug: uncomment to see path detection
		// fmt.Printf("DEBUG: Development mode detected. Executable: %s, Project root: %s\n", exePath, root)
		return root, err
	}

	// Production mode: project root is the directory containing the executable
	root := filepath.Dir(exePath)
	// Debug: uncomment to see path detection
	// fmt.Printf("DEBUG: Production mode detected. Executable: %s, Project root: %s\n", exePath, root)
	return root, nil
}

// isDevelopmentMode checks if the executable path indicates we're running via "go run"
func isDevelopmentMode(exePath string) bool {
	// go run typically creates executables in temp directories or go build cache
	tempDir := os.TempDir()

	// Normalize paths for comparison
	tempDir = filepath.Clean(tempDir)
	exePath = filepath.Clean(exePath)

	// Check if executable is in temp directory (traditional temp dir)
	if strings.HasPrefix(exePath, tempDir) {
		return true
	}

	// Check if executable is in Go build cache directory
	// Go build cache is typically ~/.cache/go-build on Linux/Mac
	homeDir, err := os.UserHomeDir()
	if err == nil {
		goBuildCache := filepath.Join(homeDir, ".cache", "go-build")
		goBuildCache = filepath.Clean(goBuildCache)
		if strings.HasPrefix(exePath, goBuildCache) {
			return true
		}
	}

	// Check for other common Go temporary patterns
	// Go run also sometimes uses patterns like "go-build" in the name
	if strings.Contains(exePath, "go-build") {
		return true
	}

	return false
}

// findProjectRootFromWorkingDir searches upward from working directory to find go.mod
func findProjectRootFromWorkingDir() (string, error) {
	// Start from current working directory
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	return findProjectRootFromPath(wd)
}

// findProjectRootFromPath searches upward from given path to find project root indicators
func findProjectRootFromPath(startPath string) (string, error) {
	currentPath := filepath.Clean(startPath)

	for {
		// Check for go.mod (primary indicator for Go projects)
		if _, err := os.Stat(filepath.Join(currentPath, "go.mod")); err == nil {
			return currentPath, nil
		}

		// Move to parent directory
		parentPath := filepath.Dir(currentPath)

		// Stop if we've reached the root or can't go higher
		if parentPath == currentPath || parentPath == "." {
			break
		}

		currentPath = parentPath
	}

	// Second pass: Look for main.go at project level (fallback for Go projects)
	currentPath = filepath.Clean(startPath)
	for {
		// Check for main.go in the current directory
		if _, err := os.Stat(filepath.Join(currentPath, "main.go")); err == nil {
			return currentPath, nil
		}

		// Move to parent directory
		parentPath := filepath.Dir(currentPath)

		// Stop if we've reached the root or can't go higher
		if parentPath == currentPath || parentPath == "." {
			break
		}

		currentPath = parentPath
	}

	// Third pass: Look for make-sync.yaml but verify it's at project level
	currentPath = filepath.Clean(startPath)
	for {
		// Check for make-sync.yaml (project-specific indicator)
		if _, err := os.Stat(filepath.Join(currentPath, "make-sync.yaml")); err == nil {
			// Only accept make-sync.yaml if it's at a level that also has go.mod or main.go
			// This prevents subfolder configs from being treated as project root
			if _, err := os.Stat(filepath.Join(currentPath, "go.mod")); err == nil {
				return currentPath, nil
			}
			if _, err := os.Stat(filepath.Join(currentPath, "main.go")); err == nil {
				return currentPath, nil
			}
		}

		// Move to parent directory
		parentPath := filepath.Dir(currentPath)

		// Stop if we've reached the root or can't go higher
		if parentPath == currentPath || parentPath == "." {
			break
		}

		currentPath = parentPath
	}

	// Fallback: if we can't find project root, return the original working directory
	wd, err := os.Getwd()
	if err != nil {
		// Last resort: use the directory containing the executable
		exePath, execErr := os.Executable()
		if execErr != nil {
			return "", execErr
		}
		return filepath.Dir(exePath), nil
	}

	return wd, nil
}

// GetProjectRootFromCaller returns project root using runtime caller information
// This is useful when you want to find project root relative to the calling file
func GetProjectRootFromCaller() (string, error) {
	_, filename, _, ok := runtime.Caller(1)
	if !ok {
		return GetProjectRoot() // fallback to regular method
	}

	// Start searching from the directory containing the caller
	return findProjectRootFromPath(filepath.Dir(filename))
}
