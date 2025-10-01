package backuprestore

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"make-sync/internal/config"
	"make-sync/internal/history"
	"make-sync/internal/securestore"

	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// NewBackupRestoreCmd returns a cobra command for backup/restore (data) operations.
func NewBackupRestoreCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "data",
		Short: "Manage saved config data",
		Long:  `Save, load, or delete saved configuration data.`,
		Run: func(cmd *cobra.Command, args []string) {
			cwd, _ := os.Getwd()
			fmt.Printf("You are in: %s\n", cwd)
			fmt.Println("Initialize Bootstrap Is Done!")
			fmt.Printf("process.execPath :: %s\n", os.Args[0])
			fmt.Printf("process.execPath dirname :: %s\n", filepath.Dir(os.Args[0]))
			fmt.Printf("process.execPath basename :: %s\n", filepath.Base(os.Args[0]))

			// Note: Do NOT validate configuration here.
			// We allow entering the data menu even if current make-sync.yaml is missing/invalid,
			// because user may want to load or delete collections.

			for {
				prompt := promptui.Select{
					Label: "? Action Mode",
					Items: []string{"save", "load", "delete", "exit"},
				}

				_, result, err := prompt.Run()
				if err != nil {
					fmt.Printf("Prompt failed %v\n", err)
					return
				}

				switch result {
				case "save":
					if err := showSaveMenu(); err != nil {
						if err.Error() == "back_selected" {
							continue
						}
						fmt.Printf("Error saving config: %v\n", err)
						continue
					}
					return
				case "load":
					if err := showLoadMenu(); err != nil {
						if err.Error() == "back_selected" {
							continue
						}
						fmt.Printf("Error loading config: %v\n", err)
						continue
					}
					return
				case "delete":
					if err := showDeleteMenu(); err != nil {
						if err.Error() == "back_selected" {
							continue
						}
						fmt.Printf("Error deleting config: %v\n", err)
						continue
					}
				case "exit":
					fmt.Println("Goodbye!")
					return
				}
			}
		},
	}
	return cmd
}

// Below: placeholder stubs referencing old implementations in root.go. During
// incremental refactor we'll move full implementations here and replace root.go's.

// showLoadMenu is implemented in root.go for now; it will be migrated later.

