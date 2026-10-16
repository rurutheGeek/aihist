// Package claudeai は claude.ai の書き出しの conversations.json を読む。
//
// 1会話は chat_messages の並び。parent_message_uuid があれば分岐の木になる。
package claudeai

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/model"
)

const Tool = "claude_ai"

// parser は読み方の版。上げると取り込み済みの会話も読み直す。
const parser = "v1;"

type conv struct {
	UUID      string    `json:"uuid"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Model     string    `json:"model"`
	Messages  []message `json:"chat_messages"`
}

type message struct {
	UUID      string    `json:"uuid"`
	Parent    string    `json:"parent_message_uuid"`
	Sender    string    `json:"sender"`
	Text      string    `json:"text"`
	Content   []content `json:"content"`
	CreatedAt time.Time `json:"created_at"`
	Attach    []struct {
		FileName  string `json:"file_name"`
		Extracted string `json:"extracted_content"`
	} `json:"attachments"`
	Files []struct {
		FileName string `json:"file_name"`
	} `json:"files"`
}

type content struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	Thinking string          `json:"thinking"`
	Name     string          `json:"name"`
	ID       string          `json:"id"`
	Input    json.RawMessage `json:"input"`
	Content  json.RawMessage `json:"content"`
	IsError  bool            `json:"is_error"`
}

// Conversation は書き出しの1会話と、取り込みを省くかの判定に使う印。
type Conversation struct {
	ID    string
	Stamp string
	Raw   json.RawMessage
}

// Is は conversations.json の先頭が claude.ai の形か。
func Is(head []byte) bool { return strings.Contains(string(head), `"chat_messages"`) }

// Split は conversations.json を会話ごとに分ける。
func Split(data []byte) ([]Conversation, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, err
	}
	var out []Conversation
	for _, r := range raws {
		var h struct {
			UUID      string `json:"uuid"`
			UpdatedAt string `json:"updated_at"`
			N         []struct {
				UUID string `json:"uuid"`
			} `json:"chat_messages"`
		}
		if json.Unmarshal(r, &h) != nil || h.UUID == "" {
			continue
		}
		stamp := parser + h.UpdatedAt
		if len(h.N) > 0 {
			stamp += ";" + h.N[len(h.N)-1].UUID
		}
		out = append(out, Conversation{ID: h.UUID, Stamp: stamp, Raw: r})
	}
	return out, nil
}

// Parse は1会話を読む。
func Parse(raw []byte) (*model.Conversation, error) {
	var v conv
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	c := &model.Conversation{Tool: Tool, ExternalID: v.UUID, Title: v.Name, Model: v.Model,
		StartedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
	seen := map[string]bool{}
	prev := ""
	for _, m := range v.Messages {
		role := "assistant"
		if m.Sender == "human" {
			role = "user"
		}
		parent := prev
		if m.Parent != "" && seen[m.Parent] {
			parent = m.Parent
		}
		var parts []model.Part
		for _, x := range m.Content {
			switch x.Type {
			case "text":
				parts = appendText(parts, model.Text, x.Text)
			case "thinking":
				parts = appendText(parts, model.Thinking, x.Thinking)
			case "tool_use":
				parts = append(parts, model.Part{Kind: model.ToolCall, Text: string(x.Input), ToolName: x.Name, ToolCallID: x.ID})
			case "tool_result":
				parts = append(parts, model.Part{Kind: model.ToolResult, Text: resultText(x.Content), ToolName: x.Name, IsError: x.IsError})
			}
		}
		if len(m.Content) == 0 {
			parts = appendText(parts, model.Text, m.Text)
		}
		for _, a := range m.Attach {
			parts = append(parts, model.Part{Kind: model.File, Text: a.FileName + "\n" + a.Extracted})
		}
		for _, f := range m.Files {
			parts = append(parts, model.Part{Kind: model.File, Text: f.FileName})
		}
		if len(parts) == 0 {
			continue
		}
		c.Messages = append(c.Messages, model.Message{ExternalID: m.UUID, ParentID: parent, Role: role, CreatedAt: m.CreatedAt, Parts: parts})
		seen[m.UUID] = true
		prev = m.UUID
	}
	return c, nil
}

func appendText(ps []model.Part, k model.PartKind, s string) []model.Part {
	if strings.TrimSpace(s) == "" {
		return ps
	}
	return append(ps, model.Part{Kind: k, Text: s})
}

// resultText は文字列か {type,text} の配列。
func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var cs []content
	if json.Unmarshal(raw, &cs) == nil {
		var b []string
		for _, x := range cs {
			if x.Text != "" {
				b = append(b, x.Text)
			}
		}
		return strings.Join(b, "\n")
	}
	return string(raw)
}
