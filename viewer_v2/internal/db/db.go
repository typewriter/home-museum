// Package db は viewer.db を開き、取り込み層を入れ替える。
//
// viewer.db は 2 層を持つ。取り込み層 (works / work_artists / artists) は
// hm.db から export したものを import で丸ごと入れ替え、所有層 (collections /
// collection_works / image_cache) は import では触らない。
package db

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// DB は viewer.db への接続。書き込みは W の 1 本に集め、読み取りは R から並列に引く。
type DB struct {
	R, W *sql.DB
	Path string
}

func dsn(abs, params string) string {
	return "file:" + url.PathEscape(abs) + "?" + params
}

// ReadOnlyDSN は ATTACH 用。ATTACH 先はメイン接続の読み取り専用フラグを継承せず、
// DSN の mode=ro だけで決まる。読ませたいだけの DB には必ずこれを使う。
func ReadOnlyDSN(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return dsn(abs, "mode=ro"), nil
}

// Open は viewer.db を開き、無ければ作る。取り込み層は import するまで空のまま。
func Open(ctx context.Context, path string) (*DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// _txlock=immediate は必須。既定の BEGIN DEFERRED だと、読み取りで始まった
	// トランザクションが書き込みへ昇格する瞬間に他の書き手と競合し、
	// SQLITE_BUSY_SNAPSHOT (517) が busy_timeout を無視して即座に返る。
	// 館ごとの取得ワーカーが同時に claim すると実際にこれを踏む。
	w, err := sql.Open("sqlite", dsn(abs,
		"_pragma=journal_mode(wal)&_pragma=busy_timeout(10000)&_pragma=synchronous(normal)"+
			"&_pragma=foreign_keys(1)&_txlock=immediate"))
	if err != nil {
		return nil, err
	}
	// 書き手をプロセス内で直列化する。1 件の書き込みはミリ秒なので実害がない。
	w.SetMaxOpenConns(1)

	if err := ensureSchema(ctx, w); err != nil {
		w.Close()
		return nil, err
	}

	r, err := sql.Open("sqlite", dsn(abs, "mode=ro&_pragma=busy_timeout(10000)&_pragma=cache_size(-64000)"))
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(8)
	r.SetMaxIdleConns(8)
	r.SetConnMaxIdleTime(5 * time.Minute)
	return &DB{R: r, W: w, Path: abs}, nil
}

func (d *DB) Close() error {
	rerr := d.R.Close()
	if err := d.W.Close(); err != nil {
		return err
	}
	return rerr
}

func ensureSchema(ctx context.Context, w *sql.DB) error {
	if _, err := w.ExecContext(ctx, ownedDDL); err != nil {
		return fmt.Errorf("所有層のスキーマを作れません: %w", err)
	}
	var n int
	if err := w.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'works'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if _, err := w.ExecContext(ctx, importTables+importIndexes); err != nil {
		return fmt.Errorf("取り込み層のスキーマを作れません: %w", err)
	}
	return nil
}

// execer は *sql.Conn と *sql.Tx の両方を受けるため。
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// CreateImportTables は取り込み層のテーブルを (インデックスなしで) main に作る。
// export が works.db を作るときにも使う。
func CreateImportTables(ctx context.Context, x execer) error {
	_, err := x.ExecContext(ctx, importTables)
	return err
}

// CreateImportIndexes は INSERT を終えてから呼ぶ。
func CreateImportIndexes(ctx context.Context, x execer) error {
	_, err := x.ExecContext(ctx, importIndexes)
	return err
}
