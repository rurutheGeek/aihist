package ai

import (
	"regexp"
	"slices"
	"strings"

	"github.com/rurutheGeek/aihist/internal/scrub"
)

// 個人の名前としては扱わない、パスによく出る言葉
var commonWords = map[string]bool{
	"home": true, "users": true, "user": true, "project": true, "projects": true, "src": true, "work": true, "repos": true,
	"repo": true, "code": true, "documents": true, "desktop": true, "downloads": true, "worktrees": true, "tmp": true,
	"var": true, "opt": true, "root": true, "dev": true, "main": true, "test": true, "app": true, "apps": true,
	"cloud": true, "server": true, "local": true, "data": true, "upload": true,
}

var (
	homeName = regexp.MustCompile(`(?:/home/|/Users/|\\Users\\)([^/\\\s"']+)`)
	urlRe    = regexp.MustCompile(`https?://[^\s)>\]"'` + "`" + `]+`)
	splitter = regexp.MustCompile(`[^\p{L}\p{N}]+`)
)

// Personal は会話の付帯情報（マシン名・作業フォルダ・ブランチ）と本文のパスから、個人の名前らしい言葉を集める。
func Personal(host, cwd, branch, text string) []string {
	var out []string
	add := func(s string) {
		for _, w := range splitter.Split(strings.ToLower(s), -1) {
			if len([]rune(w)) >= 4 && !commonWords[w] && !slices.Contains(out, w) {
				out = append(out, w)
			}
		}
	}
	add(host)
	add(cwd)
	if branch != "main" && branch != "master" {
		add(branch)
	}
	for _, m := range homeName.FindAllStringSubmatch(text, -1) {
		add(m[1])
	}
	return out
}

// Leaks は記事 text に混じっているかもしれない個人の情報（words・URL・IP・メール・秘密）を返す。
func Leaks(text string, words []string) []string {
	var out []string
	add := func(s string) {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	low := strings.ToLower(text)
	for _, w := range words {
		if strings.Contains(low, w) {
			add(w)
		}
	}
	for _, u := range urlRe.FindAllString(text, -1) {
		add(u)
	}
	all := strings.Join(scrub.Kinds, ",")
	if scrub.Text(text, all) != text {
		// どこが消えるかを1つずつ見る
		for _, f := range strings.Fields(text) {
			if scrub.Text(f, all) != f {
				add(strings.Trim(f, "`'\"、。,()（）"))
			}
		}
	}
	return out
}
