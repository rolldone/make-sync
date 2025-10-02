package deployagent

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"make-sync/internal/config"
	"make-sync/internal/sshclient"
)

// SSHClient defines the minimal methods used by deployagent. Concrete
// implementations should adapt the repository's SSH client to this interface.
type SSHClient interface {
	Connect() error
	Close() error
	UploadFile(localPath, remotePath string) error
	DownloadFile(localPath, remotePath string) error
	RunCommand(cmd string) error
	RunCommandWithOutput(cmd string) (string, error)
}

// DeployOptions contains parameters that control deployment behavior
type DeployOptions struct {
	// Timeout for remote operations
	Timeout time.Duration
	// Overwrite if remote binary exists
	Overwrite bool
	// OSTarget indicates remote OS ("windows", "linux", etc.)
	OSTarget string
}

// BuildOptions contains parameters that control agent building behavior
type BuildOptions struct {
	// SourceDir is the directory containing the agent source code
	SourceDir string
	// OutputDir is the directory where the built binary should be placed
	OutputDir string
	// TargetOS specifies the target operating system (linux, windows, darwin)
	TargetOS string
	// SSHClient for remote architecture detection (optional)
	SSHClient SSHClient
	// ProjectRoot for determining default paths
	ProjectRoot string
	// Config for unique agent naming
	Config *config.Config
}

// SSHClientAdapter adapts the concrete sshclient.SSHClient to the deployagent.SSHClient interface
type SSHClientAdapter struct {
	client *sshclient.SSHClient
}

// NewSSHClientAdapter creates an adapter from the concrete SSH client
func NewSSHClientAdapter(client *sshclient.SSHClient) *SSHClientAdapter {
	return &SSHClientAdapter{client: client}
}

func (a *SSHClientAdapter) Connect() error {
	return a.client.Connect()
}

func (a *SSHClientAdapter) Close() error {
	return a.client.Close()
}

func (a *SSHClientAdapter) UploadFile(localPath, remotePath string) error {
	return a.client.UploadFile(localPath, remotePath)
}

func (a *SSHClientAdapter) DownloadFile(localPath, remotePath string) error {
	return a.client.DownloadFile(localPath, remotePath)
}

func (a *SSHClientAdapter) RunCommand(cmd string) error {
	return a.client.RunCommand(cmd)
}

func (a *SSHClientAdapter) RunCommandWithOutput(cmd string) (string, error) {
	return a.client.RunCommandWithOutput(cmd)
}

// EnsureRemoteDir creates remote directory if it doesn't exist
func EnsureRemoteDir(ssh SSHClient, remoteDir, osTarget string) error {
	isWindows := strings.Contains(strings.ToLower(osTarget), "win")
	var cmd string
	if isWindows {
		cmd = fmt.Sprintf("cmd.exe /C if not exist \"%s\" mkdir \"%s\"", remoteDir, remoteDir)
	} else {
		cmd = fmt.Sprintf("mkdir -p '%s'", strings.ReplaceAll(remoteDir, "'", "'\\''"))
	}
	return ssh.RunCommand(cmd)
}

// GetFileHash calculates SHA256 hash of local file
func GetFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

// ShouldSkipUpload checks if remote file has same hash as local file
func ShouldSkipUpload(ssh SSHClient, localPath, remotePath, osTarget string) (bool, error) {
	localHash, err := GetFileHash(localPath)
	if err != nil {
		return false, fmt.Errorf("failed to get local file hash: %v", err)
	}

	isWindows := strings.Contains(strings.ToLower(osTarget), "win")
	var hashCmd string
	if isWindows {
		// Use PowerShell Get-FileHash on Windows
		hashCmd = fmt.Sprintf("powershell -Command \"(Get-FileHash '%s' -Algorithm SHA256).Hash.ToLower()\"", remotePath)
	} else {
		// Use sha256sum on Unix
		hashCmd = fmt.Sprintf("sha256sum '%s' | cut -d' ' -f1", strings.ReplaceAll(remotePath, "'", "'\\''"))
	}

	remoteHash, err := ssh.RunCommandWithOutput(hashCmd)
	if err != nil {
		// File might not exist or command failed - don't skip upload
		return false, nil
	}

	remoteHash = strings.TrimSpace(strings.ToLower(remoteHash))
	localHashLower := strings.ToLower(localHash)

	return remoteHash == localHashLower, nil
}

// SetExecutePermission makes file executable on Unix systems
func SetExecutePermission(ssh SSHClient, remotePath, osTarget string) error {
	isWindows := strings.Contains(strings.ToLower(osTarget), "win")
	if isWindows {
		return nil // No chmod needed on Windows
	}

	cmd := fmt.Sprintf("chmod +x '%s'", strings.ReplaceAll(remotePath, "'", "'\\''"))
	return ssh.RunCommand(cmd)
}

