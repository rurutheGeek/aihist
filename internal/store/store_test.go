package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/rurutheGeek/aihist/internal/model"
)

func conv(tool, host, id, cwd string, at time.Time, models ...string) *model.Conversation {
	c := &model.Conversation{Tool: tool, Host: host, ExternalID: id, Cwd: cwd, Title: id, UpdatedAt: at}
	for i, m := range models {
		c.Messages = append(c.Messages, model.Message{ExternalID: id + string(rune('a'+i)), Role: "assistant", Model: m,
			Parts: []model.Part{{Kind: model.Text, Text: "hello " + id}}})
	}
	return c
}

func TestBrowse(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Now()
	for _, c := range []*model.Conversation{
		conv("claude_code", "pc", "1", "/w/a", now, "opus", "sonnet", "<synthetic>"),
		conv("claude_code", "dev", "2", "/w/b", now.Add(-48*time.Hour), "sonnet"),
		conv("codex", "dev", "3", "/w/a", now.Add(-24*time.Hour), "gpt"),
	} {
		if err := st.Put(c, "s"); err != nil {
			t.Fatal(err)
		}
	}
	ids := func(rs []ConvRow) (out []string) {
		for _, r := range rs {
			out = append(out, r.ExternalID)
		}
		return
	}
	rows, facets, err := st.Browse(Filter{Models: []string{"sonnet"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(rows); len(got) != 2 || got[0] != "1" || got[1] != "2" {
		t.Fatalf("model: %v", got)
	}
	if len(rows[0].Models) != 2 {
		t.Fatalf("models: %v", rows[0].Models)
	}
	// モデルの件数はモデル以外の条件で数える
	if fs := facets[DimModel]; len(fs) != 3 {
		t.Fatalf("model facets: %v", fs)
	}
	if fs := facets[DimTool]; len(fs) != 1 || fs[0] != (Facet{"claude_code", 2}) {
		t.Fatalf("tool facets: %v", fs)
	}
	rows, _, _ = st.Browse(Filter{Projects: []string{"/w/a"}, Tools: []string{"codex", "claude_code"}, Since: now.Add(-30 * time.Hour)})
	if got := ids(rows); len(got) != 2 {
		t.Fatalf("project+since: %v", got)
	}
	rows, _, _ = st.Browse(Filter{Query: "hello 3", Hosts: []string{"dev"}})
	if got := ids(rows); len(got) != 1 || got[0] != "3" {
		t.Fatalf("query: %v", got)
	}
}
