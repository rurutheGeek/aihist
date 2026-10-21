// Package skillsrc は Skill（SKILL.md のあるフォルダ）を外から読み込む。
package skillsrc

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/rurutheGeek/aihist/internal/store"
)

// 大きすぎるリポジトリやファイルは読まない。
const (
	maxArchive  = 100 << 20
	maxFile     = 5 << 20
	maxSkillSum = 30 << 20
)

// Repo は GitHub の URL から読み取った場所。
type Repo struct {
	Owner, Name, Ref, Dir string
}

func (r Repo) String() string {
	s := "https://github.com/" + r.Owner + "/" + r.Name
	if r.Ref != "" {
		s += "/tree/" + r.Ref
		if r.Dir != "" {
			s += "/" + r.Dir
		}
	}
	return s
}

// ParseGitHub は github.com/owner/repo（/tree/ref/dir、/blob/ref/dir/SKILL.md）を読む。
func ParseGitHub(raw string) (Repo, error) {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Host != "github.com" && u.Host != "www.github.com") {
		return Repo{}, errors.New("GitHub の URL ではない")
	}
	p := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(p) < 2 || p[0] == "" || p[1] == "" {
		return Repo{}, errors.New("GitHub の URL ではない")
	}
	r := Repo{Owner: p[0], Name: strings.TrimSuffix(p[1], ".git")}
	if len(p) >= 4 && (p[2] == "tree" || p[2] == "blob") {
		r.Ref, r.Dir = p[3], strings.Join(p[4:], "/")
		if p[2] == "blob" {
			r.Dir = path.Dir(r.Dir)
			if r.Dir == "." {
				r.Dir = ""
			}
		}
	}
	return r, nil
}

var client = &http.Client{Timeout: 2 * time.Minute}

// Commit は ref の今のコミット（調べられなければ空）。
func Commit(ctx context.Context, r Repo) string {
	ref := r.Ref
	if ref == "" {
		ref = "HEAD"
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/"+r.Owner+"/"+r.Name+"/commits/"+url.PathEscape(ref), nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	var c struct {
		SHA string `json:"sha"`
	}
	if res.StatusCode != 200 || json.NewDecoder(res.Body).Decode(&c) != nil {
		return ""
	}
	return c.SHA
}

// Fetch はリポジトリを読み、r.Dir の下にある Skill を全部返す（Source と Version を付ける）。
func Fetch(ctx context.Context, r Repo) ([]store.Skill, error) {
	ver := Commit(ctx, r)
	ref := ver
	if ref == "" {
		ref = r.Ref
	}
	if ref == "" {
		ref = "HEAD"
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://codeload.github.com/"+r.Owner+"/"+r.Name+"/tar.gz/"+url.PathEscape(ref), nil)
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return nil, errors.New("リポジトリが見つからない（公開されていない・URL が違う）")
	}
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub から読めない（%s）", res.Status)
	}
	files, err := untar(io.LimitReader(res.Body, maxArchive))
	if err != nil {
		return nil, err
	}
	ks := Collect(files, r.Dir)
	for i := range ks {
		rr := r
		rr.Ref = r.Ref
		if rr.Ref == "" {
			rr.Ref = "HEAD"
		}
		rr.Dir = ks[i].Source
		ks[i].Source, ks[i].Version = rr.String(), ver
	}
	return ks, nil
}

// untar は tar.gz を読み、先頭のフォルダ（repo-ref/）を外したパスとファイルの中身を返す。
func untar(rd io.Reader) (map[string][]byte, error) {
	gz, err := gzip.NewReader(rd)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg || h.Size > maxFile {
			continue
		}
		_, p, ok := strings.Cut(h.Name, "/")
		if !ok || p == "" {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return nil, err
		}
		out[p] = b
	}
	return out, nil
}

// Collect はファイルの集まりから、dir の下の SKILL.md のあるフォルダを Skill にする。
// Source には仮にそのフォルダのパスを入れる。中に別の Skill があるフォルダは、その分を除く。
func Collect(files map[string][]byte, dir string) []store.Skill {
	dir = strings.Trim(dir, "/")
	var roots []string
	for p := range files {
		if path.Base(p) != "SKILL.md" {
			continue
		}
		d := path.Dir(p)
		if d == "." {
			d = ""
		}
		if dir == "" || d == dir || strings.HasPrefix(d, dir+"/") {
			roots = append(roots, d)
		}
	}
	sort.Strings(roots)
	inner := func(p, root string) bool {
		for _, o := range roots {
			if o != root && len(o) > len(root) && (root == "" || strings.HasPrefix(o, root+"/")) && strings.HasPrefix(p, o+"/") {
				return true
			}
		}
		return false
	}
	var out []store.Skill
	for _, root := range roots {
		pre := ""
		if root != "" {
			pre = root + "/"
		}
		name, desc, body := store.ParseSkillMD(string(files[pre+"SKILL.md"]))
		if name == "" {
			name = path.Base("/" + root)
			if name == "/" {
				name = "skill"
			}
		}
		k := store.Skill{Name: name, Description: desc, Body: body, Source: root, Files: []store.SkillFile{}}
		sum := 0
		var ps []string
		for p := range files {
			if strings.HasPrefix(p, pre) && p != pre+"SKILL.md" && !inner(p, root) {
				ps = append(ps, p)
			}
		}
		sort.Strings(ps)
		for _, p := range ps {
			if sum += len(files[p]); sum > maxSkillSum {
				break
			}
			k.Files = append(k.Files, store.SkillFile{Path: strings.TrimPrefix(p, pre), Size: len(files[p]), Data: files[p]})
		}
		out = append(out, k)
	}
	return out
}