// BuildAgentForTarget builds the agent executable for the specified target OS with cross-compilation support.
// It handles architecture detection and proper build environment setup.
func BuildAgentForTarget(opts BuildOptions) (string, error) {
	// Set defaults
	if opts.TargetOS == "" {
		opts.TargetOS = "linux"
	}
	if opts.SourceDir == "" && opts.ProjectRoot != "" {
		opts.SourceDir = filepath.Join(opts.ProjectRoot, "sub_app", "agent")
		log.Printf("🔍 DEBUG: SourceDir auto-resolved from ProjectRoot: %s -> %s", opts.ProjectRoot, opts.SourceDir)
	}
	if opts.OutputDir == "" && opts.ProjectRoot != "" {
		opts.OutputDir = opts.ProjectRoot
		log.Printf("🔍 DEBUG: OutputDir set from ProjectRoot: %s", opts.OutputDir)
	}

	log.Printf("🔍 DEBUG: Final paths - ProjectRoot: %s, SourceDir: %s, OutputDir: %s", opts.ProjectRoot, opts.SourceDir, opts.OutputDir)

	// Validate source directory
	if opts.SourceDir == "" {
		return "", fmt.Errorf("source directory required")
	}
	if _, err := os.Stat(opts.SourceDir); os.IsNotExist(err) {
		fmt.Printf("❌ DEBUG: Source directory check failed: %s\n", opts.SourceDir)
		return "", fmt.Errorf("source directory does not exist: %s", opts.SourceDir)
	}

	// Determine GOOS and binary name using local config
	var goos string
	var binaryName string

	// Get unique agent name from local config
	localConfig, err := config.GetOrCreateLocalConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load local config: %v", err)
	}

	binaryName = localConfig.GetAgentBinaryName(opts.TargetOS)

	switch strings.ToLower(opts.TargetOS) {
	case "linux":
		goos = "linux"
	case "windows", "win":
		goos = "windows"
		// Note: .exe extension already handled in GetAgentBinaryName() or fallback above
	case "darwin", "macos":
		goos = "darwin"
	default:
		goos = "linux" // default fallback
	}

	// Build output path
	outputPath := filepath.Join(opts.OutputDir, binaryName)

	log.Printf("🔨 DEBUG: Building agent - SourceDir: %s, GOOS: %s, OutputPath: %s", opts.SourceDir, goos, outputPath)
	log.Printf("🔍 DEBUG: Binary name: %s, Binary will be created at: %s", binaryName, outputPath)

	// Prepare build command with static linking flags for maximum compatibility
	cmd := exec.Command("go", "build",
		"-ldflags", "-w -s -extldflags '-static'", // Strip symbols and create static binary
		"-o", outputPath, ".")
	cmd.Dir = opts.SourceDir

	// Prepare environment variables for cross-compilation
	env := []string{}
	for _, e := range os.Environ() {
		// Skip existing GOOS/GOARCH/GOARM/CGO_ENABLED to avoid conflicts
		if strings.HasPrefix(e, "GOOS=") ||
			strings.HasPrefix(e, "GOARCH=") ||
			strings.HasPrefix(e, "GOARM=") ||
			strings.HasPrefix(e, "CGO_ENABLED=") {
			continue
		}
		env = append(env, e)
	}

	// Set target OS and disable CGO for maximum compatibility
	env = append(env, "GOOS="+goos)
	env = append(env, "CGO_ENABLED=0")

	// Detect remote architecture if SSH client provided
	if opts.SSHClient != nil {
		if output, err := opts.SSHClient.RunCommandWithOutput("uname -m"); err == nil {
			arch := strings.TrimSpace(output)

			// Map uname -m output to Go architecture
			switch arch {
			case "x86_64", "amd64":
				env = append(env, "GOARCH=amd64")
			case "aarch64", "arm64":
				env = append(env, "GOARCH=arm64")
			case "armv7l", "armv7":
				env = append(env, "GOARCH=arm")
				env = append(env, "GOARM=7")
			case "armv6l", "armv6":
				env = append(env, "GOARCH=arm")
				env = append(env, "GOARM=6")
			default:
				fmt.Printf("⚠️  Unknown architecture '%s', using Go defaults\n", arch)
			}
			fmt.Printf("ℹ️  Detected remote architecture: %s\n", arch)
		} else {
			fmt.Printf("⚠️  Could not detect remote architecture: %v\n", err)
		}
	}

	cmd.Env = env

	// Execute build
	log.Printf("🔧 DEBUG: Executing build command: %s", cmd.String())
	log.Printf("🔧 DEBUG: Build environment variables: %v", cmd.Env)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("❌ DEBUG: Build failed: %v", err)
		log.Printf("❌ DEBUG: Build output: %s", string(output))
		return "", fmt.Errorf("build failed: %v\nOutput: %s", err, string(output))
	}

	if len(output) > 0 {
		log.Printf("✅ DEBUG: Build output: %s", string(output))
	}

	// Verify the output file exists
	if _, err := os.Stat(outputPath); os.IsNotExist(err) {
		log.Printf("❌ DEBUG: Build output file does not exist: %s", outputPath)
		return "", fmt.Errorf("build output file does not exist: %s", outputPath)
	}

	// Get file info for verification
	if fileInfo, err := os.Stat(outputPath); err == nil {
		log.Printf("✅ DEBUG: Built file verified - Path: %s, Size: %d bytes, Mode: %s", outputPath, fileInfo.Size(), fileInfo.Mode())
	}

	log.Printf("✅ DEBUG: Agent built successfully at: %s", outputPath)
	return outputPath, nil
}

