package antigravity

import (
	"database/sql"
	"encoding/binary"
	"path/filepath"
	"testing"

	"github.com/rurutheGeek/aihist/internal/model"
)

// pb は protobuf を組み立てる。値は uint64（varint）か string / []byte（長さ付き）。
func pb(kv ...any) []byte {
	var b []byte
	for i := 0; i < len(kv); i += 2 {
		f := uint64(kv[i].(int))
		switch v := kv[i+1].(type) {
		case int:
			b = binary.AppendUvarint(b, f<<3)
			b = binary.AppendUvarint(b, uint64(v))
		case string:
			b = binary.AppendUvarint(b, f<<3|2)
			b = binary.AppendUvarint(b, uint64(len(v)))
			b = append(b, v...)
		case []byte:
			b = binary.AppendUvarint(b, f<<3|2)
			b = binary.AppendUvarint(b, uint64(len(v)))
			b = append(b, v...)
		}
	}
	return b
}

func TestParse(t *testing.T) {
	p := filepath.Join(t.TempDir(), "conv-1.db")
	db, err := sql.Open("sqlite", p)
	if err != nil {
		t.Fatal(err)
	}
	ts := pb(1, pb(1, 1779379819, 2, 5))
	steps := []struct {
		typ     int
		payload []byte
	}{
		{14, pb(1, 14, 5, ts, 19, pb(2, "質問です\n続き"))},
		{15, pb(1, 15, 5, ts, 20, pb(1, "答え", 3, "考え", 7, pb(1, "c1", 2, "view_file", 3, `{"p":"a"}`)))},
		{8, pb(1, 8, 5, ts, 14, pb(1, "file:///a", 4, "中身"))},
		{21, pb(1, 21, 5, ts, 28, pb(23, "ls", 21, pb(1, "a.txt")))},
	}
	if _, err := db.Exec(`CREATE TABLE steps (idx INTEGER, step_type INTEGER, step_payload BLOB)`); err != nil {
		t.Fatal(err)
	}
	for i, s := range steps {
		if _, err := db.Exec(`INSERT INTO steps VALUES (?,?,?)`, i, s.typ, s.payload); err != nil {
			t.Fatal(err)
		}
	}
	db.Close()

	c, err := Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.ExternalID != "conv-1" || c.Title != "質問です" || c.StartedAt.Unix() != 1779379819 {
		t.Fatalf("meta: %+v", c)
	}
	if len(c.Messages) != 4 {
		t.Fatalf("messages = %d", len(c.Messages))
	}
	a := c.Messages[1].Parts
	if len(a) != 3 || a[0].Kind != model.Thinking || a[1].Text != "答え" || a[2].ToolName != "view_file" || a[2].Text != `{"p":"a"}` {
		t.Fatalf("assistant: %+v", a)
	}
	if got := c.Messages[2].Parts[0].Text; got != "file:///a\n\n中身" {
		t.Fatalf("view_file: %q", got)
	}
	if got := c.Messages[3].Parts[0].Text; got != "$ ls\na.txt" {
		t.Fatalf("run_command: %q", got)
	}
}

func TestURIPath(t *testing.T) {
	for in, want := range map[string]string{
		"file:///home/a/b":                  "/home/a/b",
		"file:///c%3A/Users/a/My%20Project": `C:\Users\a\My Project`,
	} {
		if got := uriPath(in); got != want {
			t.Errorf("%s: %s", in, got)
		}
	}
}
