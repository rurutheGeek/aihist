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

func TestTokens(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	p, err := st.CreateToken("pc")
	if err != nil {
		t.Fatal(err)
	}
	if !st.CheckToken(p) || st.CheckToken(p+"x") || st.CheckToken("") {
		t.Fatal("check")
	}
	ts, _ := st.Tokens()
	if len(ts) != 1 || ts[0].Name != "pc" || ts[0].UsedAt.IsZero() {
		t.Fatalf("%+v", ts)
	}
	st.DeleteToken(ts[0].ID)
	if st.CheckToken(p) {
		t.Fatal("deleted")
	}
}

func TestMachines(t *testing.T) {
	st, _ := Open(filepath.Join(t.TempDir(), "a.db"))
	defer st.Close()
	if m, ok := st.Machine("pc"); ok || m.Tools != "*" || !m.Attachments || m.Cleanup != -1 {
		t.Fatalf("%+v", m)
	}
	st.Report("pc", "v1", []Detected{{"codex", 2, 10}}, &Machine{Tools: "codex", Attachments: false, Cleanup: -1})
	st.Report("pc", "v2", []Detected{{"codex", 3, 20}}, &Machine{Tools: "*", Attachments: true})
	m, ok := st.Machine("pc")
	if !ok || m.Version != "v2" || m.Tools != "codex" || m.Attachments || len(m.Detected) != 1 || m.Detected[0].Files != 3 {
		t.Fatalf("%+v", m)
	}
	m.Schedule, m.Cleanup = "0 9 * * *", 7
	st.SetMachine(m)
	if m2, _ := st.Machine("pc"); m2.Schedule != "0 9 * * *" || m2.Cleanup != 7 || m2.Version != "v2" {
		t.Fatalf("%+v", m2)
	}
}

func TestMeta(t *testing.T) {
	st, _ := Open(filepath.Join(t.TempDir(), "a.db"))
	defer st.Close()
	now := time.Now()
	st.Put(conv("claude_code", "pc", "1", "/w", now, "m"), "s")
	st.Put(conv("claude_code", "pc", "2", "/w", now.Add(-time.Hour), "m"), "s")
	rows, _ := st.List(Filter{})
	id := rows[0].ID
	name, yes := "改名", true
	st.SetMeta(id, &name, &yes, nil)
	// 取り込み直しても残る
	st.Put(conv("claude_code", "pc", "1", "/w", now, "m"), "s2")
	rows, _ = st.List(Filter{})
	if rows[0].Title != "改名" || !rows[0].Pinned {
		t.Fatalf("%+v", rows[0])
	}
	st.SetMeta(rows[0].ID, nil, nil, &yes)
	if rows, _ = st.List(Filter{}); len(rows) != 1 || rows[0].ExternalID != "2" {
		t.Fatalf("active: %+v", rows)
	}
	if rows, _ = st.List(Filter{Status: "archived"}); len(rows) != 1 || rows[0].ExternalID != "1" {
		t.Fatalf("archived: %+v", rows)
	}
	if rows, _ = st.List(Filter{Status: "all"}); len(rows) != 2 {
		t.Fatalf("all: %+v", rows)
	}
}

func TestDeleteKeepsMark(t *testing.T) {
	st, _ := Open(filepath.Join(t.TempDir(), "a.db"))
	c := &model.Conversation{Tool: "codex", Host: "h", ExternalID: "x1", Title: "a", Messages: []model.Message{{Role: "user", Parts: []model.Part{{Kind: model.Text, Text: "hi"}}}}}
	if err := st.Put(c, "s1"); err != nil {
		t.Fatal(err)
	}
	id, _ := st.Lookup("codex", "h", "x1")
	if n, err := st.Delete(id); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	st.Put(c, "s2")
	if id, _ := st.Lookup("codex", "h", "x1"); id != 0 {
		t.Fatal("came back")
	}
	if st.Deleted() != 1 {
		t.Fatal("mark")
	}
	st.ForgetDeleted()
	st.Put(c, "s3")
	if id, _ := st.Lookup("codex", "h", "x1"); id == 0 {
		t.Fatal("not reimported")
	}
}

