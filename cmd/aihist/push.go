package main

import (
	"flag"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"

	"github.com/rurutheGeek/aihist/internal/push"
	"github.com/rurutheGeek/aihist/internal/sched"
)

// version はリリースのときに -ldflags "-X main.version=v1.2.3" で入る。
var version = "dev"

// 設定を受け取り直す間隔（タイマーは1分ごとに起きるので、毎回は聞かない）。
const refetch = 5 * time.Minute

func runPush(home, host string, args []string) {
	fs := flag.NewFlagSet("push", flag.ExitOnError)
	cf := clientConfig()
	server := fs.String("server", cf["AIHIST_SERVER"], "送り先 http://host:port")
	token := fs.String("token", cf["AIHIST_TOKEN"], "トークン")
	name := fs.String("host", or(cf["AIHIST_HOST"], host), "このマシンの名前")
	every := fs.Duration("every", 0, "この間隔で送り続ける（0 は1回だけ）")
	tick := fs.Bool("tick", false, "タイマーから: サーバーの設定の予定の時刻のときだけ送る")
	initTools := fs.String("init-tools", "", "初めての報告で、送る AI（カンマ区切り）")
	initAttach := fs.String("init-attachments", "", "初めての報告で、添付ファイルも送るか（1・0）")
	fs.Parse(args)
	if *server == "" {
		usage()
	}
	c := push.NewClient(*server, *token, *name)
	confDir := filepath.Dir(configPath())
	st := push.LoadState(confDir)
	now := time.Now()

	// サーバーの設定を受け取る（タイマーからは数分に1回だけ。届かなければ前に受け取ったもの）
	if !*tick || now.Sub(st.FetchedAt) > refetch || st.Config.Schedule == "" {
		var init *push.Init
		if *initTools != "" || *initAttach != "" {
			init = &push.Init{Tools: *initTools}
			if *initAttach != "" {
				b := *initAttach != "0"
				init.Attachments = &b
			}
		}
		if got, err := c.Report(home, version, init); err != nil {
			log.Printf("設定を受け取れない: %v", err)
		} else {
			st.Config, st.FetchedAt = got, now
		}
	}
	if *tick {
		if !due(st, now) {
			st.Save(confDir)
			return
		}
	}

	// 同じマシンで送る処理が重ならないように（導入の直後の1回とタイマーなど）
	unlock := func() {}
	if os.Getenv("AIHIST_UPDATED") == "" {
		if unlock = push.Lock(confDir); unlock == nil {
			if !*tick {
				log.Printf("ほかの aihist が送っている途中")
			}
			return
		}
		defer unlock()
	}

	// サーバーが配る実行ファイルに合わせて自分を新しくし、新しい方で送り直す（AIHIST_AUTO_UPDATE=0 で止める）
	if cf["AIHIST_AUTO_UPDATE"] != "0" && os.Getenv("AIHIST_UPDATED") == "" {
		if exe, err := c.Update(); err != nil {
			log.Printf("更新: %v", err)
		} else if exe != "" {
			st.Save(confDir)
			log.Printf("新しい aihist に入れ替えた")
			cmd := exec.Command(exe, os.Args[1:]...)
			cmd.Stdout, cmd.Stderr, cmd.Env = os.Stdout, os.Stderr, append(os.Environ(), "AIHIST_UPDATED=1")
			if err := cmd.Run(); err != nil {
				unlock()
				if e, ok := err.(*exec.ExitError); ok {
					os.Exit(e.ExitCode())
				}
				log.Fatal(err)
			}
			return
		}
	}

	for {
		sc := st.Config
		tools := sc.Tools
		if v, ok := cf["AIHIST_TOOLS"]; ok && v != "" {
			tools = v
		}
		if tools == "*" {
			tools = ""
		}
		c.Attach, c.Hide = sc.Attach, sc.Hide
		if c.Attach == "" && !sc.Attachments && sc.Schedule != "" {
			c.Attach = "-" // 種類を分ける前のサーバー
		}
		// 手元で 0 にしたときだけ、サーバーの設定によらず添付ファイルを抜く
		if cf["AIHIST_ATTACHMENTS"] == "0" || cf["AIHIST_IMAGES"] == "0" {
			c.Attach = "-"
		}
		days := sc.CleanupDays
		if v, err := strconv.Atoi(cf["AIHIST_CLEANUP"]); err == nil {
			days = v
		}
		for _, src := range push.Defaults(home) {
			if !push.Selected(tools, src.Tool) {
				continue
			}
			n, res, err := c.Push(src)
			switch {
			case err != nil:
				log.Printf("%s: %v", src.Tool, err)
				continue
			case n > 0:
				log.Printf("%s: %d ファイル送信、%s", src.Tool, n, res)
			}
			if n, size, err := c.Cleanup(src, days); err != nil {
				log.Printf("%s: 片付け: %v", src.Tool, err)
			} else if n > 0 {
				log.Printf("%s: %d 日より前の %d ファイル（%.1f MB）を手元から消した", src.Tool, days, n, float64(size)/(1<<20))
			}
		}
		if push.Selected(tools, "opencode") {
			if n, res, err := c.PushOpenCode(push.OpenCodeDB(home)); err != nil {
				log.Printf("opencode: %v", err)
			} else if n > 0 {
				log.Printf("opencode: %d 会話送信、%s", n, res)
			}
		}
		st.LastRun = time.Now()
		st.Save(confDir)
		if *every == 0 {
			return
		}
		time.Sleep(*every)
	}
}

// due は予定の時刻が来たか。予定が読めないときは送らない。
func due(st push.State, now time.Time) bool {
	spec, err := sched.Parse(st.Config.Schedule)
	if err != nil {
		log.Printf("予定が読めない: %v", err)
		return false
	}
	loc := time.Local
	if st.Config.TZ != "" {
		if l, err := time.LoadLocation(st.Config.TZ); err == nil {
			loc = l
		}
	}
	return spec.Due(st.LastRun.In(loc), now.In(loc))
}
