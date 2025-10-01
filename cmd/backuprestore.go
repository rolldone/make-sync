package cmd

import (
	backuprestore "make-sync/cmd/backuprestore"

	"github.com/spf13/cobra"
)

// NewBackupRestoreCmd returns the data command implemented in the new
// backuprestore package so callers in this package can remain unchanged.
func NewBackupRestoreCmd() *cobra.Command {
	return backuprestore.NewBackupRestoreCmd()
}
