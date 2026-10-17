package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/rurutheGeek/aihist/internal/ai"
	"github.com/rurutheGeek/aihist/internal/store"
)

// knowledgeList はナレッジの一覧（下書き・登録済み）。
func (s *Server) knowledgeList(w http.ResponseWriter, r *http.Request) {
	d, err := s.side(r, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	st := r.URL.Query().Get("status")
	if st != "registered" {
		st = "draft"
	}
	ks, err := s.st.KnowledgeList(st, r.URL.Query().Get("q"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d["K"], d["KStatus"], d["KQ"], d["Page"] = ks, st, r.URL.Query().Get("q"), "knowledge"
	d["Drafts"], d["Registered"] = s.st.KnowledgeCounts()
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
	d["Drafts"], d["Registered"] = s.st.KnowledgeCounts()
	s.render(w, r, "kshow.html", d)
}

// knowledgeSave は題名・本文を直し、登録（下書きへ戻す）する。個人の情報の警告は直すたびに見直す。
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
	switch r.FormValue("status") {
	case "registered", "draft":
		k.Status = r.FormValue("status")
	}
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
	http.Redirect(w, r, "/knowledge?status="+k.Status, http.StatusSeeOther)
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
