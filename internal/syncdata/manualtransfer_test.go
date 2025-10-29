package syncdata

import (
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
		// Empty prefix cases
		{"any", []string{""}, false, "empty prefix does NOT count as explicit endpoint (ignore applies)"},
		{"any/path", []string{""}, false, "empty prefix does NOT count as explicit endpoint (ignore applies)"},

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
		{"file.txt", []string{""}, false, "root level file with empty prefix (ignore applies)"},
		{"file.txt", []string{"file.txt"}, true, "exact file match"},
		{"file.txt", []string{"other.txt"}, false, "root level file no match"},

		// Empty and special cases
		{"", []string{""}, false, "empty path with empty prefix (ignore applies)"},
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
					// Do NOT treat an empty prefix as an explicit endpoint here.
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
