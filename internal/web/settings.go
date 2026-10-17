package web

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rurutheGeek/aihist/internal/push"
	"github.com/rurutheGeek/aihist/internal/store"
)

type cmdSet struct {
	OS, Install, Uninstall, Script, Name, UScript, UName string
}

// commands は入れる・消すコマンド。tok が空なら入れるコマンドは出さない。
func commands(origin, tok string) []cmdSet {
	return []cmdSet{
		{OS: "Linux・macOS・WSL",
			Install:   "curl -fsSL " + origin + "/api/install.sh | AIHIST_TOKEN=" + tok + " sh",
			Uninstall: "curl -fsSL " + origin + "/api/uninstall.sh | sh",
			Script:    "/api/install.sh", Name: "install.sh", UScript: "/api/uninstall.sh", UName: "uninstall.sh"},
		{OS: "Windows",
			Install:   `$env:AIHIST_TOKEN="` + tok + `"; irm ` + origin + "/api/install.ps1 | iex",
			Uninstall: "irm " + origin + "/api/uninstall.ps1 | iex",
			Script:    "/api/install.ps1", Name: "install.ps1", UScript: "/api/uninstall.ps1", UName: "uninstall.ps1"},
	}
}

type usageRow struct {
	Host, Tool, Label string
	Convs             int
	Bytes             int64
}

type usage struct {
	DB, Raw int64
	Rows    []usageRow
}

var rawSizes struct {
	sync.Mutex
	at time.Time
	m  map[[2]string]int64
}

// rawSize は原本の大きさをマシン・ツールごとに数える（重いので 5 分覚えておく）。
func (s *Server) rawSize() map[[2]string]int64 {
	rawSizes.Lock()
	defer rawSizes.Unlock()
	if time.Since(rawSizes.at) < 5*time.Minute && rawSizes.m != nil {
		return rawSizes.m
	}
	m := map[[2]string]int64{}
	hosts, _ := os.ReadDir(s.raw.Dir)
	for _, h := range hosts {
		tools, _ := os.ReadDir(filepath.Join(s.raw.Dir, h.Name()))
		for _, t := range tools {
			var n int64
			filepath.WalkDir(filepath.Join(s.raw.Dir, h.Name(), t.Name()), func(_ string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					if i, err := d.Info(); err == nil {
						n += i.Size()
					}
				}
				return nil
			})
			m[[2]string{h.Name(), t.Name()}] = n
		}
	}
	rawSizes.m, rawSizes.at = m, time.Now()
	return m
}

