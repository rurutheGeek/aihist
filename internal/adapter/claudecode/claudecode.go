// Package claudecode は Claude Code の ~/.claude/projects/<cwd>/<session>.jsonl を読む。
//
// 1行1イベント。assistant の応答は content ブロックごとに別の行へ分かれて書かれ、
// 同じ message.id を共有する。木構造は uuid / parentUuid。
package claudecode

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/model"
)

const Tool = "claude_code"

type line struct {
	Type        string          `json:"type"`
	UUID        string          `json:"uuid"`
	ParentUUID  string          `json:"parentUuid"`
	SessionID   string          `json:"sessionId"`
	Timestamp   time.Time       `json:"timestamp"`
	Cwd         string          `json:"cwd"`
	GitBranch   string          `json:"gitBranch"`
	IsSidechain bool            `json:"isSidechain"`
	IsMeta      bool            `json:"isMeta"`
	CustomTitle string          `json:"customTitle"`
	AgentName   string          `json:"agentName"`
	Summary     string          `json:"summary"`
	Message     json.RawMessage `json:"message"`
}

type apiMessage struct {
	ID      string          `json:"id"`
	Role    string          `json:"role"`
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
}

type block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

// Files は root 以下の会話ファイルを列挙する。サブエージェントの記録（subagents/）も含む。
func Files(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
			out = append(out, p)
		}
		return nil
	})
	return out, err
}

// Stamp は取り込みを省くかの判定に使う印。追記型なのでサイズと更新時刻で足りる。
func Stamp(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", st.Size(), st.ModTime().UnixNano())
}

// ExternalID はファイルから会話IDを決める。サブエージェントはファイル名（agent-…）を使う。
func ExternalID(path string) (id, parent string) {
	base := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	if filepath.Base(filepath.Dir(path)) == "subagents" {
		return base, filepath.Base(filepath.Dir(filepath.Dir(path)))
	}
	return base, ""
}

func Parse(path string) (*model.Conversation, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	id, parent := ExternalID(path)
	c := &model.Conversation{Tool: Tool, ExternalID: id, ParentID: parent, SourcePath: path}
	byAPIID := map[string]int{} // assistant の分割された行を1メッセージへまとめる

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var l line
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		switch l.Type {
		case "custom-title":
			c.Title = l.CustomTitle
			continue
		case "agent-name":
			if c.Title == "" {
				c.Title = l.AgentName
			}
			continue
		case "summary":
			if c.Title == "" {
				c.Title = l.Summary
			}
			continue
		case "user", "assistant":
		default:
			continue
		}
		if l.Cwd != "" {
			c.Cwd = l.Cwd
		}
		if l.GitBranch != "" {
			c.GitBranch = l.GitBranch
		}
		if c.StartedAt.IsZero() || (!l.Timestamp.IsZero() && l.Timestamp.Before(c.StartedAt)) {
			c.StartedAt = l.Timestamp
		}
		if l.Timestamp.After(c.UpdatedAt) {
			c.UpdatedAt = l.Timestamp
		}
		var m apiMessage
		if json.Unmarshal(l.Message, &m) != nil {
			continue
		}
		parts := parseContent(m.Content)
		if len(parts) == 0 {
			continue
		}
		role := l.Type
		if l.IsMeta {
			role = "system"
		} else if role == "user" && allToolResults(parts) {
			role = "tool"
		}
		if m.Model != "" && m.Model != "<synthetic>" {
			c.Model = m.Model
		}
		if l.Type == "assistant" && m.ID != "" {
			if i, ok := byAPIID[m.ID]; ok {
				c.Messages[i].Parts = append(c.Messages[i].Parts, parts...)
				continue
			}
			byAPIID[m.ID] = len(c.Messages)
		}
		c.Messages = append(c.Messages, model.Message{
			ExternalID: l.UUID, ParentID: l.ParentUUID, Role: role,
			CreatedAt: l.Timestamp, Model: m.Model, Sidechain: l.IsSidechain, Parts: parts,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if c.Title == "" {
		c.Title = firstUserText(c)
	}
	return c, nil
}

func parseContent(raw json.RawMessage) []model.Part {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if strings.TrimSpace(s) == "" {
			return nil
		}
		return []model.Part{{Kind: model.Text, Text: s}}
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	var out []model.Part
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if strings.TrimSpace(b.Text) != "" {
				out = append(out, model.Part{Kind: model.Text, Text: b.Text})
			}
		case "thinking":
			if b.Thinking != "" {
				out = append(out, model.Part{Kind: model.Thinking, Text: b.Thinking})
			}
		case "tool_use":
			out = append(out, model.Part{Kind: model.ToolCall, Text: string(b.Input), ToolName: b.Name, ToolCallID: b.ID})
		case "tool_result":
			out = append(out, model.Part{Kind: model.ToolResult, Text: resultText(b.Content), ToolCallID: b.ToolUseID, IsError: b.IsError})
		case "image":
			out = append(out, model.Part{Kind: model.Image, Text: "[image]"})
		}
	}
	return out
}

func resultText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []block
	if json.Unmarshal(raw, &blocks) != nil {
		return string(raw)
	}
	var sb strings.Builder
	for _, b := range blocks {
		switch b.Type {
		case "text":
			sb.WriteString(b.Text)
			sb.WriteString("\n")
		case "image":
			sb.WriteString("[image]\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

func allToolResults(ps []model.Part) bool {
	for _, p := range ps {
		if p.Kind != model.ToolResult {
			return false
		}
	}
	return true
}

func firstUserText(c *model.Conversation) string {
	for _, m := range c.Messages {
		if m.Role != "user" {
			continue
		}
		for _, p := range m.Parts {
			if p.Kind == model.Text {
				t := strings.Join(strings.Fields(p.Text), " ")
				if r := []rune(t); len(r) > 80 {
					t = string(r[:80]) + "…"
				}
				return t
			}
		}
	}
	return ""
}