// BuildAndDeployAgent is a high-level function that builds and deploys an agent in one operation.
// It orchestrates the build -> deploy -> verify workflow.
func BuildAndDeployAgent(ctx context.Context, cfg *config.Config, ssh SSHClient, buildOpts BuildOptions, deployOpts DeployOptions) error {
	log.Printf("🚀 DEBUG: Starting BuildAndDeployAgent with TargetOS: %s", buildOpts.TargetOS)

	// Ensure config is passed to build options
	buildOpts.Config = cfg

	// Build agent
	log.Printf("🔧 DEBUG: Starting agent build process...")
	builtAgentPath, err := BuildAgentForTarget(buildOpts)
	if err != nil {
		log.Printf("❌ DEBUG: Agent build failed: %v", err)
		return fmt.Errorf("build failed: %v", err)
	}
	log.Printf("✅ DEBUG: Agent built successfully at: %s", builtAgentPath)

	// Determine remote directory
	remoteDir := cfg.Devsync.Auth.RemotePath
	log.Printf("📁 DEBUG: RemotePath from config: '%s'", remoteDir)

	if remoteDir == "" {
		if strings.Contains(strings.ToLower(deployOpts.OSTarget), "win") {
			remoteDir = "%TEMP%\\.sync_temp"
		} else {
			remoteDir = "/tmp/.sync_temp"
		}
		log.Printf("📁 DEBUG: Using default remote directory: %s", remoteDir)
	} else {
		if strings.Contains(strings.ToLower(deployOpts.OSTarget), "win") {
			remoteDir = filepath.Join(remoteDir, ".sync_temp")
		} else {
			remoteDir = filepath.Join(remoteDir, ".sync_temp")
			remoteDir = filepath.ToSlash(remoteDir)
		}
		log.Printf("📁 DEBUG: Using configured remote directory: %s", remoteDir)
	}

	// Deploy agent
	log.Printf("📤 DEBUG: Starting deployment to remote directory: %s", remoteDir)
	if err := DeployAgent(ctx, cfg, ssh, builtAgentPath, remoteDir, deployOpts); err != nil {
		log.Printf("❌ DEBUG: Deployment failed: %v", err)
		return fmt.Errorf("deployment failed: %v", err)
	}

	fmt.Println("✅ Build and deploy completed successfully")
	return nil
}

// FindFallbackAgent looks for pre-built agent binaries as fallback options.
// Returns the path to a suitable fallback binary or empty string if none found.
func FindFallbackAgent(projectRoot, targetOS string) string {
	fallbackNames := []string{
		fmt.Sprintf("sync-agent-%s", targetOS),
		"sync-agent-linux",
		"sync-agent-windows.exe",
		filepath.Join("sub_app", "agent", "sync-agent"),
		filepath.Join("sub_app", "agent", "sync-agent.exe"),
	}

	if strings.Contains(strings.ToLower(targetOS), "win") {
		// For Windows, also try with .exe extension
		fallbackNames = append([]string{fmt.Sprintf("sync-agent-%s.exe", targetOS)}, fallbackNames...)
	}

	for _, name := range fallbackNames {
		fullPath := filepath.Join(projectRoot, name)
		if _, err := os.Stat(fullPath); err == nil {
			fmt.Printf("ℹ️  Found fallback agent binary: %s\n", fullPath)
			return fullPath
		}
	}

	return ""
}

