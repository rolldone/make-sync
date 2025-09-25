package deployagent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// MockSSHClient implements SSHClient interface for testing
type MockSSHClient struct {
	connected        bool
	files            map[string]string // remote path -> content hash
	commands         []string          // track commands executed
	uploadCalls      []UploadCall      // track upload calls
	shouldFail       map[string]bool   // operation -> should fail
	commandResponses map[string]string // command -> response mapping
}

type UploadCall struct {
	LocalPath  string
	RemotePath string
}

func NewMockSSHClient() *MockSSHClient {
	return &MockSSHClient{
		files:            make(map[string]string),
		commands:         make([]string, 0),
		uploadCalls:      make([]UploadCall, 0),
		shouldFail:       make(map[string]bool),
		commandResponses: make(map[string]string),
	}
}

func (m *MockSSHClient) Connect() error {
	if m.shouldFail["connect"] {
		return fmt.Errorf("mock connect failure")
	}
	m.connected = true
	return nil
}

func (m *MockSSHClient) Close() error {
	m.connected = false
	return nil
}

func (m *MockSSHClient) UploadFile(localPath, remotePath string) error {
	if m.shouldFail["upload"] {
		return fmt.Errorf("mock upload failure")
	}

	m.uploadCalls = append(m.uploadCalls, UploadCall{
		LocalPath:  localPath,
		RemotePath: remotePath,
	})

	// Simulate storing the file with a fake hash
	m.files[remotePath] = "uploaded"
	return nil
}

func (m *MockSSHClient) DownloadFile(localPath, remotePath string) error {
	return nil // Not used in current tests
}

func (m *MockSSHClient) RunCommand(cmd string) error {
	if m.shouldFail["command"] {
		return fmt.Errorf("mock command failure")
	}

	m.commands = append(m.commands, cmd)
	return nil
}

func (m *MockSSHClient) RunCommandWithOutput(cmd string) (string, error) {
	if m.shouldFail["command_output"] {
		return "", fmt.Errorf("mock command output failure")
	}

	m.commands = append(m.commands, cmd)

	// Check for specific command responses
	for cmdPattern, response := range m.commandResponses {
		if strings.Contains(cmd, cmdPattern) {
			return response, nil
		}
	}

	// Mock hash command responses - default behavior
	if strings.Contains(cmd, "sha256sum") || strings.Contains(cmd, "Get-FileHash") {
		// Return different hash to simulate file doesn't match
		return "different_hash", nil
	}

	return "mock output", nil
}

// SetFileHash simulates a file existing remotely with specific hash
func (m *MockSSHClient) SetFileHash(remotePath, hash string) {
	m.files[remotePath] = hash
}

// SetShouldFail makes specific operations fail for testing error paths
func (m *MockSSHClient) SetShouldFail(operation string, fail bool) {
	m.shouldFail[operation] = fail
}

// SetCommandResponse sets a response for commands containing specific pattern
func (m *MockSSHClient) SetCommandResponse(cmdPattern, response string) {
	m.commandResponses[cmdPattern] = response
}

func createTestFile(t *testing.T, content string) string {
	tmpFile, err := os.CreateTemp("", "test-agent-*")
	if err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	defer tmpFile.Close()

	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	return tmpFile.Name()
}

func TestDeployAgent_Success(t *testing.T) {
	mock := NewMockSSHClient()
	testFile := createTestFile(t, "test agent content")
	defer os.Remove(testFile)

	opts := DeployOptions{
		Timeout:   5 * time.Second,
		Overwrite: true,
		OSTarget:  "linux",
	}

	err := DeployAgent(context.Background(), nil, mock, testFile, "/tmp/test", opts)
	if err != nil {
		t.Fatalf("DeployAgent failed: %v", err)
	}

	// Verify upload was called
	if len(mock.uploadCalls) != 1 {
		t.Fatalf("Expected 1 upload call, got %d", len(mock.uploadCalls))
	}

	uploadCall := mock.uploadCalls[0]
	if uploadCall.LocalPath != testFile {
		t.Errorf("Expected local path %s, got %s", testFile, uploadCall.LocalPath)
	}

	expectedRemotePath := "/tmp/test/sync-agent"
	if uploadCall.RemotePath != expectedRemotePath {
		t.Errorf("Expected remote path %s, got %s", expectedRemotePath, uploadCall.RemotePath)
	}

	// Verify chmod command was executed
	foundChmod := false
	for _, cmd := range mock.commands {
		if strings.Contains(cmd, "chmod +x") {
			foundChmod = true
			break
		}
	}
	if !foundChmod {
		t.Error("Expected chmod +x command to be executed")
	}
}

