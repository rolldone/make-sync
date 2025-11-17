package syncdata

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"database/sql"
	"make-sync/internal/config"
	"make-sync/internal/sshclient"
	"make-sync/internal/util"

	"github.com/cespare/xxhash/v2"
	_ "github.com/glebarez/sqlite"
)

// collectPreprocessedIgnores walks root and collects .sync_ignore lines, preprocesses
// simple patterns (adds **/ variant) and returns a deduplicated list.
func collectPreprocessedIgnores(root string) ([]string, error) {
	found := map[string]struct{}{}
	// default ignores
	defaults := []string{".sync_temp", "make-sync.yaml", ".sync_ignore", ".sync_collections"}
	for _, d := range defaults {
		found[d] = struct{}{}
	}

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), ".sync_ignore") {
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				return nil
			}
			lines := strings.Split(string(data), "\n")
			for _, ln := range lines {
				l := strings.TrimSpace(ln)
				if l == "" || strings.HasPrefix(l, "#") {
					continue
				}
				neg := false
				if strings.HasPrefix(l, "!") {
					neg = true
					l = strings.TrimPrefix(l, "!")
				}
				// normalize to forward slashes
				l = filepath.ToSlash(l)
				if strings.Contains(l, "/") || strings.Contains(l, "**") {
					if neg {
						found["!"+l] = struct{}{}
					} else {
						found[l] = struct{}{}
					}
					continue
				}
				// add both forms
				if neg {
					found["!"+l] = struct{}{}
					found["!**/"+l] = struct{}{}
				} else {
					found[l] = struct{}{}
					found["**/"+l] = struct{}{}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// convert map to slice deterministic order
	out := make([]string, 0, len(found))
	for k := range found {
		out = append(out, k)
	}
	// sort for deterministic output
	sort.Strings(out)
	return out, nil
}

// RemoteAgentConfig represents the configuration sent to remote agent
// This mirrors the struct from internal/devsync/types.go
type RemoteAgentConfig struct {
	Devsync struct {
		Ignores        []string `json:"ignores"`
		AgentWatchs    []string `json:"agent_watchs"`
		ManualTransfer []string `json:"manual_transfer"`
		WorkingDir     string   `json:"working_dir"`
	} `json:"devsync"`
}

// generateRemoteConfig creates a config for the remote agent based on local config
func generateRemoteConfig(cfg *config.Config) *RemoteAgentConfig {
	remoteConfig := &RemoteAgentConfig{}
	remoteConfig.Devsync.Ignores = cfg.Devsync.Ignores
	remoteConfig.Devsync.AgentWatchs = cfg.Devsync.AgentWatchs
	remoteConfig.Devsync.ManualTransfer = cfg.Devsync.ManualTransfer
	remoteConfig.Devsync.WorkingDir = cfg.Devsync.Auth.RemotePath

	return remoteConfig
}

// uploadConfigToRemote creates and uploads config.json to remote .sync_temp directory
func uploadConfigToRemote(client *sshclient.SSHClient, cfg *config.Config, remoteSyncTemp, osTarget string) error {
	// Generate remote config
	remoteConfig := generateRemoteConfig(cfg)

	// Collect preprocessed ignores from project root and populate remoteConfig.Devsync.Ignores
	root := cfg.LocalPath
	if root == "" {
		wd, err := os.Getwd()
		if err == nil {
			root = wd
		}
	}
	if root != "" {
		if ignores, err := collectPreprocessedIgnores(root); err == nil {
			remoteConfig.Devsync.Ignores = ignores
		}
	}

	// Convert to JSON
	configJSON, err := json.MarshalIndent(remoteConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config to JSON: %v", err)
	}

	util.Default.Printf("📤 Uploading config.json with working_dir: %s\n", remoteConfig.Devsync.WorkingDir)
	util.Default.Printf("📄 Config content:\n%s\n", string(configJSON))

	// Create temporary local file
	tempFile, err := os.CreateTemp("", "remote-config-*.json")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %v", err)
	}
	defer os.Remove(tempFile.Name())
	defer tempFile.Close()

	// Write config to temp file
	if _, err := tempFile.Write(configJSON); err != nil {
		return fmt.Errorf("failed to write config to temp file: %v", err)
	}
	tempFile.Close()

	// Always use absolute remotePath for .sync_temp/config.json, but avoid double .sync_temp
	remoteBase := cfg.Devsync.Auth.RemotePath
	var remoteConfigPath string
	if strings.Contains(strings.ToLower(osTarget), "win") {
		base := strings.ReplaceAll(remoteBase, "/", "\\")
		if strings.HasSuffix(base, ".sync_temp") {
			remoteConfigPath = base + "\\config.json"
		} else {
			remoteConfigPath = base + "\\.sync_temp\\config.json"
		}
	} else {
		base := remoteBase
		if strings.HasSuffix(base, ".sync_temp") {
			remoteConfigPath = filepath.Join(base, "config.json")
		} else {
			remoteConfigPath = filepath.Join(base, ".sync_temp", "config.json")
		}
	}

	// Upload config to remote
	if err := client.SyncFile(tempFile.Name(), remoteConfigPath); err != nil {
		return fmt.Errorf("failed to upload config: %v", err)
	}

	util.Default.Printf("✅ Config uploaded to: %s\n", remoteConfigPath)
	return nil
}

// ConnectSSH creates and connects an SSH client using values from cfg.Devsync.Auth
func ConnectSSH(cfg *config.Config) (*sshclient.SSHClient, error) {
	auth := cfg.Devsync.Auth
	username := auth.Username
	privateKey := auth.PrivateKey
	password := auth.Password
	host := auth.Host
	port := auth.Port
	if port == "" {
		port = "22"
	}

	client, err := sshclient.NewPersistentSSHClient(username, privateKey, password, host, port)
	if err != nil {
		return nil, fmt.Errorf("failed to create ssh client: %v", err)
	}
	if err := client.Connect(); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to connect ssh client: %v", err)
	}
	return client, nil
}

