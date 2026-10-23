package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// releases は新しい版を確かめる先。
const releases = "https://api.github.com/repos/rurutheGeek/aihist/releases/latest"

// CheckUpdates は 1 日 1 回、新しい版が出ていないかを確かめる。設定で止めたとき・AIHIST_UPDATE_CHECK=0・開発版では確かめない。
func (s *Server) CheckUpdates(ctx context.Context) {
	if os.Getenv("AIHIST_UPDATE_CHECK") == "0" || !strings.HasPrefix(s.Version, "v") {
		return
	}
	for {
		if s.st.Setting("update_check", "on") == "on" {
			if v, url, err := latestRelease(ctx); err == nil {
				s.latest.Store(&release{v, url})
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(24 * time.Hour):
		}
	}
}

type release struct{ Version, URL string }

func latestRelease(ctx context.Context) (string, string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, releases, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer res.Body.Close()
	var r struct {
		Tag string `json:"tag_name"`
		URL string `json:"html_url"`
	}
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil {
		return "", "", err
	}
	return r.Tag, r.URL, nil
}

// newer は、いまの版より新しい版が出ていればそれを返す。
func (s *Server) newer() *release {
	r := s.latest.Load()
	if r == nil || s.st.Setting("update_check", "on") != "on" || !Newer(r.Version, s.Version) {
		return nil
	}
	return r
}

// Newer は a が b より新しい版か（vX.Y.Z）。
func Newer(a, b string) bool {
	pa, pb := semver(a), semver(b)
	if pa == nil || pb == nil {
		return false
	}
	for i := range 3 {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func semver(v string) []int {
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "-")
	p := strings.Split(v, ".")
	if len(p) != 3 {
		return nil
	}
	n := make([]int, 3)
	for i, x := range p {
		var err error
		if n[i], err = strconv.Atoi(x); err != nil {
			return nil
		}
	}
	return n
}

func (s *Server) setUpdates(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	v := "off"
	if r.FormValue("check") == "on" {
		v = "on"
	}
	s.st.SetSetting("update_check", v)
	done(w, r, "/settings/general")
}
