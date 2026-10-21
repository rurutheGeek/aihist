package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/rurutheGeek/aihist/internal/store"
)

// promptList はプロンプトの一覧（登録済み・履歴で見つけた Skill）。
func (s *Server) promptList(w http.ResponseWriter, r *http.Request) {
	d, err := s.side(r, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	ps, err := s.st.Prompts()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	found, _ := s.st.SkillsInHistory()
	have := map[string]bool{}
	for _, p := range ps {
		have[p.Name] = true
	}
	var fresh []store.SkillFound
	for _, f := range found {
		if !have[f.Name] {
			fresh = append(fresh, f)
		}
	}
	tab := r.URL.Query().Get("tab")
	if tab != "found" {
		tab = "registered"
	}
	d["Prompts"], d["Found"], d["PTab"], d["Page"] = ps, fresh, tab, "prompts"
	s.render(w, r, "prompts.html", d)
}

// promptShow はプロンプト1つ（読む・直す）。id が new なら新しく作る。
func (s *Server) promptShow(w http.ResponseWriter, r *http.Request) {
	d, err := s.side(r, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var p store.Prompt
	if id := r.PathValue("id"); id != "new" {
		n, _ := strconv.ParseInt(id, 10, 64)
		if p, err = s.st.GetPrompt(n); err != nil {
			http.NotFound(w, r)
			return
		}
	}
	d["Item"], d["Page"] = p, "prompts"
	s.render(w, r, "pshow.html", d)
}

// promptSave はプロンプトを作る・直す。from に履歴で見つけた Skill の名前があれば、その本文で登録する。
func (s *Server) promptSave(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var p store.Prompt
	if id := r.PathValue("id"); id != "new" {
		n, _ := strconv.ParseInt(id, 10, 64)
		var err error
		if p, err = s.st.GetPrompt(n); err != nil {
			http.NotFound(w, r)
			return
		}
	}
	if from := r.FormValue("from"); from != "" {
		found, _ := s.st.SkillsInHistory()
		for _, f := range found {
			if f.Name == from {
				p.Name, p.Body = f.Name, f.Body
				p.Description = strings.TrimSpace(strings.TrimLeft(strings.SplitN(f.Body, "\n", 2)[0], "# "))
			}
		}
	}
	if n := strings.TrimSpace(r.FormValue("name")); n != "" {
		p.Name = n
	}
	if _, ok := r.Form["description"]; ok {
		p.Description = strings.TrimSpace(r.FormValue("description"))
	}
	if b, ok := r.Form["body"]; ok {
		p.Body = strings.ReplaceAll(b[0], "\r\n", "\n")
	}
	if p.Name == "" || strings.TrimSpace(p.Body) == "" {
		http.Error(w, tr(lang(r), "名前と本文が要る"), http.StatusBadRequest)
		return
	}
	id, err := s.st.SavePrompt(p)
	if err != nil {
		http.Error(w, tr(lang(r), "同じ名前のプロンプトがある"), http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/prompts/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) promptDelete(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if n, err := strconv.ParseInt(r.PathValue("id"), 10, 64); err == nil {
		s.st.DeletePrompt(n)
	}
	http.Redirect(w, r, "/prompts", http.StatusSeeOther)
}