// UploadAgentBinary uploads a local agent binary to the remote directory using direct SyncFile like watcher.
// localBinaryPath: path to local binary (e.g., built sync-agent or sync-agent.exe)
// remoteDir: absolute remote directory where to place binary (usually <project>/.sync_temp)
// osTarget: string indicating remote OS (contains "win" for windows)
func UploadAgentBinary(client *sshclient.SSHClient, localBinaryPath, remoteDir, osTarget string) error {
	// Create .sync_temp directory on remote first (like watcher does)
	if strings.Contains(strings.ToLower(osTarget), "win") {
		// Use consistent backslash paths for Windows
		winRemoteDir := strings.ReplaceAll(remoteDir, "/", "\\")
		createCmd := fmt.Sprintf("cmd.exe /C if not exist \"%s\" mkdir \"%s\"", winRemoteDir, winRemoteDir)
		util.Default.Printf("🔧 DEBUG: Creating remote dir with cmd: %s\n", createCmd)
		if err := client.RunCommand(createCmd); err != nil {
			return fmt.Errorf("failed to create remote .sync_temp (windows): %v", err)
		}
	} else {
		remoteCmd := fmt.Sprintf("mkdir -p %s", remoteDir)
		if err := client.RunCommand(remoteCmd); err != nil {
			return fmt.Errorf("failed to create remote .sync_temp: %v", err)
		}
	}

	// Always use absolute remotePath for .sync_temp/[unique-agent-name]
	remoteBase := remoteDir

	// Get unique agent binary name from local config
	localConfig, err := config.GetOrCreateLocalConfig()
	if err != nil {
		return fmt.Errorf("failed to load local config: %v", err)
	}

	remoteExecName := localConfig.GetAgentBinaryName(osTarget)
	var remoteAgentPath string
	if strings.Contains(strings.ToLower(osTarget), "win") {
		base := strings.ReplaceAll(remoteBase, "/", "\\")
		if strings.HasSuffix(base, ".sync_temp") {
			remoteAgentPath = base + "\\" + remoteExecName
		} else {
			remoteAgentPath = base + "\\.sync_temp\\" + remoteExecName
		}
	} else {
		base := remoteBase
		if strings.HasSuffix(base, ".sync_temp") {
			remoteAgentPath = filepath.Join(base, remoteExecName)
		} else {
			remoteAgentPath = filepath.Join(base, ".sync_temp", remoteExecName)
		}
		remoteAgentPath = filepath.ToSlash(remoteAgentPath)
	}

	util.Default.Printf("🔧 DEBUG: Uploading %s to %s\n", localBinaryPath, remoteAgentPath)

	// Use SyncFile with absolute path
	if err := client.SyncFile(localBinaryPath, remoteAgentPath); err != nil {
		return fmt.Errorf("failed to upload agent: %v", err)
	}

	util.Default.Printf("✅ Agent binary uploaded successfully\n")

	// Set permissions on Unix (like watcher does)
	if !strings.Contains(strings.ToLower(osTarget), "win") {
		chmodCmd := fmt.Sprintf("chmod +x %s", remoteAgentPath)
		log.Println("[runner.go] DEBUG: Running chmod command:", chmodCmd)
		if err := client.RunCommand(chmodCmd); err != nil {
			log.Println("[runner.go] DEBUG: chmod command failed:", err)
			return fmt.Errorf("failed to make agent executable: %v", err)
		}
		log.Println("[runner.go] DEBUG: chmod command succeeded")
	}

	return nil
}

