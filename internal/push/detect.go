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

// Selected は送る AI の並び（カンマ区切り）に tool が入っているか。空と "*" は全部。
// "*,-opencode" は opencode 以外の全部（あとで見つかったものも送る）。
func Selected(list, tool string) bool {
	l := strings.Split(strings.ReplaceAll(list, " ", ""), ",")
	if l[0] == "" || l[0] == "*" {
		return !slices.Contains(l, "-"+tool)
	}
	return slices.Contains(l, tool)
}
