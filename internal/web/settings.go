package web

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
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
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	s.renderSettings(w, r, "", "")
}

func (s *Server) renderSettings(w http.ResponseWriter, r *http.Request, newName, newTok string) {
	d, err := s.side(r, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	toks, _ := s.st.Tokens()
	d["Tokens"], d["EnvToken"] = toks, s.token != ""
	d["NewName"], d["NewToken"] = newName, newTok
	if newTok != "" {
		d["NewCmds"] = commands(s.origin(r), newTok)
	}
	d["Cmds"] = commands(s.origin(r), "")
	d["Usage"], d["Page"] = s.usage(), "settings"
	days, _ := strconv.Atoi(s.st.Setting("cleanup_days", "0"))
	d["Cleanup"], d["CleanupOpts"] = days, []int{0, 7, 14, 30, 90}
	d["Lang"] = ""
	if c, err := r.Cookie("lang"); err == nil {
		d["Lang"] = c.Value
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
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
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
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

// apiClient は各マシンへ配る設定（push が送るたびに読む）。
func (s *Server) apiClient(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(s.st.Setting("cleanup_days", "0"))
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"cleanup_days":%d}`, days)
}

func (s *Server) setCleanup(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if d, err := strconv.Atoi(r.FormValue("days")); err == nil && d >= 0 {
		s.st.SetSetting("cleanup_days", strconv.Itoa(d))
	}
	http.Redirect(w, r, "/settings#cleanup", http.StatusSeeOther)
}
