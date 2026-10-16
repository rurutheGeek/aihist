// Package opencode は OpenCode（1.18 系）の ~/.local/share/opencode/opencode.db を読む。
//
// session / message / part の3表。message と part の本体は列 data の JSON。
// 同じ DB にある account・credential などの表は読まない。
//
// 会話ごとに、この3表と session が指す project の行を加工せずに書き出したものを
// 原本（<session id>.json）とする。DB は操作ログを含めて数 GB になり、ログイン情報も
// 入っているので、DB そのものは送らない。原本の行を DB へ戻せば OpenCode で続けられる。
package opencode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/model"
	_ "modernc.org/sqlite"
)

const Tool = "opencode"

// Format は原本の形式。行の持ち方を変えたら上げる。
const Format = "opencode-rows/1"

// Row は1行（列名→値）。値は SQLite の text・integer・real・null をそのまま持つ。
type Row map[string]any

// Rows は1会話の原本。
type Rows struct {
	Format   string `json:"format"`
	Project  Row    `json:"project,omitempty"`
	Session  Row    `json:"session"`
	Messages []Row  `json:"messages"`
	Parts    []Row  `json:"parts"`
}

type DB struct{ db *sql.DB }

// Open は読み取り専用で開く。
func Open(path string) (*DB, error) {
	return open(path, "ro")
}

func open(path, mode string) (*DB, error) {
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode="+mode+"&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, err
	}
	return &DB{db: db}, nil
}

func (d *DB) Close() error { return d.db.Close() }

type Session struct {
	ID      string
	Updated int64
}

// Stamp は取り込みを省くかの判定に使う印。
func (s Session) Stamp() string { return fmt.Sprintf("%d", s.Updated) }

func (d *DB) Sessions() ([]Session, error) {
	rows, err := d.db.Query(`SELECT id, time_updated FROM session ORDER BY time_created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.Updated); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Export は1会話の原本を読む。
func (d *DB) Export(id string) (*Rows, error) {
	r := &Rows{Format: Format}
	ss, err := d.rows(`SELECT * FROM session WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	if len(ss) == 0 {
		return nil, fmt.Errorf("session %s が無い", id)
	}
	r.Session = ss[0]
	if pid, _ := r.Session["project_id"].(string); pid != "" {
		ps, err := d.rows(`SELECT * FROM project WHERE id=?`, pid)
		if err != nil {
			return nil, err
		}
		if len(ps) > 0 {
			r.Project = ps[0]
		}
	}
	if r.Messages, err = d.rows(`SELECT * FROM message WHERE session_id=? ORDER BY time_created, id`, id); err != nil {
		return nil, err
	}
	if r.Parts, err = d.rows(`SELECT * FROM part WHERE session_id=? ORDER BY message_id, id`, id); err != nil {
		return nil, err
	}
	return r, nil
}

func (d *DB) rows(q string, args ...any) ([]Row, error) {
	rs, err := d.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	cols, err := rs.Columns()
	if err != nil {
		return nil, err
	}
	var out []Row
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := Row{}
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				vals[i] = string(b)
			}
			row[c] = vals[i]
		}
		out = append(out, row)
	}
	return out, rs.Err()
}

// Load は DB から1会話を読む（同じマシンでの取り込み用）。
func (d *DB) Load(s Session) (*model.Conversation, error) {
	r, err := d.Export(s.ID)
	if err != nil {
		return nil, err
	}
	return Convert(r), nil
}

// Files は原本の置き場所にある会話の一覧。
func Files(dir string) ([]string, error) {
	return filepath.Glob(filepath.Join(dir, "*.json"))
}

// ReadFile は原本を読む。
func ReadFile(path string) (*Rows, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var r Rows
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, err
	}
	if r.Format != Format {
		return nil, fmt.Errorf("知らない形式: %q", r.Format)
	}
	return &r, nil
}

