PRAGMA journal_mode=WAL;        -- Enables concurrent reads
PRAGMA foreign_keys=ON;         -- Enforce foreign key constraints
PRAGMA synchronous=FULL;        -- Maximum durability (slower but safer)
PRAGMA temp_store=MEMORY;       -- Store temp data in memory for performance
PRAGMA mmap_size=268435456;     -- 256MB memory mapping for better performance
PRAGMA cache_size=10000;        -- Larger cache for better performance

-- Enable additional integrity checks
PRAGMA integrity_check;

CREATE TABLE IF NOT EXISTS files (
    path TEXT NOT NULL CHECK(length(path) > 0 AND path NOT LIKE '% %'),  -- No empty paths or leading/trailing spaces
    aws_version_id TEXT NOT NULL CHECK(
        length(aws_version_id) >= 32 AND 
        length(aws_version_id) <= 1024 AND
        aws_version_id GLOB '*[A-Za-z0-9_.-]*' AND
        aws_version_id NOT GLOB '*[^A-Za-z0-9_.-]*'
    ),  -- AWS version ID constraints
    size INTEGER NOT NULL CHECK(size >= 0),  -- No negative file sizes
    last_modified_at INTEGER NOT NULL CHECK(last_modified_at > 0),  -- Unix timestamp validation
    created_at INTEGER NOT NULL DEFAULT (unixepoch()),  -- Track when record was created
    PRIMARY KEY (path, aws_version_id)
);
CREATE INDEX IF NOT EXISTS idx_files_path_size_last_modified_at ON files(path, size, last_modified_at);

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
