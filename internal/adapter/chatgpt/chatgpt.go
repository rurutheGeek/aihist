// Package chatgpt は ChatGPT の「データをエクスポート」の conversations.json を読む。
//
// 1会話は mapping（ノード ID→ノード）の木で、編集・再生成の分岐を含む。
// 書き出しを上げ直すと同じ会話が何度も現れるので、update_time の新しいものを使う。
package chatgpt

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/model"
)

const Tool = "chatgpt"

// parser は読み方の版。上げると取り込み済みの会話も読み直す。
const parser = "v1;"

type conv struct {
	ID          string          `json:"id"`
	ConvID      string          `json:"conversation_id"`
	Title       string          `json:"title"`
	CreateTime  float64         `json:"create_time"`
	UpdateTime  float64         `json:"update_time"`
	Mapping     map[string]node `json:"mapping"`
	CurrentNode string          `json:"current_node"`
	ModelSlug   string          `json:"default_model_slug"`
}

type node struct {
	ID       string   `json:"id"`
	Message  *message `json:"message"`
	Parent   string   `json:"parent"`
	Children []string `json:"children"`
}

type message struct {
	ID     string `json:"id"`
	Author struct {
		Role string `json:"role"`
		Name string `json:"name"`
	} `json:"author"`
	CreateTime float64 `json:"create_time"`
	Content    struct {
		ContentType string            `json:"content_type"`
		Parts       []json.RawMessage `json:"parts"`
		Text        string            `json:"text"`
		Language    string            `json:"language"`
		Thoughts    []struct {
			Summary string `json:"summary"`
			Content string `json:"content"`
		} `json:"thoughts"`
		Content string `json:"content"`
	} `json:"content"`
	Recipient string `json:"recipient"`
	Metadata  struct {
		ModelSlug string `json:"model_slug"`
		Hidden    bool   `json:"is_visually_hidden_from_conversation"`
	} `json:"metadata"`
}

// Conversation は書き出しの1会話と、取り込みを省くかの判定に使う印。
type Conversation struct {
	ID    string
	Stamp string
	Raw   json.RawMessage
}

// Is は conversations.json の先頭が ChatGPT の形か。
func Is(head []byte) bool { return strings.Contains(string(head), `"mapping"`) }

// Split は conversations.json を会話ごとに分ける。
func Split(data []byte) ([]Conversation, error) {
	var raws []json.RawMessage
	if err := json.Unmarshal(data, &raws); err != nil {
		return nil, err
	}
	var out []Conversation
	for _, r := range raws {
		var h struct {
			ID         string  `json:"id"`
			ConvID     string  `json:"conversation_id"`
			UpdateTime float64 `json:"update_time"`
		}
		if json.Unmarshal(r, &h) != nil {
			continue
		}
		id := h.ConvID
		if id == "" {
			id = h.ID
		}
		if id == "" {
			continue
		}
		out = append(out, Conversation{ID: id, Stamp: fmt.Sprintf("%s%f", parser, h.UpdateTime), Raw: r})
	}
	return out, nil
}

// Parse は1会話を読む。
func Parse(raw []byte) (*model.Conversation, error) {
	var v conv
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	c := &model.Conversation{Tool: Tool, ExternalID: v.ConvID, Title: v.Title, Model: v.ModelSlug,
		StartedAt: at(v.CreateTime), UpdatedAt: at(v.UpdateTime)}
	if c.ExternalID == "" {
		c.ExternalID = v.ID
	}
	// 根から子の順にたどる。表に出さないノードは飛ばし、子は最も近い表に出す祖先へつなぐ。
	var roots []string
	for id, n := range v.Mapping {
		if _, ok := v.Mapping[n.Parent]; n.Parent == "" || !ok {
			roots = append(roots, id)
		}
	}
	sort.Strings(roots)
	var walk func(id, parent string)
	walk = func(id, parent string) {
		n := v.Mapping[id]
		if m := convert(n.Message); m != nil {
			m.ParentID = parent
			if m.Model != "" {
				c.Model = m.Model
			}
			c.Messages = append(c.Messages, *m)
			parent = m.ExternalID
		}
		for _, ch := range n.Children {
			walk(ch, parent)
		}
	}
	for _, r := range roots {
		walk(r, "")
	}
	return c, nil
}

func convert(m *message) *model.Message {
	if m == nil || m.Metadata.Hidden {
		return nil
	}
	role := m.Author.Role
	if role == "" {
		role = "assistant"
	}
	out := &model.Message{ExternalID: m.ID, Role: role, CreatedAt: at(m.CreateTime), Model: m.Metadata.ModelSlug}
	if role != "assistant" {
		out.Model = ""
	}
	ct := m.Content
	add := func(k model.PartKind, s string) {
		if strings.TrimSpace(s) != "" {
			out.Parts = append(out.Parts, model.Part{Kind: k, Text: s, ToolName: m.Author.Name})
		}
	}
	switch ct.ContentType {
	case "text", "multimodal_text":
		for _, p := range ct.Parts {
			var s string
			if json.Unmarshal(p, &s) == nil {
				add(model.Text, s)
				continue
			}
			var o struct {
				ContentType  string `json:"content_type"`
				AssetPointer string `json:"asset_pointer"`
				Text         string `json:"text"`
			}
			if json.Unmarshal(p, &o) == nil {
				switch {
				case o.AssetPointer != "":
					out.Parts = append(out.Parts, model.Part{Kind: model.Image, Text: o.AssetPointer})
				case o.Text != "":
					add(model.Text, o.Text)
				}
			}
		}
	case "code":
		if role == "assistant" && m.Recipient != "" && m.Recipient != "all" {
			out.Parts = append(out.Parts, model.Part{Kind: model.ToolCall, Text: ct.Text, ToolName: m.Recipient})
		} else {
			add(model.Text, "```"+ct.Language+"\n"+ct.Text+"\n```")
		}
	case "thoughts":
		for _, t := range ct.Thoughts {
			add(model.Thinking, strings.TrimSpace(t.Summary+"\n"+t.Content))
		}
	case "reasoning_recap":
		add(model.Thinking, ct.Content)
	case "execution_output", "tether_browsing_display", "tether_quote", "system_error":
		k := model.ToolResult
		if role != "tool" {
			k = model.Text
		}
		add(k, ct.Text)
	}
	if role == "tool" {
		for i := range out.Parts {
			if out.Parts[i].Kind == model.Text {
				out.Parts[i].Kind = model.ToolResult
			}
		}
	}
	if len(out.Parts) == 0 {
		return nil
	}
	return out
}

func at(f float64) time.Time {
	if f == 0 {
		return time.Time{}
	}
	s, frac := math.Modf(f)
	return time.Unix(int64(s), int64(frac*1e9))
}
