// Package store は hm.db への読み取り専用アクセスをまとめる。
//
// hm.db は importer 側のスクリプト群が唯一の書き手なので、finder は決して書かない
// (mode=ro で開く)。finder が自分で作る派生成果物 (FTS インデックスと解決済みの
// 制作年) は別ファイル index.db に置き、接続のたびに ATTACH して `ix.` で参照する。
// 生の列は常に hm.db から直接読むので、インデックスが古くても表示値は古くならない。
package store

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	sqlite3 "modernc.org/sqlite"
)

// AttachAlias は index.db を ATTACH するときのスキーマ名。
const AttachAlias = "ix"

var (
	hookOnce sync.Once
	// 接続フックはドライバ単位のグローバルなので、DSN → ATTACH 先の対応表を持つ。
	attachMu    sync.RWMutex
	attachByDSN = map[string]string{}
)

// registerHook は「この DSN で開いた接続には index.db を ATTACH する」フックを
// 一度だけ登録する。database/sql はコネクションプールなので、Open 後に一回
// ATTACH を実行しても他の接続には効かない。フックでしか賄えない。
func registerHook() {
	hookOnce.Do(func() {
		sqlite3.RegisterConnectionHook(func(conn sqlite3.ExecQuerierContext, dsn string) error {
			attachMu.RLock()
			idx, ok := attachByDSN[dsn]
			attachMu.RUnlock()
			if !ok || idx == "" {
				return nil
			}
			_, err := conn.ExecContext(context.Background(),
				fmt.Sprintf("ATTACH DATABASE '%s' AS %s", idx, AttachAlias), nil)
			return err
		})
	})
}

// Store は hm.db (+ ATTACH した index.db) への接続。
type Store struct {
	DB        *sql.DB
	DBPath    string
	IndexPath string
	// HasIndex が false のときは FTS 検索と正規化済み制作年が使えない。
	// UI 側はその旨を出したうえで LIKE 検索にフォールバックする。
	HasIndex bool
}

func roDSN(path string) string {
	return "file:" + url.PathEscape(path) +
		"?mode=ro&_pragma=busy_timeout(10000)&_pragma=cache_size(-64000)"
}

// Open は hm.db を読み取り専用で開く。indexPath が存在すればそれも ATTACH する。
func Open(dbPath, indexPath string) (*Store, error) {
	abs, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("hm.db を開けません (%s): %w", abs, err)
	}

	st := &Store{DBPath: abs}
	dsn := roDSN(abs)

	if indexPath != "" {
		if ia, err := filepath.Abs(indexPath); err == nil {
			if _, err := os.Stat(ia); err == nil {
				st.IndexPath = ia
				st.HasIndex = true
				attachMu.Lock()
				attachByDSN[dsn] = roDSN(ia)
				attachMu.Unlock()
			} else {
				st.IndexPath = ia
			}
		}
	}
	registerHook()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// 読み取り専用なので並列に開いてよい。ローカル用途なので控えめに。
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	db.SetConnMaxIdleTime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("hm.db に接続できません: %w", err)
	}
	// ATTACH が本当に効いたか (フックが動いたか) をここで確かめる。
	if st.HasIndex {
		var n int
		if err := db.QueryRowContext(ctx,
			`SELECT count(*) FROM ix.sqlite_master WHERE name='image_meta'`).Scan(&n); err != nil || n == 0 {
			db.Close()
			return nil, fmt.Errorf("index.db (%s) を ATTACH できましたが image_meta がありません。`finder index` で作り直してください", st.IndexPath)
		}
	}
	st.DB = db
	return st, nil
}

func (s *Store) Close() error {
	if s.DB == nil {
		return nil
	}
	attachMu.Lock()
	delete(attachByDSN, roDSN(s.DBPath))
	attachMu.Unlock()
	return s.DB.Close()
}

// IndexStatus はインデックスの鮮度。作った時点の images 件数・最終更新時刻を
// 現在の hm.db と突き合わせ、ずれていれば UI に警告を出す。
type IndexStatus struct {
	Present       bool
	Path          string
	BuiltAt       string
	SchemaVersion string
	IndexedImages int64
	CurrentImages int64
	BuiltMaxUpd   string
	CurrentMaxUpd string
	SizeBytes     int64
}

// Stale は再構築が要るかどうか。件数か最終更新時刻がずれていれば true。
func (st IndexStatus) Stale() bool {
	if !st.Present {
		return true
	}
	return st.IndexedImages != st.CurrentImages || st.BuiltMaxUpd != st.CurrentMaxUpd
}

func (s *Store) IndexStatus(ctx context.Context) (IndexStatus, error) {
	out := IndexStatus{Present: s.HasIndex, Path: s.IndexPath}

	if err := s.DB.QueryRowContext(ctx,
		`SELECT count(*), coalesce(max(updated_at),'') FROM images`).
		Scan(&out.CurrentImages, &out.CurrentMaxUpd); err != nil {
		return out, err
	}
	if !s.HasIndex {
		return out, nil
	}
	if fi, err := os.Stat(s.IndexPath); err == nil {
		out.SizeBytes = fi.Size()
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT key, value FROM ix.meta`)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return out, err
		}
		switch k {
		case "built_at":
			out.BuiltAt = v
		case "schema_version":
			out.SchemaVersion = v
		case "source_images":
			fmt.Sscan(v, &out.IndexedImages)
		case "source_max_updated_at":
			out.BuiltMaxUpd = v
		}
	}
	return out, rows.Err()
}

// nullStr は NULL を空文字として読むためのヘルパ。hm.db のテキスト列は NULL と
// 空文字が混在しているので、表示側では区別しない。
func nullStr(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}
