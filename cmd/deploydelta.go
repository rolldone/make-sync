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
func buildSha256SumCommand(remoteRoot string, rels []string) string {
	quoted := make([]string, 0, len(rels))
	seen := make(map[string]struct{}, len(rels))
	for _, rel := range rels {
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

// planDeployDelta hashes the candidates locally, runs one remote sha256sum exec,
// and decides which files actually need transferring. It never fails the deploy:
// a missing sha256sum, an exec error, or output that yields no usable hash all
// degrade to the blind-push fallback (send everything). POSIX targets only —
// Windows falls back directly in runDeploy.
func planDeployDelta(client *sshclient.SSHClient, absRoot, remoteRoot string, uploads []string) deployDelta {
	delta := deployDelta{Send: uploads}
	if len(uploads) == 0 {
		return delta
	}

	cmd := buildSha256SumCommand(remoteRoot, uploads)
	if cmd == "" {
		delta.Fallback = true
		return delta
	}

	localHashes := hashLocalFiles(absRoot, uploads)

	out, err := client.RunCommandWithOutput(cmd)
	if err != nil {
		util.Default.Printf("⚠️  Delta pre-check failed (%v) — falling back to blind push.\n", err)
		delta.Fallback = true
		return delta
	}

	parsed := parseSha256SumOutput(out, "")
	if len(parsed) == 0 && sha256SumUnavailable(out) {
		util.Default.Println("⚠️  sha256sum is unavailable on the remote — falling back to blind push.")
		delta.Fallback = true
		return delta
	}

	byRemote := remoteHashesByRel(remoteRoot, uploads)
	remoteHashes := make(map[string]string, len(parsed))
	for remote, sum := range parsed {
		if rel, ok := byRemote[remote]; ok {
			remoteHashes[rel] = sum
		}
	}

	send, skip := computeDelta(uploads, localHashes, remoteHashes)
	delta.Send, delta.Skip = send, skip
	return delta
}
