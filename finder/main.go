// finder は importer/hm.db を探索するための内部ツール。
//
//	go run . index    索引 (index.db) を作り直す
//	go run . serve    Web サーバーを起動する (既定)
//
// hm.db には決して書き込まない。詳細は README.md。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/typewriter/home-museum/finder/internal/index"
	"github.com/typewriter/home-museum/finder/internal/store"
	"github.com/typewriter/home-museum/finder/internal/web"
)

func main() {
	log.SetFlags(log.Ltime)

	fs := flag.NewFlagSet("finder", flag.ExitOnError)
	dbPath := fs.String("db", env("DATABASE_PATH", "../importer/hm.db"), "hm.db のパス (読み取り専用で開く)")
	indexPath := fs.String("index", env("FINDER_INDEX", "./index.db"), "検索インデックスのパス")
	addr := fs.String("addr", env("FINDER_ADDR", "127.0.0.1:8081"), "待ち受けアドレス")
	timeout := fs.Duration("timeout", 30*time.Second, "1 クエリの上限時間")
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `finder — importer/hm.db を探索する内部ツール

使い方:
  finder [フラグ] [serve|index]

  serve   Web サーバーを起動する (既定)
  index   hm.db から index.db を作り直す

フラグ:
`)
		fs.PrintDefaults()
	}

	// サブコマンドはフラグの前後どちらに書いてもよいようにする。
	args := os.Args[1:]
	cmd := "serve"
	var rest []string
	for _, a := range args {
		switch a {
		case "serve", "index":
			cmd = a
		default:
			rest = append(rest, a)
		}
	}
	if err := fs.Parse(rest); err != nil {
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "index":
		err = index.Build(ctx, *dbPath, *indexPath, os.Stderr)
	case "serve":
		err = serve(ctx, serveOptions{
			DBPath:    *dbPath,
			IndexPath: *indexPath,
			Addr:      *addr,
			Timeout:   *timeout,
		})
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("%v", err)
	}
}

type serveOptions struct {
	DBPath, IndexPath, Addr string
	Timeout                 time.Duration
}

func serve(ctx context.Context, o serveOptions) error {
	st, err := store.Open(o.DBPath, o.IndexPath)
	if err != nil {
		return err
	}
	defer st.Close()

	log.Printf("hm.db  %s (読み取り専用)", st.DBPath)
	log.Printf("index  %s", st.IndexPath)

	srv, err := web.New(st, web.Options{
		QueryTimeout: o.Timeout,
	})
	if err != nil {
		return err
	}
	hs := &http.Server{
		Addr:              o.Addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		sd, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(sd)
	}()

	log.Printf("http://%s", o.Addr)
	return hs.ListenAndServe()
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