func RemoteRunAgentIndexing(client *sshclient.SSHClient, remoteDir, osTarget string, bypassIgnore bool, prefixes []string) (string, error) {
	isWin := strings.Contains(strings.ToLower(osTarget), "win")

	// Get unique agent binary name from local config
	localConfig, err := config.GetOrCreateLocalConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load local config: %v", err)
	}

	// Build the binary path based on OS using unique agent name
	binaryName := localConfig.GetAgentBinaryName(osTarget)
	var cmd string
	remoteBase := remoteDir
	if isWin {
		winRemoteDir := strings.ReplaceAll(remoteBase, "/", "\\")
		var agentPath, cdDir string
		if strings.HasSuffix(winRemoteDir, ".sync_temp") {
			agentPath = winRemoteDir + "\\" + binaryName
			cdDir = winRemoteDir[:len(winRemoteDir)-len(".sync_temp")]
			cdDir = strings.TrimSuffix(cdDir, "\\")
			if cdDir == "" {
				cdDir = winRemoteDir
			} // fallback
		} else {
			agentPath = winRemoteDir + "\\.sync_temp\\" + binaryName
			cdDir = winRemoteDir
		}
		indexingCmd := "indexing"
		if bypassIgnore {
			indexingCmd = "indexing --bypass-ignore"
		}
		if len(prefixes) > 0 {
			joined := strings.Join(prefixes, ",")
			indexingCmd = fmt.Sprintf("%s --manual-transfer %s", indexingCmd, joined)
		}
		cmd = fmt.Sprintf("cmd.exe /C cd /d \"%s\" && \"%s\" %s", cdDir, agentPath, indexingCmd)
	} else {
		var agentPath, cdDir string
		if strings.HasSuffix(remoteBase, ".sync_temp") {
			agentPath = filepath.Join(remoteBase, binaryName)
			cdDir = strings.TrimSuffix(remoteBase, ".sync_temp")
			if cdDir == "" {
				cdDir = remoteBase
			}
		} else {
			agentPath = filepath.Join(remoteBase, ".sync_temp", binaryName)
			cdDir = remoteBase
		}
		// Ensure forward slashes for Linux
		agentPath = filepath.ToSlash(agentPath)
		cdDir = filepath.ToSlash(cdDir)
		indexingCmd := "indexing"
		if bypassIgnore {
			indexingCmd = "indexing --bypass-ignore"
		}
		if len(prefixes) > 0 {
			joined := strings.Join(prefixes, ",")
			indexingCmd = fmt.Sprintf("%s --manual-transfer %s", indexingCmd, shellQuote(joined))
		}
		cmd = fmt.Sprintf("cd %s && %s %s", shellQuote(cdDir), shellQuote(agentPath), indexingCmd)
	}

	fmt.Printf("🔍 DEBUG: Executing remote command: %s\n", cmd)

	// Add timeout context for debugging
	start := time.Now()
	fmt.Printf("🔍 DEBUG: Starting remote command execution at %s\n", start.Format("15:04:05"))

	// Try using RunCommandWithStream for better responsiveness like watcher does
	outputChan, errorChan, err := client.RunCommandWithStream(cmd, false)
	if err != nil {
		return "", fmt.Errorf("failed to start remote indexing command: %v", err)
	}

	var output strings.Builder
	timeout := time.After(60 * time.Second) // 60 second timeout for indexing

	fmt.Printf("🔍 DEBUG: Streaming command output...\n")

	for {
		select {
		case out, ok := <-outputChan:
			if !ok {
				// Command finished
				duration := time.Since(start)
				fmt.Printf("🔍 DEBUG: Command completed in %v\n", duration)

				result := output.String()
				fmt.Printf("🔍 DEBUG: Final output (%d bytes): %s\n", len(result), result)
				return result, nil
			}
			output.WriteString(out)
			fmt.Printf("🔍 DEBUG: Output chunk: %s", out) // Real-time output

		case err := <-errorChan:
			if err != nil {
				duration := time.Since(start)
				fmt.Printf("🔍 DEBUG: Command failed after %v: %v\n", duration, err)
				return output.String(), fmt.Errorf("remote indexing failed: %v", err)
			}

		case <-timeout:
			duration := time.Since(start)
			fmt.Printf("🔍 DEBUG: Command timed out after %v\n", duration)
			return output.String(), fmt.Errorf("remote indexing timed out after 60 seconds")
		}
	}
} // shellQuote quotes a POSIX path using single quotes, escaping existing single quotes
func shellQuote(s string) string {
	// simple implementation: replace ' with '\'' and wrap with '
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// RemoteRunAgentPrune runs the agent's prune command on the remote .sync_temp agent
// remoteDir should be the remote .sync_temp directory (same as used for agent binary)
func RemoteRunAgentPrune(client *sshclient.SSHClient, remoteDir, osTarget string, bypassIgnore bool, prefixes []string, dryRun bool) (string, error) {
	isWin := strings.Contains(strings.ToLower(osTarget), "win")

	// Get unique agent binary name from local config
	localConfig, err := config.GetOrCreateLocalConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load local config: %v", err)
	}
	binaryName := localConfig.GetAgentBinaryName(osTarget)

	var cmd string
	if isWin {
		winRemoteDir := strings.ReplaceAll(remoteDir, "/", "\\")
		var agentPath, cdDir string
		if strings.HasSuffix(winRemoteDir, ".sync_temp") {
			agentPath = winRemoteDir + "\\" + binaryName
			cdDir = winRemoteDir[:len(winRemoteDir)-len(".sync_temp")]
			cdDir = strings.TrimSuffix(cdDir, "\\")
			if cdDir == "" {
				cdDir = winRemoteDir
			}
		} else {
			agentPath = winRemoteDir + "\\.sync_temp\\" + binaryName
			cdDir = winRemoteDir
		}
		pruneCmd := "prune"
		if bypassIgnore {
			pruneCmd = pruneCmd + " --bypass-ignore"
		}
		if dryRun {
			pruneCmd = pruneCmd + " --dry-run"
		}
		if len(prefixes) > 0 {
			joined := strings.Join(prefixes, ",")
			pruneCmd = fmt.Sprintf("%s --manual-transfer %s", pruneCmd, joined)
		}
		cmd = fmt.Sprintf("cmd.exe /C cd /d \"%s\" && \"%s\" %s", cdDir, agentPath, pruneCmd)
	} else {
		var agentPath, cdDir string
		if strings.HasSuffix(remoteDir, ".sync_temp") {
			agentPath = filepath.Join(remoteDir, binaryName)
			cdDir = strings.TrimSuffix(remoteDir, ".sync_temp")
			if cdDir == "" {
				cdDir = remoteDir
			}
		} else {
			agentPath = filepath.Join(remoteDir, ".sync_temp", binaryName)
			cdDir = remoteDir
		}
		agentPath = filepath.ToSlash(agentPath)
		cdDir = filepath.ToSlash(cdDir)
		pruneCmd := "prune"
		if bypassIgnore {
			pruneCmd = pruneCmd + " --bypass-ignore"
		}
		if dryRun {
			pruneCmd = pruneCmd + " --dry-run"
		}
		if len(prefixes) > 0 {
			joined := strings.Join(prefixes, ",")
			pruneCmd = fmt.Sprintf("%s --manual-transfer %s", pruneCmd, shellQuote(joined))
		}
		cmd = fmt.Sprintf("cd %s && %s %s", shellQuote(cdDir), shellQuote(agentPath), pruneCmd)
	}

	util.Default.Printf("🔍 DEBUG: Executing remote prune command: %s\n", cmd)

	outputChan, errorChan, err := client.RunCommandWithStream(cmd, false)
	if err != nil {
		return "", fmt.Errorf("failed to start remote prune command: %v", err)
	}

	var output strings.Builder
	timeout := time.After(60 * time.Second)
	for {
		select {
		case out, ok := <-outputChan:
			if !ok {
				return output.String(), nil
			}
			output.WriteString(out)
		case err := <-errorChan:
			if err != nil {
				return output.String(), fmt.Errorf("remote prune failed: %v", err)
			}
		case <-timeout:
			return output.String(), fmt.Errorf("remote prune timed out after 60s")
		}
	}
}

// RemoteRunAgentPruneFn is a variable indirection for RemoteRunAgentPrune to
// allow tests to override the remote execution logic (mocking). Production
// code should use the real RemoteRunAgentPrune implementation, but tests can
// replace this with a stub that returns synthetic output.
var RemoteRunAgentPruneFn = RemoteRunAgentPrune

