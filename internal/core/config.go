package core

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	AWSAccessKeyID     string
	AWSRegion          string
	AWSS3Bucket        string
	AWSSecretAccessKey string
	DatabasePath       string
	Directories        []string
	EncryptionSecret   string
	LogDir             string
	LogLevel           slog.Leveler
	MaxWorkers         int
	TempDir            string
}

type rawConfig struct {
	AWSAccessKeyID     string   `json:"aws_access_key_id"`
	AWSRegion          string   `json:"aws_region"`
	AWSS3Bucket        string   `json:"aws_s3_bucket"`
	AWSSecretAccessKey string   `json:"aws_secret_access_key"`
	DatabasePath       string   `json:"database_path"`
	Directories        []string `json:"directories"`
	EncryptionSecret   string   `json:"encryption_secret"`
	LogDir             string   `json:"log_dir"`
	LogLevel           string   `json:"log_level"`
	MaxWorkers         int      `json:"max_workers"`
	TempDir            string   `json:"temp_dir"`
}

func ParseConfigFile(path string) (Config, error) {
	// Check if the file exists
	if _, err := os.Stat(path); err != nil {
		return Config{}, err
	}

	// Read file
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse JSON
	var rawConfig rawConfig
	if err := json.Unmarshal(data, &rawConfig); err != nil {
		return Config{}, fmt.Errorf("failed to parse JSON config: %w", err)
	}

	// Validate config
	return validateConfig(rawConfig)
}

func validateConfig(config rawConfig) (Config, error) {
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

	if config.DatabasePath == "" {
		errs = append(errs, fmt.Sprintf("invalid database_path: %s", config.DatabasePath))
	}

	if len(config.Directories) > 0 {
		for i, dir := range config.Directories {
			if dir == "" {
				errs = append(errs, fmt.Sprintf("invalid directories entry: empty string at position %d", i))
				continue
			}
			if stat, err := os.Stat(dir); err != nil || !stat.IsDir() {
				errs = append(errs, fmt.Sprintf("invalid directories entry: not a directory: %s", dir))
			}
		}
	} else {
		errs = append(errs, "no directories specified")
	}

	if len(config.EncryptionSecret) < 32 {
		errs = append(errs, "encryption_secret must be at least 32 characters")
	}

	if stat, err := os.Stat(config.LogDir); err != nil || !stat.IsDir() {
		errs = append(errs, fmt.Sprintf("invalid log_dir: %s", config.LogDir))
	}

	var logLevel slog.Level
	if llStr := config.LogLevel; llStr != "" {
		switch strings.ToLower(llStr) {
		case "debug":
			logLevel = slog.LevelDebug
		case "info":
			logLevel = slog.LevelInfo
		case "warn":
			logLevel = slog.LevelWarn
		case "error":
			logLevel = slog.LevelError
		default:
			errs = append(errs, fmt.Sprintf("invalid log_level: %s", llStr))
		}
	}

	if config.MaxWorkers < 1 {
		errs = append(errs, fmt.Sprintf("max_workers must be at least 1: %d", config.MaxWorkers))
	}

	if config.TempDir == "" {
		errs = append(errs, "invalid temp_dir: cannot be empty")
	} else if stat, err := os.Stat(config.TempDir); err != nil || !stat.IsDir() {
		errs = append(errs, fmt.Sprintf("invalid temp_dir: %s", config.TempDir))
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("errors in environment file: %s", strings.Join(errs, ", "))
	}

	return Config{
		AWSAccessKeyID:     config.AWSAccessKeyID,
		AWSRegion:          config.AWSRegion,
		AWSS3Bucket:        config.AWSS3Bucket,
		AWSSecretAccessKey: config.AWSSecretAccessKey,
		DatabasePath:       config.DatabasePath,
		Directories:        config.Directories,
		EncryptionSecret:   config.EncryptionSecret,
		LogDir:             config.LogDir,
		LogLevel:           logLevel,
		MaxWorkers:         config.MaxWorkers,
		TempDir:            config.TempDir,
	}, nil
}