func (s *Server) usage() usage {
	u := usage{DB: s.st.Size()}
	sizes := s.rawSize()
	seen := map[[2]string]bool{}
	cs, _ := s.st.Counts()
	for _, c := range cs {
		k := [2]string{c.Host, c.Tool}
		seen[k] = true
		u.Rows = append(u.Rows, usageRow{Host: c.Host, Tool: c.Tool, Label: toolLabel(c.Tool), Convs: c.N, Bytes: sizes[k]})
	}
	for k, n := range sizes {
		if !seen[k] {
			u.Rows = append(u.Rows, usageRow{Host: k[0], Tool: k[1], Label: toolLabel(k[1]), Bytes: n})
		}
	}
	for _, r := range u.Rows {
		u.Raw += r.Bytes
	}
	sort.Slice(u.Rows, func(i, j int) bool { return u.Rows[i].Bytes > u.Rows[j].Bytes })
	return u
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// settings は設定の画面（マシンから送る・容量・言語）。
// 設定の画面の項目（左の一覧の順）。
var sections = []struct{ Key, Label string }{
	{"general", "一般"}, {"sending", "送信"}, {"upload", "アップロード"}, {"machines", "マシン"}, {"tokens", "トークン"}, {"ai", "AI"}, {"tags", "タグ"}, {"storage", "容量"},
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	s.renderSettings(w, r, "", "")
}

// done は保存のあと。画面がその場で保存したとき（X-Auto）は中身を返さず、そうでなければ元の項目へ戻す。
func done(w http.ResponseWriter, r *http.Request, back string) {
	if r.Header.Get("X-Auto") == "1" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, newName, newTok string) {
	d, err := s.side(r, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	sec := r.PathValue("section")
	if newTok != "" {
		sec = "tokens"
	}
	if sec == "" {
		sec = "general"
	}
	d["SNav"], d["Section"] = sections, sec
	if host := r.PathValue("host"); host != "" {
		sec = "m:" + host
	}
	d["Sub"] = sec
	toks, _ := s.st.Tokens()
	d["Tokens"], d["EnvToken"] = toks, s.token != ""
	d["NewName"], d["NewToken"] = newName, newTok
	if newTok != "" {
		d["NewCmds"] = commands(s.origin(r), newTok)
	}
	d["Cmds"] = commands(s.origin(r), "")
	d["Usage"], d["Page"], d["Deleted"] = s.usage(), "settings", s.st.Deleted()
	days, _ := strconv.Atoi(s.st.Setting("cleanup_days", "0"))
	d["Cleanup"], d["CleanupOpts"] = days, []int{0, 7, 14, 30, 90}
	d["Machines"] = s.machineViews(lang(r))
	var ut []toolOpt
	for _, t := range uploadTools {
		ut = append(ut, toolOpt{Tool: t, Label: toolLabel(t), On: s.uploadAllowed(t)})
	}
	d["UploadTools"], d["UploadAttach"], d["UploadHide"] = ut, attachOpts(s.uploadAttach()), hideOpts(s.uploadHide())
	d["Hide"] = hideOpts(s.hideDefault())
	ac := s.aiConfig()
	d["AIBase"], d["AIModelSet"], d["AIHasKey"] = ac.BaseURL, ac.Model, ac.Key != ""
	d["TagList"], _ = s.st.Tags()
	d["MCPURL"] = s.origin(r) + "/api/mcp"
	d["MCPAdd"] = `claude mcp add --transport http aihist ` + s.origin(r) + `/api/mcp --header "Authorization: Bearer <` + tr(lang(r), "トークン") + `>"`
	d["Schedule"] = s.st.Setting("schedule", DefaultSchedule)
	d["Next"] = s.nextRuns(lang(r), d["Schedule"].(string), 3)
	d["CleanupOpts2"] = []int{-1, 0, 7, 14, 30, 90}
	d["Lang"] = ""
	if c, err := r.Cookie("lang"); err == nil {
		d["Lang"] = c.Value
	}
	// 設定の窓は画面の上に開く。窓の中身だけを求められたら（X-Panel）中身だけを返す
	if r.Header.Get("X-Panel") == "1" {
		s.render(w, r, "settingsbody", d)
		return
	}
	s.render(w, r, "settings.html", d)
}

func (s *Server) addToken(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Redirect(w, r, "/settings/tokens", http.StatusSeeOther)
		return
	}
	tok, err := s.st.CreateToken(name)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.renderSettings(w, r, name, tok)
}

func (s *Server) deleteToken(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if id, err := strconv.ParseInt(r.PathValue("id"), 10, 64); err == nil {
		s.st.DeleteToken(id)
	}
	http.Redirect(w, r, "/settings/tokens", http.StatusSeeOther)
}

// DefaultSchedule は送るタイミングの既定（設定の画面で変える）。
const DefaultSchedule = "*/10 * * * *"

// clientConfig はマシン host へ配る設定。マシンの設定が無い項目は全体の設定に従う。
type clientConfig struct {
	Schedule    string `json:"schedule"`
	TZ          string `json:"tz"`
	CleanupDays int    `json:"cleanup_days"`
	Tools       string `json:"tools"`
	Attachments bool   `json:"attachments"` // 種類を分ける前の版のマシン向け
	Attach      string `json:"attach"`
	Hide        string `json:"hide"`
}

func (s *Server) clientConfig(host string) clientConfig {
	m, _ := s.st.Machine(host)
	cf := clientConfig{Schedule: m.Schedule, TZ: s.st.Setting("tz", ""), Tools: m.Tools, Attach: m.Attach, Attachments: m.Attach != "-", CleanupDays: m.Cleanup, Hide: s.hideFor(host)}
	if cf.Schedule == "" {
		cf.Schedule = s.st.Setting("schedule", DefaultSchedule)
	}
	if cf.CleanupDays < 0 {
		cf.CleanupDays, _ = strconv.Atoi(s.st.Setting("cleanup_days", "0"))
	}
	return cf
}

// apiClient は各マシンへ配る設定（push が読む）。
func (s *Server) apiClient(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.clientConfig(r.URL.Query().Get("host")))
}

