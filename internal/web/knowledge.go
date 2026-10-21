package web

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/rurutheGeek/aihist/internal/ai"
	"github.com/rurutheGeek/aihist/internal/store"
)

// knowledgeList はナレッジの一覧。
func (s *Server) knowledgeList(w http.ResponseWriter, r *http.Request) {
	d, err := s.side(r, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	ks, err := s.st.KnowledgeList(r.URL.Query().Get("q"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d["K"], d["KQ"], d["Page"] = ks, r.URL.Query().Get("q"), "knowledge"
	s.render(w, r, "knowledge.html", d)
}

func (s *Server) loadKnowledge(w http.ResponseWriter, r *http.Request) (store.Knowledge, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return store.Knowledge{}, false
	}
	k, err := s.st.GetKnowledge(id)
	if err != nil {
		http.NotFound(w, r)
		return k, false
	}
	return k, true
}

// knowledgeShow は記事1つ（読む・直す）。
func (s *Server) knowledgeShow(w http.ResponseWriter, r *http.Request) {
	k, ok := s.loadKnowledge(w, r)
	if !ok {
		return
	}
	d, err := s.side(r, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d["Item"], d["Page"] = k, "knowledge"
	d["ConvID"] = s.st.LookupExt(k.Tool, k.Host, k.ExtID)
	s.render(w, r, "kshow.html", d)
}

// knowledgeSave は題名・本文を直す（人が直した記事は、まとめ直しても置き換えない）。個人の情報の警告は直すたびに見直す。
func (s *Server) knowledgeSave(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	k, ok := s.loadKnowledge(w, r)
	if !ok {
		return
	}
	if t := strings.TrimSpace(r.FormValue("title")); t != "" {
		k.Title = t
	}
	if b, ok := r.Form["body"]; ok {
		k.Body = strings.ReplaceAll(b[0], "\r\n", "\n")
	}
	k.Status = "registered"
	var words []string
	if id := s.st.LookupExt(k.Tool, k.Host, k.ExtID); id != 0 {
		if c, _, err := s.st.Get(id); err == nil {
			words = ai.Personal(c.Host, c.Cwd, c.GitBranch, "")
		}
	}
	k.Warn = ai.Leaks(k.Title+"\n"+k.Body, words)
	if err := s.st.UpdateKnowledge(k); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if r.Header.Get("X-Auto") == "1" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/knowledge/"+strconv.FormatInt(k.ID, 10), http.StatusSeeOther)
}

func (s *Server) knowledgeDelete(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	k, ok := s.loadKnowledge(w, r)
	if !ok {
		return
	}
	s.st.DeleteKnowledge(k.ID)
	http.Redirect(w, r, "/knowledge", http.StatusSeeOther)
}

// knowledgeMD は記事を Markdown で渡す（付帯情報は入れない）。
func (s *Server) knowledgeMD(w http.ResponseWriter, r *http.Request) {
	k, ok := s.loadKnowledge(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	if r.URL.Query().Get("dl") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="knowledge-`+strconv.FormatInt(k.ID, 10)+`.md"`)
	}
	w.Write([]byte("# " + k.Title + "\n\n" + k.Body))
}

// knowledgeGraph はナレッジ・タグ・会話のつながりを図にする画面。
func (s *Server) knowledgeGraph(w http.ResponseWriter, r *http.Request) {
	d, err := s.side(r, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d["Page"] = "knowledge"
	s.render(w, r, "kgraph.html", d)
}

type graphNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Kind  string `json:"kind"` // tag / knowledge / conv
	URL   string `json:"url,omitempty"`
	Color string `json:"color,omitempty"`
}

// knowledgeGraphData は図の点と線。
func (s *Server) knowledgeGraphData(w http.ResponseWriter, r *http.Request) {
	convs, err := s.st.Graph()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	ks, _ := s.st.KnowledgeList("")
	nodes := []graphNode{}
	links := [][2]string{}
	seen := map[string]bool{}
	add := func(n graphNode) {
		if !seen[n.ID] {
			seen[n.ID] = true
			nodes = append(nodes, n)
		}
	}
	for _, k := range ks {
		add(graphNode{ID: "k" + strconv.FormatInt(k.ID, 10), Label: k.Title, Kind: "knowledge", URL: "/knowledge/" + strconv.FormatInt(k.ID, 10)})
	}
	for _, c := range convs {
		cid := "c" + strconv.FormatInt(c.ID, 10)
		title := strings.Join(strings.Fields(c.Title), " ")
		if title == "" {
			title = toolLabel(c.Tool)
		}
		add(graphNode{ID: cid, Label: title, Kind: "conv", URL: "/c/" + strconv.FormatInt(c.ID, 10), Color: string(toolColor(c.Tool))})
		for _, t := range c.Tags {
			add(graphNode{ID: "t" + t, Label: t, Kind: "tag", URL: "/?tag=" + url.QueryEscape(t)})
			links = append(links, [2]string{cid, "t" + t})
		}
		for _, k := range c.Knowledge {
			if kid := "k" + strconv.FormatInt(k, 10); seen[kid] {
				links = append(links, [2]string{cid, kid})
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"nodes": nodes, "links": links})
}
