package claudecode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rurutheGeek/aihist/internal/model"
)

const sample = `{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"s1","timestamp":"2026-10-01T00:00:00Z","cwd":"/w","gitBranch":"main","message":{"role":"user","content":"こんにちは"}}
{"type":"assistant","uuid":"a1","parentUuid":"u1","sessionId":"s1","timestamp":"2026-10-01T00:00:01Z","message":{"id":"msg_1","role":"assistant","model":"claude-x","content":[{"type":"thinking","thinking":"考える"}]}}
{"type":"assistant","uuid":"a2","parentUuid":"a1","sessionId":"s1","timestamp":"2026-10-01T00:00:02Z","message":{"id":"msg_1","role":"assistant","model":"claude-x","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"ls"}}]}}
{"type":"user","uuid":"r1","parentUuid":"a2","sessionId":"s1","timestamp":"2026-10-01T00:00:03Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":[{"type":"text","text":"a.txt"}]}]}}
{"type":"custom-title","customTitle":"題名","sessionId":"s1"}
not json
`

func TestParse(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s1.jsonl")
	if err := os.WriteFile(p, []byte(sample), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Parse(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Title != "題名" || c.Cwd != "/w" || c.GitBranch != "main" || c.Model != "claude-x" {
		t.Fatalf("meta: %+v", c)
	}
	if len(c.Messages) != 3 {
		t.Fatalf("messages = %d, want 3 (assistant の分割行はまとめる)", len(c.Messages))
	}
	a := c.Messages[1]
	if len(a.Parts) != 2 || a.Parts[0].Kind != model.Thinking || a.Parts[1].Kind != model.ToolCall || a.Parts[1].Text != `{"command":"ls"}` {
		t.Fatalf("assistant parts: %+v", a.Parts)
	}
	r := c.Messages[2]
	if r.Role != "tool" || r.Parts[0].Text != "a.txt" || r.Parts[0].ToolCallID != "t1" {
		t.Fatalf("tool result: %+v", r)
	}
}

func TestExternalIDSubagent(t *testing.T) {
	id, parent := ExternalID("/p/-home-x/sess-1/subagents/agent-abc.jsonl")
	if id != "agent-abc" || parent != "sess-1" {
		t.Fatalf("got %s %s", id, parent)
	}
}
