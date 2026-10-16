// Package web は検索・閲覧・書き出しの画面。
package web

import (
	"bytes"
	"embed"
	"html/template"
	"log"
	"net/http"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rurutheGeek/aihist/internal/export"
	"github.com/rurutheGeek/aihist/internal/model"
	"github.com/rurutheGeek/aihist/internal/raw"
	"github.com/rurutheGeek/aihist/internal/store"
)

//go:embed templates/*.html
var files embed.FS

//go:embed static
var static embed.FS

//go:embed install
var install embed.FS

type Server struct {
	bin   string // 配る実行ファイルの置き場所（aihist-<os>-<arch>）
	st    *store.Store
	raw   raw.Store
	token string
	tmpl  *template.Template
	mu    sync.Mutex // 取り込みは1つずつ
}

// New は画面と API を作る。token が空でなければ /api に要る。画面（/upload を含む）は
// 前に置く認証（SSO など）に任せる。
func New(st *store.Store, rs raw.Store, token string) *Server {
	funcs := template.FuncMap{
		"when": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("2006-01-02 15:04")
		},
		"clock": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("1/2 15:04")
		},
		"short": func(s string, n int) string {
			r := []rune(s)
			if len(r) <= n {
				return s
			}
			return string(r[:n]) + "…"
		},
		"oneline":   func(s string) string { return strings.Join(strings.Fields(s), " ") },
		"toolLabel": toolLabel,
		"toolColor": toolColor,
		"toolMark":  toolMark,
		"hl":        hl,
		"day":       func(t time.Time) string { return day(t.Local()) },
		"summary":   summary,
		"base":      base,
	}
	return &Server{st: st, raw: rs, token: token, tmpl: template.Must(template.New("").Funcs(funcs).ParseFS(files, "templates/*.html"))}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.list)
	mux.Handle("GET /static/", http.FileServerFS(static))
	mux.HandleFunc("GET /c/{id}", s.show)
	mux.HandleFunc("GET /find/{tool}/{host}/{ext}", func(w http.ResponseWriter, r *http.Request) {
		id, err := s.st.Lookup(r.PathValue("tool"), r.PathValue("host"), r.PathValue("ext"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, "/c/"+strconv.FormatInt(id, 10), http.StatusFound)
	})
	mux.HandleFunc("GET /c/{id}/export.md", s.exportMD)
	mux.HandleFunc("GET /upload", s.uploadForm)
	mux.HandleFunc("GET /setup", s.setup)
	mux.HandleFunc("POST /upload", s.upload)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /api/files", s.auth(s.apiList))
	mux.HandleFunc("PUT /api/files", s.auth(s.apiPut))
	mux.HandleFunc("POST /api/import", s.auth(s.apiImport))
	mux.HandleFunc("GET /api/file", s.auth(s.apiFile))
	mux.HandleFunc("GET /api/install.sh", s.installScript("install.sh"))
	mux.HandleFunc("GET /api/install.ps1", s.installScript("install.ps1"))
	mux.HandleFunc("GET /api/uninstall.sh", s.installScript("uninstall.sh"))
	mux.HandleFunc("GET /api/uninstall.ps1", s.installScript("uninstall.ps1"))
	mux.HandleFunc("GET /api/bin/{name}", s.auth(s.apiBin))
	mux.HandleFunc("GET /api/find", s.auth(s.apiFind))
	return mux
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	d, err := s.side(r)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d["Home"] = true
	s.render(w, "list.html", d)
}

func (s *Server) load(w http.ResponseWriter, r *http.Request) (*store.ConvRow, []model.Message, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return nil, nil, false
	}
	c, msgs, err := s.st.Get(id)
	if err != nil {
		http.NotFound(w, r)
		return nil, nil, false
	}
	return c, msgs, true
}

func (s *Server) show(w http.ResponseWriter, r *http.Request) {
	c, msgs, ok := s.load(w, r)
	if !ok {
		return
	}
	d, err := s.side(r)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	branches, children, err := s.st.Related(c)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d["C"], d["Turns"], d["Server"] = c, turns(msgs), s.origin(r)
	in := map[int64]bool{}
	hosts := map[string]bool{}
	for _, b := range append(branches, children...) {
		in[b.ID] = true
		hosts[b.Host] = true
	}
	d["Branches"], d["Children"], d["Group"], d["ManyHosts"] = branches, children, in, len(hosts) > 1
	s.render(w, "show.html", d)
}

func (s *Server) exportMD(w http.ResponseWriter, r *http.Request) {
	c, msgs, ok := s.load(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	o := export.Default
	o.Thinking = q.Get("thinking") == "1"
	o.Tools = q.Get("tools") != "0"
	if n, err := strconv.Atoi(q.Get("max")); err == nil {
		o.MaxToolOutput = n
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	if q.Get("dl") == "1" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+c.ExternalID+`.md"`)
	}
	export.Markdown(w, c, msgs, o)
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("template %s: %v", name, err)
		http.Error(w, "template error", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	buf.WriteTo(w)
}

// SetBin は配る実行ファイルの置き場所を決める（空なら配らない）。
func (s *Server) SetBin(dir string) { s.bin = dir }

func (s *Server) origin(r *http.Request) string {
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") == "" {
		scheme = "http"
	}
	return scheme + "://" + r.Host
}

// installScript は、このサーバーの名前を入れた導入スクリプトを返す。秘密は含まない。
func (s *Server) installScript(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := install.ReadFile("install/" + name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(bytes.ReplaceAll(b, []byte("{{SERVER}}"), []byte(s.origin(r))))
	}
}

var binName = regexp.MustCompile(`^aihist-(linux|darwin|windows)-(amd64|arm64)(\.exe)?$`)

func (s *Server) apiBin(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.bin == "" || !binName.MatchString(name) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, filepath.Join(s.bin, name))
}
