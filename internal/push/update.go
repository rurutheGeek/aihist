package push

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// binName はこのマシン向けにサーバーが配る実行ファイルの名前。
func binName() string {
	n := "aihist-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		n += ".exe"
	}
	return n
}

// Update は、サーバーが配る実行ファイルが自分と違えば、それに入れ替える。入れ替えたら新しいパスを返す。
// サーバーが配っていない（-bin なし）ときは何もしない。
func (c *Client) Update() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	os.Remove(exe + ".old") // Windows で前回どかした古いもの
	res, err := c.req("GET", "/api/bin/"+binName(), url.Values{"sum": {"1"}}, nil)
	if err != nil {
		if strings.Contains(err.Error(), "404") {
			return "", nil
		}
		return "", err
	}
	b, _ := io.ReadAll(io.LimitReader(res.Body, 200))
	res.Body.Close()
	want := strings.TrimSpace(string(b))
	have, err := fileSum(exe)
	if err != nil {
		return "", err
	}
	if want == have {
		return "", nil
	}
	res, err = c.req("GET", "/api/bin/"+binName(), nil, nil)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	tmp := exe + ".new"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), res.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != want {
		err = fmt.Errorf("落としたものの sha256 が合わない")
	}
	if err != nil {
		os.Remove(tmp)
		return "", err
	}
	// Windows は動いている exe を上書きできないが、名前は変えられる
	if runtime.GOOS == "windows" {
		if err := os.Rename(exe, exe+".old"); err != nil {
			os.Remove(tmp)
			return "", err
		}
	}
	if err := os.Rename(tmp, exe); err != nil {
		return "", err
	}
	return exe, nil
}

func fileSum(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
