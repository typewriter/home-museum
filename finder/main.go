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

	"github.com/typewriter/home-museum/finder/internal/imagecache"
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
	allowSQL := fs.Bool("sql", true, "読み取り専用 SQL コンソールを有効にする")
	timeout := fs.Duration("timeout", 30*time.Second, "1 クエリの上限時間")
	imgStore := fs.String("images", env("FINDER_IMAGE_STORE", ""),
		`画像の保管先。"r2" (要 R2_* 環境変数) / "local:<dir>" / 空で無効`)
	cachePath := fs.String("cache", env("FINDER_CACHE", "./cache.db"), "画像キャッシュの状態 DB")
	imgInterval := fs.Duration("image-interval", 10*time.Second, "館ごとの画像取得間隔")
	imgQuality := fs.Int("image-quality", 80, "WebP の品質 (0-100)")
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
			DBPath:       *dbPath,
			IndexPath:    *indexPath,
			Addr:         *addr,
			AllowSQL:     *allowSQL,
			Timeout:      *timeout,
			ImageStore:   *imgStore,
			CachePath:    *cachePath,
			ImageEvery:   *imgInterval,
			ImageQuality: *imgQuality,
		})
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("%v", err)
	}
}

type serveOptions struct {
	DBPath, IndexPath, Addr string
	AllowSQL                bool
	Timeout                 time.Duration
	ImageStore, CachePath   string
	ImageEvery              time.Duration
	ImageQuality            int
}

func serve(ctx context.Context, o serveOptions) error {
	st, err := store.Open(o.DBPath, o.IndexPath)
	if err != nil {
		return err
	}
	defer st.Close()

	log.Printf("hm.db  %s (読み取り専用)", st.DBPath)
	if st.HasIndex {
		log.Printf("index  %s", st.IndexPath)
	} else {
		log.Printf("index  なし — `finder index` を実行すると全文検索が使えます")
	}

	cache, err := openCache(ctx, o)
	if err != nil {
		return err
	}
	if cache != nil {
		defer cache.Close()
		log.Printf("画像   %s", cache.Describe())
		go cache.Run(ctx)
	} else {
		log.Printf("画像   無効 — -images r2 または -images local:<dir> で有効になります")
	}

	srv, err := web.New(st, web.Options{
		AllowSQL: o.AllowSQL, QueryTimeout: o.Timeout, Cache: cache,
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

// openCache は画像キャッシュを用意する。-images が空なら nil を返し、
// finder は画像まわりの UI を一切出さない。
func openCache(ctx context.Context, o serveOptions) (*imagecache.Cache, error) {
	blob, err := imagecache.OpenBlob(o.ImageStore)
	if err != nil {
		return nil, err
	}
	if blob == nil {
		return nil, nil
	}
	return imagecache.New(ctx, imagecache.Options{
		DBPath:   o.CachePath,
		Store:    blob,
		Interval: o.ImageEvery,
		Quality:  o.ImageQuality,
	})
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