// Below: full implementations moved from root.go
func showSaveMenu() error {
	fmt.Println("? Display data saved")

	// Load current config so we can honor sync_collection settings
	cfg, cfgErr := config.LoadAndRenderConfig()
	if cfgErr != nil {
		// proceed with defaults if config cannot be loaded (e.g., first-time save)
		fmt.Printf("⚠️  Could not load current config, using default collection dir: %v\n", cfgErr)
	}

	syncDir := ".sync_collections"
	if cfgErr == nil && cfg.SyncCollection.Src != "" {
		syncDir = cfg.SyncCollection.Src
	}
	if _, err := os.Stat(syncDir); os.IsNotExist(err) {
		os.MkdirAll(syncDir, 0755)
	}

	entries, err := os.ReadDir(syncDir)
	if err != nil {
		return fmt.Errorf("error reading sync_collections: %v", err)
	}

	var items []string
	for i, entry := range entries {
		if entry.IsDir() {
			items = append(items, fmt.Sprintf("%d) %s", i+1, entry.Name()))
		}
	}
	items = append(items, "New file")
	items = append(items, "Back")

	prompt := promptui.Select{
		Label: "? Display data saved",
		Items: items,
	}

	_, result, err := prompt.Run()
	if err != nil {
		return err
	}

	if result == "Back" {
		return fmt.Errorf("back_selected") // Special error for back selection
	}

	if result == "New file" {
		// Create new config
		namePrompt := promptui.Prompt{
			Label: "Enter config name",
		}

		configName, err := namePrompt.Run()
		if err != nil {
			return err
		}

		if configName == "" {
			return fmt.Errorf("config name cannot be empty")
		}

		// Check if config already exists
		configPath := filepath.Join(syncDir, configName)
		if _, err := os.Stat(configPath); !os.IsNotExist(err) {
			return fmt.Errorf("config '%s' already exists", configName)
		}

		// Create directory
		err = os.MkdirAll(configPath, 0755)
		if err != nil {
			return fmt.Errorf("error creating config directory: %v", err)
		}

		// Copy current make-sync.yaml if it exists
		srcPath := "make-sync.yaml"
		destPath := filepath.Join(configPath, "make-sync.yaml")

		if _, err := os.Stat(srcPath); !os.IsNotExist(err) {
			data, err := os.ReadFile(srcPath)
			if err != nil {
				return fmt.Errorf("error reading current config: %v", err)
			}

			err = os.WriteFile(destPath, data, 0644)
			if err != nil {
				return fmt.Errorf("error saving config: %v", err)
			}
			// Copy optional .sync_ignore if present
			if info, ierr := os.Stat(".sync_ignore"); ierr == nil && !info.IsDir() {
				if err := copyFile(".sync_ignore", filepath.Join(configPath, ".sync_ignore"), info.Mode()); err != nil {
					fmt.Printf("⚠️  Failed to copy .sync_ignore: %v\n", err)
				} else {
					fmt.Println("📄 Copied: .sync_ignore ->", filepath.Join(configPath, ".sync_ignore"))
				}
			}
			// Also copy registered files listed in sync_collection.files
			if cfgErr == nil {
				if err := copyRegisteredFiles(cfg, configPath); err != nil {
					fmt.Printf("⚠️  Some registered files were not copied: %v\n", err)
				}
			}

			// Build encrypted bundle (data.enc) with password
			passPrompt := promptui.Prompt{Label: "Set password", Mask: '*'}
			password, perr := passPrompt.Run()
			if perr != nil {
				return perr
			}
			if password == "" {
				return fmt.Errorf("password cannot be empty")
			}
			// Prepare bundle items (use saved copies within configPath)
			bundleItems := []securestore.BundleItem{
				{SrcPath: destPath, ArchivePath: "make-sync.yaml"},
			}
			// optional .sync_ignore
			if _, ierr := os.Stat(filepath.Join(configPath, ".sync_ignore")); ierr == nil {
				bundleItems = append(bundleItems, securestore.BundleItem{SrcPath: filepath.Join(configPath, ".sync_ignore"), ArchivePath: ".sync_ignore"})
			}
			// include registered files/folders (from where we copied them)
			if cfgErr == nil {
				for _, item := range cfg.SyncCollection.Files {
					item = strings.TrimSpace(item)
					if item == "" {
						continue
					}
					destRel := item
					if filepath.IsAbs(item) {
						destRel = filepath.Base(item)
					}
					src := filepath.Join(configPath, destRel)
					if _, serr := os.Stat(src); serr == nil {
						// include both files and directories; directory walking is handled by securestore
						bundleItems = append(bundleItems, securestore.BundleItem{SrcPath: src, ArchivePath: destRel})
					}
				}
			}
			encOut := filepath.Join(configPath, "data.enc")
			if err := securestore.EncryptFiles([]byte(password), bundleItems, encOut); err != nil {
				return fmt.Errorf("encrypt failed: %v", err)
			}
			fmt.Println("🔒 Encrypted bundle created: ", encOut)
			// Remove plaintext copies now that encryption succeeded
			cleanupPlaintextCopies(configPath, cfg)
		} else {
			// No current config to save
			return fmt.Errorf("no make-sync.yaml file found in current directory - please create config first")
		}

		fmt.Printf("Config saved as '%s'\n", configName)
		return nil
	}

	// Update existing config
	parts := strings.Split(result, ") ")
	if len(parts) < 2 {
		return fmt.Errorf("invalid selection")
	}
	folderName := parts[1]

	configPath := filepath.Join(syncDir, folderName)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("config '%s' does not exist", folderName)
	}

	// Copy current make-sync.yaml
	srcPath := "make-sync.yaml"
	destPath := filepath.Join(configPath, "make-sync.yaml")

	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		return fmt.Errorf("no current config file to save")
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("error reading current config: %v", err)
	}

	err = os.WriteFile(destPath, data, 0644)
	if err != nil {
		return fmt.Errorf("error updating config: %v", err)
	}
	// Copy optional .sync_ignore if present
	if info, ierr := os.Stat(".sync_ignore"); ierr == nil && !info.IsDir() {
		if err := copyFile(".sync_ignore", filepath.Join(configPath, ".sync_ignore"), info.Mode()); err != nil {
			fmt.Printf("⚠️  Failed to copy .sync_ignore: %v\n", err)
		} else {
			fmt.Println("📄 Copied: .sync_ignore ->", filepath.Join(configPath, ".sync_ignore"))
		}
	}

	// Also copy registered files listed in sync_collection.files
	if cfgErr == nil {
		if err := copyRegisteredFiles(cfg, configPath); err != nil {
			fmt.Printf("⚠️  Some registered files were not copied: %v\n", err)
		}
	}

	// Rebuild encrypted bundle
	passPrompt := promptui.Prompt{Label: "Set password", Mask: '*'}
	password, perr := passPrompt.Run()
	if perr != nil {
		return perr
	}
	if password == "" {
		return fmt.Errorf("password cannot be empty")
	}
	bundleItems := []securestore.BundleItem{
		{SrcPath: destPath, ArchivePath: "make-sync.yaml"},
	}
	if _, ierr := os.Stat(filepath.Join(configPath, ".sync_ignore")); ierr == nil {
		bundleItems = append(bundleItems, securestore.BundleItem{SrcPath: filepath.Join(configPath, ".sync_ignore"), ArchivePath: ".sync_ignore"})
	}
	if cfgErr == nil {
		for _, item := range cfg.SyncCollection.Files {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			destRel := item
			if filepath.IsAbs(item) {
				destRel = filepath.Base(item)
			}
			src := filepath.Join(configPath, destRel)
			if _, serr := os.Stat(src); serr == nil {
				// include both files and directories
				bundleItems = append(bundleItems, securestore.BundleItem{SrcPath: src, ArchivePath: destRel})
			}
		}
	}
	encOut := filepath.Join(configPath, "data.enc")
	if err := securestore.EncryptFiles([]byte(password), bundleItems, encOut); err != nil {
		return fmt.Errorf("encrypt failed: %v", err)
	}
	fmt.Println("🔒 Encrypted bundle created: ", encOut)
	// Remove plaintext copies now that encryption succeeded
	cleanupPlaintextCopies(configPath, cfg)

	fmt.Printf("Config '%s' updated\n", folderName)
	return nil
}

