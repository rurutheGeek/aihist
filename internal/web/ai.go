package web

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rurutheGeek/aihist/internal/ai"
	"github.com/rurutheGeek/aihist/internal/store"
)

// aiConfig は設定の「AI」で入れた接続先。入れていなければ AI の機能は出さない。
func (s *Server) aiConfig() ai.Config {
	return ai.Config{BaseURL: s.st.Setting("ai_base_url", ""), Key: s.st.Setting("ai_key", ""), Model: s.st.Setting("ai_model", "")}
}

// setAI は接続先を保存する。キーは空なら前のままにする（画面には出さないため）。
func (s *Server) setAI(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.st.SetSetting("ai_base_url", strings.TrimSpace(r.FormValue("base_url")))
	s.st.SetSetting("ai_model", strings.TrimSpace(r.FormValue("model")))
	if k := strings.TrimSpace(r.FormValue("key")); k != "" {
		s.st.SetSetting("ai_key", k)
	}
	if r.FormValue("clear_key") == "1" {
		s.st.SetSetting("ai_key", "")
	}
	done(w, r, "/settings/ai")
}

// aiModels は接続先が使えるモデル（接続を確かめるのも兼ねる）。
func (s *Server) aiModels(w http.ResponseWriter, r *http.Request) {
	c := s.aiConfig()
	w.Header().Set("Content-Type", "application/json")
	if c.BaseURL == "" {
		json.NewEncoder(w).Encode(map[string]any{"error": tr(lang(r), "接続先が無い")})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	ms, err := c.Models(ctx)
	if err != nil {
		json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"models": ms})
}

// テンプレートのタグ（設定の「タグ」から足せる）
var tagTemplates = []struct{ Name, Desc string }{
	{"仕事", "仕事の作業・相談"},
	{"調べもの", "調べる・比べる・確かめる"},
	{"学習", "知らないことを教わる"},
	{"文章", "文章を書く・直す・訳す"},
	{"創作", "物語・絵・音楽などのアイデア"},
	{"暮らし", "料理・買い物・健康・お金など"},
	{"相談", "悩みや判断の相談"},
	{"雑談", "目的の無いやりとり"},
}

func (s *Server) addTag(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.FormValue("template") == "1" {
		l := lang(r)
		for _, t := range tagTemplates {
			s.st.SetTag(tr(l, t.Name), tr(l, t.Desc))
		}
	} else if n := strings.TrimSpace(r.FormValue("name")); n != "" {
		if old := r.FormValue("old"); old != "" && old != n {
			s.st.RenameTag(old, n)
		}
		s.st.SetTag(n, strings.TrimSpace(r.FormValue("description")))
	}
	renderOr(s, w, r, "/settings/tags")
}

func (s *Server) deleteTag(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.st.DeleteTag(r.FormValue("name"))
	renderOr(s, w, r, "/settings/tags")
}