// RunAgentIndexingFlow encapsulates the full remote indexing orchestration:
// - locate local agent binary from provided candidates
// - connect SSH using cfg
// - optionally upload agent into remote .sync_temp (controlled by skipUpload)
// - run remote indexing command
// - download remote indexing_files.db into a local temp file
// Returns the local path of the downloaded DB, the remote output, or an error.
// If skipUpload is true, the function will not upload the agent or config and
// assumes they are already present on the remote (useful when caller already
// deployed the agent).
func RunAgentIndexingFlow(cfg *config.Config, localCandidates []string, bypassIgnore bool, prefixes []string) (string, string, error) {
	// find local binary
	var localBinary string
	for _, c := range localCandidates {
		if _, err := os.Stat(c); err == nil {
			localBinary = c
			break
		}
	}
	if localBinary == "" {
		return "", "", fmt.Errorf("agent binary not found in candidates: %v", localCandidates)
	}

	// determine remote base path
	remotePath := cfg.Devsync.Auth.RemotePath
	osTarget := strings.ToLower(strings.TrimSpace(cfg.Devsync.OSTarget))
	var remoteSyncTemp string
	if remotePath == "" {
		if strings.Contains(osTarget, "win") {
			remoteSyncTemp = "%TEMP%"
		} else {
			remoteSyncTemp = "/tmp/.sync_temp"
		}
	} else {
		if strings.Contains(osTarget, "win") {
			remoteSyncTemp = filepath.ToSlash(filepath.Join(remotePath, ".sync_temp"))
		} else {
			remoteSyncTemp = filepath.Join(remotePath, ".sync_temp")
		}
	}

	util.Default.Printf("ℹ️  Local agent binary: %s\n", localBinary)
	util.Default.Printf("ℹ️  Remote target .sync_temp: %s (os_target=%s)\n", remoteSyncTemp, osTarget)

	// Connect SSH
	sshCli, err := ConnectSSH(cfg)
	if err != nil {
		return "", "", fmt.Errorf("ssh connect failed: %v", err)
	}
	defer sshCli.Close()

	// Upload agent binary into remoteSyncTemp
	if err := UploadAgentBinary(sshCli, localBinary, remoteSyncTemp, osTarget); err != nil {
		return "", "", fmt.Errorf("upload agent binary failed: %v", err)
	}
	util.Default.Println("✅ Agent binary uploaded")

	// Upload config.json to remote .sync_temp (needed for agent to know working directory)
	if err := uploadConfigToRemote(sshCli, cfg, remoteSyncTemp, osTarget); err != nil {
		return "", "", fmt.Errorf("upload config failed: %v", err)
	}
	util.Default.Println("✅ Config uploaded")

	// Run remote indexing
	util.Default.Println("🔍 Running remote agent indexing...")
	out, err := RemoteRunAgentIndexing(sshCli, remoteSyncTemp, osTarget, bypassIgnore, prefixes)
	if err != nil {
		return "", out, fmt.Errorf("remote indexing failed: %v", err)
	}
	util.Default.Printf("✅ Remote indexing finished. Remote outaput:\n%s\n", out)
	return "", out, nil
}

// DownloadIndexDB downloads the remote .sync_temp/indexing_files.db into the
// provided local destination directory (localDestFolder). If localDestFolder is
// empty, it will default to cfg.LocalPath or current working directory's .sync_temp.
// Returns the local file path or an error.
func DownloadIndexDB(cfg *config.Config, localDestFolder string) (string, error) {
	// determine remote base path
	remotePath := cfg.Devsync.Auth.RemotePath
	osTarget := strings.ToLower(strings.TrimSpace(cfg.Devsync.OSTarget))
	var remoteSyncTemp string
	if remotePath == "" {
		if strings.Contains(osTarget, "win") {
			remoteSyncTemp = "%TEMP%"
		} else {
			remoteSyncTemp = "/tmp/.sync_temp"
		}
	} else {
		if strings.Contains(osTarget, "win") {
			remoteSyncTemp = filepath.ToSlash(filepath.Join(remotePath, ".sync_temp"))
		} else {
			remoteSyncTemp = filepath.Join(remotePath, ".sync_temp")
		}
	}

	// remote file path
	var remoteFile string
	if strings.Contains(osTarget, "win") {
		remoteFile = strings.ReplaceAll(remoteSyncTemp, "/", "\\") + "\\indexing_files.db"
	} else {
		remoteFile = filepath.Join(remoteSyncTemp, "indexing_files.db")
	}

	// decide local destination folder
	dest := localDestFolder
	if dest == "" {
		if cfg.LocalPath != "" {
			dest = cfg.LocalPath
		} else {
			dest = "."
		}
	}
	localSyncTemp := filepath.Join(dest, ".sync_temp")
	if err := os.MkdirAll(localSyncTemp, 0755); err != nil {
		return "", fmt.Errorf("failed to create local .sync_temp: %v", err)
	}
	localFile := filepath.Join(localSyncTemp, "indexing_files.db")

	// Connect SSH
	sshCli, err := ConnectSSH(cfg)
	if err != nil {
		return "", fmt.Errorf("ssh connect failed: %v", err)
	}
	defer sshCli.Close()

	util.Default.Printf("⬇️  Downloading remote index DB from %s to %s\n", remoteFile, localFile)
	if err := sshCli.DownloadFile(localFile, remoteFile); err != nil {
		return "", fmt.Errorf("failed to download index DB: %v", err)
	}

	util.Default.Printf("✅ Downloaded index DB to %s\n", localFile)
	return localFile, nil
}

