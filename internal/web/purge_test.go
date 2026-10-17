package web

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 書き出しの zip の中の文字のファイルだけを書き換え、ほかはそのまま写す。更新時刻も元のまま。
func TestScrubZip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "x.zip")
	f, _ := os.Create(p)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("conversations.json")
	w.Write([]byte(`[{"text":"key sk-proj-abcdefghijklmnopqrstuvwxyz0123 mail a@b.jp"}]`))
	w, _ = zw.Create("img.png")
	w.Write([]byte("sk-proj-abcdefghijklmnopqrstuvwxyz0123"))
	zw.Close()
	f.Close()
	old := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	os.Chtimes(p, old, old)

	ch, err := rewriteFile(p, func(r io.Reader, w io.Writer) (bool, error) { return scrubZip(r.(*os.File), w, "secret") })
	if err != nil || !ch {
		t.Fatal(ch, err)
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got := map[string]string{}
	for _, e := range zr.File {
		r, _ := e.Open()
		var b bytes.Buffer
		io.Copy(&b, r)
		got[e.Name] = b.String()
	}
	if strings.Contains(got["conversations.json"], "sk-proj") || !strings.Contains(got["conversations.json"], "a@b.jp") {
		t.Errorf("json %q", got["conversations.json"])
	}
	if got["img.png"] != "sk-proj-abcdefghijklmnopqrstuvwxyz0123" {
		t.Errorf("png %q", got["img.png"])
	}
	if st, _ := os.Stat(p); !st.ModTime().Equal(old) {
		t.Errorf("mtime %v", st.ModTime())
	}
}
