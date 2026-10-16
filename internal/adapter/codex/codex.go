// Package codex は Codex の ~/.codex/sessions/**/rollout-*.jsonl を読む。
//
// 1行1イベント。会話の本体は response_item（モデルとの入出力）で、user 役の
// response_item には注入された文脈が混ざるため、利用者の発言は event_msg の
// UserMessage から取る。長い会話は rollout-..._<n>.jsonl に分かれ、
// session_meta.id が同じものを1会話にまとめる。
package codex

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/model"
)

const Tool = "codex"

type line struct {
	Timestamp time.Time       `json:"timestamp"`
	Ordinal   int             `json:"ordinal"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type meta struct {
	ID             string `json:"id"`
	ParentThreadID string `json:"parent_thread_id"`
	Timestamp      time.Time
	Cwd            string `json:"cwd"`
	AgentNickname  string `json:"agent_nickname"`
	SubagentStart  int    `json:"subagent_history_start_ordinal"`
	Git            struct {
		Branch string `json:"branch"`
	} `json:"git"`
}

type payload struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Role      string          `json:"role"`
	Content   []content       `json:"content"`
	Name      string          `json:"name"`
	CallID    string          `json:"call_id"`
	Arguments string          `json:"arguments"`
	Input     string          `json:"input"`
	Output    json.RawMessage `json:"output"`
	Summary   []content       `json:"summary"`
	Author    string          `json:"author"`
	Model     string          `json:"model"`
	Item      *item           `json:"item"`
}

type content struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type item struct {
	Type    string    `json:"type"`
	ID      string    `json:"id"`
	Content []content `json:"content"`
}

// Session は1会話ぶんのファイル（分割されていれば複数）。
type Session struct {
	ID    string
	Files []string
}

// Stamp は取り込みを省くかの判定に使う印。
func (s Session) Stamp() string {
	var b strings.Builder
	for _, f := range s.Files {
		if st, err := os.Stat(f); err == nil {
			fmt.Fprintf(&b, "%d:%d;", st.Size(), st.ModTime().UnixNano())
		}
	}
	return b.String()
}

// Sessions は root 以下の会話を ID ごとにまとめて返す。
func Sessions(root string) ([]Session, error) {
	byID := map[string]*Session{}
	var order []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasPrefix(d.Name(), "rollout-") || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		m, err := readMeta(p)
		if err != nil || m.ID == "" {
			return nil
		}
		s := byID[m.ID]
		if s == nil {
			s = &Session{ID: m.ID}
			byID[m.ID] = s
			order = append(order, m.ID)
		}
		s.Files = append(s.Files, p)
		return nil
	})
	out := make([]Session, 0, len(order))
	for _, id := range order {
		s := byID[id]
		sort.Strings(s.Files) // 名前の先頭が開始時刻なので書かれた順になる
		out = append(out, *s)
	}
	return out, err
}

func readMeta(path string) (meta, error) {
	var m meta
	f, err := os.Open(path)
	if err != nil {
		return m, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Type != "session_meta" {
			continue
		}
		err := json.Unmarshal(l.Payload, &m)
		m.Timestamp = l.Timestamp
		return m, err
	}
	return m, sc.Err()
}

// Titles は session_index.jsonl から会話ID→題名を読む（無ければ空）。
func Titles(path string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.ID != "" && e.Name != "" {
			out[e.ID] = e.Name
		}
	}
	return out
}

// Load は1会話を読む。
func Load(s Session) (*model.Conversation, error) {
	c := &model.Conversation{Tool: Tool, ExternalID: s.ID}
	if len(s.Files) > 0 {
		c.SourcePath = s.Files[0]
	}
	var prev string
	add := func(m model.Message) {
		m.ParentID = prev
		prev = m.ExternalID
		c.Messages = append(c.Messages, m)
	}
	var cur *model.Message // 連続する assistant の出力をまとめる先
	assistant := func(ts time.Time, id string) *model.Message {
		if cur == nil {
			add(model.Message{ExternalID: id, Role: "assistant", CreatedAt: ts, Model: c.Model})
			cur = &c.Messages[len(c.Messages)-1]
		}
		return cur
	}
	for fi, path := range s.Files {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1<<20), 256<<20)
		skipBefore := 0
		for sc.Scan() {
			var l line
			if json.Unmarshal(sc.Bytes(), &l) != nil {
				continue
			}
			if l.Type == "session_meta" {
				var m meta
				if json.Unmarshal(l.Payload, &m) == nil && fi == 0 {
					c.Cwd, c.GitBranch, c.ParentID = m.Cwd, m.Git.Branch, m.ParentThreadID
					c.StartedAt = l.Timestamp
					if m.ParentThreadID != "" {
						c.Title = m.AgentNickname
					}
				}
				// サブエージェントの記録は親の履歴を写した後に始まる
				skipBefore = m.SubagentStart
				continue
			}
			if l.Ordinal < skipBefore {
				continue
			}
			if !l.Timestamp.IsZero() {
				c.UpdatedAt = l.Timestamp
			}
			var p payload
			if json.Unmarshal(l.Payload, &p) != nil {
				continue
			}
			id := p.ID
			if id == "" {
				id = fmt.Sprintf("%d:%d", fi, l.Ordinal)
			}
			switch {
			case l.Type == "turn_context":
				if p.Model != "" {
					c.Model = p.Model
				}
			case l.Type == "event_msg" && p.Type == "item_completed" && p.Item != nil && p.Item.Type == "UserMessage":
				cur = nil
				add(model.Message{ExternalID: p.Item.ID, Role: "user", CreatedAt: l.Timestamp, Parts: texts(p.Item.Content)})
			case l.Type != "response_item":
			case p.Type == "message" && p.Role == "assistant":
				m := assistant(l.Timestamp, id)
				m.Parts = append(m.Parts, texts(p.Content)...)
			case p.Type == "reasoning":
				if t := joinText(p.Summary); t != "" {
					m := assistant(l.Timestamp, id)
					m.Parts = append(m.Parts, model.Part{Kind: model.Thinking, Text: t})
				}
			case p.Type == "function_call" || p.Type == "custom_tool_call":
				args := p.Arguments
				if p.Type == "custom_tool_call" {
					args = p.Input
				}
				m := assistant(l.Timestamp, id)
				m.Parts = append(m.Parts, model.Part{Kind: model.ToolCall, Text: args, ToolName: p.Name, ToolCallID: p.CallID})
			case p.Type == "function_call_output" || p.Type == "custom_tool_call_output":
				cur = nil
				add(model.Message{ExternalID: id, Role: "tool", CreatedAt: l.Timestamp,
					Parts: []model.Part{{Kind: model.ToolResult, Text: output(p.Output), ToolCallID: p.CallID}}})
			case p.Type == "agent_message":
				// サブエージェントとの間の指示・報告
				cur = nil
				add(model.Message{ExternalID: id, Role: "system", CreatedAt: l.Timestamp,
					Parts: []model.Part{{Kind: model.Text, Text: p.Author + ": " + joinText(p.Content)}}})
			}
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return c, nil
}

func texts(cs []content) []model.Part {
	var out []model.Part
	for _, x := range cs {
		if strings.TrimSpace(x.Text) != "" {
			out = append(out, model.Part{Kind: model.Text, Text: x.Text})
		}
	}
	return out
}

func joinText(cs []content) string {
	var b []string
	for _, x := range cs {
		if x.Text != "" {
			b = append(b, x.Text)
		}
	}
	return strings.Join(b, "\n")
}

// output は文字列か {type,text} の配列。
func output(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var cs []content
	if json.Unmarshal(raw, &cs) == nil {
		return joinText(cs)
	}
	return string(raw)
}
