package web

import (
	"bufio"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/adapter/antigravity"
	"github.com/rurutheGeek/aihist/internal/adapter/claudecode"
	"github.com/rurutheGeek/aihist/internal/adapter/codex"
	"github.com/rurutheGeek/aihist/internal/ingest"
)

func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		h(w, r)
	}
}

func (s *Server) apiList(w http.ResponseWriter, r *http.Request) {
	m, err := s.raw.List(r.FormValue("host"), r.FormValue("tool"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m)
}

func (s *Server) apiPut(w http.ResponseWriter, r *http.Request) {
	var mt time.Time
	if n, err := strconv.ParseInt(r.Header.Get("X-Mtime"), 10, 64); err == nil {
		mt = time.Unix(0, n)
	}
	q := r.URL.Query()
	if err := s.raw.Put(q.Get("host"), q.Get("tool"), q.Get("path"), r.Body, mt); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// apiFile は原本1ファイルを返す（元の AI へ戻すときに使う）。
func (s *Server) apiFile(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, err := s.raw.Path(q.Get("host"), q.Get("tool"), q.Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, p)
}

// apiFind は会話 ID の先頭から会話を探す。
func (s *Server) apiFind(w http.ResponseWriter, r *http.Request) {
	id, err := s.st.FindByExternal(r.URL.Query().Get("id"))
	if err != nil {
		http.Error(w, "見つからない", http.StatusNotFound)
		return
	}
	c, _, err := s.st.Get(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(c)
}

func (s *Server) apiImport(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	res, err := s.ingest(q.Get("host"), q.Get("tool"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"Result": res.String()})
}

func (s *Server) ingest(host, tool string) (ingest.Result, error) {
	dir, err := s.raw.Root(host, tool)
	if err != nil {
		return ingest.Result{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := ingest.Run(s.st, tool, host, dir)
	log.Printf("%s/%s: %s %v", host, tool, res, err)
	return res, err
}

func (s *Server) uploadForm(w http.ResponseWriter, r *http.Request) {
	s.renderUpload(w, r, r.FormValue("host"), nil)
}

func (s *Server) renderUpload(w http.ResponseWriter, r *http.Request, host string, lines []string) {
	d, err := s.side(&http.Request{URL: &url.URL{}})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	d["Host"], d["Lines"] = host, lines
	s.render(w, "upload.html", d)
}

// upload は画面から上げられたファイルを、中身からツールを見分けて原本に置き、取り込む。
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	host := strings.TrimSpace(r.FormValue("host"))
	if host == "" {
		host = "upload"
	}
	tools := map[string]bool{}
	var lines []string
	for _, fh := range r.MultipartForm.File["files"] {
		tool, rel, err := s.store(host, fh)
		if err != nil {
			lines = append(lines, fh.Filename+": "+err.Error())
			continue
		}
		tools[tool] = true
		lines = append(lines, fh.Filename+" → "+tool+"/"+rel)
	}
	for tool := range tools {
		res, err := s.ingest(host, tool)
		if err != nil {
			lines = append(lines, tool+": "+err.Error())
			continue
		}
		lines = append(lines, tool+": "+res.String())
	}
	s.renderUpload(w, r, host, lines)
}

func (s *Server) store(host string, fh *multipart.FileHeader) (tool, rel string, err error) {
	f, err := fh.Open()
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	name := path.Base(strings.ReplaceAll(fh.Filename, `\`, "/"))
	tool, rel = detect(name, f)
	if tool == "" {
		return "", "", errors.New("会話の記録ではない")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", "", err
	}
	return tool, rel, s.raw.Put(host, tool, rel, f, time.Now())
}

// detect はファイル名と先頭行から、どのツールの記録かと置き場所を決める。
func detect(name string, r io.Reader) (tool, rel string) {
	switch {
	case strings.HasSuffix(name, ".db"), strings.HasSuffix(name, ".db-wal"):
		return antigravity.Tool, name
	case name == "session_index.jsonl":
		return codex.Tool, name
	}
	if !strings.HasSuffix(name, ".jsonl") {
		return "", ""
	}
	first, _ := bufio.NewReader(r).ReadString('\n')
	switch {
	case strings.Contains(first, `"session_meta"`):
		return codex.Tool, "sessions/upload/" + name
	case strings.Contains(first, `"sessionId"`):
		return claudecode.Tool, "upload/" + name
	}
	return "", ""
}

// setup は各マシンへ入れるコマンド（定期的に送る・消す）の画面。トークンを埋めて出す。
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	d, err := s.side(&http.Request{URL: &url.URL{}})
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	o := s.origin(r)
	tok := s.token
	if tok == "" {
		tok = "-"
	}
	d["Cmds"] = []map[string]string{
		{"OS": "Linux・macOS・WSL",
			"Install":   "curl -fsSL " + o + "/api/install.sh | AIHIST_TOKEN=" + tok + " sh",
			"Uninstall": "curl -fsSL " + o + "/api/uninstall.sh | sh",
			"Script":    "/api/install.sh", "Name": "install.sh", "UScript": "/api/uninstall.sh", "UName": "uninstall.sh"},
		{"OS": "Windows",
			"Install":   `$env:AIHIST_TOKEN="` + tok + `"; irm ` + o + "/api/install.ps1 | iex",
			"Uninstall": "irm " + o + "/api/uninstall.ps1 | iex",
			"Script":    "/api/install.ps1", "Name": "install.ps1", "UScript": "/api/uninstall.ps1", "UName": "uninstall.ps1"},
	}
	s.render(w, "setup.html", d)
}
