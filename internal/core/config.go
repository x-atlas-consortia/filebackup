package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrProfileNotFound = errors.New("profile not found in config file")

const DefaultProfile = "default"
const configPath = "$HOME/.config/filebackup/config.json"
const databasePath = "$HOME/.local/share/filebackup/filebackup-%s.db"

func GetDatabasePath(profile string) string {
	if profile == "" {
		profile = DefaultProfile
	}

	dbPath := fmt.Sprintf(databasePath, profile)
	return os.ExpandEnv(dbPath)
}

type Config struct {
	AWSAccessKeyID     string `json:"aws_access_key_id"`
	AWSRegion          string `json:"aws_region"`
	AWSS3Bucket        string `json:"aws_s3_bucket"`
	AWSSecretAccessKey string `json:"aws_secret_access_key"`
	EncryptionSecret   string `json:"encryption_secret"`
}

func SaveConfigFile(config Config, profile string) error {
	if profile == "" {
		profile = DefaultProfile
	}

	// Check if config file already exists
	configExists, err := DoesConfigFileExist()
	if err != nil {
		return err
	}

	configPath := os.ExpandEnv(configPath)
	if configExists {
		err := updateConfigFile(config, profile, configPath)
		if err != nil {
			return err
		}
	} else {
		err := createConfigFile(config, profile, configPath)
		if err != nil {
			return err
		}
	}

	// Create the database directory if it doesn't exist
	databasePath := GetDatabasePath(profile)
	databaseDir := filepath.Dir(databasePath)
	err = os.MkdirAll(databaseDir, 0700)
	if err != nil {
		return err
	}

	// Backup existing database file if it exists
	if _, err := os.Stat(databasePath); err == nil {
		existingDatabaseName := filepath.Base(databasePath)
		backupDatabaseName := fmt.Sprintf("%s-%s.log", existingDatabaseName, time.Now().UTC().Format("2006-01-02-15-04-05"))
		backupDatabasePath := filepath.Join(databaseDir, backupDatabaseName)
		err = os.Rename(databasePath, backupDatabasePath)
		if err != nil {
			return fmt.Errorf("failed to backup existing database file: %w", err)
		}
	}

	return nil
}

func createConfigFile(config Config, profile, configPath string) error {
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

	configMap := map[string]Config{
		profile: config,
	}

	// Save json to config file
	encoder := json.NewEncoder(configFile)
	encoder.SetIndent("", "  ")
	err = encoder.Encode(configMap)
	if err != nil {
		return fmt.Errorf("failed to write config to file: %w", err)
	}

	return nil
}

func updateConfigFile(config Config, profile, configPath string) error {
	// Read existing config file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read existing config file: %w", err)
	}

	// Parse existing config file
	var configMap map[string]Config
	err = json.Unmarshal(data, &configMap)
	if err != nil {
		return fmt.Errorf("failed to parse existing config file: %w", err)
	}

	// Update the profile with the new config
	configMap[profile] = config

	// Write updated config back to file
	configFile, err := os.Create(configPath)
	if err != nil {
		return fmt.Errorf("failed to open config file for writing: %w", err)
	}
	defer configFile.Close()

	encoder := json.NewEncoder(configFile)
	encoder.SetIndent("", "  ")
	err = encoder.Encode(configMap)
	if err != nil {
		return fmt.Errorf("failed to write updated config to file: %w", err)
	}

	return nil
}

func DoesConfigFileExist() (bool, error) {
	configPath := os.ExpandEnv(configPath)
	if _, err := os.Stat(configPath); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, fmt.Errorf("failed to check config file: %w", err)
	}
}

func ParseConfigFile(profile string) (Config, error) {
	if profile == "" {
		profile = DefaultProfile
	}

	// Check if the file exists
	exists, err := DoesConfigFileExist()
	if err != nil {
		return Config{}, err
	}
	if !exists {
		return Config{}, fmt.Errorf("config file does not exist at path: %s", os.ExpandEnv(configPath))
	}

	// Read the file
	configPath := os.ExpandEnv(configPath)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return Config{}, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse config file
	var configMap map[string]Config
	err = json.Unmarshal(data, &configMap)
	if err != nil {
		return Config{}, fmt.Errorf("failed to parse config file: %w", err)
	}

	// Check if profile exists
	if _, ok := configMap[profile]; !ok {
		return Config{}, ErrProfileNotFound
	}
	config := configMap[profile]

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
