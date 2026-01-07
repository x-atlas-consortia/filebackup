# FileBackup — CLI Reference

A command-line utility for backing up, encrypting, and restoring files to AWS S3.

Usage: `filebackup <command> [subcommand] [flags]`

Global flags (defined in [`cmd/root.go`](cmd/root.go)):
- `--log-level, -l` (string, default: `info`) — valid values: `debug`, `info`, `warn`, `error`.
- `--profile, -p` (string) — select config profile. defaults to name `default` if not specified.

Help:
- `filebackup --help` or `filebackup <command> --help`

## Commands

1. init
- Command: `filebackup init`
- File: [`cmd/init.go`](cmd/init.go)
- Purpose: Interactive initialization to create/update config profile. This must be run before any backup/restore operations.
- Notes: Prompts for AWS keys, region, S3 bucket and encryption secret.

2. backup
- Top-level: [`cmd/backup.go`](cmd/backup.go)
- Subcommands:
  - start
    - Command: `filebackup backup start`
    - Handler: calls [`backup.Backup`](internal/backup/backup.go)
    - Flags:
      - `--directories, -D` (string slice, required) — directories to back up.
      - `--details, -d` (string) — details for the backup event.
      - `--temp-dir, -t` (dirname, default: system temp) — temporary workspace.
      - `--max-workers, -w` (int, default: 4) — must be >= 2 and <= number of CPU cores.
    - Behavior: walks directories, encrypts chunks, uploads via AWS manager, batches SQLite DB inserts.
  - list
    - Command: `filebackup backup list`
    - Handler: [`list.ListBackups`](internal/list/backup.go)
    - Flags:
      - `--out, -o` (filename) — output file (default: stdout).
    - Behavior: lists backup events from the database for the selected profile.

3. restore
- Top-level: [`cmd/restore.go`](cmd/restore.go)
- Subcommands:
  - start
    - Command: `filebackup restore start --manifest <file> --out <dir> [flags]`
    - Handler: calls [`restore.Restore`](internal/restore/restore.go)
    - Flags:
      - `--manifest, -m` (filename, required) — manifest file (CSV: `bucket,key,version_id`) parsed by [`aws.ParseManifestFile`](internal/aws/manifest.go).
      - `--out, -o` (dirname, default: `.`) — output directory for restored files.
      - `--details, -d` (string) — details for the restore event.
      - `--temp-dir, -t` (dirname, default: system temp) — temporary workspace.
      - `--max-workers, -w` (int, default: 4) — must be >= 1 and <= number of CPU cores.
    - Behavior: downloads encrypted objects, decrypts, verifies SHA256 via SQLite DB.
  - list
    - Command: `filebackup restore list`
    - Handler: [`list.ListRestores`](internal/list/restore.go)
    - Flags:
      - `--out, -o` (filename) — output file (default: stdout).
    - Behavior: lists restore events from the database for the selected profile.

4. file
- Top-level: [`cmd/file.go`](cmd/file.go)
- Subcommands:
  - list
    - Command: `filebackup file list`
    - Handler: [`list.ListFiles`](internal/list/file.go)
    - Flags:
      - `--directory, -D` (dirname) — path prefix to list.
      - `--time, -t` (RFC3339 string) — list files as of specific time (default now) (ex. 2023-01-01T00:00:00Z).
      - `--out, -o` (filename) — output file (default: stdout).
      - `--manifest, -m` (bool) — produce AWS S3 manifest-format output (`bucket,path,version_id`). This format is compatible with AWS S3 Batch Operations. Defaults to false.
    - Behavior: lists backed up files at a specific time from the database for the selected profile.
    - Notes: The manifest output can be used to create AWS S3 Batch Operations jobs for bulk actions like restoring from Glacier Deep Archive.
      - [AWS S3 Batch Operations](https://docs.aws.amazon.com/AmazonS3/latest/userguide/batch-ops-create-job.html)
      - [Batch Restore From AWS S3 Deep Archice](https://repost.aws/knowledge-center/s3-batch-operation-initiate-restore)

5. version
- Top-level: [`cmd/version.go`](cmd/version.go)
- Subcommands:
  - list
    - Command: `filebackup version list`
    - Handler: [`list.ListVersions`](internal/list/version.go)
    - Flags:
      - `--file, -f` (filename, required) — path of the file to list versions for.
      - `--out, -o` (filename) — output file (default: stdout).
    - Behavior: lists all backed up versions of a specific file from the database for the selected profile.

## Profiles
The utility supports multiple configuration profiles stored in `~/.config/filebackup/config.json`. This allows separate AWS credentials, S3 buckets, and encryption secrets for different backup sets (e.g., development vs production). A new profile can be created using `filebackup init -p <profilename>`. A new SQLite database is created per profile.

## Logs
Logs are saved to `~/.local/share/filebackup` by default. The log level can be set via the `--log-level` global flag.

## Backup process overview
1. Walk specified directories to list files.
2. For each file:
   - Split file into chunks.
   - Encrypt each chunk using AES-256-GCM with a key derived from the profile secret (set during init, <u>DO NOT LOG OR EXPOSE THIS SECRET</u>). Encrypted files are in the format: `[16-byte salt]([12-byte nonce][length of ciphertext as uint64][ciphertext]) x N`. N is the number of chunks.
   - Write encrypted chunks to temporary files.
   - Unencrypt the chunks and verify SHA256 hash against the original file.
   - Upload encrypted file to S3 using the AWS SDK. AWS SDK handles integrity checks for uploaded objects.
3. Record file metadata and AWS version ID in the local SQLite database.

## Database and schema
The utility uses a local SQLite database per profile to track backed up files, versions, and events. Databases are stored at `~/.local/share/filebackup`.

### files
| Column           | Type    | Description                                     |
|------------------|---------|-------------------------------------------------|
| path             | TEXT    | Original file path                              |
| aws_version_id   | TEXT    | AWS S3 version ID after upload                  |
| sha256           | TEXT    | SHA256 hash of the original file                |
| size             | INTEGER | Size of the original file in bytes              |
| last_modified_at | INTEGER | Last modified unix timestamp of the file        |
| created_at       | INTEGER | Unix timestamp when database entry was created  |

### events
| Column     | Type    | Description                                    |
|------------|---------|------------------------------------------------|
| started_at | INTEGER | Unix timestamp when the event started          |
| ended_at   | INTEGER | Unix timestamp when the event ended            |
| type       | TEXT    | Event type: `backup` or `restore`              |
| details    | TEXT    | Optional details about the event               |
| created_at | INTEGER | Unix timestamp when database entry was created |

## Build & distribution
A build helper script and Dockerfile are provided for easy building for RedHat-based Linux systems.
- Build helper: [`build.sh`](build.sh)
- Docker build: [`Dockerfile`](Dockerfile)
- Dev run example: [`run_filebackup_dev.sh`](run_filebackup_dev.sh)
