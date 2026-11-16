package cmd

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5"

	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/format/gitattributes"
)

// go-git's Worktree.Status() compares the raw bytes on disk with the blob
// recorded in the index, but git first runs every file through the checkin
// ("clean") conversion and compares the *converted* content. On a checkout made
// with core.autocrlf=true (or a `text`/`eol` entry in .gitattributes) every
// CRLF text file therefore shows up as Modified in go-git even though git
// reports a clean tree.
//
// go-git has a second blind spot: it folds the file mode into the node hash, so
// a checkout whose files are 0755 while the index records 0644 is reported as
// Modified even when core.filemode=false tells git to ignore the exec bit.
//
// Observed on workspaces_umahloka_niaga/umah_loka_prod: go-git listed 311 paths
// while `git status` listed 6, and `make-sync deploy --since-commit <merge>`
// consequently announced "Upload files: 283" for files that were already
// identical on the remote.
//
// The helpers below drop those false positives: a working-tree entry is kept
// only when the on-disk content, after git's line-ending normalisation, or its
// mode differs from what git would record. Everything the filter cannot prove
// identical is kept, so a real change can never be lost — the failure mode is
// "still reports a file that is actually unchanged", which is what the delta
// pre-check already tolerates.

// gitAttributesFileName is the per-directory rules file git reads for text/eol
// conversion decisions.
const gitAttributesFileName = ".gitattributes"

// binarySniffLen mirrors git's buffer_is_binary(): only the first 8000 bytes of
// a file are inspected for a NUL byte when guessing whether it is text.
const binarySniffLen = 8000

// eolAttribute is the resolved `text`/`binary`/`eol` state for one path.
type eolAttribute int

const (
	eolUnspecified eolAttribute = iota
	eolText                     // attribute forces text handling
	eolAuto                     // text=auto / eol=... : convert when content looks like text
	eolBinary                   // -text / binary : never convert
)

// eolPolicy decides, per path and content, whether git would fold CRLF to LF on
// checkin, and whether git is configured to ignore the exec bit. The zero value
// converts nothing and honours the mode.
type eolPolicy struct {
	autoCRLF       string // effective core.autocrlf: "true" | "input" | "false" | ""
	ignoreFileMode bool   // effective core.filemode == false
	fs             billy.Filesystem
	matchers       map[string]gitattributes.Matcher // cache keyed by directory
}

// newEOLPolicy reads the repository configuration and prepares lazy attribute
// lookups. A nil filesystem disables attribute handling (autocrlf still works).
func newEOLPolicy(repo *git.Repository, fs billy.Filesystem) *eolPolicy {
	return &eolPolicy{
		autoCRLF:       effectiveAutoCRLF(repo),
		ignoreFileMode: gitIgnoresFileMode(repo),
		fs:             fs,
		matchers:       map[string]gitattributes.Matcher{},
	}
}

// coreOption returns the effective value of one core.* setting: the repository
// configuration wins, otherwise the user's global configuration is consulted.
// Configuration errors are treated as "unset" rather than as failures.
func coreOption(repo *git.Repository, key string) string {
	if repo != nil {
		if cfg, err := repo.Config(); err == nil && cfg != nil && cfg.Raw != nil {
			if v := strings.TrimSpace(cfg.Raw.Section("core").Option(key)); v != "" {
				return strings.ToLower(v)
			}
		}
	}
	if g, err := config.LoadConfig(config.GlobalScope); err == nil && g != nil && g.Raw != nil {
		if v := strings.TrimSpace(g.Raw.Section("core").Option(key)); v != "" {
			return strings.ToLower(v)
		}
	}
	return ""
}

// effectiveAutoCRLF returns the core.autocrlf value git would use.
func effectiveAutoCRLF(repo *git.Repository) string {
	return coreOption(repo, "autocrlf")
}

// gitIgnoresFileMode reports whether core.filemode tells git to skip exec-bit
// differences. Unset means "on" for git on POSIX, so only an explicit false
// disables it.
func gitIgnoresFileMode(repo *git.Repository) bool {
	switch coreOption(repo, "filemode") {
	case "false", "no", "off", "0":
		return true
	default:
		return false
	}
}

// matcherFor returns the gitattributes matcher for the directory holding rel,
// or nil when no .gitattributes rule applies. Results are cached per directory
// so a deploy that spans thousands of files reads each rules file once.
func (p *eolPolicy) matcherFor(rel string) gitattributes.Matcher {
	dir := path.Dir(rel)
	if dir == "" {
		dir = "."
	}
	if m, ok := p.matchers[dir]; ok {
		return m
	}

	stack := attributeStack(p.fs, dir)
	var m gitattributes.Matcher
	if len(stack) > 0 {
		m = gitattributes.NewMatcher(stack)
	}
	p.matchers[dir] = m
	return m
}

// attributeStack collects the .gitattributes rules that apply inside dir, from
// the repository root (lowest priority) down to dir itself (highest priority).
// Missing or malformed files are ignored: an unreadable rules file simply means
// "no attribute-driven conversion", never a hard failure.
func attributeStack(fs billy.Filesystem, dir string) []gitattributes.MatchAttribute {
	if fs == nil {
		return nil
	}

	var parts []string
	if dir != "." && dir != "/" && dir != "" {
		parts = strings.Split(strings.Trim(dir, "/"), "/")
	}

	// The root file is the only one allowed to define macros, like git.
	var stack []gitattributes.MatchAttribute
	if attrs, err := gitattributes.ReadAttributesFile(fs, nil, gitAttributesFileName, true); err == nil {
		stack = append(stack, attrs...)
	}
	for i := range parts {
		if attrs, err := gitattributes.ReadAttributesFile(fs, parts[:i+1], gitAttributesFileName, false); err == nil {
			stack = append(stack, attrs...)
		}
	}
	return stack
}