// apiMachine はマシンからの報告（見つけた AI・版、初めてのときは導入で選んだ設定）を受ける。
func (s *Server) apiMachine(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host     string           `json:"host"`
		Version  string           `json:"version"`
		Detected []store.Detected `json:"detected"`
		Init     *struct {
			Tools       string `json:"tools"`
			Attachments *bool  `json:"attachments"`
		} `json:"init"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil || req.Host == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var init *store.Machine
	if req.Init != nil {
		init = &store.Machine{Tools: "*", Attachments: true, Cleanup: -1}
		if req.Init.Tools != "" {
			init.Tools = req.Init.Tools
		}
		if req.Init.Attachments != nil {
			init.Attachments = *req.Init.Attachments
		}
	}
	if err := s.st.Report(req.Host, req.Version, req.Detected, init); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.clientConfig(req.Host))
}

func (s *Server) setCleanup(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if d, err := strconv.Atoi(r.FormValue("days")); err == nil && d >= 0 {
		s.st.SetSetting("cleanup_days", strconv.Itoa(d))
	}
	done(w, r, "/settings/sending")
}

// アップロードで受け付ける AI（設定の「アップロード」に並べる順）。
var uploadTools = []string{"claude_code", "codex", "antigravity", "chatgpt", "claude_ai", "gemini"}

// setUpload はアップロードで取り込む種類を保存する。外したものだけを覚え、あとで増えた AI は取り込む。
func (s *Server) setUpload(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	r.ParseForm()
	l := []string{"*"}
	for _, t := range uploadTools {
		if !slices.Contains(r.Form["tool"], t) {
			l = append(l, "-"+t)
		}
	}
	s.st.SetSetting("upload_tools", strings.Join(l, ","))
	s.st.SetSetting("upload_attach", attachList(r.Form["attach"]))
	s.st.SetSetting("upload_hide", hideList(r.Form["hide"]))
	done(w, r, "/settings/upload")
}

type attachOpt struct {
	Kind, Label string
	On          bool
}

var attachLabels = map[string]string{"image": "画像", "pdf": "PDF", "doc": "文書（Word・Excel・テキストなど）", "audio": "音声", "video": "動画", "other": "その他"}

func attachOpts(list string) []attachOpt {
	var out []attachOpt
	for _, k := range push.AttachKinds {
		out = append(out, attachOpt{Kind: k, Label: attachLabels[k], On: push.Selected(list, k)})
	}
	return out
}

// attachList は選んだ種類を並びにする（全部なら "*"、何も無ければ "-"、ほかは外したものを書く）。
func attachList(on []string) string {
	l := []string{"*"}
	for _, k := range push.AttachKinds {
		if !slices.Contains(on, k) {
			l = append(l, "-"+k)
		}
	}
	if len(l) == len(push.AttachKinds)+1 {
		return "-"
	}
	return strings.Join(l, ",")
}

// uploadAttach はアップロードで取り込む添付ファイルの種類。
func (s *Server) uploadAttach() string {
	if v := s.st.Setting("upload_attach", ""); v != "" {
		return v
	}
	if s.st.Setting("upload_attachments", "1") == "0" {
		return "-"
	}
	return "*"
}

// forgetDeleted は消した会話の印を消す。次に同じ元データが来たら、また取り込む。
func (s *Server) forgetDeleted(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	s.st.ForgetDeleted()
	done(w, r, "/settings/storage")
}
