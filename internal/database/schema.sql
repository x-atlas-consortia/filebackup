PRAGMA journal_mode=WAL;        -- Enables concurrent reads
PRAGMA foreign_keys=ON;         -- Enforce foreign key constraints
PRAGMA synchronous=FULL;        -- Maximum durability (slower but safer)
PRAGMA temp_store=MEMORY;       -- Store temp data in memory for performance
PRAGMA mmap_size=268435456;     -- 256MB memory mapping for better performance
PRAGMA cache_size=10000;        -- Larger cache for better performance

-- Enable additional integrity checks
PRAGMA integrity_check;

CREATE TABLE IF NOT EXISTS files (
    path TEXT NOT NULL CHECK(length(path) > 0),  -- No empty paths
    aws_version_id TEXT NOT NULL,
    sha256 TEXT NOT NULL CHECK(length(sha256) = 64 AND sha256 GLOB '[0-9a-f]*'),  -- SHA-256 checksum validation
    size INTEGER NOT NULL CHECK(size >= 0),  -- No negative file sizes
    last_modified_at INTEGER NOT NULL CHECK(last_modified_at > 0),  -- Valid Unix timestamp
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),  -- Track when record was created
    PRIMARY KEY (path, aws_version_id)
);
CREATE INDEX IF NOT EXISTS idx_files_path_size_last_modified_at ON files(path, size, last_modified_at);
CREATE INDEX IF NOT EXISTS idx_files_path_aws_version_id ON files(path, aws_version_id);

CREATE TABLE IF NOT EXISTS timestamps (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp INTEGER NOT NULL CHECK(timestamp > 0),  -- Valid Unix timestamp
    event TEXT NOT NULL CHECK(event IN ('backup', 'restore')),  -- Restrict to known events
    details TEXT,  -- Optional field for additional context
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),  -- Track when record was created
    UNIQUE(timestamp, event)  -- Prevent duplicate events at same timestamp
);
CREATE INDEX IF NOT EXISTS idx_timestamps_event ON timestamps(event);
CREATE INDEX IF NOT EXISTS idx_timestamps_timestamp ON timestamps(timestamp);