// resolvesText inspects the `text`, `binary` and `eol` attributes for rel.
func (p *eolPolicy) resolvesText(rel string) eolAttribute {
	m := p.matcherFor(rel)
	if m == nil {
		return eolUnspecified
	}
	attrs, _ := m.Match(strings.Split(rel, "/"), nil)

	if a, ok := attrs["binary"]; ok && a.IsSet() {
		return eolBinary
	}
	if a, ok := attrs["text"]; ok {
		switch {
		case a.IsUnset():
			return eolBinary
		case a.IsValueSet() && strings.EqualFold(a.Value(), "auto"):
			return eolAuto
		default:
			return eolText
		}
	}
	if a, ok := attrs["eol"]; ok && a.IsValueSet() {
		return eolAuto
	}
	return eolUnspecified
}

// converts reports whether git folds CRLF to LF for this path and content.
func (p *eolPolicy) converts(rel string, content []byte) bool {
	switch p.resolvesText(rel) {
	case eolBinary:
		return false
	case eolText:
		return true
	case eolAuto:
		return !isBinaryContent(content)
	}
	switch p.autoCRLF {
	case "true", "input":
		return !isBinaryContent(content)
	default:
		return false
	}
}

// crlfToLF folds CRLF sequences to LF, the checkin conversion git applies to
// text files. Content without a CR is returned unchanged.
func crlfToLF(data []byte) []byte {
	if !bytes.Contains(data, []byte("\r")) {
		return data
	}
	out := make([]byte, 0, len(data))
	for i := 0; i < len(data); i++ {
		if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
			continue
		}
		out = append(out, data[i])
	}
	return out
}

// isBinaryContent applies git's heuristic: content containing a NUL byte within
// the first 8000 bytes is treated as binary.
func isBinaryContent(content []byte) bool {
	if len(content) > binarySniffLen {
		content = content[:binarySniffLen]
	}
	return bytes.IndexByte(content, 0) >= 0
}

// worktreeMatchesTrackedBlob reports whether the file on disk is identical to
// blobHash once git's checkin line-ending normalisation is applied.
func worktreeMatchesTrackedBlob(absRoot string, policy *eolPolicy, rel string, blobHash plumbing.Hash) bool {
	content, err := os.ReadFile(filepath.Join(absRoot, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	// No CR means normalisation cannot change anything: any hash difference is a
	// real content change, so do not touch it.
	if !bytes.Contains(content, []byte("\r")) {
		return false
	}
	if !policy.converts(rel, content) {
		return false
	}
	return plumbing.ComputeHash(plumbing.BlobObject, crlfToLF(content)) == blobHash
}

// gitModeOf returns the mode git would record for a path on disk, expressed as
// the file's exec bit. A stat failure reports "unknown" and therefore keeps the
// candidate.
func gitModeOf(absRoot, rel string) (filemode.FileMode, bool) {
	info, err := os.Stat(filepath.Join(absRoot, filepath.FromSlash(rel)))
	if err != nil {
		return 0, false
	}
	mode, err := filemode.NewFromOSFileMode(info.Mode())
	if err != nil {
		return 0, false
	}
	return mode, true
}

// modeOnlyDifference reports whether the only difference between the working
// tree file and its index entry is the exec bit that git is told to ignore
// (core.filemode=false). File content must already be identical.
func modeOnlyDifference(absRoot string, policy *eolPolicy, rel string, entryMode filemode.FileMode, blobHash plumbing.Hash) bool {
	if !policy.ignoreFileMode || !entryMode.IsFile() {
		return false
	}
	diskMode, ok := gitModeOf(absRoot, rel)
	if !ok || !diskMode.IsFile() || diskMode == entryMode {
		return false
	}
	// The mode difference is meaningless unless the content also matches.
	content, err := os.ReadFile(filepath.Join(absRoot, filepath.FromSlash(rel)))
	if err != nil {
		return false
	}
	return plumbing.ComputeHash(plumbing.BlobObject, content) == blobHash
}

// dropStatusFalsePositives removes status entries that go-git reports as
// worktree-Modified only because of the CRLF/checkin conversion or the ignored
// exec-bit difference described at the top of this file. It mutates and returns
// status.
//
// Only entries whose staging side is unmodified are considered: a staged change
// is a genuine index-vs-HEAD difference that must be deployed. Added, untracked
// and deleted entries are never touched, and any uncertainty (missing index
// entry, unreadable file, unknown conversion) keeps the entry — the filter can
// only remove a path when content and mode prove it unchanged.
func dropStatusFalsePositives(absRoot string, repo *git.Repository, wt *git.Worktree, status git.Status) git.Status {
	if len(status) == 0 || repo == nil || wt == nil {
		return status
	}

	idx, err := repo.Storer.Index()
	if err != nil {
		return status
	}

	policy := newEOLPolicy(repo, wt.Filesystem)
	for rel, fs := range status {
		if fs == nil || fs.Staging != git.Unmodified || fs.Worktree != git.Modified {
			continue
		}
		entry, err := idx.Entry(rel)
		if err != nil || entry == nil {
			continue
		}
		if worktreeMatchesTrackedBlob(absRoot, policy, rel, entry.Hash) ||
			modeOnlyDifference(absRoot, policy, rel, entry.Mode, entry.Hash) {
			delete(status, rel)
		}
	}
	return status
}
