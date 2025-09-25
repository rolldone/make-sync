# DeployAgent Package

Package `deployagent` menyediakan fungsionalitas lengkap untuk build, deploy, dan manage agent binaries secara remote melalui SSH. Package ini mendukung workflow Build -> Deploy -> Verify dengan fitur-fitur enterprise seperti identity checking dan cross-compilation.

## Features

### 🏗️ **Build System**
- **Cross-Compilation**: Build agent untuk target OS yang berbeda (Linux, Windows, Darwin)
- **Architecture Detection**: Otomatis deteksi arsitektur remote via SSH (x86_64, arm64, armv7, etc.)
- **Fallback Support**: Pencarian otomatis pre-built binaries sebagai fallback
- **Source Discovery**: Otomatis menemukan source directory dari project structure

### 🚀 **Deployment System**  
- **Identity Check**: SHA256 hash comparison untuk skip upload yang tidak perlu
- **Cross-Platform Support**: Windows dan Unix systems dengan path/command handling yang tepat
- **Timeout Support**: Context-based timeout protection untuk semua operasi
- **Platform-Aware Permissions**: Otomatis `chmod +x` pada Unix systems
- **Directory Creation**: Otomatis buat remote directory jika belum ada

### 🔧 **Architecture & Quality**
- **Interface-Based Design**: Easy mocking dan testing
- **Comprehensive Error Handling**: Wrapped errors dengan context yang jelas
- **Build -> Deploy -> Verify**: End-to-end workflow orchestration

## Usage

### Build & Deploy Workflow (Recommended)

```go
package main

import (
    "context"
    "time"
    
    "make-sync/internal/deployagent"
    "make-sync/internal/sshclient"
)

func main() {
    // Setup SSH client
    sshClient, err := sshclient.NewSSHClient("user@host:22")
    if err != nil {
        panic(err)
    }
    defer sshClient.Close()
    
    if err := sshClient.Connect(); err != nil {
        panic(err)
    }
    
    // Create adapter
    adapter := deployagent.NewSSHClientAdapter(sshClient)
    
    // Build options
    buildOpts := deployagent.BuildOptions{
        ProjectRoot: "/path/to/project",
        TargetOS:    "linux",
        SSHClient:   adapter, // For architecture detection
    }
    
    // Deploy options  
    deployOpts := deployagent.DeployOptions{
        Timeout:   30 * time.Second,
        Overwrite: false,
        OSTarget:  "linux",
    }
    
    // Build and deploy in one call
    err = deployagent.BuildAndDeployAgent(
        context.Background(),
        cfg,
        adapter, 
        buildOpts,
        deployOpts,
    )
    if err != nil {
        panic(err)
    }
}
```

### Build Only

```go
// Build agent with cross-compilation
buildOpts := deployagent.BuildOptions{
    ProjectRoot: "/path/to/project", 
    TargetOS:    "linux",
    SSHClient:   sshAdapter, // Optional: for arch detection
}

agentPath, err := deployagent.BuildAgentForTarget(buildOpts)
if err != nil {
    // Try fallback binary
    fallback := deployagent.FindFallbackAgent("/path/to/project", "linux")
    if fallback != "" {
        agentPath = fallback
    } else {
        panic(err)
    }
}

fmt.Printf("Agent ready: %s\n", agentPath)
```

### Deploy Only (Legacy)

```go
package main

import (
    "context"
    "time"
    
    "make-sync/internal/deployagent"
    "make-sync/internal/sshclient"
)

func main() {
    // Setup SSH client
    sshClient, err := sshclient.NewSSHClient("user@host:22")
    if err != nil {
        panic(err)
    }
    defer sshClient.Close()
    
    if err := sshClient.Connect(); err != nil {
        panic(err)
    }
    
    // Create adapter untuk interface compliance
    adapter := &deployagent.SSHClientAdapter{Client: sshClient}
    
    // Deploy options
    opts := deployagent.DeployOptions{
        Timeout:   30 * time.Second,
        Overwrite: false, // Skip jika file sudah identik
        OSTarget:  "linux",
    }
    
    // Deploy agent
    err = deployagent.DeployAgent(
        context.Background(),
        nil,
        adapter,
        "./local-agent-binary",
        "/tmp/remote-dir",
        opts,
    )
    if err != nil {
        panic(err)
    }
}
```

### Advanced Usage dengan Identity Check

```go
// Cek apakah file perlu diupload
shouldSkip, err := deployagent.ShouldSkipUpload(
    sshAdapter,
    "./local-agent", 
    "/remote/path/agent",
    "linux",
)
if err == nil && shouldSkip {
    fmt.Println("File identik, skip upload")
}

// Manual file hash check
localHash, err := deployagent.GetFileHash("./local-file")
if err != nil {
    panic(err)
}
fmt.Printf("Local file hash: %s\n", localHash)
```

### Testing dengan Mock

```go
func TestMyDeployment(t *testing.T) {
    mock := NewMockSSHClient()
    
    // Setup mock responses
    mock.SetCommandResponse("sha256sum", "expected-hash")
    mock.SetShouldFail("upload", false)
    
    opts := deployagent.DeployOptions{
        Timeout:  5 * time.Second,
        OSTarget: "linux",
    }
    
    err := deployagent.DeployAgent(ctx, nil, mock, localPath, remotePath, opts)
    assert.NoError(t, err)
    
    // Verify behavior
    assert.Equal(t, 1, len(mock.uploadCalls))
}
```

