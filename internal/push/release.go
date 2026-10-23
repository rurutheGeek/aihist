package push

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Repo は公開しているリリースの置き場所。
const Repo = "rurutheGeek/aihist"

// Latest は GitHub Releases の最新版の名前（vX.Y.Z）。
func Latest(ctx context.Context) (string, error) {
	var r struct {
		Tag string `json:"tag_name"`
	}
	res, err := get(ctx, "https://api.github.com/repos/"+Repo+"/releases/latest")
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(&r); err != nil || r.Tag == "" {
		return "", fmt.Errorf("最新版がわからない: %v", err)
	}
	return r.Tag, nil
}

func get(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	res, err := http.DefaultClient.Do(req)
	if err == nil && res.StatusCode != http.StatusOK {
		res.Body.Close()
		err = fmt.Errorf("%s: %s", url, res.Status)
	}
	return res, err
}

// SelfUpdate は、このマシン向けの tag の実行ファイルを GitHub Releases から落とし、SHA256SUMS で確かめて自分と入れ替える。
func SelfUpdate(ctx context.Context, tag string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	os.Remove(exe + ".old")
	base := "https://github.com/" + Repo + "/releases/download/" + tag + "/"
	res, err := get(ctx, base+"SHA256SUMS")
	if err != nil {
		return err
	}
	want := ""
	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		if f := strings.Fields(sc.Text()); len(f) == 2 && strings.TrimPrefix(f[1], "*") == binName() {
			want = f[0]
		}
	}
	res.Body.Close()
	if want == "" {
		return fmt.Errorf("%s に %s が無い", tag, binName())
	}
	if have, _ := fileSum(exe); have == want {
		return nil
	}
	res, err = get(ctx, base+binName())
	if err != nil {
		return err
	}
	defer res.Body.Close()
	return replace(exe, res.Body, want)
}
