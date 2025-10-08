package cmd

import (
	"github.com/spf13/cobra"
	"github.com/x-atlas-consortia/filebackup/internal/backup"
	"github.com/x-atlas-consortia/filebackup/internal/core"
)

// backupCmd represents the backup subcommand
var backupCmd = &cobra.Command{
	Use:   "backup",
	Short: "Create a backup of specified files or directories",
	RunE: func(cmd *cobra.Command, args []string) error {
		config, ok := cmd.Context().Value("config").(core.Config)
		if !ok {
			panic("config not found in context")
		}

		return backup.Backup(config)
	},
}

func init() {
	rootCmd.AddCommand(backupCmd)
}