// StampOf は原本の印（session.time_updated）。
func (r *Rows) Stamp() string { return fmt.Sprintf("%d", num(r.Session["time_updated"])) }

func str(v any) string {
	s, _ := v.(string)
	return s
}

// num は JSON から読んだ数（float64）と DB から読んだ数（int64）のどちらも受ける。
func num(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

type msgData struct {
	Role     string `json:"role"`
	ParentID string `json:"parentID"`
	ModelID  string `json:"modelID"`
	Model    struct {
		ModelID string `json:"modelID"`
	} `json:"model"`
}

type partData struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Tool   string `json:"tool"`
	CallID string `json:"callID"`
	State  struct {
		Status string          `json:"status"`
		Input  json.RawMessage `json:"input"`
		Output string          `json:"output"`
		Error  string          `json:"error"`
	} `json:"state"`
	Filename string   `json:"filename"`
	URL      string   `json:"url"`
	Files    []string `json:"files"`
}

// Convert は原本を内部モデルにする。
func Convert(r *Rows) *model.Conversation {
	s := r.Session
	c := &model.Conversation{
		Tool: Tool, ExternalID: str(s["id"]), ParentID: str(s["parent_id"]), Title: str(s["title"]),
		Cwd: str(s["directory"]), StartedAt: ms(num(s["time_created"])), UpdatedAt: ms(num(s["time_updated"])),
	}
	var sm struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(str(s["model"])), &sm) == nil {
		c.Model = sm.ID
	}
	parts := map[string][]model.Part{}
	for _, p := range r.Parts {
		var d partData
		if json.Unmarshal([]byte(str(p["data"])), &d) != nil {
			continue
		}
		mid := str(p["message_id"])
		parts[mid] = append(parts[mid], convertPart(d)...)
	}
	prev := ""
	for _, m := range r.Messages {
		id := str(m["id"])
		var md msgData
		json.Unmarshal([]byte(str(m["data"])), &md)
		ps := parts[id]
		parent := md.ParentID
		if parent == "" {
			parent = prev
		}
		prev = id
		if len(ps) == 0 {
			continue
		}
		mod := md.ModelID
		if mod == "" {
			mod = md.Model.ModelID
		}
		c.Messages = append(c.Messages, model.Message{
			ExternalID: id, ParentID: parent, Role: md.Role, CreatedAt: ms(num(m["time_created"])), Model: mod, Parts: ps,
		})
	}
	return c
}

func convertPart(p partData) []model.Part {
	switch p.Type {
	case "text":
		if strings.TrimSpace(p.Text) == "" {
			return nil
		}
		return []model.Part{{Kind: model.Text, Text: p.Text}}
	case "reasoning":
		if strings.TrimSpace(p.Text) == "" {
			return nil
		}
		return []model.Part{{Kind: model.Thinking, Text: p.Text}}
	case "tool":
		out := []model.Part{{Kind: model.ToolCall, Text: string(p.State.Input), ToolName: p.Tool, ToolCallID: p.CallID}}
		switch p.State.Status {
		case "completed":
			out = append(out, model.Part{Kind: model.ToolResult, Text: p.State.Output, ToolName: p.Tool, ToolCallID: p.CallID})
		case "error":
			out = append(out, model.Part{Kind: model.ToolResult, Text: p.State.Error, ToolName: p.Tool, ToolCallID: p.CallID, IsError: true})
		}
		return out
	case "file":
		u := p.URL
		if strings.HasPrefix(u, "data:") { // 画像などの埋め込みは本文に入れない
			u = ""
		}
		return []model.Part{{Kind: model.File, Text: strings.TrimSpace(p.Filename + " " + u)}}
	case "patch":
		return []model.Part{{Kind: model.File, Text: "patch: " + strings.Join(p.Files, ", ")}}
	}
	return nil // step-start / step-finish / compaction は表示に要らない
}

func ms(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}
	return time.UnixMilli(v)
}
