package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/x-atlas-consortia/filebackup/internal/core"
	"golang.org/x/term"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize filebackup CLI",
	RunE: func(cmd *cobra.Command, args []string) error {
		reader := bufio.NewReader(os.Stdin)

		fmt.Println("Welcome to filebackup CLI initialization!\nThe following steps are required to initialize filebackup CLI.")

		// AWS Access Key ID
		fmt.Print("1. AWS Access Key ID: ")
		accessKeyByteSecret, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println() // new line after input
		if err != nil {
			return fmt.Errorf("failed to read aws access key id: %w", err)
		}
		accessKeySecret := strings.TrimSpace(string(accessKeyByteSecret))
		if len(accessKeySecret) == 0 {
			return fmt.Errorf("aws access key id cannot be empty")
		}

		// AWS Secret Access Key
		fmt.Print("2. AWS Secret Access Key: ")
		secretAccessKeyByteSecret, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println() // new line after input
		if err != nil {
			return fmt.Errorf("failed to read aws secret access key: %w", err)
		}
		secretAccessKeySecret := strings.TrimSpace(string(secretAccessKeyByteSecret))
		if len(secretAccessKeySecret) == 0 {
			return fmt.Errorf("aws secret access key cannot be empty")
		}

		// AWS Region
		fmt.Print("3. AWS region (e.g. us-east-1, us-west-2): ")
		region, _ := reader.ReadString('\n')
		region = strings.TrimSpace(region)

		// AWS S3 Bucket
		fmt.Print("4. AWS S3 bucket name: ")
		bucket, _ := reader.ReadString('\n')
		bucket = strings.TrimSpace(bucket)

		// Encryption Secret
		fmt.Print("5. Encryption secret (>32 characters): ")
		byteSecret, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Println() // new line after input
		if err != nil {
			return fmt.Errorf("failed to read encryption secret: %w", err)
		}
		secret := strings.TrimSpace(string(byteSecret))
		if len(secret) < 32 {
			return fmt.Errorf("encryption secret must be at least 32 characters")
		}

		config := core.Config{
			AWSAccessKeyID:     accessKeySecret,
			AWSRegion:          region,
			AWSS3Bucket:        bucket,
			AWSSecretAccessKey: secretAccessKeySecret,
			EncryptionSecret:   secret,
		}
		err = core.SaveConfigFile(config)
		if err != nil {
			return err
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
