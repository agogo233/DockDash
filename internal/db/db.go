package db

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mattn/go-sqlite3"
)

type journal struct {
	Entries []journalEntry `json:"entries"`
}

type journalEntry struct {
	When int64  `json:"when"`
	Tag  string `json:"tag"`
}

func init() {
	sql.Register("sqlite3_dockdash", &sqlite3.SQLiteDriver{})
}

func Open(path string) (*sql.DB, error) {
	database, err := sql.Open("sqlite3_dockdash", path)
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
	} {
		if _, err := database.Exec(pragma); err != nil {
			database.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	return database, nil
}

func Migrate(database *sql.DB, folder string) error {
	journalPath := filepath.Join(folder, "meta", "_journal.json")
	data, err := os.ReadFile(journalPath)
	if err != nil {
		return fmt.Errorf("cannot read %s: %w", journalPath, err)
	}
	var log journal
	if err := json.Unmarshal(data, &log); err != nil {
		return fmt.Errorf("cannot parse %s: %w", journalPath, err)
	}

	const createTable = `CREATE TABLE IF NOT EXISTS __drizzle_migrations (
				id SERIAL PRIMARY KEY,
				hash text NOT NULL,
				created_at numeric
			)`
	if _, err := database.Exec(createTable); err != nil {
		return err
	}

	var applied sql.NullInt64
	err = database.QueryRow(`SELECT created_at FROM __drizzle_migrations ORDER BY created_at DESC LIMIT 1`).Scan(&applied)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	last := int64(0)
	if applied.Valid {
		last = applied.Int64
	}

	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, entry := range log.Entries {
		if last >= entry.When {
			continue
		}
		path := filepath.Join(folder, entry.Tag+".sql")
		content, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("cannot read %s: %w", path, err)
		}
		sum := sha256.Sum256(content)
		hash := hex.EncodeToString(sum[:])
		for _, statement := range strings.Split(string(content), "--> statement-breakpoint") {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if _, err := tx.Exec(statement); err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
		}
		if _, err := tx.Exec(`INSERT INTO __drizzle_migrations ("hash", "created_at") VALUES (?, ?)`, hash, entry.When); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func Healthy(database *sql.DB) bool {
	var one int
	return database.QueryRow(`SELECT 1`).Scan(&one) == nil
}
