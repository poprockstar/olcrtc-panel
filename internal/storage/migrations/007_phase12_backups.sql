ALTER TABLE backups ADD COLUMN format_version INTEGER NOT NULL DEFAULT 0;
ALTER TABLE backups ADD COLUMN size_bytes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE backups ADD COLUMN checksum_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE backups ADD COLUMN completed_at TEXT;
ALTER TABLE backups ADD COLUMN error_message TEXT NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_backups_created_at ON backups(created_at);
