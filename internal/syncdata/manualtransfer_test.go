package syncdata

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestManualTransferContextAwareIgnore(t *testing.T) {
	// Test the isExplicitEndpoint helper function logic
	normPrefixes := []string{"vendor", "src"}

	isExplicitEndpoint := func(relPath string) bool {
		for _, pr := range normPrefixes {
			if pr == "" || relPath == pr || strings.HasPrefix(relPath, pr+"/") {
				return true
			}
		}
		return false
	}

	// Test cases
	testCases := []struct {
		relPath  string
		expected bool
		desc     string
	}{
		{"vendor", true, "exact vendor directory match"},
		{"vendor/lib.js", true, "file inside vendor directory"},
		{"vendor/subdir/file.js", true, "file in vendor subdirectory"},
		{"src", true, "exact src directory match"},
		{"src/main.go", true, "file inside src directory"},
		{"node_modules", false, "node_modules not in prefixes"},
		{"node_modules/package.json", false, "file in node_modules not in prefixes"},
		{"other", false, "unrelated directory"},
		{"other/file.txt", false, "file in unrelated directory"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			result := isExplicitEndpoint(tc.relPath)
			if result != tc.expected {
				t.Errorf("isExplicitEndpoint(%q) = %v, expected %v", tc.relPath, result, tc.expected)
			}
		})
	}

	// Test edge cases
	edgeCases := []struct {
		relPath  string
		prefixes []string
		expected bool
		desc     string
	}{
		// Empty prefix means full-scope sync, not an explicit endpoint.
		{"any", []string{""}, false, "empty prefix does not bypass ignore for full-scope force"},
		{"any/path", []string{""}, false, "empty prefix does not protect nested paths"},

		// Multiple prefixes
		{"vendor/lib.js", []string{"vendor", "src"}, true, "multiple prefixes - vendor match"},
		{"src/main.go", []string{"vendor", "src"}, true, "multiple prefixes - src match"},
		{"node_modules/pkg.json", []string{"vendor", "src"}, false, "multiple prefixes - no match"},

		// Nested directory structures
		{"a/b/c/file.txt", []string{"a"}, true, "deep nesting within prefix"},
		{"a/b/c/file.txt", []string{"a/b"}, true, "partial prefix match"},
		{"a/b/c/file.txt", []string{"a/b/c"}, true, "exact directory prefix"},
		{"a/b/c/file.txt", []string{"x/y/z"}, false, "no match in deep nesting"},

		// Root level files
		{"file.txt", []string{""}, false, "root level file with empty prefix still respects ignore"},
		{"file.txt", []string{"file.txt"}, true, "exact file match"},
		{"file.txt", []string{"other.txt"}, false, "root level file no match"},

		// Empty and special cases
		{"", []string{""}, false, "empty path with empty prefix is not an explicit endpoint"},
		{"", []string{"vendor"}, false, "empty path no match"},
		{"vendor", []string{"vendor/"}, true, "trailing slash in prefix"},
		{"vendor/file.txt", []string{"vendor/"}, true, "trailing slash matches subdirectory"},
	}

	for _, tc := range edgeCases {
		t.Run("edge_"+tc.desc, func(t *testing.T) {
			isExplicitEndpoint := func(relPath string) bool {
				for _, pr := range tc.prefixes {
					// Normalize prefix by removing trailing slash for comparison
					normalizedPr := strings.TrimSuffix(pr, "/")
					// Empty prefix means full-scope sync, so ignore rules still apply.
					if normalizedPr == "" {
						continue
					}
					if relPath == normalizedPr || strings.HasPrefix(relPath, normalizedPr+"/") {
						return true
					}
				}
				return false
			}

			result := isExplicitEndpoint(tc.relPath)
			if result != tc.expected {
				t.Errorf("isExplicitEndpoint(%q, %v) = %v, expected %v", tc.relPath, tc.prefixes, result, tc.expected)
			}
		})
	}
}

func TestManualTransferPerEntryIgnoreProfiles(t *testing.T) {
	absRoot := filepath.Join(string(filepath.Separator), "tmp", "manual-transfer")

	SetManualTransferIgnoreProfiles(map[string][]string{
		"vendor": {
			"lib_a",
			"kokoko.txt",
			"!lib_a/ttt.txt",
		},
	})
	defer ClearManualTransferIgnoreProfiles()

	cases := []struct {
		path string
		want bool
	}{
		{filepath.Join(absRoot, "vendor", "lib_a", "hello.go"), true},
		{filepath.Join(absRoot, "vendor", "lib_a", "ttt.txt"), false},
		{filepath.Join(absRoot, "vendor", "kokoko.txt"), true},
		{filepath.Join(absRoot, "vendor", "keep.txt"), false},
		{filepath.Join(absRoot, "other", "lib_a", "hello.go"), false},
	}

	for _, tc := range cases {
		got := shouldIgnoreManualTransferPath(absRoot, tc.path, false, func(_ string, _ bool) bool {
			return true
		})
		if got != tc.want {
			t.Fatalf("shouldIgnoreManualTransferPath(%s) = %v, want %v", tc.path, got, tc.want)
		}
	}
}

func TestManualTransferIgnoreBypassMode(t *testing.T) {
	absRoot := filepath.Join(string(filepath.Separator), "tmp", "manual-transfer")
	SetManualTransferIgnoreProfiles(map[string][]string{})
	defer ClearManualTransferIgnoreProfiles()

	got := shouldIgnoreManualTransferPath(absRoot, filepath.Join(absRoot, "vendor", "lib_a", "hello.go"), false, func(_ string, _ bool) bool {
		return true
	})
	if got {
		t.Fatalf("expected bypass mode with empty profiles to not ignore paths")
	}
}

func TestManualTransferNegationCanPassSingleFileUnderIgnoredDir(t *testing.T) {
	absRoot := filepath.Join(string(filepath.Separator), "tmp", "manual-transfer")
	SetManualTransferIgnoreProfiles(map[string][]string{
		"vendor": {
			"*",
			"!test/kokok.txt",
		},
	})
	defer ClearManualTransferIgnoreProfiles()

	if shouldIgnoreManualTransferPath(absRoot, filepath.Join(absRoot, "vendor", "test"), true, nil) {
		t.Fatalf("expected vendor/test directory to stay traversable because of negation")
	}

	if !shouldIgnoreManualTransferPath(absRoot, filepath.Join(absRoot, "vendor", "other"), true, nil) {
		t.Fatalf("expected unrelated directory to remain ignored by '*' rule")
	}

	if shouldIgnoreManualTransferPath(absRoot, filepath.Join(absRoot, "vendor", "test", "kokok.txt"), false, nil) {
		t.Fatalf("expected negated file to be included")
	}

	if !shouldIgnoreManualTransferPath(absRoot, filepath.Join(absRoot, "vendor", "test", "other.txt"), false, nil) {
		t.Fatalf("expected non-negated file under test/ to stay ignored")
	}
}
