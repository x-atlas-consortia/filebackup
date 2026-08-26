ALTER TABLE files ADD COLUMN deleted_at INTEGER CHECK(deleted_at > 0);
CREATE INDEX IF NOT EXISTS idx_files_path_deleted_at ON files(path, deleted_at);
