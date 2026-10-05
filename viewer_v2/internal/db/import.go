package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

// Import は works.db の中身で取り込み層を入れ替える。所有層には触れない。
//
// DELETE ではなく DROP → CREATE → INSERT → CREATE INDEX にしている。180 万行を
// インデックス付きのテーブルへ入れるより速い。捨てたページは次の import で
// 再利用されるので VACUUM は要らない。
func Import(ctx context.Context, d *DB, srcPath string, log io.Writer) error {
	if _, err := os.Stat(srcPath); err != nil {
		return fmt.Errorf("works.db を開けません: %w", err)
	}
	srcDSN, err := ReadOnlyDSN(srcPath)
	if err != nil {
		return err
	}
	conn, err := d.W.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS src`, srcDSN); err != nil {
		return fmt.Errorf("works.db を ATTACH できません: %w", err)
	}
	defer conn.ExecContext(context.Background(), `DETACH DATABASE src`)

	var ver string
	err = conn.QueryRowContext(ctx,
		`SELECT value FROM src.import_meta WHERE key = 'schema_version'`).Scan(&ver)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && ver != SchemaVersion) {
		return fmt.Errorf("works.db のスキーマ版 %q が viewer_v2 の %q と一致しません。同じ版の viewer_v2 で export し直してください", ver, SchemaVersion)
	}
	if err != nil {
		return fmt.Errorf("works.db の import_meta を読めません: %w", err)
	}

	start := time.Now()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, t := range importTableNames {
		if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS main.`+t); err != nil {
			return err
		}
	}
	if err := CreateImportTables(ctx, tx); err != nil {
		return err
	}
	for i := len(importTableNames) - 1; i >= 0; i-- {
		t := importTableNames[i]
		res, err := tx.ExecContext(ctx, `INSERT INTO main.`+t+` SELECT * FROM src.`+t)
		if err != nil {
			return fmt.Errorf("%s を入れられません: %w", t, err)
		}
		n, _ := res.RowsAffected()
		fmt.Fprintf(log, "  %-13s %10d 行\n", t, n)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO main.import_meta (key, value) VALUES ('imported_at', ?)`,
		time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	if err := CreateImportIndexes(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	// DROP で統計も消えている。無いと作者の作品一覧で person_key のインデックスを
	// 使わない計画を選ぶことがある。
	if _, err := conn.ExecContext(ctx, `ANALYZE main`); err != nil {
		return err
	}
	fmt.Fprintf(log, "取り込み層を入れ替えました (%s)\n", time.Since(start).Round(time.Second))

	return reportUnresolved(ctx, conn, log)
}

// reportUnresolved は所有層のうち、今回の works で引けなくなった作品を数える。
// 行は消さない。館側で一時的に消えた作品が次の export で戻ることがあるため。
func reportUnresolved(ctx context.Context, conn *sql.Conn, log io.Writer) error {
	for _, q := range []struct{ label, table string }{
		{"コレクションのメンバー", "collection_works"},
		{"画像キャッシュ", "image_cache"},
	} {
		var n int
		if err := conn.QueryRowContext(ctx, `SELECT count(*) FROM `+q.table+` o
			WHERE NOT EXISTS (SELECT 1 FROM works w WHERE w.source_url = o.source_url)`).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			fmt.Fprintf(log, "注意: %s %d 件が works に見つかりません (行は残してあります)\n", q.label, n)
		}
	}
	return nil
}
