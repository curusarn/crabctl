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

// Open creates or opens the state database at $XDG_STATE_HOME/crabctl/state.db.
func Open() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		stateHome = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(stateHome, "crabctl")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	dbPath := filepath.Join(dir, "state.db")
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
		"ALTER TABLE sessions ADD COLUMN pr_url TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE sessions ADD COLUMN parent TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE sessions ADD COLUMN last_interacted TIMESTAMP",
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

// SaveSessionUUID persists the Claude session UUID for an active session.
// Called when a UUID is first resolved so it survives accidental kills.
func (s *Store) SaveSessionUUID(name, sessionUUID, workDir, firstMsg string) error {
	_, err := s.db.Exec(`
		INSERT INTO sessions (name, session_file, work_dir, first_msg, updated_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(name) DO UPDATE SET
			session_file = excluded.session_file,
			work_dir = excluded.work_dir,
			first_msg = excluded.first_msg,
			updated_at = CURRENT_TIMESTAMP
	`, name, sessionUUID, workDir, firstMsg)
	return err
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

// PastSession represents a session that can be resumed.
type PastSession struct {
	Name        string
	SessionUUID string
	WorkDir     string
	FirstMsg    string
	LastSeen    time.Time
	Killed      bool // true if explicitly killed via crabctl
}

// ListResumable returns all sessions with a UUID, ordered by most recent first.
// Includes both explicitly killed sessions and ones that disappeared (Ctrl+C, crash).
func (s *Store) ListResumable(limit int) ([]PastSession, error) {
	rows, err := s.db.Query(`
		SELECT name, session_file, work_dir, first_msg, killed,
			COALESCE(killed_at, updated_at) AS last_seen
		FROM sessions
		WHERE session_file != ''
		ORDER BY last_seen DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []PastSession
	for rows.Next() {
		var ps PastSession
		var killed int
		if err := rows.Scan(&ps.Name, &ps.SessionUUID, &ps.WorkDir, &ps.FirstMsg, &killed, &ps.LastSeen); err != nil {
			return nil, err
		}
		ps.Killed = killed == 1
		result = append(result, ps)
	}
	return result, rows.Err()
}

// SaveParent persists the parent session key for a session.
func (s *Store) SaveParent(name, parent string) error {
	_, err := s.db.Exec(`
		INSERT INTO sessions (name, parent, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(name) DO UPDATE SET
			parent = excluded.parent,
			updated_at = CURRENT_TIMESTAMP
	`, name, parent)
	return err
}

// LoadAllParents returns a map of session name -> parent key for all sessions with a parent.
func (s *Store) LoadAllParents() (map[string]string, error) {
	rows, err := s.db.Query("SELECT name, parent FROM sessions WHERE parent != ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var name, parent string
		if err := rows.Scan(&name, &parent); err != nil {
			return nil, err
		}
		result[name] = parent
	}
	return result, rows.Err()
}

// SavePR persists the PR URL for a session.
func (s *Store) SavePR(name, prURL string) error {
	_, err := s.db.Exec(`
		INSERT INTO sessions (name, pr_url, updated_at)
		VALUES (?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(name) DO UPDATE SET
			pr_url = excluded.pr_url,
			updated_at = CURRENT_TIMESTAMP
	`, name, prURL)
	return err
}

// LoadAllPRs returns a map of session fullName -> PR URL for all sessions with a PR.
func (s *Store) LoadAllPRs() (map[string]string, error) {
	rows, err := s.db.Query("SELECT name, pr_url FROM sessions WHERE pr_url != ''")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]string)
	for rows.Next() {
		var name, prURL string
		if err := rows.Scan(&name, &prURL); err != nil {
			return nil, err
		}
		result[name] = prURL
	}
	return result, rows.Err()
}

// SaveInteraction records an interaction timestamp for a session.
func (s *Store) SaveInteraction(name string) error {
	_, err := s.db.Exec(`
		INSERT INTO sessions (name, last_interacted, updated_at)
		VALUES (?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(name) DO UPDATE SET
			last_interacted = CURRENT_TIMESTAMP,
			updated_at = CURRENT_TIMESTAMP
	`, name)
	return err
}

// LoadAllInteractions returns a map of session name -> last interaction time.
func (s *Store) LoadAllInteractions() (map[string]time.Time, error) {
	rows, err := s.db.Query("SELECT name, last_interacted FROM sessions WHERE last_interacted IS NOT NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make(map[string]time.Time)
	for rows.Next() {
		var name string
		var t time.Time
		if err := rows.Scan(&name, &t); err != nil {
			return nil, err
		}
		result[name] = t
	}
	return result, rows.Err()
}
