// Package raw は送られてきた元データ（原本）を <dir>/<host>/<tool>/<相対パス> に置く。
// 置いた形は各ツールの既定の置き場所と同じなので、ingest にそのまま読ませられる。
// 原本を残すので、アダプタを直したときは取り込み直せる。
package raw

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Info はファイルの大きさと更新時刻（UnixNano）。差分の判定に使う。
type Info struct {
	Size  int64 `json:"size"`
	MTime int64 `json:"mtime"`
}

type Store struct{ Dir string }

var name = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

var ErrBadPath = errors.New("使えないパス")

// Root は host・tool の原本の置き場所。
func (s Store) Root(host, tool string) (string, error) {
	if !name.MatchString(host) || !name.MatchString(tool) || host == "." || host == ".." || tool == "." || tool == ".." {
		return "", ErrBadPath
	}
	return filepath.Join(s.Dir, host, tool), nil
}

func (s Store) path(host, tool, rel string) (string, error) {
	root, err := s.Root(host, tool)
	if err != nil {
		return "", err
	}
	rel = path.Clean("/" + strings.ReplaceAll(rel, `\`, "/"))[1:]
	if rel == "" || rel == "." {
		return "", ErrBadPath
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

// Put は1ファイルを書く。途中で切れても前の版が残るよう、別名に書いてから置き換える。
func (s Store) Put(host, tool, rel string, r io.Reader, mtime time.Time) error {
	p, err := s.path(host, tool, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".up-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(f.Name(), mtime, mtime); err != nil {
			return err
		}
	}
	return os.Rename(f.Name(), p)
}

// List は host・tool の原本の一覧（相対パスは / 区切り）。
func (s Store) List(host, tool string) (map[string]Info, error) {
	root, err := s.Root(host, tool)
	if err != nil {
		return nil, err
	}
	out := map[string]Info{}
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".up-") {
			return nil
		}
		st, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		out[filepath.ToSlash(rel)] = Info{st.Size(), st.ModTime().UnixNano()}
		return nil
	})
	return out, err
}
