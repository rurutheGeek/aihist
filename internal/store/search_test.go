package store

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rurutheGeek/aihist/internal/model"
)

func textConv(id string, texts ...string) *model.Conversation {
	c := &model.Conversation{Tool: "codex", Host: "h", ExternalID: id, Title: "t" + id, UpdatedAt: time.Now()}
	for i, t := range texts {
		c.Messages = append(c.Messages, model.Message{ExternalID: id + string(rune('a'+i)), Role: "user", Parts: []model.Part{{Kind: model.Text, Text: t}}})
	}
	return c
}

func found(t *testing.T, st *Store, q string) (ids []string, snip map[string]string) {
	t.Helper()
	rows, _, err := st.Browse(context.Background(), Filter{Query: q, Status: "all", All: true})
	if err != nil {
		t.Fatal(err)
	}
	snip = map[string]string{}
	for _, r := range rows {
		ids = append(ids, r.ExternalID)
		snip[r.ExternalID] = r.Snippet
	}
	slices.Sort(ids)
	return ids, snip
}

func TestSearch(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, c := range []*model.Conversation{
		textConv("1", "検索の設定を見直す", "Docker Compose で動かす"),
		textConv("2", "設計の話", "設"),
		textConv("3", "docker\ncompose は別々", "compose docker"),
	} {
		if err := st.Put(c, "s"); err != nil {
			t.Fatal(err)
		}
	}
	for q, want := range map[string][]string{
		"設":              {"1", "2"},
		"設定":             {"1"},
		"設計の":            {"2"},
		"docker compose": {"1", "3"}, // 改行も空白とみなす
		"DOCKER":         {"1", "3"},
		"compose docker": {"3"},
		"の設定を見":          {"1"},
		"無い言葉":           nil,
	} {
		got, _ := found(t, st, q)
		if !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v", q, got, want)
		}
	}
	_, sn := found(t, st, "compose")
	if !strings.Contains(sn["1"], "\x02Compose\x03") || !strings.Contains(sn["1"], "Docker ") {
		t.Errorf("抜粋: %q", sn["1"])
	}
	// 書き換え・削除は索引にも入る
	if _, err := st.Rewrite(func(s string) string { return strings.ReplaceAll(s, "設計", "企画") }, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := found(t, st, "企画"); !slices.Equal(got, []string{"2"}) {
		t.Errorf("書き換えたあと: %v", got)
	}
	if got, _ := found(t, st, "設計"); got != nil {
		t.Errorf("書き換える前の語が残る: %v", got)
	}
	id, _ := st.Lookup("codex", "h", "1")
	if _, err := st.Delete(id); err != nil {
		t.Fatal(err)
	}
	if got, _ := found(t, st, "設定"); got != nil {
		t.Errorf("消したあと: %v", got)
	}
}

// 前の版の索引（3文字ずつ）を持つ DB を、新しい索引に移す。
func TestBuildSearch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Put(textConv("1", "古い索引の会話"), "s"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DELETE FROM parts_search`, `DELETE FROM settings WHERE key='search_index'`,
		`CREATE VIRTUAL TABLE parts_fts USING fts5(text, content='parts', content_rowid='id', tokenize='trigram')`,
		`INSERT INTO parts_fts(parts_fts) VALUES ('rebuild')`,
	} {
		if _, err := st.db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	st.Close()
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// 作る前も（索引を使わずに）見つかる
	if got, _ := found(t, st, "索引"); !slices.Equal(got, []string{"1"}) {
		t.Fatalf("作る前: %v", got)
	}
	if err := st.Put(textConv("2", "作っている間に来た索引"), "s"); err != nil {
		t.Fatal(err)
	}
	if err := st.BuildSearch(context.Background(), func(string, ...any) {}); err != nil {
		t.Fatal(err)
	}
	if !st.searchReady() {
		t.Fatal("出来上がらない")
	}
	var n int
	st.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name LIKE 'parts_fts%'`).Scan(&n)
	if n != 0 {
		t.Errorf("前の索引が残る: %d", n)
	}
	if got, _ := found(t, st, "索引"); !slices.Equal(got, []string{"1", "2"}) {
		t.Errorf("作ったあと: %v", got)
	}
}

func TestSearchCancel(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.Put(textConv("1", "hello"), "s")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := st.Browse(ctx, Filter{Query: "hello"}); err == nil {
		t.Error("取り消した検索が終わらない")
	}
}
