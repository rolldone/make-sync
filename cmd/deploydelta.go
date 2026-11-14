package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"make-sync/internal/sshclient"
	"make-sync/internal/util"
)

// Delta push (deploy v2): before transferring, hash every candidate file on the
// remote in a single SSH exec and skip the files whose SHA-256 already matches
// the local copy. No agent, no index, no DB file — the manifest lives only in
// memory. When the pre-check cannot run (sha256sum missing, exec error, output
// unparseable) the deploy degrades to the v1 blind push instead of failing.

// sha256SumLineRE matches one valid `sha256sum` text-mode line:
// "<64-hex-digit-hash>  <path>" (two spaces). The path is captured greedily so
// it may itself contain spaces. Any other line — including per-file diagnostics
// such as "No such file or directory" — is ignored by parseSha256SumOutput.
var sha256SumLineRE = regexp.MustCompile(`^([0-9a-fA-F]{64})  (.+)$`)

// sha256SumUnavailableRE detects a remote that has no usable sha256sum so the
// pre-check can warn and fall back to a blind push instead of silently sending
// everything.
var sha256SumUnavailableRE = regexp.MustCompile(`(?i)['"]?sha256sum(?:\.exe)?['"]?: (?:command not found|not found)|['"]?sha256sum(?:\.exe)?['"]? is not recognized`)

// sha256SumCommandOverhead is the fixed part of every generated command
// ("sha256sum -- " prefix plus " 2>&1; true" suffix), excluding the paths.
const sha256SumCommandOverhead = len("sha256sum -- ") + len(" 2>&1; true")

// sha256SumBatchBudget caps the byte length of a single sha256sum command. A
// 1272-file deploy used to build a ~135 KB argv and the SSH exec failed with an
// empty output ("Process exited with status 1"). Measured limit is ~130 KB ok /
// >=138 KB failing, so 32 KB per batch leaves a wide safety margin and keeps
// each command line small.
const sha256SumBatchBudget = 32 * 1024

// hashLocalFiles computes the SHA-256 of every upload-list entry (relative,
// forward-slash) under absRoot. Entries that are missing or unreadable are
// omitted: the caller treats "no local hash" as "must send", never as failure.
func hashLocalFiles(absRoot string, uploads []string) map[string]string {
	hashes := make(map[string]string, len(uploads))
	for _, rel := range uploads {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		if _, seen := hashes[rel]; seen {
			continue
		}
		sum, err := hashFileSHA256(filepath.Join(absRoot, filepath.FromSlash(rel)))
		if err != nil {
			continue // missing/unreadable local file → send candidate
		}
		hashes[rel] = sum
	}
	return hashes
}

// hashFileSHA256 returns the lowercase hex SHA-256 digest of a local file.
func hashFileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// buildSha256SumCommand builds a single POSIX command hashing every remote path
// in one exec. Paths are quoted with shellEscapePOSIX (reused read-only from
// exec.go). stderr is merged into stdout so per-file diagnostics are visible to
// parseSha256SumOutput, and the trailing "; true" normalises the exit status:
// sha256sum already exits non-zero when a path is missing, which is an expected
// ("file not present") case rather than a failure.
//
// Use splitSha256SumBatches for large candidate lists: one command per batch
// keeps the argv far below the SSH exec limit.
func buildSha256SumCommand(remoteRoot string, rels []string) string {
	quoted := make([]string, 0, len(rels))
	seen := make(map[string]struct{}, len(rels))
	for _, rel := range rels {
		if strings.TrimSpace(rel) == "" {
			continue
		}
		remote := deployRemotePath(remoteRoot, rel, "")
		if remote == "" {
			continue
		}
		if _, dup := seen[remote]; dup {
			continue
		}
		seen[remote] = struct{}{}
		quoted = append(quoted, shellEscapePOSIX(remote))
	}
	if len(quoted) == 0 {
		return ""
	}
	return "sha256sum -- " + strings.Join(quoted, " ") + " 2>&1; true"
}

// splitSha256SumBatches groups relative paths into batches whose generated
// command stays within budget bytes. Paths are never split across batches; a
// single path larger than the budget occupies its own batch (progress is
// guaranteed because at least one path is always consumed per iteration).
//
// Order and de-duplication match buildSha256SumCommand so the batches are
// deterministic.
func splitSha256SumBatches(remoteRoot string, rels []string, budget int) [][]string {
	if budget <= 0 {
		budget = sha256SumBatchBudget
	}
	batches := make([][]string, 0, len(rels)/64+1)
	current := make([]string, 0, len(rels))
	currentLen := sha256SumCommandOverhead
	seen := make(map[string]struct{}, len(rels))

	for _, rel := range rels {
		if strings.TrimSpace(rel) == "" {
			continue
		}
		remote := deployRemotePath(remoteRoot, rel, "")
		if remote == "" {
			continue
		}
		if _, dup := seen[remote]; dup {
			continue
		}
		seen[remote] = struct{}{}

		tokenLen := len(shellEscapePOSIX(remote)) + 1 // + separating space
		if len(current) > 0 && currentLen+tokenLen > budget {
			batches = append(batches, current)
			current = make([]string, 0, len(rels))
			currentLen = sha256SumCommandOverhead
		}
		current = append(current, rel)
		currentLen += tokenLen
	}
	if len(current) > 0 {
		batches = append(batches, current)
	}
	return batches
}