// copyRegisteredFiles copies files listed under cfg.SyncCollection.Files into destDir,
// preserving relative paths when possible. Missing files are skipped with a warning.
func copyRegisteredFiles(cfg *config.Config, destDir string) error {
	if cfg == nil {
		return nil
	}
	var firstErr error
	for _, item := range cfg.SyncCollection.Files {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		src := item
		// Determine destination path: for absolute sources, use just the basename; for relative, keep structure
		destRel := item
		if filepath.IsAbs(item) {
			destRel = filepath.Base(item)
		}
		destPath := filepath.Join(destDir, destRel)

		// Ensure destination directory exists (for files); for directories, we'll ensure inside copyDirRecursive
		if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			fmt.Printf("⚠️  Cannot create folder for %s: %v\n", destRel, err)
			continue
		}

		// Check source exists and copy based on type
		info, err := os.Stat(src)
		if err != nil {
			fmt.Printf("ℹ️  Skip missing path: %s\n", src)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if info.IsDir() {
			if err := copyDirRecursive(src, destPath); err != nil {
				fmt.Printf("⚠️  Failed to copy dir %s: %v\n", src, err)
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			fmt.Printf("📁 Copied dir: %s -> %s\n", src, destPath)
			continue
		}

		if err := copyFile(src, destPath, info.Mode()); err != nil {
			fmt.Printf("⚠️  Failed to copy %s: %v\n", src, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		fmt.Printf("📄 Copied: %s -> %s\n", src, destPath)
	}
	return firstErr
}

// copyFile copies a file from src to dst with the provided file mode.
func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return nil
}

// copyDirRecursive copies all files under srcDir into dstDir, preserving relative structure.
func copyDirRecursive(srcDir, dstDir string) error {
	return filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(srcDir, path)
		if rerr != nil {
			return nil
		}
		outPath := filepath.Join(dstDir, rel)
		if d.IsDir() {
			return os.MkdirAll(outPath, 0755)
		}
		info, ierr := os.Stat(path)
		if ierr != nil {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
			return nil
		}
		return copyFile(path, outPath, info.Mode())
	})
}

