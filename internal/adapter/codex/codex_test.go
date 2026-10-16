package codex

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rurutheGeek/aihist/internal/model"
)

const page1 = `{"timestamp":"2026-09-12T00:00:00Z","ordinal":0,"type":"session_meta","payload":{"id":"s1","cwd":"/w","git":{"branch":"main"}}}
{"timestamp":"2026-09-12T00:00:01Z","ordinal":1,"type":"turn_context","payload":{"model":"gpt-x"}}
{"timestamp":"2026-09-12T00:00:01Z","ordinal":2,"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<注入された文脈>"}]}}
{"timestamp":"2026-09-12T00:00:01Z","ordinal":3,"type":"event_msg","payload":{"type":"item_completed","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"こんにちは"}]}}}
{"timestamp":"2026-09-12T00:00:02Z","ordinal":4,"type":"response_item","payload":{"type":"reasoning","id":"r1","summary":[{"type":"summary_text","text":"考える"}]}}
{"timestamp":"2026-09-12T00:00:02Z","ordinal":5,"type":"response_item","payload":{"type":"function_call","id":"f1","name":"shell","arguments":"{\"cmd\":\"ls\"}","call_id":"c1"}}
`

const page2 = `{"timestamp":"2026-09-12T00:01:00Z","ordinal":0,"type":"session_meta","payload":{"id":"s1","cwd":"/w"}}
{"timestamp":"2026-09-12T00:01:01Z","ordinal":6,"type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":[{"type":"input_text","text":"a.txt"}]}}
{"timestamp":"2026-09-12T00:01:02Z","ordinal":7,"type":"response_item","payload":{"type":"message","id":"m1","role":"assistant","content":[{"type":"output_text","text":"終わり"}]}}
`

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	write := func(name, s string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("rollout-2026-09-12T00-00-00-s1.jsonl", page1)
	write("rollout-2026-09-12T00-01-00-s1_x.jsonl", page2)
	ss, err := Sessions(dir)
	if err != nil || len(ss) != 1 || len(ss[0].Files) != 2 {
		t.Fatalf("sessions: %+v %v", ss, err)
	}
	c, err := Load(ss[0])
	if err != nil {
		t.Fatal(err)
	}
	if c.Cwd != "/w" || c.GitBranch != "main" || c.Model != "gpt-x" {
		t.Fatalf("meta: %+v", c)
	}
	if len(c.Messages) != 4 {
		t.Fatalf("messages = %d: %+v", len(c.Messages), c.Messages)
	}
	if c.Messages[0].Role != "user" || c.Messages[0].Parts[0].Text != "こんにちは" {
		t.Fatalf("user: %+v", c.Messages[0])
	}
	a := c.Messages[1].Parts
	if len(a) != 2 || a[0].Kind != model.Thinking || a[1].Kind != model.ToolCall || a[1].ToolCallID != "c1" {
		t.Fatalf("assistant: %+v", a)
	}
	if r := c.Messages[2]; r.Role != "tool" || r.Parts[0].Text != "a.txt" {
		t.Fatalf("tool: %+v", r)
	}
	if c.Messages[3].ParentID != c.Messages[2].ExternalID {
		t.Fatal("parent chain")
	}
}