// renderOr は設定の窓から送られたら（X-Panel）窓の中身を返し、そうでなければ戻す。
func renderOr(s *Server, w http.ResponseWriter, r *http.Request, back string) {
	if r.Header.Get("X-Panel") == "1" {
		s.renderSettings(w, r, "", "")
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// setConvTags は会話のタグを手で変える。
func (s *Server) setConvTags(w http.ResponseWriter, r *http.Request) {
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
	if err := s.st.SetConvTags(id, r.Form["tag"]); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AI でまとめるのは1つずつ（API に一度に多く送らない）
var aiMu sync.Mutex

// aiRun は選んだ会話を AI でまとめる（タグ・要約・ナレッジ）。利用者が押したときだけ動く。
// 同じモデルで今の版をまとめたことがある会話は飛ばす（force なら飛ばさない）。
func (s *Server) aiRun(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.ParseForm()
	var ids []int64
	for _, v := range r.Form["id"] {
		if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			ids = append(ids, id)
		}
	}
	c := s.aiConfig()
	if m := strings.TrimSpace(r.FormValue("model")); m != "" {
		c.Model = m
	}
	if !c.Ready() || len(ids) == 0 {
		http.Error(w, tr(lang(r), "AI の接続先が無い"), 400)
		return
	}
	id := s.startOrganize(ids, c, lang(r), r.FormValue("force") == "1")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": id})
}

func (s *Server) startOrganize(ids []int64, c ai.Config, l string, force bool) string {
	id := newJobID()
	j := &job{State: "waiting", Total: len(ids)}
	jobs.Lock()
	jobs.m[id] = j
	jobs.Unlock()
	set := func(f func()) { jobs.Lock(); f(); jobs.Unlock() }
	go func() {
		aiMu.Lock()
		defer aiMu.Unlock()
		set(func() { j.State = "running" })
		for i, cid := range ids {
			if !force && s.st.Organized(cid, c.Model) {
				set(func() { j.Skipped++; j.Done = i + 1 })
				continue
			}
			n, err := s.organize(cid, c, l)
			set(func() {
				j.Imported += n
				j.Done = i + 1
				if err != nil {
					j.Error = err.Error()
				}
			})
		}
		set(func() {
			j.State = "done"
			if j.Error != "" && len(ids) == 1 {
				j.State = "error"
			}
		})
		time.AfterFunc(time.Hour, func() { jobs.Lock(); delete(jobs.m, id); jobs.Unlock() })
	}()
	return id
}

// organize は会話1件をまとめて保存し、作ったナレッジの数を返す。
func (s *Server) organize(id int64, c ai.Config, l string) (int, error) {
	conv, msgs, err := s.st.Get(id)
	if err != nil {
		return 0, err
	}
	tags := s.aiTags()
	pages := ai.Pages(msgs, ai.PageSize)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	title := conv.Title + "（" + conv.StartedAt.Local().Format("2006-01-02") + " の会話）"
	res, err := c.Organize(ctx, title, pages, tags, l, nil)
	if err != nil {
		log.Printf("ai %d: %v", id, err)
		return 0, err
	}
	// プロフィールは本人の発言だけを別に読む
	if p := s.profileReq(); p != nil {
		ts, err := c.Profile(ctx, title, ai.UserSays(pages), l, p)
		if err != nil {
			log.Printf("ai %d profile: %v", id, err)
		}
		s.saveTraits(conv, ts)
	}
	return s.saveResult(conv, pages, res, c.Model, l)
}

// saveResult は整理の結果（API でも MCP でも）を保存し、作ったナレッジの数を返す。
func (s *Server) saveResult(conv *store.ConvRow, pages []string, res ai.Result, model, l string) (int, error) {
	if err := s.st.SetConvTags(conv.ID, res.Tags); err != nil {
		return 0, err
	}
	if err := s.st.SetSummary(conv.ID, res.Summary, model); err != nil {
		return 0, err
	}
	// まとめ直したら、AI が書いたまま人が直していない前の記事は置き換える
	s.st.DropDrafts(conv.Tool, conv.Host, conv.ExternalID)
	words := ai.Personal(conv.Host, conv.Cwd, conv.GitBranch, strings.Join(pages, "\n"))
	for _, k := range res.Knowledge {
		body := k.Markdown(l)
		s.st.AddKnowledge(store.Knowledge{
			Title: string(k.Title), Body: body, Tool: conv.Tool, Host: conv.Host, ExtID: conv.ExternalID,
			ConvTitle: conv.Title, Model: model, Struggle: string(k.Struggle), Score: k.Score,
			Warn: ai.Leaks(string(k.Title)+"\n"+body, words),
		})
	}
	return len(res.Knowledge), nil
}

// saveTraits はプロフィールに足す（使わない設定なら何もしない）。
func (s *Server) saveTraits(conv *store.ConvRow, ts []ai.Trait) {
	if !s.profileOn() {
		return
	}
	for _, t := range ts {
		s.st.AddTrait(traitKind(t.Kind), string(t.Text), t.Same, t.Replaces, [3]string{conv.Tool, conv.Host, conv.ExternalID})
	}
}

func (s *Server) profileOn() bool { return s.st.Setting("profile_on", "0") == "1" }

// profileReq はプロフィールを書き足すときに AI へ渡すもの（使わない設定なら nil）。
func (s *Server) profileReq() *ai.ProfileReq {
	if !s.profileOn() {
		return nil
	}
	p := &ai.ProfileReq{Note: s.st.Setting("profile_note", "")}
	ts, _ := s.st.Profile()
	for _, t := range ts {
		p.Existing = append(p.Existing, ai.ExistingTrait{ID: t.ID, Kind: t.Kind, Text: t.Text})
	}
	return p
}

// aiTags はタグの一覧（AI に渡す形）。
func (s *Server) aiTags() []ai.Tag {
	ts, _ := s.st.Tags()
	var tags []ai.Tag
	for _, t := range ts {
		tags = append(tags, ai.Tag{Name: t.Name, Desc: t.Description})
	}
	return tags
}

// traitKind は AI が返した種類（about / wish）を保存する形にする。
func traitKind(k string) string {
	if k == "wish" {
		return store.KindWish
	}
	return ""
}