// CompareAndDownloadByHash downloads the remote index DB, builds a local index from
// localRoot (if empty, derived from cfg.LocalPath or current working dir), compares
// by relative path and hash, and downloads files whose hash differ or when remote
// hash is empty. Returns list of local downloaded file paths.
func CompareAndDownloadByHash(cfg *config.Config, localRoot string) ([]string, error) {
	// decide local root
	root := localRoot
	if root == "" {
		if cfg.LocalPath != "" {
			root = cfg.LocalPath
		} else if cfg.Devsync.Auth.LocalPath != "" {
			root = cfg.Devsync.Auth.LocalPath
		} else {
			wd, err := os.Getwd()
			if err != nil {
				return nil, fmt.Errorf("failed to determine working dir: %v", err)
			}
			root = wd
		}
	}
	// ensure absolute
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute root: %v", err)
	}

	// Download remote DB into local .sync_temp
	localDBPath, err := DownloadIndexDB(cfg, absRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to download remote DB: %v", err)
	}

	// Load remote index DB via sqlite
	remoteByRel := map[string]struct {
		Path  string
		Rel   string
		Size  int64
		Mod   int64
		Hash  string
		IsDir bool
	}{}

	db, err := sql.Open("sqlite", localDBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open remote DB: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT path, rel, size, mod_time, hash, is_dir FROM files`)
	if err != nil {
		return nil, fmt.Errorf("failed to query remote DB: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var pathStr, relStr, hashStr string
		var sizeInt, modNano int64
		var isDirInt int
		if err := rows.Scan(&pathStr, &relStr, &sizeInt, &modNano, &hashStr, &isDirInt); err != nil {
			continue
		}
		key := filepath.ToSlash(relStr)
		if key == "" {
			// fallback: derive rel from path by keeping base name
			key = filepath.ToSlash(relStr)
		}
		remoteByRel[key] = struct {
			Path  string
			Rel   string
			Size  int64
			Mod   int64
			Hash  string
			IsDir bool
		}{Path: pathStr, Rel: relStr, Size: sizeInt, Mod: modNano, Hash: hashStr, IsDir: isDirInt != 0}
	}

	// Remote-first: iterate entries from remote DB and decide per-entry
	// whether to download into absRoot. This honors the remote index and
	// avoids scanning every local file.

	// Create IgnoreCache rooted at absRoot
	ic := NewIgnoreCache(absRoot)

	// Connect SSH once and reuse for downloads
	sshCli, err := ConnectSSH(cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh connect failed: %v", err)
	}
	defer sshCli.Close()

	// deterministically iterate remote entries by sorted rel
	rels := make([]string, 0, len(remoteByRel))
	for r := range remoteByRel {
		rels = append(rels, r)
	}
	sort.Strings(rels)

	downloaded := []string{}
	examined := 0
	skippedIgnored := 0
	skippedUpToDate := 0
	downloadErrors := 0
	var downloadTasks []util.ConcurrentTask
	var mu sync.Mutex // mutex for thread-safe access to shared variables used by tasks
	const maxConcurrency = 5
	slotCh := make(chan int, maxConcurrency)
	for i := 1; i <= maxConcurrency; i++ {
		slotCh <- i
	}

	for _, rel := range rels {
		rm := remoteByRel[rel]
		examined++
		if rm.IsDir {
			continue
		}

		relNorm := filepath.ToSlash(rel)
		localPath := filepath.Join(absRoot, filepath.FromSlash(relNorm))

		// Always skip anything under a remote .sync_temp directory — we don't
		// want to pull agent artifacts and the remote .sync_temp into local tree.
		if relNorm == ".sync_temp" || strings.HasPrefix(relNorm, ".sync_temp/") || strings.Contains(relNorm, "/.sync_temp/") {
			skippedIgnored++
			continue
		}

		// Respect local .sync_ignore — if user ignores it locally, do not download
		if ic.Match(localPath, false) {
			skippedIgnored++
			continue
		}

		// Check local file
		info, statErr := os.Stat(localPath)
		if statErr != nil {
			// file missing -> download
			remotePath := buildRemotePath(cfg, relNorm)
			rp := remotePath
			lp := localPath
			downloadTasks = append(downloadTasks, func() error {
				id := <-slotCh
				defer func() { slotCh <- id }()
				util.Default.Printf("⬇️  %d -> Downloading %s -> %s\n", id, rp, lp)
				if err := sshCli.DownloadFile(lp, rp); err != nil {
					util.Default.Printf("❌ %d Failed to download %s: %v\n", id, rp, err)
					mu.Lock()
					downloadErrors++
					mu.Unlock()
					return err
				}
				mu.Lock()
				downloaded = append(downloaded, lp)
				mu.Unlock()
				return nil
			})
			continue
		}
		if info.IsDir() {
			continue
		}

		// compute local hash
		localHash := ""
		f, err := os.Open(localPath)
		if err == nil {
			h := xxhash.New()
			if _, err := io.Copy(h, f); err == nil {
				localHash = fmt.Sprintf("%x", h.Sum(nil))
			}
			f.Close()
		}

		if strings.TrimSpace(rm.Hash) == "" || rm.Hash != localHash {
			remotePath := buildRemotePath(cfg, relNorm)
			rp := remotePath
			lp := localPath
			downloadTasks = append(downloadTasks, func() error {
				id := <-slotCh
				defer func() { slotCh <- id }()
				util.Default.Printf("⬇️  %d -> Downloading %s -> %s\n", id, rp, lp)
				if err := sshCli.DownloadFile(lp, rp); err != nil {
					util.Default.Printf("❌ %d Failed to download %s: %v\n", id, rp, err)
					mu.Lock()
					downloadErrors++
					mu.Unlock()
					return err
				}
				mu.Lock()
				downloaded = append(downloaded, lp)
				mu.Unlock()
				return nil
			})
			continue
		}

		skippedUpToDate++
	}

	// Execute all download tasks with bounded concurrency (max 5 parallel)
	if len(downloadTasks) > 0 {
		if err := util.RunConcurrent(downloadTasks, maxConcurrency); err != nil {
			util.Default.Printf("⚠️  Some downloads failed: %v\n", err)
		}
	}

	util.Default.Printf("🔁 Remote index entries examined: %d, downloaded: %d, skipped(ignored): %d, skipped(up-to-date): %d, errors: %d\n",
		examined, len(downloaded), skippedIgnored, skippedUpToDate, downloadErrors)

	return downloaded, nil
}

// buildRemotePath constructs an absolute remote path for a given rel using cfg
func buildRemotePath(cfg *config.Config, rel string) string {
	remoteBase := cfg.Devsync.Auth.RemotePath
	osTarget := strings.ToLower(strings.TrimSpace(cfg.Devsync.OSTarget))
	if strings.Contains(osTarget, "win") {
		// prefer backslashes on windows remote
		p := filepath.ToSlash(filepath.Join(remoteBase, rel))
		return strings.ReplaceAll(p, "/", "\\")
	}
	return filepath.ToSlash(filepath.Join(remoteBase, rel))
}

// CompareAndUploadByHash performs a local-first safe-push:
// - downloads remote index DB into local .sync_temp
// - walks local tree and for each file compares to remote entry by rel/hash
// - uploads files that are new or whose hash differs (or remote hash is empty)
// Returns list of uploaded local paths.
func CompareAndUploadByHash(cfg *config.Config, localRoot string) ([]string, error) {
	// determine local root
	root := localRoot
	if root == "" {
		if cfg.LocalPath != "" {
			root = cfg.LocalPath
		} else if cfg.Devsync.Auth.LocalPath != "" {
			root = cfg.Devsync.Auth.LocalPath
		} else {
			wd, err := os.Getwd()
			if err != nil {
				return nil, fmt.Errorf("failed to determine working dir: %v", err)
			}
			root = wd
		}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute root: %v", err)
	}

	// Download remote DB into local .sync_temp
	localDBPath, err := DownloadIndexDB(cfg, absRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to download remote DB: %v", err)
	}

	// Load remote index DB
	remoteByRel := map[string]struct {
		Path  string
		Rel   string
		Size  int64
		Mod   int64
		Hash  string
		IsDir bool
	}{}

	db, err := sql.Open("sqlite", localDBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open remote DB: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT path, rel, size, mod_time, hash, is_dir FROM files`)
	if err != nil {
		return nil, fmt.Errorf("failed to query remote DB: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var pathStr, relStr, hashStr string
		var sizeInt, modNano int64
		var isDirInt int
		if err := rows.Scan(&pathStr, &relStr, &sizeInt, &modNano, &hashStr, &isDirInt); err != nil {
			continue
		}
		key := filepath.ToSlash(relStr)
		remoteByRel[key] = struct {
			Path  string
			Rel   string
			Size  int64
			Mod   int64
			Hash  string
			IsDir bool
		}{Path: pathStr, Rel: relStr, Size: sizeInt, Mod: modNano, Hash: hashStr, IsDir: isDirInt != 0}
	}

	// Create IgnoreCache
	ic := NewIgnoreCache(absRoot)

	// Connect SSH for uploads
	sshCli, err := ConnectSSH(cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh connect failed: %v", err)
	}
	defer sshCli.Close()

	// Prepare checked file path
	syncTemp := filepath.Join(absRoot, ".sync_temp")
	if err := os.MkdirAll(syncTemp, 0755); err != nil {
		return nil, fmt.Errorf("failed to create local .sync_temp: %v", err)
	}
	// checked list persisted previously to JSON is no longer required;
	// we rely on DB 'checked' in force mode paths.

	uploaded := make([]string, 0)
	var examined, skippedIgnored, skippedUpToDate, uploadErrors int
	var mu sync.Mutex // mutex for thread-safe access to shared variables

	// Collect all files that need upload
	const maxConcurrency = 5
	var uploadTasks []util.ConcurrentTask

	// worker slot channel: provides stable slot numbers 1..maxConcurrency
	slotCh := make(chan int, maxConcurrency)
	for i := 1; i <= maxConcurrency; i++ {
		slotCh <- i
	}
	var filesToProcess []struct {
		path      string
		rel       string
		localHash string
	}

	// First pass: collect all files that might need upload
	err = filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		// skip root directory itself
		if p == absRoot {
			return nil
		}

		// compute rel path
		rel, rerr := filepath.Rel(absRoot, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		// Always skip .sync_temp
		if rel == ".sync_temp" || strings.HasPrefix(rel, ".sync_temp/") || strings.Contains(rel, "/.sync_temp/") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// respect ignore cache
		if ic.Match(p, false) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			mu.Lock()
			skippedIgnored++
			mu.Unlock()
			return nil
		}

		if d.IsDir() {
			return nil
		}

		mu.Lock()
		examined++
		mu.Unlock()

		// compute local hash
		localHash := ""
		f, ferr := os.Open(p)
		if ferr == nil {
			h := xxhash.New()
			if _, err := io.Copy(h, f); err == nil {
				localHash = fmt.Sprintf("%x", h.Sum(nil))
			}
			f.Close()
		}

		filesToProcess = append(filesToProcess, struct {
			path      string
			rel       string
			localHash string
		}{p, rel, localHash})

		return nil
	})
	if err != nil {
		return uploaded, fmt.Errorf("walk error: %v", err)
	}

	// Second pass: create upload tasks for files that need uploading
	for _, file := range filesToProcess {
		// decide upload
		rm, exists := remoteByRel[file.rel]
		needUpload := false
		if !exists {
			needUpload = true
		} else if strings.TrimSpace(rm.Hash) == "" || rm.Hash != file.localHash {
			needUpload = true
		}

		if needUpload {
			lp := file.path
			rp := buildRemotePath(cfg, file.rel)
			uploadTasks = append(uploadTasks, func() error {
				id := <-slotCh
				defer func() { slotCh <- id }()
				util.Default.Printf("⬆️  %d -> Uploading %s -> %s\n", id, lp, rp)
				if err := sshCli.SyncFile(lp, rp); err != nil {
					util.Default.Printf("❌ %d Failed to upload %s: %v\n", id, lp, err)
					mu.Lock()
					uploadErrors++
					mu.Unlock()
					return err
				}
				mu.Lock()
				uploaded = append(uploaded, lp)
				mu.Unlock()
				return nil
			})
		} else {
			mu.Lock()
			skippedUpToDate++
			mu.Unlock()
		}
	}

	// Execute all upload tasks with bounded concurrency (max 5 parallel)
	if err := util.RunConcurrent(uploadTasks, maxConcurrency); err != nil {
		util.Default.Printf("⚠️  Some uploads failed: %v\n", err)
	}

	util.Default.Printf("🔁 Local files examined: %d, uploaded: %d, skipped(ignored): %d, skipped(up-to-date): %d, upload errors: %d\n",
		examined, len(uploaded), skippedIgnored, skippedUpToDate, uploadErrors)

	return uploaded, nil
}

