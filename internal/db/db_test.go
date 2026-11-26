package db

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateAppliesEachMigrationOnce(t *testing.T) {
	folder := t.TempDir()
	if err := os.MkdirAll(filepath.Join(folder, "meta"), 0o755); err != nil {
		t.Fatal(err)
	}
	journal := `{"version":"7","dialect":"sqlite","entries":[{"idx":0,"version":"6","when":1234,"tag":"0000_test","breakpoints":true}]}`
	if err := os.WriteFile(filepath.Join(folder, "meta", "_journal.json"), []byte(journal), 0o644); err != nil {
		t.Fatal(err)
	}
	sqlText := "CREATE TABLE demo (id INTEGER);--> statement-breakpoint\nINSERT INTO demo (id) VALUES (1);"
	if err := os.WriteFile(filepath.Join(folder, "0000_test.sql"), []byte(sqlText), 0o644); err != nil {
		t.Fatal(err)
	}

	database, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	if err := Migrate(database, folder); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM demo`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one row, got %d", count)
	}
	sum := sha256.Sum256([]byte(sqlText))
	expected := hex.EncodeToString(sum[:])
	var stored string
	if err := database.QueryRow(`SELECT hash FROM __drizzle_migrations`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != expected {
		t.Fatalf("hash mismatch: %s != %s", stored, expected)
	}

	if err := Migrate(database, folder); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM demo`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("migration ran twice, got %d rows", count)
	}
}
