// Package index は finder が所有する検索インデックス (index.db) を作る。
//
// hm.db には一切書かない。「テーブルごとに書き手を1つに固定する」(spec_schema.md)
// という importer 側の原則を守るため、finder の派生成果物は別ファイルに分ける。
// index.db はいつでも捨てて作り直せる。
package index

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// SchemaVersion は index.db の構造の版。上げると store 側が stale として扱う。
const SchemaVersion = "1"

const ddl = `
CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);

-- 作品ごとの「解決済み」の値。生の列は hm.db から直接読むので、ここには
-- 検索・絞り込みに使う派生値だけを置く (表示値のコピーは持たない)。
CREATE TABLE image_meta (
  image_id       INTEGER PRIMARY KEY,
  year_start     INTEGER,   -- image_dates 優先、無ければ images.date_raw_*
  year_end       INTEGER,
  year_kind      TEXT NOT NULL,  -- normalized | raw | '' (年が無い)
  date_precision TEXT NOT NULL,  -- image_dates.date_precision (無ければ '')
  has_title_ja   INTEGER NOT NULL,
  artist_count   INTEGER NOT NULL,
  creator_count  INTEGER NOT NULL,  -- role_bucket が creator / creator_uncertain
  person_count   INTEGER NOT NULL   -- person_key が付いた実人数
);
CREATE INDEX image_meta_year      ON image_meta (year_start, year_end);
CREATE INDEX image_meta_kind      ON image_meta (year_kind);
CREATE INDEX image_meta_precision ON image_meta (date_precision);
CREATE INDEX image_meta_hasja     ON image_meta (has_title_ja);
CREATE INDEX image_meta_person    ON image_meta (person_count);

-- 欧文用。remove_diacritics 2 で Cézanne を cezanne でも引ける。
CREATE VIRTUAL TABLE search USING fts5(
  title, artist, description, terms,
  content='', tokenize='unicode61 remove_diacritics 2'
);

-- 日本語用。unicode61 は CJK を「1語」に切ってしまい部分一致できないので、
-- 訳文だけ trigram で別に張る (対象が短いのでコストは小さい)。
CREATE VIRTUAL TABLE search_ja USING fts5(
  text,
  content='', tokenize='trigram remove_diacritics 1'
);

-- 絞り込み UI の選択肢。毎回 DISTINCT を取ると重いので焼いておく。
CREATE TABLE facet (
  kind  TEXT NOT NULL,
  value TEXT NOT NULL,
  n     INTEGER NOT NULL,
  PRIMARY KEY (kind, value)
) WITHOUT ROWID;
`

type step struct {
	label string
	sql   string
}