// cleanupPlaintextCopies removes plaintext files from a saved collection folder after encryption.
// It deletes make-sync.yaml, .sync_ignore (if present), and any files listed in cfg.SyncCollection.Files
// that were copied into the collection folder.
func cleanupPlaintextCopies(collectionDir string, cfg *config.Config) {
	// remove make-sync.yaml
	_ = os.Remove(filepath.Join(collectionDir, "make-sync.yaml"))
	// remove .sync_ignore
	_ = os.Remove(filepath.Join(collectionDir, ".sync_ignore"))
	// remove registered files
	if cfg != nil {
		for _, item := range cfg.SyncCollection.Files {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			rel := item
			if filepath.IsAbs(item) {
				rel = filepath.Base(item)
			}
			// remove file or directory recursively
			_ = os.RemoveAll(filepath.Join(collectionDir, rel))
		}
	}
}

func showDeleteMenu() error {
	fmt.Println("? Display data saved")

	syncDir := ".sync_collections"
	if _, err := os.Stat(syncDir); os.IsNotExist(err) {
		os.MkdirAll(syncDir, 0755)
	}

	entries, err := os.ReadDir(syncDir)
	if err != nil {
		return fmt.Errorf("error reading sync_collections: %v", err)
	}

	if len(entries) == 0 {
		fmt.Println("No saved configs found")
		return nil
	}

	var items []string
	for i, entry := range entries {
		if entry.IsDir() {
			items = append(items, fmt.Sprintf("%d) %s", i+1, entry.Name()))
		}
	}
	items = append(items, "Back")

	prompt := promptui.Select{
		Label: "? Display data saved",
		Items: items,
	}

	_, result, err := prompt.Run()
	if err != nil {
		return err
	}

	if result == "Back" {
		return fmt.Errorf("back_selected") // Special error for back selection
	}

	parts := strings.Split(result, ") ")
	if len(parts) < 2 {
		return fmt.Errorf("invalid selection")
	}
	folderName := parts[1]

	configPath := filepath.Join(syncDir, folderName)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return fmt.Errorf("config '%s' does not exist", folderName)
	}

	// Confirm deletion
	confirmPrompt := promptui.Prompt{
		Label: fmt.Sprintf("Delete config '%s'? (y/N)", folderName),
	}

	confirm, err := confirmPrompt.Run()
	if err != nil {
		return err
	}

	if strings.ToLower(confirm) != "y" && strings.ToLower(confirm) != "yes" {
		fmt.Println("Deletion cancelled")
		return nil
	}

	err = os.RemoveAll(configPath)
	if err != nil {
		return fmt.Errorf("error deleting config: %v", err)
	}

	fmt.Printf("Config '%s' deleted\n", folderName)
	return nil
}

