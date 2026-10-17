package push

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// ServerConfig はサーバーの設定の画面で決め、各マシンへ配る設定。
type ServerConfig struct {
	Schedule    string `json:"schedule"` // cron の式
	TZ          string `json:"tz"`       // 予定の時刻の地域（空ならこのマシンの時刻）
	CleanupDays int    `json:"cleanup_days"`
	Tools       string `json:"tools"` // "*" は全部
	Attachments bool   `json:"attachments"`
}

// Init は初めて報告するときに渡す、導入で選んだ設定。
type Init struct {
	Tools       string `json:"tools,omitempty"`
	Attachments *bool  `json:"attachments,omitempty"`
}

// Report は見つけた AI を知らせ、このマシンの設定を受け取る。
func (c *Client) Report(home, version string, init *Init) (ServerConfig, error) {
	var det []map[string]any
	for _, f := range Detect(home) {
		det = append(det, map[string]any{"tool": f.Tool, "files": f.Files, "bytes": f.Bytes})
	}
	body, _ := json.Marshal(map[string]any{"host": c.Host, "version": version, "detected": det, "init": init})
	req, err := http.NewRequest("POST", c.Server+"/api/machine", bytes.NewReader(body))
	if err != nil {
		return ServerConfig{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return ServerConfig{}, err
	}
	defer res.Body.Close()
	var cf ServerConfig
	if res.StatusCode == 404 {
		// 古いサーバー: 片付けの日数だけ読む
		c.getJSON("/api/client", url.Values{"host": {c.Host}}, &cf)
		cf.Tools, cf.Attachments = "*", true
		return cf, nil
	}
	if res.StatusCode/100 != 2 {
		return ServerConfig{}, &httpError{res.Status}
	}
	return cf, json.NewDecoder(res.Body).Decode(&cf)
}

type httpError struct{ s string }

func (e *httpError) Error() string { return e.s }

// State はタイマーで起きたときに使う、前に送った時刻と、受け取った設定の写し。
type State struct {
	LastRun   time.Time    `json:"last_run"`
	FetchedAt time.Time    `json:"fetched_at"`
	Config    ServerConfig `json:"config"`
}

func statePath(confDir string) string { return filepath.Join(confDir, "state.json") }

func LoadState(confDir string) State {
	var s State
	if b, err := os.ReadFile(statePath(confDir)); err == nil {
		json.Unmarshal(b, &s)
	}
	return s
}

func (s State) Save(confDir string) error {
	if err := os.MkdirAll(confDir, 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(s)
	tmp := statePath(confDir) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(confDir))
}

// Lock は、同じマシンで送る処理が重ならないようにする。前の処理が 6 時間より前に残した印は捨てる。
// 取れなければ nil を返す。
func Lock(confDir string) func() {
	p := filepath.Join(confDir, "push.lock")
	if st, err := os.Stat(p); err == nil && time.Since(st.ModTime()) > 6*time.Hour {
		os.Remove(p)
	}
	os.MkdirAll(confDir, 0o700)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	f.Close()
	return func() { os.Remove(p) }
}