// CompareAndDownloadByHashWithFilter behaves like CompareAndDownloadByHash but
// only operates on remote entries whose rel matches any of the provided prefixes.
// If prefixes is empty, it behaves like the full CompareAndDownloadByHash.
func CompareAndDownloadByHashWithFilter(cfg *config.Config, localRoot string, prefixes []string) ([]string, error) {
	// if prefixes empty, call existing
	if len(prefixes) == 0 {
		return CompareAndDownloadByHash(cfg, localRoot)
	}
	// reuse existing function but filter remote entries during download
	// determine local root (same as other function)
	root := localRoot
	if root == "" {
		if cfg.LocalPath != "" {
			root = cfg.LocalPath
		} else if cfg.Devsync.Auth.LocalPath != "" {
			root = cfg.Devsync.Auth.LocalPath
		} else {
			wd, err := os.Getwd()
			if err != nil {
				return nil, fmt.Errorf("failed to determine working dir: %v", err)
			}
			root = wd
		}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute root: %v", err)
	}

	localDBPath, err := DownloadIndexDB(cfg, absRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to download remote DB: %v", err)
	}

	// Load remote DB
	remoteByRel := map[string]struct {
		Path  string
		Rel   string
		Size  int64
		Mod   int64
		Hash  string
		IsDir bool
	}{}

	db, err := sql.Open("sqlite", localDBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open remote DB: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT path, rel, size, mod_time, hash, is_dir FROM files`)
	if err != nil {
		return nil, fmt.Errorf("failed to query remote DB: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var pathStr, relStr, hashStr string
		var sizeInt, modNano int64
		var isDirInt int
		if err := rows.Scan(&pathStr, &relStr, &sizeInt, &modNano, &hashStr, &isDirInt); err != nil {
			continue
		}
		key := filepath.ToSlash(relStr)
		// check if key matches any prefix
		matched := false
		for _, p := range prefixes {
			pp := strings.TrimPrefix(p, "/")
			if pp == "" {
				matched = true
				break
			}
			if strings.HasPrefix(key, pp) {
				matched = true
				break
			}
		}
		if matched {
			remoteByRel[key] = struct {
				Path  string
				Rel   string
				Size  int64
				Mod   int64
				Hash  string
				IsDir bool
			}{Path: pathStr, Rel: relStr, Size: sizeInt, Mod: modNano, Hash: hashStr, IsDir: isDirInt != 0}
		}
	}

	// Create IgnoreCache rooted at absRoot
	ic := NewIgnoreCache(absRoot)

	// Connect SSH once and reuse for downloads
	sshCli, err := ConnectSSH(cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh connect failed: %v", err)
	}
	defer sshCli.Close()

	// deterministically iterate remote entries by sorted rel
	rels := make([]string, 0, len(remoteByRel))
	for r := range remoteByRel {
		rels = append(rels, r)
	}
	sort.Strings(rels)

	downloaded := []string{}
	examined := 0
	skippedIgnored := 0
	skippedUpToDate := 0
	downloadErrors := 0
	var mu sync.Mutex // mutex for thread-safe access to shared variables

	// Collect all files that need download
	const maxConcurrency = 5
	var downloadTasks []util.ConcurrentTask

	// worker slot channel: provides stable slot numbers 1..maxConcurrency
	slotCh := make(chan int, maxConcurrency)
	for i := 1; i <= maxConcurrency; i++ {
		slotCh <- i
	}

	for _, rel := range rels {
		rm := remoteByRel[rel]
		mu.Lock()
		examined++
		mu.Unlock()

		if rm.IsDir {
			continue
		}

		relNorm := filepath.ToSlash(rel)
		localPath := filepath.Join(absRoot, filepath.FromSlash(relNorm))

		if relNorm == ".sync_temp" || strings.HasPrefix(relNorm, ".sync_temp/") || strings.Contains(relNorm, "/.sync_temp/") {
			mu.Lock()
			skippedIgnored++
			mu.Unlock()
			continue
		}

		if ic.Match(localPath, false) {
			mu.Lock()
			skippedIgnored++
			mu.Unlock()
			continue
		}

		info, statErr := os.Stat(localPath)
		if statErr != nil {
			// File doesn't exist, needs download
			remotePath := buildRemotePath(cfg, relNorm)
			// capture vars for closure
			rp := remotePath
			lp := localPath
			downloadTasks = append(downloadTasks, func() error {
				id := <-slotCh
				defer func() { slotCh <- id }()
				util.Default.Printf("⬇️  %d -> Downloading %s -> %s\n", id, rp, lp)
				if err := sshCli.DownloadFile(lp, rp); err != nil {
					util.Default.Printf("❌ %d Failed to download %s: %v\n", id, rp, err)
					mu.Lock()
					downloadErrors++
					mu.Unlock()
					return err
				}
				mu.Lock()
				downloaded = append(downloaded, lp)
				mu.Unlock()
				return nil
			})
			continue
		}
		if info.IsDir() {
			continue
		}

		// compute local hash
		localHash := ""
		f, err := os.Open(localPath)
		if err == nil {
			h := xxhash.New()
			if _, err := io.Copy(h, f); err == nil {
				localHash = fmt.Sprintf("%x", h.Sum(nil))
			}
			f.Close()
		}

		if strings.TrimSpace(rm.Hash) == "" || rm.Hash != localHash {
			// File exists but different, needs download
			remotePath := buildRemotePath(cfg, relNorm)
			rp := remotePath
			lp := localPath
			downloadTasks = append(downloadTasks, func() error {
				id := <-slotCh
				defer func() { slotCh <- id }()
				util.Default.Printf("⬇️  %d -> Downloading %s -> %s\n", id, rp, lp)
				if err := sshCli.DownloadFile(lp, rp); err != nil {
					util.Default.Printf("❌ %d Failed to download %s: %v\n", id, rp, err)
					mu.Lock()
					downloadErrors++
					mu.Unlock()
					return err
				}
				mu.Lock()
				downloaded = append(downloaded, lp)
				mu.Unlock()
				return nil
			})
			continue
		}

		mu.Lock()
		skippedUpToDate++
		mu.Unlock()
	}

	// Execute all download tasks with bounded concurrency (max 5 parallel)
	if err := util.RunConcurrent(downloadTasks, maxConcurrency); err != nil {
		util.Default.Printf("⚠️  Some downloads failed: %v\n", err)
	}

	util.Default.Printf("🔁 Remote index entries examined: %d, downloaded: %d, skipped(ignored): %d, skipped(up-to-date): %d, errors: %d\n",
		examined, len(downloaded), skippedIgnored, skippedUpToDate, downloadErrors)

	return downloaded, nil
}

// CompareAndUploadByHashWithFilter behaves like CompareAndUploadByHash but only
// operates on local paths whose rel starts with any of the provided prefixes.
// If prefixes is empty, it behaves like full CompareAndUploadByHash.
func CompareAndUploadByHashWithFilter(cfg *config.Config, localRoot string, prefixes []string) ([]string, error) {
	if len(prefixes) == 0 {
		return CompareAndUploadByHash(cfg, localRoot)
	}
	// reuse CompareAndUploadByHash logic but skip entries whose rel doesn't match prefixes
	root := localRoot
	if root == "" {
		if cfg.LocalPath != "" {
			root = cfg.LocalPath
		} else if cfg.Devsync.Auth.LocalPath != "" {
			root = cfg.Devsync.Auth.LocalPath
		} else {
			wd, err := os.Getwd()
			if err != nil {
				return nil, fmt.Errorf("failed to determine working dir: %v", err)
			}
			root = wd
		}
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute root: %v", err)
	}

	// Download remote DB into local .sync_temp
	localDBPath, err := DownloadIndexDB(cfg, absRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to download remote DB: %v", err)
	}

	// Load remote index DB
	remoteByRel := map[string]struct {
		Path  string
		Rel   string
		Size  int64
		Mod   int64
		Hash  string
		IsDir bool
	}{}

	db, err := sql.Open("sqlite", localDBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open remote DB: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(`SELECT path, rel, size, mod_time, hash, is_dir FROM files`)
	if err != nil {
		return nil, fmt.Errorf("failed to query remote DB: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var pathStr, relStr, hashStr string
		var sizeInt, modNano int64
		var isDirInt int
		if err := rows.Scan(&pathStr, &relStr, &sizeInt, &modNano, &hashStr, &isDirInt); err != nil {
			continue
		}
		key := filepath.ToSlash(relStr)
		remoteByRel[key] = struct {
			Path  string
			Rel   string
			Size  int64
			Mod   int64
			Hash  string
			IsDir bool
		}{Path: pathStr, Rel: relStr, Size: sizeInt, Mod: modNano, Hash: hashStr, IsDir: isDirInt != 0}
	}

	// Create IgnoreCache
	ic := NewIgnoreCache(absRoot)

	// Connect SSH for uploads
	sshCli, err := ConnectSSH(cfg)
	if err != nil {
		return nil, fmt.Errorf("ssh connect failed: %v", err)
	}
	defer sshCli.Close()

	// Prepare checked file path
	syncTemp := filepath.Join(absRoot, ".sync_temp")
	if err := os.MkdirAll(syncTemp, 0755); err != nil {
		return nil, fmt.Errorf("failed to create local .sync_temp: %v", err)
	}
	// No JSON checked file persistence; force mode uses DB 'checked'

	uploaded := make([]string, 0)
	var examined, skippedIgnored, skippedUpToDate, uploadErrors int
	var mu sync.Mutex // mutex for thread-safe access to shared variables

	// Collect all files that need upload
	const maxConcurrency = 5
	var uploadTasks []util.ConcurrentTask
	// worker slot channel: provides stable slot numbers 1..maxConcurrency
	slotCh := make(chan int, maxConcurrency)
	for i := 1; i <= maxConcurrency; i++ {
		slotCh <- i
	}

	var filesToProcess []struct {
		path      string
		rel       string
		localHash string
	}

	// First pass: collect all files that might need upload
	err = filepath.WalkDir(absRoot, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if p == absRoot {
			return nil
		}

		rel, rerr := filepath.Rel(absRoot, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)

		// check prefix match
		matched := false
		for _, pr := range prefixes {
			pp := strings.TrimPrefix(pr, "/")
			if pp == "" || strings.HasPrefix(rel, pp) {
				matched = true
				break
			}
		}
		if !matched {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Always skip .sync_temp
		if rel == ".sync_temp" || strings.HasPrefix(rel, ".sync_temp/") || strings.Contains(rel, "/.sync_temp/") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if ic.Match(p, false) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			mu.Lock()
			skippedIgnored++
			mu.Unlock()
			return nil
		}

		if d.IsDir() {
			return nil
		}

		mu.Lock()
		examined++
		mu.Unlock()

		// compute local hash
		localHash := ""
		f, ferr := os.Open(p)
		if ferr == nil {
			h := xxhash.New()
			if _, err := io.Copy(h, f); err == nil {
				localHash = fmt.Sprintf("%x", h.Sum(nil))
			}
			f.Close()
		}

		filesToProcess = append(filesToProcess, struct {
			path      string
			rel       string
			localHash string
		}{p, rel, localHash})

		return nil
	})
	if err != nil {
		return uploaded, fmt.Errorf("walk error: %v", err)
	}

	// Second pass: create upload tasks for files that need uploading
	for _, file := range filesToProcess {
		rm, exists := remoteByRel[file.rel]
		needUpload := false
		if !exists {
			needUpload = true
		} else if strings.TrimSpace(rm.Hash) == "" || rm.Hash != file.localHash {
			needUpload = true
		}

		if needUpload {
			lp := file.path
			rp := buildRemotePath(cfg, file.rel)
			uploadTasks = append(uploadTasks, func() error {
				id := <-slotCh
				defer func() { slotCh <- id }()
				util.Default.Printf("⬆️  %d -> Uploading %s -> %s\n", id, lp, rp)
				if err := sshCli.SyncFile(lp, rp); err != nil {
					util.Default.Printf("❌ %d Failed to upload %s: %v\n", id, lp, err)
					mu.Lock()
					uploadErrors++
					mu.Unlock()
					return err
				}
				mu.Lock()
				uploaded = append(uploaded, lp)
				mu.Unlock()
				return nil
			})
		} else {
			mu.Lock()
			skippedUpToDate++
			mu.Unlock()
		}
	}

	// Execute all upload tasks with bounded concurrency (max 5 parallel)
	if err := util.RunConcurrent(uploadTasks, maxConcurrency); err != nil {
		util.Default.Printf("⚠️  Some uploads failed: %v\n", err)
	}

	util.Default.Printf("🔁 Local files examined: %d, uploaded: %d, skipped(ignored): %d, skipped(up-to-date): %d, upload errors: %d\n",
		examined, len(uploaded), skippedIgnored, skippedUpToDate, uploadErrors)

	return uploaded, nil
}
