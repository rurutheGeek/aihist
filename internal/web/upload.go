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
			if got == "" {
				got = r.FormValue("token")
			}
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
	s.render(w, "upload.html", map[string]any{"Token": s.token != "", "Host": r.FormValue("host")})
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
	s.render(w, "upload.html", map[string]any{"Token": s.token != "", "Host": host, "Lines": lines})
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
