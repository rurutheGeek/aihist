package main

import (
	"context"
	"fmt"
	"log"

	"github.com/rurutheGeek/aihist/internal/push"
	"github.com/rurutheGeek/aihist/internal/web"
)

// runUpdate は GitHub Releases の最新版に入れ替える。動いている serve は入れ替えたあと起動し直す。
func runUpdate() {
	ctx := context.Background()
	tag, err := push.Latest(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if !web.Newer(tag, version) && version != "dev" {
		fmt.Println(m("already up to date:", "最新です:"), version)
		return
	}
	if err := push.SelfUpdate(ctx, tag); err != nil {
		log.Fatal(err)
	}
	fmt.Printf(m("updated %s → %s. Restart aihist serve to use it.\n", "%s → %s に更新した。aihist serve は起動し直すと新しい版になる。\n"), version, tag)
}