var steps = []step{
	{"image_meta (制作年・訳の有無・作者数)", `
INSERT INTO image_meta (image_id, year_start, year_end, year_kind, date_precision,
                        has_title_ja, artist_count, creator_count, person_count)
SELECT id,
       CASE WHEN norm THEN d_start ELSE r_start END,
       CASE WHEN norm THEN d_end   ELSE r_end   END,
       CASE WHEN norm THEN 'normalized'
            WHEN r_start IS NOT NULL OR r_end IS NOT NULL THEN 'raw'
            ELSE '' END,
       prec, has_ja, ac, cc, pc
FROM (
  SELECT i.id AS id,
         (d.date_start IS NOT NULL OR d.date_end IS NOT NULL) AS norm,
         d.date_start AS d_start, d.date_end AS d_end,
         i.date_raw_start AS r_start, i.date_raw_end AS r_end,
         coalesce(d.date_precision, '') AS prec,
         CASE WHEN tj.image_id IS NOT NULL THEN 1 ELSE 0 END AS has_ja,
         coalesce(a.n, 0) AS ac, coalesce(a.creators, 0) AS cc, coalesce(a.persons, 0) AS pc
  FROM src.images i
  LEFT JOIN src.image_dates d ON d.image_id = i.id
  LEFT JOIN src.image_translations tj
         ON tj.image_id = i.id AND tj.field = 'title' AND tj.lang = 'ja'
  LEFT JOIN (
    SELECT image_id,
           count(*) AS n,
           sum(CASE WHEN role_bucket IN ('creator', 'creator_uncertain') THEN 1 ELSE 0 END) AS creators,
           count(DISTINCT person_key) AS persons
    FROM src.image_artists GROUP BY image_id
  ) a ON a.image_id = i.id
)`},

	{"search (欧文 FTS)", `
INSERT INTO search (rowid, title, artist, description, terms)
SELECT i.id,
       coalesce(i.title, ''),
       trim(coalesce(i.artist, '') || ' ' || coalesce(an.names, '')),
       coalesce(i.description, ''),
       trim(coalesce(i.date, '')     || ' ' || coalesce(i.style, '')  || ' ' ||
            coalesce(i.category, '') || ' ' || coalesce(i.medium, '') || ' ' ||
            coalesce(i.origin, '')   || ' ' || coalesce(i.credit, ''))
FROM src.images i
LEFT JOIN (
  SELECT image_id, group_concat(name_raw, ' ') AS names
  FROM src.image_artists GROUP BY image_id
) an ON an.image_id = i.id`},

	{"search_ja (日本語 trigram FTS)", `
INSERT INTO search_ja (rowid, text)
SELECT i.id, trim(coalesce(tj.text, '') || ' ' || coalesce(anj.names, ''))
FROM src.images i
LEFT JOIN src.image_translations tj
       ON tj.image_id = i.id AND tj.field = 'title' AND tj.lang = 'ja'
LEFT JOIN (
  SELECT ia.image_id, group_concat(n.name, ' ') AS names
  FROM src.image_artist_names n
  JOIN src.image_artists ia ON ia.id = n.image_artist_id
  WHERE n.lang = 'ja'
  GROUP BY ia.image_id
) anj ON anj.image_id = i.id
WHERE tj.image_id IS NOT NULL OR anj.image_id IS NOT NULL`},

	{"facet (絞り込みの選択肢)", `
INSERT INTO facet (kind, value, n)
  SELECT 'source', source, count(*) FROM src.images GROUP BY source;
INSERT INTO facet (kind, value, n)
  SELECT 'style', style, count(*) FROM src.images
   WHERE style IS NOT NULL AND style <> '' GROUP BY style;
INSERT INTO facet (kind, value, n)
  SELECT * FROM (
    SELECT 'category' AS k, category, count(*) AS n FROM src.images
     WHERE category IS NOT NULL AND category <> ''
     GROUP BY category ORDER BY n DESC LIMIT 500);
INSERT INTO facet (kind, value, n)
  SELECT 'date_precision', date_precision, count(*) FROM image_meta
   WHERE date_precision <> '' GROUP BY date_precision;
INSERT INTO facet (kind, value, n)
  SELECT 'year_kind', year_kind, count(*) FROM image_meta GROUP BY year_kind;
INSERT INTO facet (kind, value, n)
  SELECT 'role_bucket', coalesce(role_bucket, ''), count(*)
    FROM src.image_artists GROUP BY 2;`},

	{"FTS の最適化", `
INSERT INTO search(search) VALUES('optimize');
INSERT INTO search_ja(search_ja) VALUES('optimize');`},
}

