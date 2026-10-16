// aihist は AI ツールの会話履歴を取り込み、検索・閲覧・書き出しする。
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/rurutheGeek/aihist/internal/adapter/antigravity"
	"github.com/rurutheGeek/aihist/internal/adapter/claudecode"
	"github.com/rurutheGeek/aihist/internal/adapter/codex"
	"github.com/rurutheGeek/aihist/internal/adapter/opencode"
	"github.com/rurutheGeek/aihist/internal/export"
	"github.com/rurutheGeek/aihist/internal/ingest"
	"github.com/rurutheGeek/aihist/internal/push"
	"github.com/rurutheGeek/aihist/internal/raw"
	"github.com/rurutheGeek/aihist/internal/store"
	"github.com/rurutheGeek/aihist/internal/web"
)

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  aihist [-db path] import claude [dir]       既定 ~/.claude/projects
  aihist [-db path] import codex [dir]        既定 ~/.codex
  aihist [-db path] import antigravity [dir]  既定 ~/.gemini/antigravity-ide/conversations
  aihist [-db path] import opencode [db]      既定 ~/.local/share/opencode/opencode.db
  aihist [-db path] serve [-addr :8080] [-raw dir] [-token T]
  aihist push -server URL [-token T] [-host 名前] [-every 10m]
  aihist [-db path] search <語>
  aihist [-db path] export [-thinking] [-no-tools] [-max N] <会話IDの先頭>

トークンは環境変数 AIHIST_TOKEN でも渡せる。
`)
	os.Exit(2)
}

func main() {
	home, _ := os.UserHomeDir()
	data := filepath.Join(home, ".local/share/aihist")
	dbPath := flag.String("db", filepath.Join(data, "aihist.db"), "SQLite のパス")
	flag.Usage = usage
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		usage()
	}
	host, _ := os.Hostname()

	// push は DB を使わない
	if args[0] == "push" {
		fs := flag.NewFlagSet("push", flag.ExitOnError)
		server := fs.String("server", "", "送り先 http://host:port")
		token := fs.String("token", os.Getenv("AIHIST_TOKEN"), "トークン")
		name := fs.String("host", host, "このマシンの名前")
		every := fs.Duration("every", 0, "この間隔で送り続ける（0 は1回だけ）")
		fs.Parse(args[1:])
		if *server == "" {
			usage()
		}
		c := push.NewClient(*server, *token, *name)
		for {
			for _, src := range push.Defaults(home) {
				n, res, err := c.Push(src)
				switch {
				case err != nil:
					log.Printf("%s: %v", src.Tool, err)
				case n > 0:
					log.Printf("%s: %d ファイル送信、%s", src.Tool, n, res)
				}
			}
			if *every == 0 {
				return
			}
			time.Sleep(*every)
		}
	}

	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o700); err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()

	switch args[0] {
	case "import":
		if len(args) < 2 {
			usage()
		}
		tools := map[string]struct{ tool, def string }{
			"claude":      {claudecode.Tool, filepath.Join(home, ".claude/projects")},
			"codex":       {codex.Tool, filepath.Join(home, ".codex")},
			"antigravity": {antigravity.Tool, filepath.Join(home, ".gemini/antigravity-ide/conversations")},
			"opencode":    {opencode.Tool, filepath.Join(home, ".local/share/opencode/opencode.db")},
		}
		t, ok := tools[args[1]]
		if !ok {
			usage()
		}
		dir := t.def
		if len(args) > 2 {
			dir = args[2]
		}
		var res ingest.Result
		if res, err = ingest.Run(st, t.tool, host, dir); err == nil {
			log.Printf("%s: %s", args[1], res)
		}
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		addr := fs.String("addr", ":8080", "待ち受けアドレス")
		rawDir := fs.String("raw", filepath.Join(data, "raw"), "送られてきた原本の置き場所")
		token := fs.String("token", os.Getenv("AIHIST_TOKEN"), "書き込みに要るトークン（空なら不要）")
		fs.Parse(args[1:])
		log.Printf("listening on %s", *addr)
		err = http.ListenAndServe(*addr, web.New(st, raw.Store{Dir: *rawDir}, *token).Handler())
	case "search":
		if len(args) < 2 {
			usage()
		}
		var rows []store.ConvRow
		rows, err = st.List(store.Filter{Query: args[1], Limit: 30})
		for _, r := range rows {
			fmt.Printf("%s  %-11s %s  %s\n   %s\n", r.UpdatedAt.Format("2006-01-02"), r.Tool, r.ExternalID, r.Title, r.Snippet)
		}
	case "export":
		fs := flag.NewFlagSet("export", flag.ExitOnError)
		thinking := fs.Bool("thinking", false, "思考を含める")
		noTools := fs.Bool("no-tools", false, "ツールの呼び出しと結果を除く")
		max := fs.Int("max", export.Default.MaxToolOutput, "ツール結果を切る文字数（0 は切らない）")
		fs.Parse(args[1:])
		if fs.NArg() != 1 {
			usage()
		}
		var id int64
		if id, err = st.FindByExternal(fs.Arg(0)); err == nil {
			c, msgs, gerr := st.Get(id)
			if gerr != nil {
				log.Fatal(gerr)
			}
			o := export.Default
			o.Thinking, o.Tools, o.MaxToolOutput = *thinking, !*noTools, *max
			export.Markdown(os.Stdout, c, msgs, o)
		}
	default:
		usage()
	}
	if err != nil {
		log.Fatal(err)
	}
}
