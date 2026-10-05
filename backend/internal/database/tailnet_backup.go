package database

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
)

// migrationBackupSuffix is appended to the database path. The file is a
// standalone snapshot from before tailnet ids were added, and an older
// tsflow release can open it.
const migrationBackupSuffix = ".pre-tailnet"

// skipMigrationBackupEnv skips the snapshot. The migrated database then has
// no local rollback copy.
const skipMigrationBackupEnv = "TSFLOW_SKIP_DB_BACKUP"

func migrationBackupPath(dbPath string) string {
	return dbPath + migrationBackupSuffix
}

func skipMigrationBackup() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(skipMigrationBackupEnv))) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

// backupBeforeTailnetMigration writes a consistent copy of the database next
// to the live file. Startup stops before the schema change if the copy cannot
// be written, unless TSFLOW_SKIP_DB_BACKUP is set.
func (s *SQLiteStore) backupBeforeTailnetMigration(ctx context.Context) error {
	dest := migrationBackupPath(s.dbPath)
	if skipMigrationBackup() {
		log.Printf("Skipping pre-migration database backup because %s is set. An older tsflow release cannot use this database after the tailnet migration, and no rollback copy will be written.", skipMigrationBackupEnv)
		return nil
	}

	tmp := dest + ".tmp"
	if err := os.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("refusing to migrate %s until a pre-migration backup can be written to %s: %w. Set %s=1 to skip the backup", s.dbPath, dest, err, skipMigrationBackupEnv)
	}

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("refusing to migrate %s until a pre-migration backup can be written to %s: %w. Set %s=1 to skip the backup", s.dbPath, dest, err, skipMigrationBackupEnv)
	}
	defer conn.Close()

	var busy, logFrames, checkpointed int
	if err := conn.QueryRowContext(ctx, "PRAGMA wal_checkpoint(FULL)").Scan(&busy, &logFrames, &checkpointed); err != nil {
		return fmt.Errorf("refusing to migrate %s until a pre-migration backup can be written to %s: %w. Set %s=1 to skip the backup", s.dbPath, dest, err, skipMigrationBackupEnv)
	}
	if busy != 0 {
		return fmt.Errorf("refusing to migrate %s until a pre-migration backup can be written to %s: write-ahead log checkpoint was busy. Set %s=1 to skip the backup", s.dbPath, dest, skipMigrationBackupEnv)
	}

	if _, err := conn.ExecContext(ctx, "VACUUM INTO "+sqliteStringLiteral(tmp)); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("refusing to migrate %s until a pre-migration backup can be written to %s: %w. Set %s=1 to skip the backup", s.dbPath, dest, err, skipMigrationBackupEnv)
	}
	if info, err := os.Stat(s.dbPath); err == nil {
		_ = os.Chmod(tmp, info.Mode().Perm())
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("refusing to migrate %s until a pre-migration backup can be written to %s: %w. Set %s=1 to skip the backup", s.dbPath, dest, err, skipMigrationBackupEnv)
	}

	log.Printf("Wrote pre-migration database backup to %s. To roll back to an older tsflow release, stop this process, replace %s with %s, and remove %s-wal and %s-shm if they are present. Rows written after this migration are not in the backup.", dest, s.dbPath, dest, s.dbPath, s.dbPath)
	return nil
}

func sqliteStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
