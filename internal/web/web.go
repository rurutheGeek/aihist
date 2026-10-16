// Package web は検索・閲覧・書き出しの画面。
package web

import (
	"bytes"
	"embed"
	"html/template"
	"log"
	"net/http"
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

type Server struct {
	st    *store.Store
	raw   raw.Store
	token string
	tmpl  *template.Template
	mu    sync.Mutex // 取り込みは1つずつ
}

// New は画面と API を作る。token が空でなければ、書き込み（/api と /upload）に要る。
func New(st *store.Store, rs raw.Store, token string) *Server {
	funcs := template.FuncMap{
		"when": func(t time.Time) string {
			if t.IsZero() {
				return ""
			}
			return t.Local().Format("2006-01-02 15:04")
		},
		"short": func(s string, n int) string {
			r := []rune(s)
			if len(r) <= n {
				return s
			}
			return string(r[:n]) + "…"
		},
		"oneline": func(s string) string { return strings.Join(strings.Fields(s), " ") },
		"isKind":  func(p model.Part, k string) bool { return string(p.Kind) == k },
	}
	return &Server{st: st, raw: rs, token: token, tmpl: template.Must(template.New("").Funcs(funcs).ParseFS(files, "templates/*.html"))}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.list)
	mux.HandleFunc("GET /c/{id}", s.show)
	mux.HandleFunc("GET /c/{id}/export.md", s.exportMD)
	mux.HandleFunc("GET /upload", s.uploadForm)
	mux.HandleFunc("POST /upload", s.auth(s.upload))
	mux.HandleFunc("GET /api/files", s.auth(s.apiList))
	mux.HandleFunc("PUT /api/files", s.auth(s.apiPut))
	mux.HandleFunc("POST /api/import", s.auth(s.apiImport))
	return mux
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	f := store.Filter{Query: r.URL.Query().Get("q"), Tool: r.URL.Query().Get("tool"), Limit: 200}
	rows, err := s.st.List(f)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	tools, _ := s.st.Tools()
	s.render(w, "list.html", map[string]any{"Filter": f, "Rows": rows, "Tools": tools})
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
	// 結果を呼び出しの直後に並べるため、tool_call_id で引けるようにする
	results := map[string]model.Part{}
	for _, m := range msgs {
		for _, p := range m.Parts {
			if p.Kind == model.ToolResult && p.ToolCallID != "" {
				results[p.ToolCallID] = p
			}
		}
	}
	s.render(w, "show.html", map[string]any{"C": c, "Msgs": msgs, "Results": results, "Q": r.URL.Query().Get("q")})
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
