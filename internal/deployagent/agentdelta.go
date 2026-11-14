package deployagent

import (
	"regexp"
	"strings"
)

// Delta pre-check for the agent binary upload.
//
// The agent binary is ~6.7 MB and rarely changes between runs, so
// uploadAgentBinary hashes it locally and asks the remote for the hash of the
// existing file in a single exec. When the hashes match the (expensive) upload
// is skipped; everything else keeps the previous blind-upload behavior.
//
// The parsing rules intentionally mirror cmd/deploydelta.go (the deploy delta
// pre-check) so both features read `sha256sum` output the same way:
// "<64-hex>  <path>" with exactly two spaces, per-file stderr diagnostics
// ignored. See agentdelta_test.go for the shared fixtures.

// agentSha256SumLineRE matches one valid `sha256sum` text-mode line:
// "<64-hex-digit-hash>  <path>" (two spaces; the path may contain spaces).
var agentSha256SumLineRE = regexp.MustCompile(`^([0-9a-fA-F]{64})  (.+)$`)

// agentSha256SumUnavailableRE detects a remote that has no usable sha256sum.
var agentSha256SumUnavailableRE = regexp.MustCompile(`(?i)['"]?sha256sum(?:\.exe)?['"]?: (?:command not found|not found)|['"]?sha256sum(?:\.exe)?['"]? is not recognized`)

// shellQuotePOSIX wraps s in single quotes so it is a single safe POSIX shell
// word; embedded single quotes are escaped using the standard close-escape-open
// sequence.
func shellQuotePOSIX(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// buildSha256CheckCommand builds the single command that prints the remote
// file's SHA-256. stderr is merged into stdout so a missing file is visible to
// the parser, and the trailing "; true" neutralises the non-zero exit status
// sha256sum returns for a missing path (an expected case, not a failure).
func buildSha256CheckCommand(remotePath string) string {
	return "sha256sum -- " + shellQuotePOSIX(remotePath) + " 2>&1; true"
}

// parseSha256SumOutput extracts "<hash>  <path>" pairs keyed by path. Only
// well-formed lines are kept; per-file diagnostics (e.g. "No such file or
// directory") are ignored so one unreadable file never fails the pre-check.
func parseSha256SumOutput(output string) map[string]string {
	hashes := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		m := agentSha256SumLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		p := strings.TrimSpace(m[2])
		if p == "" {
			continue
		}
		hashes[p] = strings.ToLower(m[1])
	}
	return hashes
}

// sha256SumUnavailable reports whether the output shows the remote has no
// sha256sum command at all.
func sha256SumUnavailable(output string) bool {
	return agentSha256SumUnavailableRE.MatchString(output)
}

// agentBinaryUnchanged reports whether the remote copy of the agent binary
// already matches the local file.
//
// checked is false whenever the pre-check could not be trusted (local file
// unreadable, exec failure, sha256sum missing); the caller then keeps the
// blind-upload behavior. When checked is true, same tells whether the hashes
// matched — a missing remote file yields checked=true, same=false (upload).
func agentBinaryUnchanged(client SSHClient, localAgentPath, remoteAgentPath string) (same, checked bool) {
	localHash, err := GetFileHash(localAgentPath)
	if err != nil {
		return false, false
	}

	out, err := client.RunCommandWithOutput(buildSha256CheckCommand(remoteAgentPath))
	if err != nil {
		return false, false
	}
	if sha256SumUnavailable(out) {
		return false, false
	}

	remoteHash, ok := parseSha256SumOutput(out)[remoteAgentPath]
	if !ok {
		// Not in the output: the file does not exist on the remote yet.
		return false, true
	}
	return strings.EqualFold(remoteHash, localHash), true
}
