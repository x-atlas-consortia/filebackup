# FileBackup - CLI Reference

A command-line utility for backing up, encrypting, and restoring files to AWS S3 Deep Archive with version tracking.

Usage: `filebackup <command> [subcommand] [flags]`

Global flags (defined in [`cmd/root.go`](cmd/root.go)):
- `--log-level, -l` (string, default: `info`) - valid values: `debug`, `info`, `warn`, `error`.
- `--profile, -p` (string) - select config profile. defaults to name `default` if not specified.

Help:
- `filebackup --help` or `filebackup <command> --help`

## Commands

### init
- Command: `filebackup init`
- File: [`cmd/init.go`](cmd/init.go)
- Purpose: Interactive initialization to create/update config profile. This must be run before any backup/restore operations.
- Notes: Prompts for AWS keys, region, S3 bucket and encryption secret. It is recommended to use a strong, unique encryption secret such as the output of `openssl rand -hex 32`. <u>DO NOT LOSE OR EXPOSE THIS SECRET</u>

### backup
- Top-level: [`cmd/backup.go`](cmd/backup.go)
- Subcommands:
  - start
    - Command: `filebackup backup start`
    - Handler: calls [`backup.Backup`](internal/backup/backup.go)
    - Flags:
      - `--directories, -D` (string slice, required) - directories to back up.
      - `--details, -d` (string) - details for the backup event.
      - `--temp-dir, -t` (dirname, default: system temp) - temporary workspace.
      - `--max-workers, -w` (int, default: 4) - must be >= 2 and <= number of CPU cores.
    - Behavior: walks directories, encrypts chunks, uploads via AWS manager, batches SQLite DB inserts. Also detects files that were previously backed up but are no longer found under the given directories, and marks them deleted in the database (see [Database and schema](#database-and-schema)).
  - list
    - Command: `filebackup backup list`
    - Handler: [`list.ListBackups`](internal/list/backup.go)
    - Flags:
      - `--out, -o` (filename) - output file (default: stdout).
    - Behavior: lists backup events from the database for the selected profile.

### restore
- Top-level: [`cmd/restore.go`](cmd/restore.go)
- Subcommands:
  - start
    - Command: `filebackup restore start --manifest <file> --out <dir> [flags]`
    - Handler: calls [`restore.Restore`](internal/restore/restore.go)
    - Flags:
      - `--manifest, -m` (filename, required) - manifest file (CSV: `bucket,key,version_id`) (See: [filebackup file list --manifest](#file))
      - `--out, -o` (dirname, default: `.`) - output directory for restored files.
      - `--details, -d` (string) - details for the restore event.
      - `--temp-dir, -t` (dirname, default: system temp) - temporary workspace.
      - `--max-workers, -w` (int, default: 4) - must be >= 1 and <= number of CPU cores.
    - Behavior: downloads encrypted objects, decrypts, verifies SHA256 via SQLite DB.
    - Notes: The file to be restored must have already been moved to a restorable state in S3 (e.g., restored from Glacier) before running this command. This can be achieved using AWS S3 Batch Operations and the [filebackup file list --manifest](#file) command to generate the manifest. The Batch Operations job must complete before starting `filebackup restore start` and may take several hours/days depending on the number of files and retrieval speed.
  - list
    - Command: `filebackup restore list`
    - Handler: [`list.ListRestores`](internal/list/restore.go)
    - Flags:
      - `--out, -o` (filename) - output file (default: stdout).
    - Behavior: lists restore events from the database for the selected profile.
  - status
    - Command: `filebackup restore status`
    - Handler: [`list.ListRestoreStatus`](internal/list/status.go)
    - Flags:
      - `--manifest, -m` (filename, required) - manifest file (CSV: `bucket,key,version_id`) (See: [filebackup file list --manifest](#file))
      - `--out, -o` (filename) - output file (default: stdout).
    - Behavior: checks the restore status of files in AWS Deep Archive in the provided manifest.

### file
- Top-level: [`cmd/file.go`](cmd/file.go)
- Subcommands:
  - list
    - Command: `filebackup file list`
    - Handler: [`list.ListFiles`](internal/list/file.go)
    - Flags:
      - `--directories, -D` (string slice) - path prefixes to list files under. If not specified, lists all files in the database.
      - `--time, -t` (RFC3339 string) - list files as of specific time (default now) (ex. 2023-01-01T00:00:00Z).
      - `--out, -o` (filename) - output file (default: stdout).
      - `--manifest, -m` (bool) - produce AWS S3 manifest-format output (`bucket,path,version_id`). This format is compatible with AWS S3 Batch Operations. Defaults to false.
    - Behavior: lists backed up files at a specific time from the database for the selected profile. Files that were deleted on or before the requested time are excluded (see [Database and schema](#database-and-schema)).
    - Notes: The manifest output can be used to create AWS S3 Batch Operations jobs for bulk actions like restoring from Glacier Deep Archive.
      - [AWS S3 Batch Operations](https://docs.aws.amazon.com/AmazonS3/latest/userguide/batch-ops-create-job.html)
      - [Batch Restore From AWS S3 Deep Archice](https://repost.aws/knowledge-center/s3-batch-operation-initiate-restore)

### version
- Top-level: [`cmd/version.go`](cmd/version.go)
- Subcommands:
  - list
    - Command: `filebackup version list`
    - Handler: [`list.ListVersions`](internal/list/version.go)
    - Flags:
      - `--file, -f` (filename, required) - path of the file to list versions for.
      - `--out, -o` (filename) - output file (default: stdout).
    - Behavior: lists all backed up versions of a specific file from the database for the selected profile.

## Profiles
The utility supports multiple configuration profiles stored in `~/.config/filebackup/config.json`. This allows separate AWS credentials, S3 buckets, and encryption secrets for different backup sets (e.g., development vs production). A new profile can be created using [filebackup init -p <profilename>](#init). A new SQLite database is created per profile.

## Logs
Logs are saved to `~/.local/share/filebackup` by default. The log level can be set via the `--log-level` global flag.

## Backup process overview
The backup process involves encrypting files using AES-256-GCM before uploading them to AWS S3. AWS S3 also provides encryption at rest and integrity checks for uploaded objects. Essentially, AWS S3 is encrypting an already encrypted file. AWS S3 does not have access to the encryption secret, ensuring end-to-end encryption. AWS S3 versioning must be enabled on the target bucket to allow for file version tracking.
1. Walk specified directories to list files.
2. For each file:
   - Split the file into 16 MiB plaintext chunks.
   - Derive a 32-byte AES-256 key from the profile encryption secret using Argon2id (<u>DO NOT LOSE OR EXPOSE THIS SECRET</u>).
   - Encrypt each chunk using AES-256-GCM and write the result in the [current encrypted file format](#encrypted-file-format) to a temporary file.
   - Decrypt the temporary file and verify its SHA256 hash matches the original file before uploading.
   - Upload the encrypted file to S3 using the AWS SDK. The AWS SDK handles upload integrity checks.
3. Record file metadata and AWS version ID in the local SQLite database.

## Restore process overview
1. Create a manifest file in CSV format: `bucket,key,version_id` for desired files using the [filebackup file list --manifest](file) command.
2. Start a restore job using [AWS S3 Batch Operations](https://docs.aws.amazon.com/AmazonS3/latest/userguide/batch-ops-create-job.html) with the manifest file to restore files.
3. Wait for the Batch Operations job to complete. This may take several hours/days depending on the number of files and retrieval speed.
4. Run the [filebackup restore start](#restore) command with the manifest file to download, decrypt, and verify the restored files.

## Encrypted file format

All files are encrypted with AES-256-GCM. The 32-byte key is derived from the profile encryption secret using Argon2id. Two on-disk formats exist: the current headered format written by all new backups, and a legacy format written by older versions. On restore, `filebackup` reads the first 12 bytes of each file and selects the correct decryption path automatically. No user action is required for files in either format.

### Current format (v1)

Files begin with a fixed 49-byte header, followed by a sequence of encrypted chunks. The header is included as additional authenticated data (AAD) on every AES-GCM seal and open, so any modification to the header, including the salt, Argon2id parameters, nonce prefix, or chunk size, causes authentication to fail on decryption.

**Header layout** (49 bytes, all multi-byte integers big-endian):

| Offset | Size | Field | Notes |
|--------|------|-------|-------|
| 0 | 12 | Magic | `X-FILEBACKUP`; identifies this format |
| 12 | 1 | Version | Currently `1` |
| 13 | 16 | Argon2id salt | Random per file |
| 29 | 4 | Argon2id time cost | Iterations; default `3` |
| 33 | 4 | Argon2id memory cost | KiB; default `65536` (64 MiB) |
| 37 | 1 | Argon2id parallelism | Default `4` |
| 38 | 7 | AES-GCM nonce prefix | Random per file |
| 45 | 4 | Plaintext chunk size | Bytes; default `16777216` (16 MiB) |

The Argon2id parameters follow RFC 9106's second recommended option (time=3, memory=64 MiB, parallelism=4). The parallelism value is stored in the header so decryption always uses the exact parameters the file was encrypted with, regardless of the CPU count of the decrypting machine.

**Chunk layout** (immediately following the header, repeated):

```
[ ciphertext (≤ chunk size bytes) ][ 16-byte GCM authentication tag ]
```

Every chunk except the last is exactly `chunk size` plaintext bytes on disk (`chunk size + 16` bytes including the tag). The final chunk holds the remaining bytes and may be shorter. Chunk boundaries are derived from the total file size; there is no per-chunk length field.

**Nonce construction** (12 bytes per chunk):

```
[ 7-byte random prefix (from header) ][ 4-byte big-endian chunk index ][ 1-byte last-chunk flag ]
```

Binding the chunk index and last-chunk flag into the nonce makes chunk reordering, truncation, and splicing between files detectable as authentication failures rather than silent data corruption.

### Legacy format

> **Note:** The legacy format is supported for restore only. All new backups use the current format.

Files written by older versions of `filebackup` begin with a 16-byte Argon2id salt directly, with no magic prefix or version field.

**Layout:**

```
[ 16-byte Argon2id salt ]
  repeated for each chunk:
    [ 12-byte random AES-GCM nonce ]
    [ 8-byte little-endian ciphertext length ]
    [ ciphertext (includes 16-byte GCM tag) ]
```

The legacy format has one known limitation that cannot be corrected retroactively: Argon2id parallelism was set to `runtime.NumCPU()` at encryption time and was never stored in the file. **A legacy file can only be decrypted on a machine with the same CPU core count as the one that encrypted it.** If decryption of a legacy file fails with an authentication error on an unexpected machine, a CPU count mismatch is the most likely cause. The current format eliminates this problem by storing all Argon2id parameters in the header.

Additionally, the legacy format uses no AAD, so header fields are not authenticated.

## Database and schema
The utility uses a local SQLite database per profile to track backed up files, versions, and events. Databases are stored at `~/.local/share/filebackup`. The schema is applied and upgraded automatically via versioned migration files in [`internal/database/migrations`](internal/database/migrations), tracked in the `schema_migrations` table - no manual migration steps are required.

### files
| Column           | Type    | Description                                                                                 |
|------------------|---------|---------------------------------------------------------------------------------------------|
| path             | TEXT    | Original file path                                                                          |
| aws_version_id   | TEXT    | AWS S3 version ID after upload                                                              |
| sha256           | TEXT    | SHA256 hash of the original file                                                            |
| size             | INTEGER | Size of the original file in bytes                                                          |
| last_modified_at | INTEGER | Last modified unix timestamp of the file                                                    |
| created_at       | INTEGER | Unix timestamp when database entry was created                                              |
| deleted_at       | INTEGER | Unix timestamp when the file was detected as deleted, or NULL if it hasn't been (see below) |

A file's current state is determined by its **latest** row per `path` (highest `last_modified_at`). During `backup start`, any previously-known file under the scanned directories that is not found on disk is marked deleted by setting `deleted_at` on its latest row. If a file with the same path is backed up again later, a new row is inserted with `deleted_at` set to `NULL`, which supersedes the deleted state. `filebackup file list` and `filebackup file random` exclude currently deleted files from their output so they aren't included in restore manifests.

### schema_migrations
| Column     | Type    | Description                                                  |
|------------|---------|--------------------------------------------------------------|
| version    | INTEGER | Migration version number (matches migration filename prefix) |
| applied_at | INTEGER | Unix timestamp when the migration was applied                |

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