// DeployAgent uploads an agent binary, sets permissions, and handles identity checks.
// This is the high-level function that orchestrates the deployment process.
func DeployAgent(ctx context.Context, cfg *config.Config, ssh SSHClient, localAgentPath, remoteDir string, opts DeployOptions) error {
	log.Printf("🚀 DEBUG: DeployAgent called with localPath: %s, remoteDir: %s", localAgentPath, remoteDir)

	// Basic validation
	if ssh == nil {
		log.Printf("❌ DEBUG: SSH client is nil")
		return fmt.Errorf("ssh client required")
	}

	// Check if local agent file exists
	if localAgentPath == "" {
		log.Printf("❌ DEBUG: localAgentPath is empty")
		return fmt.Errorf("local agent path is required")
	}
	if _, err := os.Stat(localAgentPath); os.IsNotExist(err) {
		log.Printf("❌ DEBUG: Local agent file does not exist: %s", localAgentPath)
		return fmt.Errorf("local agent file does not exist: %s", localAgentPath)
	}
	log.Printf("✅ DEBUG: Local agent file exists: %s", localAgentPath)

	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.OSTarget == "" {
		opts.OSTarget = "linux" // default assumption
	}
	log.Printf("⚙️ DEBUG: Deploy options - OSTarget: %s, Timeout: %v, Overwrite: %v", opts.OSTarget, opts.Timeout, opts.Overwrite)

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, opts.Timeout)
	defer cancel()

	// Ensure remote directory exists
	log.Printf("📁 DEBUG: Ensuring remote directory exists: %s", remoteDir)
	if err := EnsureRemoteDir(ssh, remoteDir, opts.OSTarget); err != nil {
		log.Printf("❌ DEBUG: Failed to create remote directory: %v", err)
		return fmt.Errorf("failed to create remote directory: %v", err)
	}
	log.Printf("✅ DEBUG: Remote directory ensured: %s", remoteDir)

	// Build remote file path using unique agent binary name
	localConfig, err := config.GetOrCreateLocalConfig()
	if err != nil {
		log.Printf("❌ DEBUG: Failed to load local config: %v", err)
		return fmt.Errorf("failed to load local config: %v", err)
	}

	agentBinaryName := localConfig.GetAgentBinaryName(opts.OSTarget)
	log.Printf("🔧 DEBUG: Agent binary name from config: %s", agentBinaryName)

	isWindows := strings.Contains(strings.ToLower(opts.OSTarget), "win")
	var remotePath string
	if isWindows {
		remotePath = filepath.Join(remoteDir, agentBinaryName)
		remotePath = strings.ReplaceAll(remotePath, "/", "\\")
	} else {
		remotePath = filepath.Join(remoteDir, agentBinaryName)
		remotePath = filepath.ToSlash(remotePath)
	}
	log.Printf("🎯 DEBUG: Final remote path constructed: %s (isWindows: %v)", remotePath, isWindows)

	// Check if we should skip upload (unless Overwrite is true)
	if !opts.Overwrite {
		log.Printf("🔍 DEBUG: Checking if upload should be skipped (Overwrite=false)")
		select {
		case <-timeoutCtx.Done():
			return fmt.Errorf("timeout during skip check")
		default:
		}

		shouldSkip, err := ShouldSkipUpload(ssh, localAgentPath, remotePath, opts.OSTarget)
		if err == nil && shouldSkip {
			log.Printf("⏭️ DEBUG: File is identical, skipping upload")
			// File is identical, just ensure permissions and return
			if err := SetExecutePermission(ssh, remotePath, opts.OSTarget); err != nil {
				return fmt.Errorf("failed to set execute permission: %v", err)
			}
			log.Printf("✅ DEBUG: Permissions set, deployment complete")
			return nil
		}
		log.Printf("🔄 DEBUG: File differs or error checking, proceeding with upload")
		// If error or not identical, proceed with upload
	} else {
		log.Printf("🔄 DEBUG: Overwrite=true, forcing upload")
	}

	// Upload the file
	log.Printf("📤 DEBUG: Starting file upload from %s to %s", localAgentPath, remotePath)
	select {
	case <-timeoutCtx.Done():
		log.Printf("⏰ DEBUG: Timeout during upload")
		return fmt.Errorf("timeout during upload")
	default:
	}

	if err := ssh.UploadFile(localAgentPath, remotePath); err != nil {
		log.Printf("❌ DEBUG: Upload failed: %v", err)
		return fmt.Errorf("upload failed: %v", err)
	}
	log.Printf("✅ DEBUG: File uploaded successfully to %s", remotePath)

	// Set execute permission on Unix
	log.Printf("🔧 DEBUG: Setting execute permissions on %s", remotePath)
	if err := SetExecutePermission(ssh, remotePath, opts.OSTarget); err != nil {
		log.Printf("❌ DEBUG: Failed to set execute permission: %v", err)
		return fmt.Errorf("failed to set execute permission: %v", err)
	}
	log.Printf("✅ DEBUG: Execute permissions set successfully")

	log.Printf("🎉 DEBUG: DeployAgent completed successfully")
	return nil
}
