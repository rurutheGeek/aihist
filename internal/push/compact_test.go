package push

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompactOpenCode(t *testing.T) {
	p := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", "file:"+p+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	db.Exec("CREATE TABLE part (id INTEGER PRIMARY KEY, data TEXT)")
	blob := strings.Repeat("x", 1<<20)
	for i := 0; i < 100; i++ {
		db.Exec("INSERT INTO part (data) VALUES (?)", blob)
	}
	db.Exec("DELETE FROM part WHERE id > 5")
	db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	db.Close()
	before, _ := os.Stat(p)
	n, err := CompactOpenCode(p)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(p)
	if n < 64<<20 || after.Size() > 10<<20 {
		t.Fatalf("before %d after %d freed %d", before.Size(), after.Size(), n)
	}
	// 空きが少なければ何もしない
	if n, err := CompactOpenCode(p); err != nil || n != 0 {
		t.Fatalf("second: %d %v", n, err)
	}
}
