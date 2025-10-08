package cmd

import (
	"context"
	"os"

	"github.com/spf13/cobra"
	"github.com/x-atlas-consortia/filebackup/internal/core"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "filebackup",
	Short: "FileBackup is a simple file backup tool",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// validate config file path
		configPath, err := cmd.Flags().GetString("config")
		if err != nil {
			return err
		}

		config, err := core.ParseConfigFile(configPath)
		if err != nil {
			return err
		}

		cmd.SetContext(context.WithValue(cmd.Context(), "config", config))

		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

// Initialize persistent flags
func init() {
	rootCmd.PersistentFlags().StringP("config", "c", "./config.json", "Path to the configuration file (required)")
	rootCmd.MarkPersistentFlagRequired("config")
}