// showLoadMenu implementation moved from root.go
func showLoadMenu() error {
	fmt.Println("? Action Mode : load")

	syncDir := ".sync_collections"
	if _, err := os.Stat(syncDir); os.IsNotExist(err) {
		os.MkdirAll(syncDir, 0755)
	}

	entries, err := os.ReadDir(syncDir)
	if err != nil {
		return fmt.Errorf("error reading sync_collections: %v", err)
	}

	if len(entries) == 0 {
		fmt.Println("No saved configs found in .sync_collections")
		// Give option to go back
		backPrompt := promptui.Select{
			Label: "What would you like to do?",
			Items: []string{"Back to main menu"},
		}
		_, backResult, err := backPrompt.Run()
		if err != nil {
			return err
		}
		if backResult == "Back to main menu" {
			return fmt.Errorf("back_selected")
		}
		return nil
	}

	var items []string
	for i, entry := range entries {
		if entry.IsDir() {
			items = append(items, fmt.Sprintf("%d) %s", i+1, entry.Name()))
		}
	}
	items = append(items, "Back")

	prompt := promptui.Select{
		Label: "? Display data saved",
		Items: items,
	}

	_, result, err := prompt.Run()
	if err != nil {
		return err
	}

	if result == "Back" {
		return fmt.Errorf("back_selected") // Special error for back selection
	}

	// Extract folder name
	parts := strings.Split(result, ") ")
	if len(parts) < 2 {
		return fmt.Errorf("invalid selection")
	}
	folderName := parts[1]

	// If encrypted bundle exists, prefer decrypt flow
	encPath := filepath.Join(syncDir, folderName, "data.enc")
	if _, eerr := os.Stat(encPath); eerr == nil {
		passPrompt := promptui.Prompt{Label: "Enter password", Mask: '*'}
		password, perr := passPrompt.Run()
		if perr != nil {
			return perr
		}
		if password == "" {
			return fmt.Errorf("password cannot be empty")
		}
		// Decrypt into a staging folder first
		restoreDir := filepath.Join(".sync_temp", "restore_"+time.Now().Format("20060102_150405"))
		if err := os.MkdirAll(restoreDir, 0755); err != nil {
			return fmt.Errorf("failed to create restore dir: %v", err)
		}
		if err := securestore.DecryptToDir([]byte(password), encPath, restoreDir); err != nil {
			return fmt.Errorf("decrypt failed: %v", err)
		}
		// Copy make-sync.yaml to CWD (required to know mapping)
		srcCfg := filepath.Join(restoreDir, "make-sync.yaml")
		if info, err := os.Stat(srcCfg); err == nil && !info.IsDir() {
			if err := copyFile(srcCfg, "make-sync.yaml", info.Mode()); err != nil {
				return fmt.Errorf("failed to restore make-sync.yaml: %v", err)
			}
		} else {
			return fmt.Errorf("decrypted bundle missing make-sync.yaml")
		}
		// Copy .sync_ignore if present
		if info, err := os.Stat(filepath.Join(restoreDir, ".sync_ignore")); err == nil && !info.IsDir() {
			_ = copyFile(filepath.Join(restoreDir, ".sync_ignore"), ".sync_ignore", info.Mode())
		}
		// Parse only sync_collection from restored config (no validation)
		type scOnly struct {
			SyncCollection struct {
				Files []string `yaml:"files"`
			} `yaml:"sync_collection"`
		}
		var sc scOnly
		if b, rerr := os.ReadFile(srcCfg); rerr == nil {
			_ = yaml.Unmarshal(b, &sc)
		}
		// Restore registered files/folders to their intended paths
		for _, item := range sc.SyncCollection.Files {
			p := strings.TrimSpace(item)
			if p == "" {
				continue
			}
			var src string
			if filepath.IsAbs(p) {
				src = filepath.Join(restoreDir, filepath.Base(p))
			} else {
				src = filepath.Join(restoreDir, p)
			}
			// destination path: force relative restore (never absolute)
			// if user configured absolute path, we map it to basename under CWD
			dest := p
			if filepath.IsAbs(p) {
				dest = filepath.Base(p)
			}
			if info, err := os.Stat(src); err == nil {
				if info.IsDir() {
					if err := os.MkdirAll(dest, 0755); err != nil {
						fmt.Printf("⚠️  Skip restore dir %s: cannot create dir: %v\n", dest, err)
						continue
					}
					if err := copyDirRecursive(src, dest); err != nil {
						fmt.Printf("⚠️  Failed to restore dir %s: %v\n", dest, err)
						continue
					}
					fmt.Printf("📦 Restored dir: %s\n", dest)
				} else {
					if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
						fmt.Printf("⚠️  Skip restore %s: cannot create dir: %v\n", dest, err)
						continue
					}
					if err := copyFile(src, dest, info.Mode()); err != nil {
						fmt.Printf("⚠️  Failed to restore %s: %v\n", dest, err)
					} else {
						fmt.Printf("📦 Restored: %s\n", dest)
					}
				}
			} else {
				fmt.Printf("ℹ️  Skip missing in bundle: %s\n", src)
			}
		}
		// Clean up staging
		_ = os.RemoveAll(restoreDir)
		fmt.Printf("🔓 Decrypted and restored from %s\n", encPath)
	} else {
		// Legacy: copy plain make-sync.yaml
		configPath := filepath.Join(syncDir, folderName, "make-sync.yaml")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			return fmt.Errorf("config file not found in %s", folderName)
		}
		destPath := "make-sync.yaml"
		data, err := os.ReadFile(configPath)
		if err != nil {
			return fmt.Errorf("error reading config: %v", err)
		}
		if err := os.WriteFile(destPath, data, 0644); err != nil {
			return fmt.Errorf("error writing config: %v", err)
		}
		// Copy .sync_ignore if present
		if info, ierr := os.Stat(filepath.Join(syncDir, folderName, ".sync_ignore")); ierr == nil && !info.IsDir() {
			_ = copyFile(filepath.Join(syncDir, folderName, ".sync_ignore"), ".sync_ignore", info.Mode())
		}
		fmt.Printf("Config loaded from %s\n", configPath)
	}

	// Add to history
	cwd, _ := os.Getwd()
	history.AddPath(cwd)

	return nil
}
