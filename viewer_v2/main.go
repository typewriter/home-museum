// viewer_v2 は home-museum の作品を展覧会のように見せる公開アプリ。
//
//	viewer_v2 export  hm.db から works.db を作る (ローカルで実行する)
//	viewer_v2 import  works.db で viewer.db の取り込み層を入れ替える (サービス停止中に VPS で実行する)
//	viewer_v2 migrate-cache  finder の cache.db から画像キャッシュの状態を移す
//	viewer_v2 serve   Web サーバーと画像の取得ワーカーを動かす
//
// 詳細は README.md。
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
	"strconv"
	"syscall"
	"time"

	"github.com/typewriter/home-museum/viewer_v2/internal/db"
	"github.com/typewriter/home-museum/viewer_v2/internal/export"
	"github.com/typewriter/home-museum/viewer_v2/internal/imagecache"
	"github.com/typewriter/home-museum/viewer_v2/internal/web"
	webdist "github.com/typewriter/home-museum/viewer_v2/web"
)

const usage = `viewer_v2 — home-museum の公開ビューア

使い方:
  viewer_v2 export [-hm ../importer/hm.db] [-out works.db]
  viewer_v2 import [-db viewer.db] works.db
  viewer_v2 migrate-cache [-db viewer.db] [-images r2] finder/cache.db
  viewer_v2 serve [-db viewer.db] [-addr 127.0.0.1:8080] [-images r2] [-artist-min-works 20]
`

func main() {
	log.SetFlags(log.Ltime)
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "export":
		err = runExport(ctx, args)
	case "import":
		err = runImport(ctx, args)
	case "migrate-cache":
		err = runMigrateCache(ctx, args)
	case "serve":
		err = runServe(ctx, args)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("%s: %v", cmd, err)
	}
}

func runExport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	hm := fs.String("hm", "../importer/hm.db", "hm.db のパス (読み取り専用で開く)")
	out := fs.String("out", "works.db", "出力先")
	fs.Parse(args)
	return export.Run(ctx, *hm, *out, os.Stderr)
}

func runImport(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	dbPath := fs.String("db", env("VIEWER_DB", "viewer.db"), "viewer.db のパス")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("works.db のパスを 1 つ指定してください")
	}
	d, err := db.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer d.Close()
	return db.Import(ctx, d, fs.Arg(0), os.Stderr)
}

func runMigrateCache(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("migrate-cache", flag.ExitOnError)
	dbPath := fs.String("db", env("VIEWER_DB", "viewer.db"), "viewer.db のパス")
	images := fs.String("images", env("VIEWER_IMAGE_STORE", "r2"),
		`画像の保管先。"r2" (要 R2_* 環境変数) / "local:<dir>"`)
	fs.Parse(args)
	if fs.NArg() != 1 {
		return fmt.Errorf("finder の cache.db のパスを 1 つ指定してください")
	}
	store, err := imagecache.OpenBlob(*images)
	if err != nil {
		return err
	}
	if store == nil {
		return fmt.Errorf("-images を指定してください")
	}
	d, err := db.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer d.Close()
	return imagecache.Migrate(ctx, d.W, store, fs.Arg(0), os.Stderr)
}

func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := fs.String("db", env("VIEWER_DB", "viewer.db"), "viewer.db のパス")
	addr := fs.String("addr", env("VIEWER_ADDR", "127.0.0.1:8080"), "待ち受けアドレス")
	images := fs.String("images", env("VIEWER_IMAGE_STORE", ""),
		`画像の保管先。"r2" (要 R2_* 環境変数) / "local:<dir>" / 空で無効`)
	interval := fs.Duration("image-interval", 10*time.Second, "館ごとの画像取得間隔")
	quality := fs.Int("image-quality", 80, "WebP の品質 (0-100)")
	minWorks := fs.Int("artist-min-works", envInt("VIEWER_ARTIST_MIN_WORKS", 20), "公開する作者の作品数の下限")
	baseURL := fs.String("base-url", env("VIEWER_BASE_URL", ""), "OGP に使う公開 URL (例: https://uchibi.nyamikan.net)。空ならリクエストから組み立てる")
	fs.Parse(args)

	d, err := db.Open(ctx, *dbPath)
	if err != nil {
		return err
	}
	defer d.Close()
	log.Printf("viewer.db %s", d.Path)

	store, err := imagecache.OpenBlob(*images)
	if err != nil {
		return err
	}
	var cache *imagecache.Cache
	if store != nil {
		cache, err = imagecache.New(ctx, imagecache.Options{
			DB: d.W, Store: store, Interval: *interval, Quality: *quality,
		})
		if err != nil {
			return err
		}
		defer cache.Close()
		log.Printf("画像   %s", cache.Describe())
		go cache.Run(ctx)
	} else {
		log.Printf("画像   無効 — -images r2 または -images local:<dir> で有効になります")
	}

	hs := &http.Server{
		Addr: *addr,
		Handler: web.New(web.Options{
			DB: d, Cache: cache, Dist: webdist.Dist(), ArtistMinWorks: *minWorks, BaseURL: *baseURL,
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sd, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		hs.Shutdown(sd)
	}()
	log.Printf("http://%s", *addr)
	return hs.ListenAndServe()
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
