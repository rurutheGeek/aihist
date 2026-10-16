package ingest

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rurutheGeek/aihist/internal/store"
)

func writeZip(t *testing.T, p, body string, mt time.Time) {
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	w, _ := z.Create("conversations.json")
	w.Write([]byte(body))
	z.Close()
	f.Close()
	os.Chtimes(p, mt, mt)
}

// 同じ会話が古い書き出しと新しい書き出しにあれば、新しい方を取り込み、上げ直しは省く。
func TestWebExport(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dir := t.TempDir()
	conv := func(updated, text string) string {
		return `[{"uuid":"c1","name":"t","updated_at":"` + updated + `","chat_messages":[{"uuid":"m1","sender":"human","text":"` + text + `","created_at":"2026-10-01T00:00:00Z"}]}]`
	}
	now := time.Now()
	writeZip(t, filepath.Join(dir, "new.zip"), conv("2026-10-02T00:00:00Z", "新"), now)
	writeZip(t, filepath.Join(dir, "old.zip"), conv("2026-10-01T00:00:00Z", "旧"), now.Add(-time.Hour))
	r, err := Run(st, "claude_ai", "h", dir)
	if err != nil || r.Imported != 1 {
		t.Fatalf("%v %+v", err, r)
	}
	if got := st.Stamp("claude_ai", "h", "c1"); got != "v1;2026-10-02T00:00:00Z;m1" {
		t.Fatalf("stamp %q", got)
	}
	if r, _ = Run(st, "claude_ai", "h", dir); r.Skipped != 1 || r.Imported != 0 {
		t.Fatalf("again %+v", r)
	}
}
