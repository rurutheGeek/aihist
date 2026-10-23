package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rurutheGeek/aihist/internal/model"
	"github.com/rurutheGeek/aihist/internal/raw"
	"github.com/rurutheGeek/aihist/internal/store"
)

// 整理は設定の数まで同時に進め、全部の会話に要約が付き、残りの記録は空になる。
func TestOrganizeParallel(t *testing.T) {
	var now, peak atomic.Int32
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := now.Add(1)
		defer now.Add(-1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		reply := `{"tags": [], "summary": "要約", "knowledge": []}`
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": reply}}}})
	}))
	defer ai.Close()
	st, err := store.Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := range 7 {
		c := &model.Conversation{Tool: "codex", Host: "h", ExternalID: fmt.Sprint("c", i), Title: "t",
			Messages: []model.Message{{Role: "user", Parts: []model.Part{{Kind: model.Text, Text: "こんにちは"}}}}}
		st.Put(c, "s")
		id, _ := st.Lookup("codex", "h", c.ExternalID)
		ids = append(ids, id)
	}
	st.SetSetting("ai_base_url", ai.URL)
	st.SetSetting("ai_model", "m")
	st.SetSetting("ai_parallel", "3")
	s := New(st, raw.Store{Dir: t.TempDir()}, "")
	job := s.startOrganize(ids, s.aiConfig(), "ja", false)
	for i := 0; ; i++ {
		jobs.Lock()
		state := jobs.m[job].State
		jobs.Unlock()
		if state == "done" || state == "error" {
			break
		}
		if i > 200 {
			t.Fatal("終わらない")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if p := peak.Load(); p < 2 || p > 3 {
		t.Errorf("同時に動いた数: %d", p)
	}
	for _, id := range ids {
		if !st.Organized(id, "m") {
			t.Errorf("%d が整理されていない", id)
		}
	}
	if q := st.Setting("ai_queue", ""); q != "" {
		t.Errorf("残りの記録が残った: %s", q)
	}
}
