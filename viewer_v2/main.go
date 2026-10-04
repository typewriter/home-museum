// viewer_v2 は home-museum の作品を展覧会のように見せる公開アプリ。
//
//	viewer_v2 export  hm.db から works.db を作る (ローカルで実行する)
//	viewer_v2 import  works.db で viewer.db の取り込み層を入れ替える (サービス停止中に VPS で実行する)
//
// 詳細は README.md。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/typewriter/home-museum/viewer_v2/internal/db"
	"github.com/typewriter/home-museum/viewer_v2/internal/export"
)

const usage = `viewer_v2 — home-museum の公開ビューア

使い方:
  viewer_v2 export [-hm ../importer/hm.db] [-out works.db]
  viewer_v2 import [-db viewer.db] works.db
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
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
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

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
