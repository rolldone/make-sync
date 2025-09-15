package devsync

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cespare/xxhash/v2"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// FileMetadata represents file metadata stored in database
type FileMetadata struct {
	ID        uint      `gorm:"primarykey"`
	Path      string    `gorm:"uniqueIndex;not null"`
	Hash      string    `gorm:"not null"`
	Size      int64     `gorm:"not null"`
	ModTime   time.Time `gorm:"not null"`
	LastSync  time.Time `gorm:"not null"`
	CreatedAt time.Time
	UpdatedAt time.Time
}

// FileCache manages file metadata database
type FileCache struct {
	db        *gorm.DB
	watchPath string
}

// NewFileCache creates a new file cache instance
func NewFileCache(dbPath string, watchPath string) (*FileCache, error) {
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %v", err)
	}

	// Auto migrate the schema
	err = db.AutoMigrate(&FileMetadata{})
	if err != nil {
		return nil, fmt.Errorf("failed to migrate database: %v", err)
	}

	return &FileCache{db: db, watchPath: watchPath}, nil
}

// ResetCache clears all cached file metadata
func (fc *FileCache) ResetCache() error {
	fmt.Println("🗑️  Resetting file cache...")

	// Delete all records from FileMetadata table
	result := fc.db.Unscoped().Delete(&FileMetadata{}, "1 = 1") // Delete all records
	if result.Error != nil {
		return fmt.Errorf("failed to reset cache: %v", result.Error)
	}

	fmt.Printf("✅ Cache reset complete: %d records deleted\n", result.RowsAffected)
	return nil
}

// CalculateFileHash calculates xxHash of file for fast comparison
func (fc *FileCache) CalculateFileHash(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := xxhash.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// ShouldSyncFile checks if file should be synced based on metadata
func (fc *FileCache) ShouldSyncFile(filePath string) (bool, error) {
	// Get file info
	_, err := os.Stat(filePath)
	if err != nil {
		return false, err
	}

	// Calculate current hash
	currentHash, err := fc.CalculateFileHash(filePath)
	if err != nil {
		return false, err
	}

	// Get relative path for database lookup from watch directory
	relPath, err := filepath.Rel(fc.watchPath, filePath)
	if err != nil {
		// If relative path fails, try to extract from absolute path
		if strings.HasPrefix(filePath, fc.watchPath) {
			relPath = strings.TrimPrefix(filePath, fc.watchPath)
			relPath = strings.TrimPrefix(relPath, "/")
		} else {
			relPath = filepath.Base(filePath)
		}
	}

	// Check if file exists in database (silently - suppress GORM logs)
	var existingRecords []FileMetadata
	silentDB := fc.db.Session(&gorm.Session{Logger: fc.db.Logger.LogMode(0)}) // Silent logger
	err = silentDB.Where("path = ?", relPath).Find(&existingRecords).Error

	if err != nil {
		return false, err
	}

	if len(existingRecords) == 0 {
		// File not in database, should sync
		return true, nil
	}

	existing := existingRecords[0]

	// Check if file has changed
	if existing.Hash != currentHash {
		// File has changed, should sync
		return true, nil
	}

	// File hasn't changed, skip sync
	return false, nil
}

func (fc *FileCache) UpdateMetaDataFromDownload(filePath string, hash string) error {

	relPath, err := filepath.Rel(fc.watchPath, filePath)
	if err != nil {
		return err
	}
	// Update or create metadata
	// metadata := FileMetadata{
	// 	Path:     relPath,
	// 	Hash:     hash,
	// 	Size:     0,
	// 	ModTime:  time.Now(),
	// 	LastSync: time.Now(),
	// }
	fmt.Println("💾 Updating cache for", relPath, "with hash", hash)
	err = fc.db.Model(&FileMetadata{}).Where("path = ?", relPath).Update("hash", hash).Error
	return err
}

// UpdateFileMetadata updates or creates file metadata after successful sync
func (fc *FileCache) UpdateFileMetadata(filePath string) error {
	// Get file info
	info, err := os.Stat(filePath)
	if err != nil {
		return err
	}

	// Calculate hash
	hash, err := fc.CalculateFileHash(filePath)
	if err != nil {
		return err
	}

	// Get relative path from watch directory
	relPath, err := filepath.Rel(fc.watchPath, filePath)
	if err != nil {
		// If relative path fails, try to extract from absolute path
		if strings.HasPrefix(filePath, fc.watchPath) {
			relPath = strings.TrimPrefix(filePath, fc.watchPath)
			relPath = strings.TrimPrefix(relPath, "/")
		} else {
			relPath = filepath.Base(filePath)
		}
	}

	// Update or create metadata
	metadata := FileMetadata{
		Path:     relPath,
		Hash:     hash,
		Size:     info.Size(),
		ModTime:  info.ModTime(),
		LastSync: time.Now(),
	}

	result := fc.db.Where("path = ?", relPath).Assign(metadata).FirstOrCreate(&metadata)
	return result.Error
}

// GetFileStats returns statistics about cached files
func (fc *FileCache) GetFileStats() (totalFiles int64, totalSize int64, err error) {
	var count int64
	err = fc.db.Model(&FileMetadata{}).Count(&count).Error
	if err != nil {
		return 0, 0, err
	}

	var size int64
	err = fc.db.Model(&FileMetadata{}).Select("COALESCE(SUM(size), 0)").Scan(&size).Error
	if err != nil {
		return count, 0, err
	}

	return count, size, nil
}

// Close closes the database connection
func (fc *FileCache) Close() error {
	sqlDB, err := fc.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}
