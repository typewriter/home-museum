package export

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/typewriter/home-museum/viewer_v2/internal/db"
)

// newHM は importer のスキーマそのもので小さな hm.db を作る。importer 側の列が
// 変わったら export の SQL が壊れることをここで検知したいので、DDL を写さない。
func newHM(t *testing.T, dir string) string {
	t.Helper()
	ddl, err := os.ReadFile("../../../importer/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "hm.db")
	h, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	exec(t, h, string(ddl))
	exec(t, h, `
INSERT INTO images (id, source, source_url, image_url, title, artist, date, date_raw_start, first_seen_at, updated_at) VALUES
  (1, 'aic', 'https://a/1', 'https://img/1', 'Water Lilies', 'Monet', '1906', NULL, 'x', 'x'),
  (2, 'aic', 'https://a/2', '',              'No image',     'Monet', '',     NULL, 'x', 'x'),
  (3, 'met', 'https://m/3', 'https://img/3', 'Haystacks',    'Monet', '1890', 1890, 'x', 'x'),
  (4, 'met', 'https://m/4', 'https://img/4', 'Le Charivari', '',      '',     NULL, 'x', 'x');
INSERT INTO image_translations (image_id, field, lang, text) VALUES (1, 'title', 'ja', '睡蓮');
INSERT INTO image_dates (image_id, date_start, date_end, date_precision) VALUES (1, 1906, 1906, 'exact');
INSERT INTO image_artists (id, image_id, position, name_raw, role_bucket, person_key) VALUES
  (10, 1, 0, 'Claude Monet', 'creator',     'ulan:1'),
  (11, 1, 1, 'Unknown',      NULL,          NULL),
  (12, 2, 0, 'Claude Monet', 'creator',     'ulan:1'),
  (13, 3, 0, 'Monet',        NULL,          'ulan:1'),
  (14, 4, 0, 'Publisher',    'non_creator', 'wd:2');
INSERT INTO artists (person_key, display_name, image_count) VALUES ('ulan:1', 'Claude Monet', 3), ('wd:2', 'Publisher', 1);
INSERT INTO image_artist_names (image_artist_id, lang, name, method) VALUES
  (10, 'ja', 'クロード・モネ', 'source'),
  (13, 'ja', 'モネ',           'llm');`)
	return path
}

func exec(t *testing.T, d interface {
	Exec(string, ...any) (sql.Result, error)
}, q string, args ...any) {
	t.Helper()
	if _, err := d.Exec(q, args...); err != nil {
		t.Fatalf("%v\n%s", err, q)
	}
}

func count(t *testing.T, d *sql.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := d.QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%v\n%s", err, q)
	}
	return n
}

func TestExportImport(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	hm := newHM(t, dir)
	works := filepath.Join(dir, "works.db")
	if err := Run(ctx, hm, works, io.Discard); err != nil {
		t.Fatal(err)
	}

	d, err := db.Open(ctx, filepath.Join(dir, "viewer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	exec(t, d.W, `INSERT INTO collections (id, slug, title, created_at, updated_at) VALUES (1, 'monet', 'Monet', 'x', 'x')`)
	exec(t, d.W, `INSERT INTO collection_works (collection_id, source_url, position, added_at) VALUES
		(1, 'https://a/1', 0, 'x'), (1, 'https://gone/9', 1, 'x')`)

	// 2 回入れても所有層が残り、取り込み層が重複しないこと。
	for i := 0; i < 2; i++ {
		if err := db.Import(ctx, d, works, io.Discard); err != nil {
			t.Fatal(err)
		}
	}

	if n := count(t, d.R, `SELECT count(*) FROM works`); n != 3 {
		t.Errorf("works = %d, want 3 (画像の無い作品は除く)", n)
	}
	var titleJA string
	var year int
	d.R.QueryRow(`SELECT title_ja, year_start FROM works WHERE id = 1`).Scan(&titleJA, &year)
	if titleJA != "睡蓮" || year != 1906 {
		t.Errorf("work 1 = %q %d", titleJA, year)
	}
	if n := count(t, d.R, `SELECT year_start FROM works WHERE id = 3`); n != 1890 {
		t.Errorf("work 3 year = %d, want 1890 (date_raw_start にフォールバック)", n)
	}
	if n := count(t, d.R, `SELECT count(*) FROM work_artists`); n != 3 {
		t.Errorf("work_artists = %d, want 3", n)
	}

	var nameJA string
	var wc int
	if err := d.R.QueryRow(`SELECT name_ja, work_count FROM artists WHERE person_key = 'ulan:1'`).Scan(&nameJA, &wc); err != nil {
		t.Fatal(err)
	}
	if nameJA != "クロード・モネ" {
		t.Errorf("name_ja = %q, want 館の表記を優先", nameJA)
	}
	if wc != 2 {
		t.Errorf("work_count = %d, want 2 (画像の無い作品を数えない)", wc)
	}
	if n := count(t, d.R, `SELECT count(*) FROM artists WHERE person_key = 'wd:2'`); n != 0 {
		t.Errorf("non_creator だけの人物が artists に入っている")
	}

	if n := count(t, d.R, `SELECT count(*) FROM collection_works`); n != 2 {
		t.Errorf("collection_works = %d, want 2 (import で消えてはいけない)", n)
	}
}

func TestImportRejectsOtherSchemaVersion(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	works := filepath.Join(dir, "works.db")
	if err := Run(ctx, newHM(t, dir), works, io.Discard); err != nil {
		t.Fatal(err)
	}
	w, _ := sql.Open("sqlite", works)
	exec(t, w, `UPDATE import_meta SET value = '0' WHERE key = 'schema_version'`)
	w.Close()

	d, err := db.Open(ctx, filepath.Join(dir, "viewer.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	err = db.Import(ctx, d, works, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "スキーマ版") {
		t.Fatalf("err = %v, want スキーマ版の不一致", err)
	}
}
