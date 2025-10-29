package syncdata

import (
	"testing"

	"make-sync/internal/config"
	"make-sync/internal/sshclient"
)

// Test that pruneRemoteEmptyDirs uses the RemoteRunAgentPruneFn output and
// parses the JSON-first line produced by the agent, returning the number of
// removed entries reported by the agent.
func TestPruneRemoteWiring_ParsesJSON(t *testing.T) {
	// Save/restore real function
	real := RemoteRunAgentPruneFn
	defer func() { RemoteRunAgentPruneFn = real }()

	// Fake implementation returns a JSON-first line and some human text
	RemoteRunAgentPruneFn = func(client *sshclient.SSHClient, remoteDir, osTarget string, bypassIgnore bool, prefixes []string, dryRun bool) (string, error) {
		// agent prints JSON-first line then human summary lines
		jsonLine := `{"removed":["a","b","c"],"failed":[],"dry_run":false}` + "\n"
		human := "Prune summary: removed=3 failed=0\n"
		return jsonLine + human, nil
	}

	cfg := &config.Config{}
	cfg.Devsync.Auth.RemotePath = "/tmp/remote/project"
	cfg.Devsync.OSTarget = "linux"

	// Call with nil ssh client because our fake ignores it
	got := pruneRemoteEmptyDirs(nil, cfg, []string{"prefix"})
	if got != 3 {
		t.Fatalf("expected removed count 3, got %d", got)
	}
}
