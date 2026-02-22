package state

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    name         TEXT PRIMARY KEY,
    autoforward  INTEGER NOT NULL DEFAULT 0,
    killed       INTEGER NOT NULL DEFAULT 0,
    session_file TEXT NOT NULL DEFAULT '',
    work_dir     TEXT NOT NULL DEFAULT '',
    first_msg    TEXT NOT NULL DEFAULT '',
    last_send    TIMESTAMP,
    created_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
`

// Store wraps a SQLite database for persistent session state.
type Store struct {
	db *sql.DB
}

// Open creates or opens the state database at ~/.local/state/crabctl/state.db.
// Migrates from the old location (~/.config/crabctl/state.db) if present.
func Open() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	dir := filepath.Join(home, ".local", "state", "crabctl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(dir, "state.db")

	// Migrate from old location
	oldPath := filepath.Join(home, ".config", "crabctl", "state.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		if _, err := os.Stat(oldPath); err == nil {
			_ = os.Rename(oldPath, dbPath)
			// Also move WAL/SHM files if present
			_ = os.Rename(oldPath+"-wal", dbPath+"-wal")
			_ = os.Rename(oldPath+"-shm", dbPath+"-shm")
		}
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}

	// WAL mode for safe concurrent access
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, err
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}

	// Run migrations (ignore errors for already-existing columns)
	for _, m := range []string{
		"ALTER TABLE sessions ADD COLUMN work_dir TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE sessions ADD COLUMN first_msg TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE sessions ADD COLUMN killed_at TIMESTAMP",
	} {
		db.Exec(m) //nolint:errcheck
	}

	return &Store{db: db}, nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// SetAutoForward enables or disables autoforward for a session.
func (s *Store) SetAutoForward(name string, enabled bool) error {
	val := 0
	if enabled {
		val = 1
	}
	_, err := s.db.Exec(`
		INSERT INTO sessions (name, autoforward, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(name) DO UPDATE SET
			autoforward = excluded.autoforward,
			updated_at = CURRENT_TIMESTAMP
	`, name, val)
	return err
}

// LoadAllAutoForward returns a map of session names that have autoforward enabled.
func (s *Store) LoadAllAutoForward() (map[string]bool, error) {
	rows, err := s.db.Query("SELECT name FROM sessions WHERE autoforward = 1")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]bool)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		result[name] = true
	}
	return result, rows.Err()
}

// MarkKilled records a session as killed with its Claude session UUID, workdir, and first message.
func (s *Store) MarkKilled(name, sessionUUID, workDir, firstMsg string) error {
	_, err := s.db.Exec(`
		INSERT INTO sessions (name, killed, session_file, work_dir, first_msg, killed_at, updated_at)
		VALUES (?, 1, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(name) DO UPDATE SET
			killed = 1,
			session_file = excluded.session_file,
			work_dir = excluded.work_dir,
			first_msg = excluded.first_msg,
			killed_at = CURRENT_TIMESTAMP,
			updated_at = CURRENT_TIMESTAMP
	`, name, sessionUUID, workDir, firstMsg)
	return err
}

// KilledSession represents a killed session that can be resumed.
type KilledSession struct {
	Name        string
	SessionUUID string
	WorkDir     string
	FirstMsg    string
	KilledAt    time.Time
}

// ListKilled returns killed sessions ordered by most recently killed first.
func (s *Store) ListKilled(limit int) ([]KilledSession, error) {
	rows, err := s.db.Query(`
		SELECT name, session_file, work_dir, first_msg,
			COALESCE(killed_at, updated_at) AS killed_time
		FROM sessions
		WHERE killed = 1 AND session_file != ''
		ORDER BY killed_time DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []KilledSession
	for rows.Next() {
		var ks KilledSession
		var killedAt string
		if err := rows.Scan(&ks.Name, &ks.SessionUUID, &ks.WorkDir, &ks.FirstMsg, &killedAt); err != nil {
			return nil, err
		}
		ks.KilledAt, _ = time.Parse("2006-01-02 15:04:05", killedAt)
		result = append(result, ks)
	}
	return result, rows.Err()
}
