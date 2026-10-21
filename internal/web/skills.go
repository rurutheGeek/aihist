package web

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rurutheGeek/aihist/internal/ingest"
	"github.com/rurutheGeek/aihist/internal/skillsrc"
	"github.com/rurutheGeek/aihist/internal/store"
)

// 取り込む前に見せた Skill（選んで取り込むまで 30 分覚えておく）。
var skillPicks struct {
	sync.Mutex
	m map[string]skillPick
}

type skillPick struct {
	at     time.Time
	url    string
	skills []store.Skill
}

func keepPick(url string, ks []store.Skill) string {
	b := make([]byte, 12)
	rand.Read(b)
	id := hex.EncodeToString(b)
	skillPicks.Lock()
	defer skillPicks.Unlock()
	if skillPicks.m == nil {
		skillPicks.m = map[string]skillPick{}
	}
	for k, v := range skillPicks.m {
		if time.Since(v.at) > 30*time.Minute {
			delete(skillPicks.m, k)
		}
	}
	skillPicks.m[id] = skillPick{time.Now(), url, ks}
	return id
}

func takePick(id string) (skillPick, bool) {
	skillPicks.Lock()
	defer skillPicks.Unlock()
	p, ok := skillPicks.m[id]
	delete(skillPicks.m, id)
	return p, ok
}

type skillRow struct {
	store.Skill
	Exists bool // 同じ名前の Skill がある（取り込むと置き換える）
}

