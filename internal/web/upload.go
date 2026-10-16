package web

import (
	"archive/zip"
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
	"github.com/rurutheGeek/aihist/internal/adapter/chatgpt"
	"github.com/rurutheGeek/aihist/internal/adapter/claudeai"
	"github.com/rurutheGeek/aihist/internal/adapter/claudecode"
	"github.com/rurutheGeek/aihist/internal/adapter/codex"
	"github.com/rurutheGeek/aihist/internal/adapter/webexport"
	"github.com/rurutheGeek/aihist/internal/ingest"
)

// auth は /api に、設定の画面で発行したトークンか、起動時に渡したトークン（AIHIST_TOKEN）を求める。
func (s *Server) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		ok := s.token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
		if !ok && !s.st.CheckToken(got) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// sameOrigin は画面からの POST が、別のサイトから送らされたものでないかを確かめる。
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		o = r.Header.Get("Referer")
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
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
	d, err := s.side(r, nil)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	hosts := map[string]bool{}
	cs, _ := s.st.Counts()
	var hs []string
	for _, c := range cs {
		if !hosts[c.Host] {
			hosts[c.Host] = true
			hs = append(hs, c.Host)
		}
	}
	d["Hosts"], d["Page"] = hs, "upload"
	s.render(w, r, "upload.html", d)
}

// uploaded は上げたファイル1つの結果（画面が表示する）。
type uploaded struct {
	Name     string `json:"name"`
	Tool     string `json:"tool,omitempty"`
	Label    string `json:"label,omitempty"`
	Imported int    `json:"imported"`
	Skipped  int    `json:"skipped"`
	Error    string `json:"error,omitempty"`
	Href     string `json:"href,omitempty"`
}

// upload は画面から上げられたファイルを、中身からツールを見分けて原本に置き、取り込む。
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	l := lang(r)
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	host := strings.TrimSpace(r.FormValue("host"))
	if host == "" {
		host = "upload"
	}
	var out []uploaded
	for _, fh := range r.MultipartForm.File["files"] {
		u := uploaded{Name: fh.Filename}
		tool, _, err := s.store(host, fh)
		if err != nil {
			u.Error = tr(l, err.Error())
		} else if res, err := s.ingest(host, tool); err != nil {
			u.Error = err.Error()
		} else {
			u.Tool, u.Label, u.Imported, u.Skipped = tool, toolLabel(tool), res.Imported, res.Skipped
			u.Href = "/?" + url.Values{"tool": {tool}, "host": {host}}.Encode()
		}
		out = append(out, u)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func (s *Server) store(host string, fh *multipart.FileHeader) (tool, rel string, err error) {
	f, err := fh.Open()
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	name := path.Base(strings.ReplaceAll(fh.Filename, `\`, "/"))
	if strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".json") {
		tool = detectWeb(name, f, fh.Size)
		rel = "upload/" + time.Now().Format("20060102-150405") + "-" + name
	} else {
		tool, rel = detect(name, f)
	}
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

// detectWeb は Web のチャットの書き出し（zip か conversations.json）がどの AI のものかを見分ける。
func detectWeb(name string, f multipart.File, size int64) string {
	var head []byte
	if strings.HasSuffix(name, ".zip") {
		z, err := zip.NewReader(f, size)
		if err != nil {
			return ""
		}
		if head, err = webexport.FromZip(z); err != nil {
			return ""
		}
	} else {
		head, _ = io.ReadAll(io.LimitReader(f, 1<<20))
	}
	if len(head) > 1<<20 {
		head = head[:1<<20]
	}
	switch {
	case chatgpt.Is(head):
		return chatgpt.Tool
	case claudeai.Is(head):
		return claudeai.Tool
	}
	return ""
}