// Build は hm.db から index.db を作り直す。作業は一時ファイルに対して行い、
// 完成してから rename する。途中で落ちても既存のインデックスは壊れない。
func Build(ctx context.Context, dbPath, indexPath string, log io.Writer) error {
	srcAbs, err := filepath.Abs(dbPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(srcAbs); err != nil {
		return fmt.Errorf("hm.db を開けません (%s): %w", srcAbs, err)
	}
	dstAbs, err := filepath.Abs(indexPath)
	if err != nil {
		return err
	}
	tmp := dstAbs + ".building"
	for _, suffix := range []string{"", "-wal", "-shm"} {
		os.Remove(tmp + suffix)
	}

	dsn := "file:" + url.PathEscape(tmp) +
		"?_pragma=journal_mode(off)&_pragma=synchronous(off)" +
		"&_pragma=temp_store(memory)&_pragma=cache_size(-262144)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	// ATTACH は接続ごとなので、1本に固定して同じ接続を使い回す。
	db.SetMaxOpenConns(1)
	defer db.Close()

	if _, err := db.ExecContext(ctx,
		fmt.Sprintf("ATTACH DATABASE 'file:%s?mode=ro' AS src", url.PathEscape(srcAbs))); err != nil {
		return fmt.Errorf("hm.db の ATTACH に失敗: %w", err)
	}
	if _, err := db.ExecContext(ctx, ddl); err != nil {
		return fmt.Errorf("index.db のスキーマ作成に失敗: %w", err)
	}

	total := time.Now()
	for i, s := range steps {
		start := time.Now()
		fmt.Fprintf(log, "[%d/%d] %s ... ", i+1, len(steps), s.label)
		if _, err := db.ExecContext(ctx, s.sql); err != nil {
			return fmt.Errorf("%s: %w", s.label, err)
		}
		fmt.Fprintf(log, "%.1fs\n", time.Since(start).Seconds())
	}

	// 鮮度判定の材料。hm.db 側の件数と最終更新時刻を焼いておき、ずれたら
	// UI が「インデックスが古い」と出す。
	var srcImages int64
	var srcMaxUpd string
	if err := db.QueryRowContext(ctx,
		`SELECT count(*), coalesce(max(updated_at), '') FROM src.images`).Scan(&srcImages, &srcMaxUpd); err != nil {
		return err
	}
	for k, v := range map[string]string{
		"schema_version":        SchemaVersion,
		"built_at":              time.Now().Format(time.RFC3339),
		"source_db":             srcAbs,
		"source_images":         fmt.Sprint(srcImages),
		"source_max_updated_at": srcMaxUpd,
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO meta (key, value) VALUES (?, ?)`, k, v); err != nil {
			return err
		}
	}

	var indexed int64
	db.QueryRowContext(ctx, `SELECT count(*) FROM image_meta`).Scan(&indexed)
	var ja int64
	db.QueryRowContext(ctx, `SELECT count(*) FROM search_ja`).Scan(&ja)

	// PRAGMA optimize / VACUUM は ATTACH 中の全 DB を触りにいくので、
	// 読み取り専用の hm.db を外してから実行する。
	if _, err := db.ExecContext(ctx, `DETACH DATABASE src`); err != nil {
		return fmt.Errorf("hm.db の DETACH に失敗: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA main.optimize`); err != nil {
		return fmt.Errorf("PRAGMA optimize に失敗: %w", err)
	}
	if _, err := db.ExecContext(ctx, `VACUUM main`); err != nil {
		return fmt.Errorf("VACUUM に失敗: %w", err)
	}
	if err := db.Close(); err != nil {
		return err
	}

	for _, suffix := range []string{"-wal", "-shm"} {
		os.Remove(tmp + suffix)
	}
	if err := os.Rename(tmp, dstAbs); err != nil {
		return fmt.Errorf("インデックスの差し替えに失敗: %w", err)
	}

	size := int64(0)
	if fi, err := os.Stat(dstAbs); err == nil {
		size = fi.Size()
	}
	fmt.Fprintf(log, "\n完了: %s (%.1f MB) / 作品 %d 件・日本語索引 %d 件 / %.1fs\n",
		dstAbs, float64(size)/(1<<20), indexed, ja, time.Since(total).Seconds())
	if ja == 0 {
		fmt.Fprintf(log, "注意: 日本語の索引が空です。importer で `ruby apply_translations.rb titles` "+
			"を実行してから作り直すと、日本語での検索が効くようになります。\n")
	}
	return nil
}
