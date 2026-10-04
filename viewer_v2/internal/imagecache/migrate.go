package imagecache

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
)

// Migrate は finder の cache.db の行を viewer.db の image_cache に移す。
// 保管キーは同じ sha256(source_url) なので、保管先のオブジェクトは動かさない。
//
// ready の行は、保管先に全サイズが揃っていることを確かめてから移す。
// finder の古い cache.db には、ready なのにオブジェクトが無い行があった。
// そのまま移すと、302 の先が 404 になって二度と取り直されない。
// 移さなければ、次に要求されたときに普通に取り直される。
func Migrate(ctx context.Context, w *sql.DB, store Blob, oldPath string, log io.Writer) error {
	abs, err := filepath.Abs(oldPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("cache.db を開けません: %w", err)
	}
	old, err := sql.Open("sqlite", "file:"+url.PathEscape(abs)+"?mode=ro")
	if err != nil {
		return err
	}
	defer old.Close()

	rows, err := old.QueryContext(ctx, `
		SELECT url_hash, image_id, source, source_url, origin_url, state, variants,
		       bytes, pixel_w, pixel_h, attempts, last_error, requested_at,
		       queued_at, retry_after, fetched_at, hits
		  FROM image_cache`)
	if err != nil {
		return err
	}
	type row struct {
		hash, source, sourceURL, originURL, state, variants, lastError, requestedAt string
		imageID, bytes, hits                                                        int64
		pixelW, pixelH, attempts                                                    int
		queuedAt, retryAfter, fetchedAt                                             sql.NullString
	}
	var all []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.hash, &r.imageID, &r.source, &r.sourceURL, &r.originURL,
			&r.state, &r.variants, &r.bytes, &r.pixelW, &r.pixelH, &r.attempts,
			&r.lastError, &r.requestedAt, &r.queuedAt, &r.retryAfter, &r.fetchedAt, &r.hits); err != nil {
			rows.Close()
			return err
		}
		all = append(all, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	var moved, skipped, missing int
	for _, r := range all {
		if r.state == StateFetching {
			r.state = StateQueued
		}
		if r.state == StateReady {
			ok, err := hasAll(ctx, store, r.source, r.hash, parseVariants(r.variants))
			if err != nil {
				return err
			}
			if !ok {
				missing++
				fmt.Fprintf(log, "保管先に無いので移しません: %s\n", r.sourceURL)
				continue
			}
		}
		res, err := w.ExecContext(ctx, `
			INSERT OR IGNORE INTO image_cache
			  (url_hash, image_id, source, source_url, origin_url, state, variants,
			   bytes, pixel_w, pixel_h, attempts, last_error, requested_at,
			   queued_at, retry_after, fetched_at, hits)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.hash, r.imageID, r.source, r.sourceURL, r.originURL, r.state, r.variants,
			r.bytes, r.pixelW, r.pixelH, r.attempts, r.lastError, r.requestedAt,
			r.queuedAt, r.retryAfter, r.fetchedAt, r.hits)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			skipped++
		} else {
			moved++
		}
	}
	fmt.Fprintf(log, "移した %d 件 / 既にあった %d 件 / 保管先に無かった %d 件\n", moved, skipped, missing)
	return nil
}

func hasAll(ctx context.Context, store Blob, source, hash string, widths []int) (bool, error) {
	if len(widths) == 0 {
		return false, nil
	}
	for _, w := range widths {
		if _, err := store.Stat(ctx, Key(source, hash, w)); err != nil {
			if errors.Is(err, ErrNotFound) {
				return false, nil
			}
			return false, err
		}
	}
	return true, nil
}
