package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/rurutheGeek/aihist/internal/store"
)

// profileMarkdown はプロフィールを、ほかの AI に渡す形にする。
func profileMarkdown(ts []store.Trait) string {
	var b strings.Builder
	for _, t := range ts {
		b.WriteString("- " + t.Text + "\n")
	}
	return b.String()
}

func (s *Server) profilePage(w http.ResponseWriter, r *http.Request) {
	d, err := s.side(r, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	ts, _ := s.st.Profile()
	d["Traits"], d["Page"], d["ProfileOn"], d["ProfileNote"] = ts, "profile", s.profileOn(), s.st.Setting("profile_note", "")
	d["ProfileMD"] = profileMarkdown(ts)
	s.render(w, r, "profile.html", d)
}

// profileSettings は使うかどうかと AI への指示を保存する。
func (s *Server) profileSettings(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	on := "0"
	if r.FormValue("on") == "1" {
		on = "1"
	}
	s.st.SetSetting("profile_on", on)
	s.st.SetSetting("profile_note", strings.TrimSpace(r.FormValue("note")))
	w.WriteHeader(http.StatusNoContent)
}

// profileEdit は特徴を直す・足す（文が空なら消す）。
func (s *Server) profileEdit(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, _ := strconv.ParseInt(r.FormValue("id"), 10, 64)
	if err := s.st.EditTrait(id, strings.TrimSpace(r.FormValue("text"))); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
