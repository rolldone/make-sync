package syncdata

import (
	"os"
	"path/filepath"
	"strings"

	"make-sync/internal/config"
	"make-sync/internal/tui"
	"make-sync/internal/util"
)

// readNegationPatterns reads .sync_ignore in the given root and returns lines
// starting with '!' with leading '!' and whitespace trimmed.
func readNegationPatterns(root string) []string {
	path := filepath.Join(root, ".sync_ignore")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	out := make([]string, 0)
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "!") {
			p := strings.TrimSpace(strings.TrimPrefix(t, "!"))
			if p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// ForceSingleSyncMenu implements the nested Single Sync interactive flow.
// It uses cfg.Devsync.ManualTransfer as the list of registered paths.
func ForceSingleSyncMenu(cfg *config.Config, localRoot string) {
	// build a stable list of registered paths
	registered := cfg.Devsync.ManualTransfer

	for {
		// top: choose between Download / Upload / Back
		choice, err := tui.ShowMenuWithPrints([]string{"Download", "Upload", "Back"}, "Single Sync")
		if err != nil {
			util.Default.Printf("❌ Failed to show Single Sync menu: %v\n", err)
			return
		}
		if choice == "Back" {
			return
		}

		// folder selection menu
		for {
			// construct folder choices
			items := make([]string, 0, len(registered)+4)
			for _, p := range registered {
				items = append(items, p)
			}
			items = append(items, "----------")
			items = append(items, "All data registered in single_sync only")
			items = append(items, "All Data Only In Your \"Sync Ignore\" File Pattern")
			items = append(items, "Back Previous / Exit")

			folderChoice, err := tui.ShowMenuWithPrints(items, "? Single Sync : Which folder :")
			if err != nil {
				util.Default.Printf("❌ Folder selection cancelled: %v\n", err)
				break
			}
			if folderChoice == "Back Previous / Exit" {
				break // go back to Download/Upload selection
			}

			// determine prefixes list to pass to filter-based compare functions
			var prefixes []string
			switch folderChoice {
			case "All data registered in single_sync only":
				// use registered entries as prefixes (normalize)
				for _, p := range registered {
					pp := strings.TrimSpace(p)
					if pp != "" {
						prefixes = append(prefixes, filepath.ToSlash(strings.TrimPrefix(pp, "/")))
					}
				}
				if len(prefixes) == 0 {
					util.Default.Println("ℹ️  No registered paths found.")
					continue
				}
			case "All Data Only In Your \"Sync Ignore\" File Pattern":
				// read negation patterns from localRoot
				neg := readNegationPatterns(localRoot)
				if len(neg) == 0 {
					util.Default.Println("ℹ️  No negation patterns found in .sync_ignore")
					continue
				}
				// use negation patterns as prefixes
				for _, p := range neg {
					prefixes = append(prefixes, filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(p), "/")))
				}
			default:
				// assume it's a registered path
				pp := strings.TrimSpace(folderChoice)
				if pp == "" {
					util.Default.Println("ℹ️  Empty selection")
					continue
				}
				prefixes = []string{filepath.ToSlash(strings.TrimPrefix(pp, "/"))}
			}

			// mode selection
			modeChoice, err := tui.ShowMenuWithPrints([]string{"Rsync Soft Mode", "Rsync Force Mode", "Back Previous / Exit"}, "? Single Sync : ["+choice+"] | Which mode :")
			if err != nil {
				util.Default.Printf("❌ Mode selection cancelled: %v\n", err)
				continue
			}
			if modeChoice == "Back Previous / Exit" {
				continue // go back to folder selection
			}

			// execute selected action
			switch choice {
			case "Download":
				util.Default.Printf("🔁 Running Download (%s) for prefixes: %v\n", modeChoice, prefixes)
				// Note: modeChoice currently not used to change behavior; implement force handling if needed
				downloaded, err := CompareAndDownloadByHashWithFilter(cfg, localRoot, prefixes)
				if err != nil {
					util.Default.Printf("❌ Download failed: %v\n", err)
				} else {
					if len(downloaded) == 0 {
						util.Default.Println("✅ No files downloaded (nothing matched or already up-to-date)")
					} else {
						util.Default.Printf("⬇️  Downloaded %d files:\n", len(downloaded))
						for _, f := range downloaded {
							util.Default.Printf(" - %s\n", f)
						}
					}
				}
			case "Upload":
				util.Default.Printf("🔁 Running Upload (%s) for prefixes: %v\n", modeChoice, prefixes)
				uploaded, err := CompareAndUploadByHashWithFilter(cfg, localRoot, prefixes)
				if err != nil {
					util.Default.Printf("❌ Upload failed: %v\n", err)
				} else {
					if len(uploaded) == 0 {
						util.Default.Println("✅ No files uploaded (nothing matched or already up-to-date)")
					} else {
						util.Default.Printf("⬆️  Uploaded %d files:\n", len(uploaded))
						for _, f := range uploaded {
							util.Default.Printf(" - %s\n", f)
						}
					}
				}
			default:
				// no-op
			}
			// after operation, return to folder selection (per stepwise behavior)
		}
	}
}
