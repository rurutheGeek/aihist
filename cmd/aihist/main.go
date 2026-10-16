// aihist は AI ツールの会話履歴を取り込み、検索・閲覧・書き出しする。
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/rurutheGeek/aihist/internal/adapter/claudecode"
	"github.com/rurutheGeek/aihist/internal/adapter/opencode"
	"github.com/rurutheGeek/aihist/internal/export"
	"github.com/rurutheGeek/aihist/internal/model"
	"github.com/rurutheGeek/aihist/internal/store"
	"github.com/rurutheGeek/aihist/internal/web"
)

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  aihist [-db path] import claude [dir]     既定 ~/.claude/projects
  aihist [-db path] import opencode [db]    既定 ~/.local/share/opencode/opencode.db
  aihist [-db path] serve [-addr :8080]
  aihist [-db path] search <語>
  aihist [-db path] export [-thinking] [-no-tools] [-max N] <会話IDの先頭>
`)
	os.Exit(2)
}

func main() {
	home, _ := os.UserHomeDir()
	dbPath := flag.String("db", filepath.Join(home, ".local/share/aihist/aihist.db"), "SQLite のパス")
	flag.Usage = usage
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		usage()
	}
	if err := os.MkdirAll(filepath.Dir(*dbPath), 0o700); err != nil {
		log.Fatal(err)
	}
	st, err := store.Open(*dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	host, _ := os.Hostname()

	switch args[0] {
	case "import":
		if len(args) < 2 {
			usage()
		}
		switch args[1] {
		case "claude":
			dir := filepath.Join(home, ".claude/projects")
			if len(args) > 2 {
				dir = args[2]
			}
			err = importClaude(st, host, dir)
		case "opencode":
			db := filepath.Join(home, ".local/share/opencode/opencode.db")
			if len(args) > 2 {
				db = args[2]
			}
			err = importOpenCode(st, host, db)
		default:
			usage()
		}
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ExitOnError)
		addr := fs.String("addr", ":8080", "待ち受けアドレス")
		fs.Parse(args[1:])
		log.Printf("listening on %s", *addr)
		err = http.ListenAndServe(*addr, web.New(st).Handler())
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

func importClaude(st *store.Store, host, dir string) error {
	files, err := claudecode.Files(dir)
	if err != nil {
		return err
	}
	var n, skip int
	for _, f := range files {
		id, _ := claudecode.ExternalID(f)
		stamp := claudecode.Stamp(f)
		if st.Stamp(claudecode.Tool, host, id) == stamp {
			skip++
			continue
		}
		c, err := claudecode.Parse(f)
		if err != nil {
			log.Printf("%s: %v", f, err)
			continue
		}
		if len(c.Messages) == 0 {
			continue
		}
		if err := put(st, host, c, stamp); err != nil {
			return err
		}
		n++
	}
	log.Printf("claude: %d 件取り込み、%d 件は変更なし", n, skip)
	return nil
}

func importOpenCode(st *store.Store, host, path string) error {
	db, err := opencode.Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	sessions, err := db.Sessions()
	if err != nil {
		return err
	}
	var n, skip int
	for _, s := range sessions {
		if st.Stamp(opencode.Tool, host, s.ID) == s.Stamp() {
			skip++
			continue
		}
		c, err := db.Load(s)
		if err != nil {
			return fmt.Errorf("%s: %w", s.ID, err)
		}
		c.SourcePath = path
		if err := put(st, host, c, s.Stamp()); err != nil {
			return err
		}
		n++
	}
	log.Printf("opencode: %d 件取り込み、%d 件は変更なし", n, skip)
	return nil
}

func put(st *store.Store, host string, c *model.Conversation, stamp string) error {
	c.Host = host
	return st.Put(c, stamp)
}
