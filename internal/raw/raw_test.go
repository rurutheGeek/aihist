package raw

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPutStaysInside(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	mt := time.Unix(1700000000, 5)
	for _, rel := range []string{"a/b.jsonl", "../../x", `..\..\y`, "/etc/z"} {
		if err := s.Put("h", "t", rel, strings.NewReader("1"), mt); err != nil {
			t.Fatal(rel, err)
		}
	}
	m, err := s.List("h", "t")
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"a/b.jsonl", "x", "y", "etc/z"} {
		if m[rel] != (Info{1, mt.UnixNano()}) {
			t.Fatalf("%s: %+v", rel, m)
		}
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "x")); err == nil {
		t.Fatal("外に書いた")
	}
	if err := s.Put("..", "t", "a", strings.NewReader(""), mt); err != ErrBadPath {
		t.Fatal(err)
	}
}
