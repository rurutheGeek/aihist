package push

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Found はこのマシンで見つかった AI の記録。
type Found struct {
	Tool  string
	Files int
	Bytes int64
}

// OpenCodeDB は OpenCode の DB の既定の場所。
func OpenCodeDB(home string) string {
	return filepath.Join(home, ".local", "share", "opencode", "opencode.db")
}

// Detect は既定の置き場所に記録がある AI を返す。
func Detect(home string) []Found {
	var out []Found
	for _, src := range Defaults(home) {
		f := Found{Tool: src.Tool}
		filepath.WalkDir(src.Dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			r, _ := filepath.Rel(src.Dir, p)
			if src.Want(filepath.ToSlash(r)) {
				if i, err := d.Info(); err == nil {
					f.Files++
					f.Bytes += i.Size()
				}
			}
			return nil
		})
		if f.Files > 0 {
			out = append(out, f)
		}
	}
	if st, err := os.Stat(OpenCodeDB(home)); err == nil {
		out = append(out, Found{Tool: "opencode", Files: 1, Bytes: st.Size()})
	}
	return out
}

// Selected は AIHIST_TOOLS（カンマ区切り。空なら全部）に tool が入っているか。
func Selected(list, tool string) bool {
	if strings.TrimSpace(list) == "" {
		return true
	}
	return slices.Contains(strings.Split(strings.ReplaceAll(list, " ", ""), ","), tool)
}