// skillList は Skill の一覧（登録済み・履歴で見つけたもの）。
func (s *Server) skillList(w http.ResponseWriter, r *http.Request) {
	d, err := s.side(r, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	ks, err := s.st.Skills()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	found, _ := s.st.SkillsInHistory()
	var fresh []store.SkillFound
	for _, f := range found {
		if !slices.ContainsFunc(ks, func(k store.Skill) bool { return k.Name == f.Name }) {
			fresh = append(fresh, f)
		}
	}
	tab := r.URL.Query().Get("tab")
	if tab != "found" {
		tab = "registered"
	}
	d["Skills"], d["Found"], d["STab"], d["Page"] = ks, fresh, tab, "skills"
	s.render(w, r, "skills.html", d)
}

// skillShow は Skill 1つ。id が new なら新しく作る。file を渡すと付くファイルの中身を返す。
func (s *Server) skillShow(w http.ResponseWriter, r *http.Request) {
	var k store.Skill
	if id := r.PathValue("id"); id != "new" {
		n, _ := strconv.ParseInt(id, 10, 64)
		var err error
		if k, err = s.st.GetSkill(n); err != nil {
			http.NotFound(w, r)
			return
		}
	}
	if f := r.URL.Query().Get("file"); f != "" && k.ID != 0 {
		b, err := s.st.SkillFileData(k.ID, f)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(b)
		return
	}
	d, err := s.side(r, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d["Item"], d["Page"] = k, "skills"
	s.render(w, r, "sshow.html", d)
}

// skillSave は Skill を作る・直す。from に履歴で見つけた Skill の名前があれば、その本文で作る。
func (s *Server) skillSave(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var k store.Skill
	if id := r.PathValue("id"); id != "new" {
		n, _ := strconv.ParseInt(id, 10, 64)
		var err error
		if k, err = s.st.GetSkill(n); err != nil {
			http.NotFound(w, r)
			return
		}
		k.Files = nil // 付くファイルはそのまま
	}
	if from := r.FormValue("from"); from != "" {
		found, _ := s.st.SkillsInHistory()
		for _, f := range found {
			if f.Name == from {
				k.Name, k.Body, k.Source = f.Name, f.Body, ""
				k.Description = strings.TrimSpace(strings.TrimLeft(strings.SplitN(f.Body, "\n", 2)[0], "# "))
			}
		}
	}
	if n := strings.TrimSpace(r.FormValue("name")); n != "" {
		k.Name = n
	}
	if _, ok := r.Form["description"]; ok {
		k.Description = strings.TrimSpace(r.FormValue("description"))
	}
	if b, ok := r.Form["body"]; ok {
		k.Body = strings.TrimSpace(strings.ReplaceAll(b[0], "\r\n", "\n"))
	}
	if k.Name == "" || k.Body == "" {
		http.Error(w, tr(lang(r), "名前と本文が要る"), http.StatusBadRequest)
		return
	}
	id, err := s.st.SaveSkill(k)
	if err != nil {
		http.Error(w, tr(lang(r), "同じ名前の Skill がある"), http.StatusConflict)
		return
	}
	http.Redirect(w, r, "/skills/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) skillDelete(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if n, err := strconv.ParseInt(r.PathValue("id"), 10, 64); err == nil {
		s.st.DeleteSkill(n)
	}
	http.Redirect(w, r, "/skills", http.StatusSeeOther)
}

// skillImport は GitHub の URL から Skill を読み、選ぶ画面を出す。
func (s *Server) skillImport(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	d, err := s.side(r, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d["Page"], d["ImportURL"] = "skills", r.FormValue("url")
	repo, err := skillsrc.ParseGitHub(r.FormValue("url"))
	var ks []store.Skill
	if err == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
		defer cancel()
		ks, err = skillsrc.Fetch(ctx, repo)
	}
	if err != nil {
		d["ImportErr"] = tr(lang(r), err.Error())
		s.render(w, r, "simport.html", d)
		return
	}
	have, _ := s.st.Skills()
	var rows []skillRow
	for _, k := range ks {
		rows = append(rows, skillRow{k, slices.ContainsFunc(have, func(h store.Skill) bool { return h.Name == k.Name })})
	}
	d["Pick"], d["Rows"] = keepPick(repo.String(), ks), rows
	s.render(w, r, "simport.html", d)
}

// skillImportDo は選んだ Skill を取り込む（同じ名前は置き換える）。
func (s *Server) skillImportDo(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, ok := takePick(r.FormValue("pick"))
	if !ok {
		http.Redirect(w, r, "/skills", http.StatusSeeOther)
		return
	}
	want := r.Form["name"]
	for _, k := range p.skills {
		if !slices.Contains(want, k.Name) {
			continue
		}
		if old, err := s.st.SkillByName(k.Name); err == nil {
			k.ID = old.ID
		}
		s.st.SaveSkill(k)
	}
	http.Redirect(w, r, "/skills", http.StatusSeeOther)
}

// skillUpdate は GitHub から取り込んだ Skill を、出どころの今の版で置き換える。
func (s *Server) skillUpdate(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	n, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	k, err := s.st.GetSkill(n)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	repo, err := skillsrc.ParseGitHub(k.Source)
	if err != nil {
		http.Error(w, tr(lang(r), err.Error()), 400)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	ks, err := skillsrc.Fetch(ctx, repo)
	if err != nil {
		http.Error(w, tr(lang(r), err.Error()), 502)
		return
	}
	for _, nk := range ks {
		if nk.Source == k.Source || len(ks) == 1 {
			nk.ID = k.ID
			if _, err := s.st.SaveSkill(nk); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
			break
		}
	}
	http.Redirect(w, r, "/skills/"+strconv.FormatInt(k.ID, 10), http.StatusSeeOther)
}

// skillHome は Skill の置き場所を送るときの名前から、マシンでの場所。
var skillHome = map[string]string{"skills_claude": "~/.claude/skills", "skills_codex": "~/.codex/skills", "skills_agents": "~/.agents/skills"}

// ingestSkills はマシンから送られた Skill の置き場所を読み、Skill を足す・置き換える。
// GitHub から取り込んだ同じ名前の Skill は置き換えない（あちらが正本）。
func (s *Server) ingestSkills(host, tool, dir string) (ingest.Result, error) {
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		b, err := os.ReadFile(p)
		if err == nil {
			files[filepath.ToSlash(rel)] = b
		}
		return nil
	})
	if err != nil {
		return ingest.Result{}, err
	}
	var res ingest.Result
	for _, k := range skillsrc.Collect(files, "") {
		k.Source = host + " " + skillHome[tool] + "/" + k.Source
		if old, err := s.st.SkillByName(k.Name); err == nil {
			if strings.HasPrefix(old.Source, "https://") {
				res.Skipped++
				continue
			}
			k.ID = old.ID
		}
		if _, err := s.st.SaveSkill(k); err != nil {
			return res, err
		}
		res.Imported++
	}
	log.Printf("%s/%s: %s", host, tool, res)
	return res, nil
}