## Types

### SSHClient Interface

```go
type SSHClient interface {
    Connect() error
    Close() error
    UploadFile(localPath, remotePath string) error
    DownloadFile(localPath, remotePath string) error
    RunCommand(cmd string) error
    RunCommandWithOutput(cmd string) (string, error)
}
```

### DeployOptions

```go
type DeployOptions struct {
    Timeout   time.Duration // Timeout untuk operasi deployment
    Overwrite bool         // True = selalu upload, False = skip jika identik
    OSTarget  string       // "linux", "windows", "darwin", etc.
}
```

### BuildOptions

```go
type BuildOptions struct {
    SourceDir   string    // Directory containing agent source code
    OutputDir   string    // Directory untuk output binary
    TargetOS    string    // Target OS: "linux", "windows", "darwin"
    SSHClient   SSHClient // Optional: untuk remote architecture detection
    ProjectRoot string    // Project root untuk default paths
}
```

### SSHClientAdapter

```go
type SSHClientAdapter struct {
    Client *sshclient.SSHClient
}
```

Adapter ini mengimplementasikan interface `SSHClient` dengan membungkus concrete `sshclient.SSHClient`.

## Functions

### BuildAgentForTarget

```go
func BuildAgentForTarget(opts BuildOptions) (string, error)
```

Membangun agent binary untuk target OS dengan dukungan cross-compilation. Features:
- Deteksi arsitektur remote otomatis via SSH
- Cross-compilation dengan GOOS/GOARCH yang tepat  
- Validasi source directory dan build environment
- Support ARM variants (armv6, armv7) dengan GOARM

### BuildAndDeployAgent

```go
func BuildAndDeployAgent(ctx context.Context, cfg *config.Config, ssh SSHClient, buildOpts BuildOptions, deployOpts DeployOptions) error
```

High-level orchestration function untuk Build -> Deploy workflow:
1. Build agent menggunakan `BuildAgentForTarget`
2. Deploy hasil build menggunakan `DeployAgent` 
3. Verify deployment success

### FindFallbackAgent

```go
func FindFallbackAgent(projectRoot, targetOS string) string
```

Mencari pre-built agent binaries sebagai fallback. Memeriksa lokasi-lokasi seperti:
- `sync-agent-{targetOS}[.exe]` di project root
- `sub_app/agent/sync-agent[.exe]`
- Generic fallbacks berdasarkan OS pattern

### DeployAgent

```go
func DeployAgent(ctx context.Context, cfg *config.Config, ssh SSHClient, localAgentPath, remoteDir string, opts DeployOptions) error
```

Fungsi utama untuk mengunggah agent binary ke remote system. Melakukan:
1. Validasi input dan setup timeout
2. Membuat directory remote jika perlu
3. Pengecekan identitas file (jika Overwrite=false)
4. Upload file jika diperlukan
5. Set execute permission (Unix only)

### ShouldSkipUpload

```go
func ShouldSkipUpload(ssh SSHClient, localPath, remotePath, osTarget string) (bool, error)
```

Membandingkan SHA256 hash file lokal dan remote. Returns `true` jika file identik dan upload bisa di-skip.

### GetFileHash

```go
func GetFileHash(filePath string) (string, error)
```

Menghitung SHA256 hash dari file lokal. Returns lowercase hex string.

### EnsureRemoteDir

```go
func EnsureRemoteDir(ssh SSHClient, remotePath, osTarget string) error
```

Membuat directory remote jika belum ada. Menggunakan:
- `mkdir -p` untuk Unix systems
- `cmd.exe /C if not exist ... mkdir` untuk Windows

### SetExecutePermission

```go
func SetExecutePermission(ssh SSHClient, remotePath, osTarget string) error
```

Mengset execute permission dengan `chmod +x` pada Unix systems. No-op pada Windows.

## Platform Differences

### Windows
- Menggunakan `PowerShell Get-FileHash` untuk hash calculation
- Path separator: `\`
- File extension: `.exe`
- No execute permission setting
- Directory creation: `cmd.exe /C if not exist ... mkdir`

### Unix (Linux/macOS)
- Menggunakan `sha256sum` untuk hash calculation  
- Path separator: `/`
- No file extension
- Execute permission: `chmod +x`
- Directory creation: `mkdir -p`

## Error Handling

Package menggunakan wrapped errors dengan context yang jelas:

```go
return fmt.Errorf("upload failed: %v", err)
return fmt.Errorf("failed to create remote directory: %v", err)
return fmt.Errorf("timeout during upload")
```

## Testing

Package menyediakan `MockSSHClient` untuk unit testing:

```go
mock := NewMockSSHClient()
mock.SetCommandResponse("sha256sum", "expected-hash")
mock.SetShouldFail("upload", true)

// Run tests...
assert.Equal(t, expectedCalls, len(mock.uploadCalls))
```

## Dependencies

- `crypto/sha256`: Untuk file hashing
- `context`: Untuk timeout management
- `make-sync/internal/sshclient`: SSH client implementation
- `make-sync/internal/config`: Configuration handling