// 続きが足されて取り込み直しても、会話の番号は変わらず、中身と全文検索は新しくなる。
func TestPutKeepsID(t *testing.T) {
	st, _ := Open(filepath.Join(t.TempDir(), "a.db"))
	c := &model.Conversation{Tool: "codex", Host: "h", ExternalID: "x1", Title: "a", Messages: []model.Message{{Role: "user", Parts: []model.Part{{Kind: model.Text, Text: "最初の発言"}}}}}
	st.Put(c, "s1")
	id1, _ := st.Lookup("codex", "h", "x1")
	c.Messages = append(c.Messages, model.Message{Role: "assistant", Parts: []model.Part{{Kind: model.Text, Text: "続きの返事"}}})
	if err := st.Put(c, "s2"); err != nil {
		t.Fatal(err)
	}
	id2, _ := st.Lookup("codex", "h", "x1")
	if id1 == 0 || id1 != id2 {
		t.Fatalf("番号が変わった: %d → %d", id1, id2)
	}
	_, msgs, err := st.Get(id2)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("中身: %d %v", len(msgs), err)
	}
	for q, want := range map[string]int{"続きの返事": 1, "最初の発言": 1} {
		rows, _, _ := st.Browse(Filter{Query: q, Status: "all", Limit: 10})
		if len(rows) != want {
			t.Errorf("%s: %d 件", q, len(rows))
		}
	}
}

// 見直していない記事だけが出て、外すと消え、残すと見直し済みになる。人が直した記事は消さない。
func TestReview(t *testing.T) {
	st, _ := Open(filepath.Join(t.TempDir(), "a.db"))
	a, _ := st.AddKnowledge(Knowledge{Title: "a", Body: "x"})
	b, _ := st.AddKnowledge(Knowledge{Title: "b", Body: "x"})
	c, _ := st.AddKnowledge(Knowledge{Title: "c", Body: "x", Status: "registered"})
	if ks, _ := st.Unreviewed(); len(ks) != 2 {
		t.Fatalf("見直す記事: %d", len(ks))
	}
	st.Review(a, true)
	st.Review(b, false)
	st.Review(c, false)
	if ks, _ := st.Unreviewed(); len(ks) != 0 {
		t.Errorf("残った: %+v", ks)
	}
	if _, err := st.GetKnowledge(a); err != nil {
		t.Error("残した記事が無い")
	}
	if _, err := st.GetKnowledge(b); err == nil {
		t.Error("外した記事が残った")
	}
	if _, err := st.GetKnowledge(c); err != nil {
		t.Error("人が直した記事を消した")
	}
}

// 自動実行の会話のサブエージェントは、親と子のどちらが先に来ても自動実行になる。
func TestAutoChildren(t *testing.T) {
	st, _ := Open(filepath.Join(t.TempDir(), "a.db"))
	msg := []model.Message{{Role: "user", Parts: []model.Part{{Kind: model.Text, Text: "x"}}}}
	st.Put(&model.Conversation{Tool: "opencode", Host: "h", ExternalID: "c1", ParentID: "p", Messages: msg}, "s")
	st.Put(&model.Conversation{Tool: "opencode", Host: "h", ExternalID: "p", Auto: true, Messages: msg}, "s")
	st.Put(&model.Conversation{Tool: "opencode", Host: "h", ExternalID: "c2", ParentID: "p", Messages: msg}, "s")
	for _, e := range []string{"c1", "c2"} {
		id, _ := st.Lookup("opencode", "h", e)
		r, _, _ := st.Get(id)
		if !r.Auto {
			t.Errorf("%s が自動実行になっていない", e)
		}
	}
}
