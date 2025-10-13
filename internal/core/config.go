package core

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const configPath = "$HOME/.config/filebackup/config.json"
const databasePath = "$HOME/.local/share/filebackup/filebackup.db"

func GetDatabasePath() string {
	return os.ExpandEnv(databasePath)
}

type Config struct {
	AWSAccessKeyID     string `json:"aws_access_key_id"`
	AWSRegion          string `json:"aws_region"`
	AWSS3Bucket        string `json:"aws_s3_bucket"`
	AWSSecretAccessKey string `json:"aws_secret_access_key"`
	EncryptionSecret   string `json:"encryption_secret"`
}

func SaveConfigFile(config Config) error {
	// Save to a config file
	configPath := os.ExpandEnv(configPath)
	configDir := filepath.Dir(configPath)
	err := os.MkdirAll(configDir, 0700)
	if err != nil {
		return err
	}
	configFile, err := os.Create(configPath)
	if err != nil {
		return err
	}
	defer configFile.Close()

	// save json to configFile
	encoder := json.NewEncoder(configFile)
	encoder.SetIndent("", "  ")
	err = encoder.Encode(config)
	if err != nil {
		return fmt.Errorf("failed to write config to file: %w", err)
	}

	// Create the database directory if it doesn't exist
	databasePath := os.ExpandEnv(databasePath)
	databaseDir := filepath.Dir(databasePath)
	err = os.MkdirAll(databaseDir, 0700)
	if err != nil {
		return err
	}

	// Backup existing database file if it exists
	if _, err := os.Stat(databasePath); err == nil {
		backupDatabaseName := fmt.Sprintf("filebackup-%s.log", time.Now().UTC().Format("2006-01-02-15-04-05"))
		backupDatabasePath := filepath.Join(databaseDir, backupDatabaseName)
		err = os.Rename(databasePath, backupDatabasePath)
		if err != nil {
			return fmt.Errorf("failed to backup existing database file: %w", err)
		}
	}

	return nil
}

func DoesConfigFileExist() bool {
	configPath := os.ExpandEnv(configPath)
	if _, err := os.Stat(configPath); err == nil {
		return true
	}
	return false
}

func ParseConfigFile() (Config, error) {
	// Check if the file exists
	if !DoesConfigFileExist() {
		return Config{}, fmt.Errorf("config file does not exist at path: %s", os.ExpandEnv(configPath))
	}

	// Read the file
	configPath := os.ExpandEnv(configPath)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return Config{}, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse config file
	var config Config
	err = json.Unmarshal(data, &config)
	if err != nil {
		return Config{}, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Validate config
	err = validateConfig(config)
	if err != nil {
		return Config{}, fmt.Errorf("invalid config file: %w", err)
	}

	return config, nil
}

func validateConfig(config Config) error {
	var errs []string

	if len(config.AWSAccessKeyID) < 1 {
		errs = append(errs, fmt.Sprintf("invalid aws_access_key_id: %s", config.AWSAccessKeyID))
	}

	if len(config.AWSRegion) < 1 {
		errs = append(errs, fmt.Sprintf("invalid aws_region: %s", config.AWSRegion))
	}

	if len(config.AWSS3Bucket) < 1 {
		errs = append(errs, fmt.Sprintf("invalid aws_s3_bucket: %s", config.AWSS3Bucket))
	}

	if len(config.AWSSecretAccessKey) < 1 {
		errs = append(errs, fmt.Sprintf("invalid aws_secret_access_key: %s", config.AWSSecretAccessKey))
	}

	if len(config.EncryptionSecret) < 32 {
		errs = append(errs, "encryption_secret must be at least 32 characters")
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors in environment file: %s", strings.Join(errs, ", "))
	}

	return nil
}
