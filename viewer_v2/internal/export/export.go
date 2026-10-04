// Package export は importer/hm.db から viewer_v2 の取り込み層だけを抜き出し、
// VPS へ持っていく works.db を作る。hm.db (1.7 GB) と LMDB は VPS に置かない。
package export

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/typewriter/home-museum/viewer_v2/internal/db"
)

// Run は hmPath を読み取り専用で開き、outPath に works.db を作る。
// 書きかけを残さないよう一時ファイルに作ってから rename する。
// works.db にはインデックスを張らない。import が張り直すので、運ぶ量を減らす。
func Run(ctx context.Context, hmPath, outPath string, log io.Writer) error {
	if _, err := os.Stat(hmPath); err != nil {
		return fmt.Errorf("hm.db を開けません: %w", err)
	}
	hmDSN, err := db.ReadOnlyDSN(hmPath)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(outPath)
	if err != nil {
		return err
	}
	tmp := out + ".tmp"
	os.Remove(tmp)
	defer os.Remove(tmp)

	// 作り直せる一時ファイルなので、ジャーナルも fsync も要らない。
	w, err := sql.Open("sqlite", "file:"+url.PathEscape(tmp)+
		"?_pragma=journal_mode(off)&_pragma=synchronous(off)")
	if err != nil {
		return err
	}
	defer w.Close()
	conn, err := w.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, `ATTACH DATABASE ? AS hm`, hmDSN); err != nil {
		return fmt.Errorf("hm.db を ATTACH できません: %w", err)
	}
	if err := db.CreateImportTables(ctx, conn); err != nil {
		return err
	}
	for _, st := range steps {
		start := time.Now()
		res, err := conn.ExecContext(ctx, st.sql)
		if err != nil {
			return fmt.Errorf("%s: %w", st.label, err)
		}
		n, _ := res.RowsAffected()
		fmt.Fprintf(log, "  %-13s %10d 行 (%s)\n", st.label, n, time.Since(start).Round(time.Second))
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO import_meta (key, value) VALUES
		('schema_version', ?), ('exported_at', ?),
		('hm_images', (SELECT count(*) FROM hm.images))`,
		db.SchemaVersion, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `DETACH DATABASE hm`); err != nil {
		return err
	}
	if err := conn.Close(); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, out)
}

var steps = []struct{ label, sql string }{
	// 画像の無い作品は展示できないので持っていかない。
	// 制作年は正規化済み (image_dates) を優先し、無ければ館の構造化値を使う。
	{"works", `
INSERT INTO works
SELECT i.id, i.source, i.source_url, i.image_url,
       nullif(i.title, ''), t.text, nullif(i.artist, ''), nullif(i.date, ''),
       coalesce(d.date_start, i.date_raw_start), coalesce(d.date_end, i.date_raw_end),
       nullif(i.medium, ''), nullif(i.dimensions, ''), nullif(i.credit, ''),
       nullif(i.description, '')
  FROM hm.images i
  LEFT JOIN hm.image_translations t
         ON t.image_id = i.id AND t.field = 'title' AND t.lang = 'ja'
  LEFT JOIN hm.image_dates d ON d.image_id = i.id
 WHERE i.image_url IS NOT NULL AND i.image_url <> ''`},

	{"work_artists", `
INSERT INTO work_artists
SELECT ia.image_id, ia.position, ia.person_key, ia.role_bucket
  FROM hm.image_artists ia
  JOIN works w ON w.id = ia.image_id
 WHERE ia.person_key IS NOT NULL`},

	// work_count は hm.db の artists.image_count を使わずに数え直す。あちらは
	// 画像の無い作品や、出版社・刷り師としての関与 (non_creator) まで数えており、
	// 公開する作者の閾値の判定に使えない。
	// 日本語名は人物ではなく作者エントリ単位で訳されているので、寄せた人物ごとに
	// 最頻の訳を採る。同数なら館の表記 (source) を LLM の訳より優先する。
	{"artists", `
WITH ja AS (
  SELECT person_key, name FROM (
    SELECT ia.person_key, n.name,
           row_number() OVER (
             PARTITION BY ia.person_key
             ORDER BY count(*) DESC,
                      min(CASE n.method WHEN 'source' THEN 0 WHEN 'manual' THEN 1 ELSE 2 END),
                      n.name) AS rk
      FROM hm.image_artist_names n
      JOIN hm.image_artists ia ON ia.id = n.image_artist_id
     WHERE n.lang = 'ja' AND ia.person_key IS NOT NULL
     GROUP BY ia.person_key, n.name)
   WHERE rk = 1),
cnt AS (
  SELECT person_key, count(DISTINCT work_id) AS n
    FROM work_artists
   WHERE role_bucket IS NULL OR role_bucket <> 'non_creator'
   GROUP BY person_key)
INSERT INTO artists
SELECT a.person_key, a.display_name, ja.name, a.birth_year, a.death_year, cnt.n
  FROM cnt
  JOIN hm.artists a ON a.person_key = cnt.person_key
  LEFT JOIN ja ON ja.person_key = cnt.person_key`},
}
