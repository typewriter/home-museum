// コレクションの git へのエクスポート / インポート。spec_collections.md §6。
//
// collections.db は壊れたら復旧できない唯一のファイルなので、テキストに出して
// git に置けるようにする。メタ情報は 1 枚の YAML に、メンバーはコレクション
// ごとの CSV に分ける (importer の titles_ja_*.csv と同じ扱いで、行単位の
// diff が読めるように)。
package store

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	yaml "go.yaml.in/yaml/v3"
)

// ManifestName は YAML のファイル名。
const ManifestName = "collections.yaml"

type manifest struct {
	Collections []manifestEntry `yaml:"collections"`
}

type manifestEntry struct {
	Slug        string `yaml:"slug"`
	Title       string `yaml:"title"`
	TitleEn     string `yaml:"title_en,omitempty"`
	Description string `yaml:"description,omitempty"`
	CoverURL    string `yaml:"cover_url,omitempty"`
	Sort        string `yaml:"sort"`
	CreatedAt   string `yaml:"created_at"`
	UpdatedAt   string `yaml:"updated_at"`
}

// ExportCollections は dir に collections.yaml と <slug>.csv を書き出す。
func (s *Store) ExportCollections(ctx context.Context, dir string, log io.Writer) error {
	if err := s.requireCollections(); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	cols, err := s.Collections(ctx)
	if err != nil {
		return err
	}

	m := manifest{}
	for _, c := range cols {
		m.Collections = append(m.Collections, manifestEntry{
			Slug: c.Slug, Title: c.Title, TitleEn: c.TitleEn,
			Description: c.Description, CoverURL: c.CoverURL, Sort: c.Sort,
			CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
		})
	}
	buf, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	head := "# finder のコレクション定義。メンバーは <slug>.csv。\n" +
		"# `finder collections import` で collections.db に読み戻せる。\n"
	if err := writeAtomic(filepath.Join(dir, ManifestName), append([]byte(head), buf...)); err != nil {
		return err
	}

	total := 0
	for _, c := range cols {
		rows, err := s.exportRows(ctx, c.ID)
		if err != nil {
			return err
		}
		var out [][]string
		out = append(out, []string{"source_url", "position", "note"})
		out = append(out, rows...)
		if err := writeCSV(filepath.Join(dir, c.Slug+".csv"), out); err != nil {
			return err
		}
		total += len(rows)
		fmt.Fprintf(log, "  %-24s %6d 件\n", c.Slug+".csv", len(rows))
	}
	fmt.Fprintf(log, "%s に %d コレクション / %d 件を書き出しました\n", dir, len(cols), total)

	// 消えたコレクションの CSV は自動では消さない。人手の成果物を機械が
	// 消さない、という方針 (spec_collections.md §7)。
	if stale, _ := staleCSV(dir, cols); len(stale) > 0 {
		fmt.Fprintf(log, "注意: 対応するコレクションが無い CSV があります (削除は手動で): %v\n", stale)
	}
	return nil
}

func (s *Store) exportRows(ctx context.Context, id int64) ([][]string, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT source_url, position, coalesce(note, '')
		  FROM co.collection_images
		 WHERE collection_id = ?
		 ORDER BY position IS NULL, position, added_at`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out [][]string
	for rows.Next() {
		var url, note string
		var pos *int64
		if err := rows.Scan(&url, &pos, &note); err != nil {
			return nil, err
		}
		p := ""
		if pos != nil {
			p = strconv.FormatInt(*pos, 10)
		}
		out = append(out, []string{url, p, note})
	}
	return out, rows.Err()
}

func staleCSV(dir string, cols []Collection) ([]string, error) {
	known := map[string]bool{}
	for _, c := range cols {
		known[c.Slug+".csv"] = true
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".csv" && !known[e.Name()] {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// ImportCollections は dir の内容で collections.db を作り直す。YAML に無い
// コレクションは消える (git 側を正本として扱う)。
func (s *Store) ImportCollections(ctx context.Context, dir string, log io.Writer) error {
	if err := s.requireCollections(); err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(dir, ManifestName))
	if err != nil {
		return fmt.Errorf("%s を読めません: %w", ManifestName, err)
	}
	var m manifest
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return fmt.Errorf("%s の解析に失敗: %w", ManifestName, err)
	}

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM co.collection_images`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM co.collections`); err != nil {
		return err
	}

	total := 0
	for _, e := range m.Collections {
		if e.Slug == "" || e.Title == "" {
			return fmt.Errorf("slug と title は必須です: %+v", e)
		}
		if !validSort(e.Sort) {
			e.Sort = "manual"
		}
		ts := e.CreatedAt
		if ts == "" {
			ts = now()
		}
		upd := e.UpdatedAt
		if upd == "" {
			upd = ts
		}
		r, err := tx.ExecContext(ctx, `
			INSERT INTO co.collections
				(slug, title, title_en, description, cover_url, sort, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			e.Slug, e.Title, nullIfEmpty(e.TitleEn), nullIfEmpty(e.Description),
			nullIfEmpty(e.CoverURL), e.Sort, ts, upd)
		if err != nil {
			return collErr(err, e.Slug)
		}
		id, _ := r.LastInsertId()

		members, err := readCSV(filepath.Join(dir, e.Slug+".csv"))
		if err != nil {
			return err
		}
		for i, row := range members {
			var pos any
			if row[1] != "" {
				n, err := strconv.ParseInt(row[1], 10, 64)
				if err != nil {
					return fmt.Errorf("%s.csv %d 行目: position が数値ではありません (%q)", e.Slug, i+2, row[1])
				}
				pos = n
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO co.collection_images
					(collection_id, source_url, position, note, added_at)
				VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (collection_id, source_url) DO NOTHING`,
				id, row[0], pos, nullIfEmpty(row[2]), upd); err != nil {
				return err
			}
		}
		total += len(members)
		fmt.Fprintf(log, "  %-24s %6d 件\n", e.Slug, len(members))
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	fmt.Fprintf(log, "%d コレクション / %d 件を %s に読み込みました\n",
		len(m.Collections), total, s.CollectionsPath)
	return nil
}

// readCSV は <slug>.csv を読む。無ければメンバー 0 件として扱う
// (ルールだけ書いてまだ集めていない状態を許す)。
func readCSV(path string) ([][]string, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	recs, err := r.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var out [][]string
	for i, rec := range recs {
		if i == 0 && len(rec) > 0 && rec[0] == "source_url" {
			continue // ヘッダ
		}
		for len(rec) < 3 {
			rec = append(rec, "")
		}
		if rec[0] == "" {
			continue
		}
		out = append(out, rec[:3])
	}
	return out, nil
}

func writeCSV(path string, rows [][]string) error {
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	w := csv.NewWriter(f)
	if err := w.WriteAll(rows); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
