package web

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/store"
)

// setMeta は会話のピン留め・名前・アーカイブを変える（行の ⋮ メニューから）。
func (s *Server) setMeta(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	r.ParseForm()
	var title *string
	var pinned, archived *bool
	if _, ok := r.Form["title"]; ok {
		t := strings.TrimSpace(r.FormValue("title"))
		title = &t
	}
	if v := r.FormValue("pinned"); v != "" {
		b := v == "1"
		pinned = &b
	}
	if v := r.FormValue("archived"); v != "" {
		b := v == "1"
		archived = &b
	}
	if err := s.st.SetMeta(id, title, pinned, archived); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type searchHit struct {
	ID      int64     `json:"id"`
	Title   string    `json:"title"`
	Tool    string    `json:"tool"`
	Label   string    `json:"label"`
	Color   string    `json:"color"`
	Host    string    `json:"host"`
	Snippet string    `json:"snippet"` // 一致した所を \x02 \x03 で囲む
	When    string    `json:"when"`
	At      time.Time `json:"at"`
}

// search は検索の窓の結果（本文まで探し、抜粋を付ける）。
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	f := store.Filter{Query: q, Status: "all", Limit: 50}
	if t := r.URL.Query().Get("tool"); t != "" {
		f.Tools = []string{t}
	}
	if q == "" {
		f.Limit = 12
	}
	rows, _, err := s.st.Browse(f)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	out := []searchHit{}
	l := lang(r)
	for _, c := range rows {
		t := c.Title
		if t == "" {
			t = c.ExternalID
		}
		out = append(out, searchHit{ID: c.ID, Title: strings.Join(strings.Fields(t), " "), Tool: c.Tool, Label: toolLabel(c.Tool),
			Color: string(toolColor(c.Tool)), Host: c.Host, Snippet: c.Snippet, When: dayIn(l, c.UpdatedAt.Local()), At: c.UpdatedAt})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// sideList は左の一覧だけを返す（絞り込みを変えたとき、読み込み直さずに差し替える）。
func (s *Server) sideList(w http.ResponseWriter, r *http.Request) {
	d, err := s.side(r, r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if id, err := strconv.ParseInt(r.URL.Query().Get("cur"), 10, 64); err == nil {
		if c, _, err := s.st.Get(id); err == nil {
			d["C"] = c
		}
	}
	s.render(w, r, "sidelist", d)
}
