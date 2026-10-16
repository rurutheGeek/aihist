// Package antigravity は Antigravity の %USERPROFILE%\.gemini\antigravity-ide\conversations\*.db を読む。
//
// 会話ごとに SQLite が1つ。steps の1行が1ステップで、step_payload は protobuf。
// .proto は公開されていないので、フィールド番号は実物から割り出した（README）。
package antigravity

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	_ "modernc.org/sqlite"

	"github.com/rurutheGeek/aihist/internal/model"
)

const Tool = "antigravity"

// Files は dir 直下の会話 DB を返す。
func Files(dir string) ([]string, error) {
	return filepath.Glob(filepath.Join(dir, "*.db"))
}

// ExternalID はファイル名（会話 ID）。
func ExternalID(path string) string {
	return strings.TrimSuffix(filepath.Base(path), ".db")
}

// Stamp は取り込みを省くかの判定に使う印。WAL も見る。
func Stamp(path string) string {
	var b strings.Builder
	for _, p := range []string{path, path + "-wal"} {
		if st, err := os.Stat(p); err == nil {
			fmt.Fprintf(&b, "%d:%d;", st.Size(), st.ModTime().UnixNano())
		}
	}
	return b.String()
}

// ステップの種類（step_type）と本体のフィールド番号。
var tools = map[uint64]struct {
	name  string
	field uint64
}{
	5:  {"write_to_file", 10},
	7:  {"grep_search", 13},
	8:  {"view_file", 14},
	9:  {"list_dir", 15},
	17: {"error", 24},
	21: {"run_command", 28},
}

// Parse は1会話を読む。
func Parse(path string) (*model.Conversation, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`SELECT idx, step_type, step_payload FROM steps ORDER BY idx`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	c := &model.Conversation{Tool: Tool, ExternalID: ExternalID(path), SourcePath: path}
	var prev string
	var cur *model.Message
	add := func(m model.Message) *model.Message {
		m.ParentID = prev
		prev = m.ExternalID
		c.Messages = append(c.Messages, m)
		return &c.Messages[len(c.Messages)-1]
	}
	for rows.Next() {
		var idx int64
		var typ uint64
		var payload []byte
		if err := rows.Scan(&idx, &typ, &payload); err != nil {
			return nil, err
		}
		p, err := parse(payload)
		if err != nil {
			continue
		}
		ts := time.Unix(int64(p.int(5, 1, 1)), int64(p.int(5, 1, 2))).UTC()
		if p.int(5, 1, 1) == 0 {
			ts = time.Time{}
		}
		if !ts.IsZero() {
			if c.StartedAt.IsZero() {
				c.StartedAt = ts
			}
			c.UpdatedAt = ts
		}
		id := fmt.Sprint(idx)
		switch typ {
		case 14:
			cur = nil
			add(model.Message{ExternalID: id, Role: "user", CreatedAt: ts,
				Parts: []model.Part{{Kind: model.Text, Text: p.str(19, 2)}}})
		case 15:
			r := p.msg(20)
			if cur == nil {
				cur = add(model.Message{ExternalID: id, Role: "assistant", CreatedAt: ts})
			}
			if t := r.str(3); t != "" {
				cur.Parts = append(cur.Parts, model.Part{Kind: model.Thinking, Text: t})
			}
			if t := r.str(1); t != "" {
				cur.Parts = append(cur.Parts, model.Part{Kind: model.Text, Text: t})
			}
			for _, tc := range r.all(7) {
				cur.Parts = append(cur.Parts, model.Part{Kind: model.ToolCall, ToolCallID: tc.str(1), ToolName: tc.str(2), Text: tc.str(3)})
			}
		case 23, 98:
			f := uint64(30)
			if typ == 98 {
				f = 111
			}
			if t := longest(p.msg(f)); t != "" {
				cur = nil
				add(model.Message{ExternalID: id, Role: "system", CreatedAt: ts,
					Parts: []model.Part{{Kind: model.Text, Text: t}}})
			}
		default:
			t, ok := tools[typ]
			if !ok {
				continue
			}
			b := p.msg(t.field)
			var text string
			switch typ {
			case 8:
				text = b.str(1) + "\n\n" + b.str(4)
			case 21:
				text = "$ " + b.str(23) + "\n" + b.str(21, 1)
			default:
				text = longest(b)
			}
			cur = nil
			add(model.Message{ExternalID: id, Role: "tool", CreatedAt: ts,
				Parts: []model.Part{{Kind: model.ToolResult, ToolName: t.name, Text: text, IsError: typ == 17}}})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, m := range c.Messages {
		if m.Role == "user" && len(m.Parts) > 0 {
			c.Title = firstLine(m.Parts[0].Text)
			break
		}
	}
	return c, nil
}

// longest は構造の分からない本体から、いちばん長い文字列を拾う。
func longest(m msg) string {
	var best string
	var walk func(m msg, depth int)
	walk = func(m msg, depth int) {
		for _, vs := range m {
			for _, v := range vs {
				b, ok := v.([]byte)
				if !ok {
					continue
				}
				if p, err := parse(b); err == nil && depth < 6 && len(p) > 0 && !printable(b) {
					walk(p, depth+1)
				} else if len(b) > len(best) && printable(b) {
					best = string(b)
				}
			}
		}
	}
	walk(m, 0)
	return best
}

func printable(b []byte) bool {
	if len(b) == 0 || !utf8.Valid(b) {
		return false
	}
	for _, r := range string(b) {
		if r < 0x20 && r != '\n' && r != '\r' && r != '\t' {
			return false
		}
	}
	return true
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80])
	}
	return s
}