func TestDeployAgent_Windows(t *testing.T) {
	mock := NewMockSSHClient()
	testFile := createTestFile(t, "test agent content")
	defer os.Remove(testFile)

	opts := DeployOptions{
		Timeout:   5 * time.Second,
		Overwrite: true,
		OSTarget:  "windows",
	}

	err := DeployAgent(context.Background(), nil, mock, testFile, "C:\\temp\\test", opts)
	if err != nil {
		t.Fatalf("DeployAgent failed: %v", err)
	}

	// Verify Windows path was used
	if len(mock.uploadCalls) != 1 {
		t.Fatalf("Expected 1 upload call, got %d", len(mock.uploadCalls))
	}

	expectedRemotePath := "C:\\temp\\test\\sync-agent.exe"
	if mock.uploadCalls[0].RemotePath != expectedRemotePath {
		t.Errorf("Expected remote path %s, got %s", expectedRemotePath, mock.uploadCalls[0].RemotePath)
	}

	// Verify no chmod command for Windows
	for _, cmd := range mock.commands {
		if strings.Contains(cmd, "chmod") {
			t.Error("chmod should not be executed on Windows")
		}
	}
}

func TestDeployAgent_SkipIfIdentical(t *testing.T) {
	mock := NewMockSSHClient()
	testFile := createTestFile(t, "test content")
	defer os.Remove(testFile)

	// Get the actual hash of our test file
	actualHash, err := GetFileHash(testFile)
	if err != nil {
		t.Fatalf("Failed to get test file hash: %v", err)
	}

	// Set mock to return just the hash (sha256sum | cut -d' ' -f1 gives only the hash part)
	mock.SetCommandResponse("sha256sum", strings.ToLower(actualHash))

	opts := DeployOptions{
		Timeout:   5 * time.Second,
		Overwrite: false, // Don't overwrite - should skip if identical
		OSTarget:  "linux",
	}

	err = DeployAgent(context.Background(), nil, mock, testFile, "/tmp/test", opts)
	if err != nil {
		t.Fatalf("DeployAgent failed: %v", err)
	}

	// Verify NO upload was performed since files are identical
	if len(mock.uploadCalls) != 0 {
		t.Errorf("Expected 0 upload calls (should skip), got %d", len(mock.uploadCalls))
	}

	// Verify chmod was still executed (to ensure permissions)
	foundChmod := false
	for _, cmd := range mock.commands {
		if strings.Contains(cmd, "chmod +x") {
			foundChmod = true
			break
		}
	}
	if !foundChmod {
		t.Error("Expected chmod +x command to be executed even when skipping upload")
	}
}

func TestDeployAgent_UploadFailure(t *testing.T) {
	mock := NewMockSSHClient()
	mock.SetShouldFail("upload", true)

	testFile := createTestFile(t, "test content")
	defer os.Remove(testFile)

	opts := DeployOptions{
		Timeout:   5 * time.Second,
		Overwrite: true,
		OSTarget:  "linux",
	}

	err := DeployAgent(context.Background(), nil, mock, testFile, "/tmp/test", opts)
	if err == nil {
		t.Fatal("Expected DeployAgent to fail when upload fails")
	}

	if !strings.Contains(err.Error(), "upload failed") {
		t.Errorf("Expected 'upload failed' in error, got: %v", err)
	}
}

