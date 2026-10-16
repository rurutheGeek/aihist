// Package webexport は Web の AI チャットの書き出し（zip の中か、そのままの conversations.json）を読む。
package webexport

import (
	"archive/zip"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Files は root 以下の書き出し（*.zip・*.json）を古い順に返す。
func Files(root string) ([]string, error) {
	type f struct {
		p string
		t int64
	}
	var list []f
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !(strings.HasSuffix(p, ".zip") || strings.HasSuffix(p, ".json")) {
			return nil
		}
		if st, err := d.Info(); err == nil {
			list = append(list, f{p, st.ModTime().UnixNano()})
		}
		return nil
	})
	sort.SliceStable(list, func(i, j int) bool { return list[i].t < list[j].t })
	out := make([]string, len(list))
	for i, x := range list {
		out[i] = x.p
	}
	return out, err
}

// Conversations は書き出し1つの conversations.json の中身を返す。
func Conversations(path string) ([]byte, error) {
	if !strings.HasSuffix(path, ".zip") {
		return os.ReadFile(path)
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, err
	}
	defer z.Close()
	return FromZip(&z.Reader)
}

// FromZip は zip の中の conversations.json を読む。
func FromZip(z *zip.Reader) ([]byte, error) {
	for _, f := range z.File {
		if filepath.Base(f.Name) == "conversations.json" {
			r, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer r.Close()
			return io.ReadAll(r)
		}
	}
	return nil, fmt.Errorf("conversations.json が無い")
}
