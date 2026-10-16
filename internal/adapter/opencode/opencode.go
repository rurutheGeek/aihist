// Package opencode は OpenCode（1.18 系）の ~/.local/share/opencode/opencode.db を読み取り専用で読む。
//
// session / message / part の3表。message と part の本体は列 data の JSON。
// 同じ DB にある account・credential などの表は読まない。
package opencode

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/model"
	_ "modernc.org/sqlite"
)

const Tool = "opencode"

type DB struct{ db *sql.DB }

func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	return &DB{db: db}, nil
}

func (d *DB) Close() error { return d.db.Close() }

type Session struct {
	ID, ParentID, Directory, Title, ModelJSON string
	Created, Updated                          int64
}

// Stamp は取り込みを省くかの判定に使う印。
func (s Session) Stamp() string { return fmt.Sprintf("%d", s.Updated) }

func (d *DB) Sessions() ([]Session, error) {
	rows, err := d.db.Query(`SELECT id, ifnull(parent_id,''), directory, title, ifnull(model,''), time_created, time_updated FROM session ORDER BY time_created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(&s.ID, &s.ParentID, &s.Directory, &s.Title, &s.ModelJSON, &s.Created, &s.Updated); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
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

func (d *DB) Load(s Session) (*model.Conversation, error) {
	c := &model.Conversation{
		Tool: Tool, ExternalID: s.ID, ParentID: s.ParentID, Title: s.Title, Cwd: s.Directory,
		StartedAt: ms(s.Created), UpdatedAt: ms(s.Updated),
	}
	var sm struct {
		ID string `json:"id"`
	}
	if json.Unmarshal([]byte(s.ModelJSON), &sm) == nil {
		c.Model = sm.ID
	}

	parts := map[string][]model.Part{}
	prow, err := d.db.Query(`SELECT message_id, data FROM part WHERE session_id=? ORDER BY message_id, id`, s.ID)
	if err != nil {
		return nil, err
	}
	for prow.Next() {
		var mid, raw string
		if err := prow.Scan(&mid, &raw); err != nil {
			prow.Close()
			return nil, err
		}
		var p partData
		if json.Unmarshal([]byte(raw), &p) != nil {
			continue
		}
		parts[mid] = append(parts[mid], convertPart(p)...)
	}
	prow.Close()
	if err := prow.Err(); err != nil {
		return nil, err
	}

	mrow, err := d.db.Query(`SELECT id, time_created, data FROM message WHERE session_id=? ORDER BY time_created, id`, s.ID)
	if err != nil {
		return nil, err
	}
	defer mrow.Close()
	prev := ""
	for mrow.Next() {
		var id, raw string
		var at int64
		if err := mrow.Scan(&id, &at, &raw); err != nil {
			return nil, err
		}
		var md msgData
		json.Unmarshal([]byte(raw), &md)
		ps := parts[id]
		parent := md.ParentID
		if parent == "" {
			parent = prev
		}
		prev = id
		if len(ps) == 0 {
			continue
		}
		m := md.ModelID
		if m == "" {
			m = md.Model.ModelID
		}
		c.Messages = append(c.Messages, model.Message{
			ExternalID: id, ParentID: parent, Role: md.Role, CreatedAt: ms(at), Model: m, Parts: ps,
		})
	}
	return c, mrow.Err()
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