// parseSha256SumOutput extracts "<hash>  <path>" pairs keyed by remote path.
// Only well-formed lines are kept; per-file errors (e.g. "No such file or
// directory") are ignored so one unreadable remote file never fails the whole
// pre-check. Both streams are scanned because the executor merges them (and a
// caller may pass them separately).
func parseSha256SumOutput(out, stderr string) map[string]string {
	hashes := map[string]string{}
	for _, block := range []string{out, stderr} {
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimRight(line, "\r")
			if line == "" {
				continue
			}
			m := sha256SumLineRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			path := strings.TrimSpace(m[2])
			if path == "" {
				continue
			}
			hashes[path] = strings.ToLower(m[1])
		}
	}
	return hashes
}

// sha256SumUnavailable reports whether combined pre-check output indicates that
// the remote has no sha256sum command at all.
func sha256SumUnavailable(output string) bool {
	return sha256SumUnavailableRE.MatchString(output)
}

// computeDelta classifies each upload candidate as send or skip, preserving
// upload order and de-duplicating. A file is skipped only when both hashes exist
// and match; every other case (hash differs, no local hash, no remote hash) is
// sent.
func computeDelta(uploads []string, localHashes, remoteHashes map[string]string) (send, skip []string) {
	seen := make(map[string]struct{}, len(uploads))
	for _, rel := range uploads {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		if _, dup := seen[rel]; dup {
			continue
		}
		seen[rel] = struct{}{}

		local, lok := localHashes[rel]
		remote, rok := remoteHashes[rel]
		if lok && rok && strings.EqualFold(local, remote) {
			skip = append(skip, rel)
			continue
		}
		send = append(send, rel)
	}
	return send, skip
}

// remoteHashesByRel maps each escaped remote path back to its relative path so
// parsed sha256sum output can be re-keyed by relative path.
func remoteHashesByRel(remoteRoot string, rels []string) map[string]string {
	byRemote := make(map[string]string, len(rels))
	for _, rel := range rels {
		rel = filepath.ToSlash(strings.TrimSpace(rel))
		if rel == "" {
			continue
		}
		byRemote[deployRemotePath(remoteRoot, rel, "")] = rel
	}
	return byRemote
}

// deployDelta is the outcome of the sha256 pre-check.
type deployDelta struct {
	Send     []string // upload candidates that need a transfer
	Skip     []string // candidates already identical on the remote
	Fallback bool     // true when the pre-check could not run: everything is sent
}

// planDeployDelta hashes the candidates from disk, runs one remote sha256sum
// exec, and decides which files actually need transferring. It never fails the
// deploy: a missing sha256sum, an exec error, or output that yields no usable
// hash all degrade to the blind-push fallback (send everything). POSIX targets
// only — Windows falls back directly in runDeploy.
func planDeployDelta(client *sshclient.SSHClient, absRoot, remoteRoot string, uploads []string) deployDelta {
	if len(uploads) == 0 {
		return deployDelta{Send: uploads}
	}
	return planDeployDeltaWithHashes(client, remoteRoot, uploads, hashLocalFiles(absRoot, uploads))
}

// planDeployDeltaWithHashes is planDeployDelta with a caller-provided local hash
// map. `deploy --to-commit` uses it to compare the remote against the blob
// content of a git revision instead of the working-tree files.
func planDeployDeltaWithHashes(client *sshclient.SSHClient, remoteRoot string, uploads []string, localHashes map[string]string) deployDelta {
	return planDeltaWithRunner(client.RunCommandWithOutput, remoteRoot, uploads, localHashes)
}

// commandRunner executes one remote command and returns its combined output.
// It is a seam for tests: production always passes SSHClient.RunCommandWithOutput.
type commandRunner func(cmd string) (string, error)

// planDeltaWithRunner hashes the remote in sequential batches (see
// sha256SumBatchBudget) on the same connection: a single exec would exceed the
// SSH argv limit for large deploys. Any failing batch degrades to the blind-push
// fallback.
func planDeltaWithRunner(run commandRunner, remoteRoot string, uploads []string, localHashes map[string]string) deployDelta {
	delta := deployDelta{Send: uploads}
	if len(uploads) == 0 {
		return delta
	}

	batches := splitSha256SumBatches(remoteRoot, uploads, sha256SumBatchBudget)
	if len(batches) == 0 {
		delta.Fallback = true
		return delta
	}

	merged := make(map[string]string, len(uploads))
	for i, batch := range batches {
		cmd := buildSha256SumCommand(remoteRoot, batch)
		if cmd == "" {
			continue
		}
		out, err := run(cmd)
		if err != nil {
			if len(batches) > 1 {
				util.Default.Printf("⚠️  Delta pre-check failed on batch %d/%d (%v) — falling back to blind push.\n", i+1, len(batches), err)
			} else {
				util.Default.Printf("⚠️  Delta pre-check failed (%v) — falling back to blind push.\n", err)
			}
			delta.Fallback = true
			return delta
		}

		// sha256sum itself missing is a remote capability problem, not a
		// partial failure: report once and fall back immediately.
		if i == 0 && sha256SumUnavailable(out) {
			util.Default.Println("⚠️  sha256sum is unavailable on the remote — falling back to blind push.")
			delta.Fallback = true
			return delta
		}

		for remote, sum := range parseSha256SumOutput(out, "") {
			merged[remote] = sum
		}
	}

	byRemote := remoteHashesByRel(remoteRoot, uploads)
	remoteHashes := make(map[string]string, len(merged))
	for remote, sum := range merged {
		if rel, ok := byRemote[remote]; ok {
			remoteHashes[rel] = sum
		}
	}

	send, skip := computeDelta(uploads, localHashes, remoteHashes)
	delta.Send, delta.Skip = send, skip
	return delta
}
