package opencode

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/rurutheGeek/aihist/internal/model"
)

const schema = `
CREATE TABLE project (id text PRIMARY KEY, worktree text NOT NULL, time_created integer NOT NULL, time_updated integer NOT NULL, sandboxes text NOT NULL);
CREATE TABLE session (id text PRIMARY KEY, project_id text NOT NULL, parent_id text, directory text NOT NULL, title text NOT NULL, model text, time_created integer NOT NULL, time_updated integer NOT NULL);
CREATE TABLE message (id text PRIMARY KEY, session_id text NOT NULL, time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL,
  FOREIGN KEY (session_id) REFERENCES session(id) ON DELETE CASCADE);
CREATE TABLE part (id text PRIMARY KEY, message_id text NOT NULL, session_id text NOT NULL, time_created integer NOT NULL, time_updated integer NOT NULL, data text NOT NULL,
  FOREIGN KEY (message_id) REFERENCES message(id) ON DELETE CASCADE);
CREATE TABLE credential (id text PRIMARY KEY, value text NOT NULL);
`

func newDB(t *testing.T, seed bool) string {
	p := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", p+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if seed {
		for _, q := range []string{
			`INSERT INTO project VALUES ('p1','/w',1,1,'[]')`,
			`INSERT INTO session VALUES ('s1','p1',NULL,'/w','題名','{"id":"m-x"}',1000,2000)`,
			`INSERT INTO message VALUES ('m1','s1',1000,1000,'{"role":"user"}')`,
			`INSERT INTO message VALUES ('m2','s1',1500,1500,'{"role":"assistant","modelID":"m-x"}')`,
			`INSERT INTO part VALUES ('a','m1','s1',1000,1000,'{"type":"text","text":"こんにちは"}')`,
			`INSERT INTO part VALUES ('b','m2','s1',1500,1500,'{"type":"tool","tool":"bash","callID":"c1","state":{"status":"completed","input":{"cmd":"ls"},"output":"a.txt"}}')`,
			`INSERT INTO credential VALUES ('k','秘密')`,
		} {
			if _, err := db.Exec(q); err != nil {
				t.Fatal(q, err)
			}
		}
	}
	return p
}

func TestExportConvertRestore(t *testing.T) {
	src, err := Open(newDB(t, true))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	rows, err := src.Export("s1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(rows)
	var streamed bytes.Buffer // 1行ずつ書いたものも同じ原本として読める
	if err := src.ExportTo(&streamed, "s1"); err != nil {
		t.Fatal(err)
	}
	var fromStream Rows
	if err := json.Unmarshal(streamed.Bytes(), &fromStream); err != nil {
		t.Fatal(err)
	}
	if b2, _ := json.Marshal(fromStream); string(b2) != string(b) {
		t.Fatalf("ExportTo が Export と違う:\n%s\n%s", b, b2)
	}
	if string(b) == "" || containsSecret(b) {
		t.Fatal("原本に credential が入った")
	}
	var back Rows // 送って受け取ったのと同じ経路（JSON）を通す
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}

	c := Convert(&back)
	if c.Title != "題名" || c.Model != "m-x" || c.Cwd != "/w" || len(c.Messages) != 2 || back.Stamp() != "2000" {
		t.Fatalf("convert: %+v", c)
	}
	if p := c.Messages[1].Parts; len(p) != 2 || p[0].Kind != model.ToolCall || p[1].Text != "a.txt" {
		t.Fatalf("tool: %+v", p)
	}

	// 空の DB へ戻すと、元と同じ行が読める。2回戻しても子の行は消えない。
	dst := newDB(t, false)
	for i := 0; i < 2; i++ {
		if err := Restore(dst, &back); err != nil {
			t.Fatal(err)
		}
	}
	d, _ := Open(dst)
	defer d.Close()
	again, err := d.Export("s1")
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := json.Marshal(again)
	if string(b) != string(b2) {
		t.Fatalf("戻した行が違う:\n%s\n%s", b, b2)
	}
}

func containsSecret(b []byte) bool {
	for i := 0; i+len("秘密") <= len(b); i++ {
		if string(b[i:i+len("秘密")]) == "秘密" {
			return true
		}
	}
	return false
}