func TestEnsureRemoteDir_Linux(t *testing.T) {
	mock := NewMockSSHClient()

	err := EnsureRemoteDir(mock, "/tmp/test/dir", "linux")
	if err != nil {
		t.Fatalf("EnsureRemoteDir failed: %v", err)
	}

	if len(mock.commands) != 1 {
		t.Fatalf("Expected 1 command, got %d", len(mock.commands))
	}

	expectedCmd := "mkdir -p '/tmp/test/dir'"
	if mock.commands[0] != expectedCmd {
		t.Errorf("Expected command %s, got %s", expectedCmd, mock.commands[0])
	}
}

func TestEnsureRemoteDir_Windows(t *testing.T) {
	mock := NewMockSSHClient()

	err := EnsureRemoteDir(mock, "C:\\temp\\test", "windows")
	if err != nil {
		t.Fatalf("EnsureRemoteDir failed: %v", err)
	}

	if len(mock.commands) != 1 {
		t.Fatalf("Expected 1 command, got %d", len(mock.commands))
	}

	expectedCmd := "cmd.exe /C if not exist \"C:\\temp\\test\" mkdir \"C:\\temp\\test\""
	if mock.commands[0] != expectedCmd {
		t.Errorf("Expected command %s, got %s", expectedCmd, mock.commands[0])
	}
}

func TestGetFileHash(t *testing.T) {
	content := "test file content for hashing"
	testFile := createTestFile(t, content)
	defer os.Remove(testFile)

	hash, err := GetFileHash(testFile)
	if err != nil {
		t.Fatalf("GetFileHash failed: %v", err)
	}

	if hash == "" {
		t.Error("Hash should not be empty")
	}

	if len(hash) != 64 { // SHA256 produces 64-character hex string
		t.Errorf("Expected 64-character hash, got %d characters", len(hash))
	}

	// Hash the same content again, should be identical
	hash2, err := GetFileHash(testFile)
	if err != nil {
		t.Fatalf("GetFileHash second call failed: %v", err)
	}

	if hash != hash2 {
		t.Error("Hash should be consistent for same file")
	}
}

func TestFindFallbackAgent(t *testing.T) {
	// Create a temporary directory structure
	tmpDir, err := os.MkdirTemp("", "test-fallback-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a fake agent binary
	testAgentPath := filepath.Join(tmpDir, "sync-agent-linux")
	if err := os.WriteFile(testAgentPath, []byte("fake agent"), 0755); err != nil {
		t.Fatalf("Failed to create test agent: %v", err)
	}

	// Test finding fallback
	fallback := FindFallbackAgent(tmpDir, "linux")
	if fallback == "" {
		t.Error("Expected to find fallback agent")
	}

	if !strings.Contains(fallback, "sync-agent-linux") {
		t.Errorf("Expected fallback path to contain 'sync-agent-linux', got: %s", fallback)
	}

	// Test no fallback found
	emptyDir, err := os.MkdirTemp("", "test-empty-*")
	if err != nil {
		t.Fatalf("Failed to create empty temp dir: %v", err)
	}
	defer os.RemoveAll(emptyDir)

	fallback = FindFallbackAgent(emptyDir, "linux")
	if fallback != "" {
		t.Errorf("Expected no fallback in empty directory, got: %s", fallback)
	}
}

func TestBuildOptions_Validation(t *testing.T) {
	// Test with missing source directory
	opts := BuildOptions{
		TargetOS: "linux",
	}

	_, err := BuildAgentForTarget(opts)
	if err == nil {
		t.Error("Expected error when source directory is missing")
	}

	if !strings.Contains(err.Error(), "source directory required") {
		t.Errorf("Expected 'source directory required' error, got: %v", err)
	}
}

// Note: TestBuildAgentForTarget is not included because it requires:
// 1. A valid Go development environment
// 2. The actual sub_app/agent source code
// 3. More complex setup than appropriate for unit tests
//
// Integration tests would be more appropriate for testing the actual build process.
