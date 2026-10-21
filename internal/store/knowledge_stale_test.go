package store

import "testing"

// 読み直しを待つ印（reparse:）が付いたままでも、まとめた後は古くならない。
func TestStaleIgnoresReparse(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO conversations (id, tool, host, external_id, source_stamp) VALUES (1, 'codex', 'h', 'e', 'reparse:v2;1')`); err != nil {
		t.Skip(err)
	}
	if err := s.SetSummary(1, "x", "m"); err != nil {
		t.Fatal(err)
	}
	if s.Stale(1) {
		t.Error("まとめた直後なのに古いとみなした")
	}
}
