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
CREATE INDEX IF NOT EXISTS idx_files_path_last_modified_at ON files(path, last_modified_at DESC);

CREATE TABLE IF NOT EXISTS events (
    started_at INTEGER NOT NULL CHECK(started_at > 0) PRIMARY KEY,  -- Valid Unix timestamp
    ended_at INTEGER CHECK(ended_at > 0),  -- Valid Unix timestamp
    type TEXT NOT NULL CHECK(type IN ('backup', 'restore')),  -- Restrict to known event types
    details TEXT,  -- Optional field for additional context
    created_at INTEGER NOT NULL DEFAULT (unixepoch())  -- Track when record was created
);
CREATE INDEX IF NOT EXISTS idx_events_type ON events(type);
