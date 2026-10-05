package db

// SchemaVersion は取り込み層の形。export した works.db と import 先の viewer.db で
// 食い違ったら import を拒否する (列のずれた INSERT SELECT は黙って値を入れ違える)。
const SchemaVersion = "1"

// importTables は取り込み層。import のたびに DROP して作り直す。
// インデックスは INSERT の後に張るほうが速いので importIndexes に分けてある。
const importTables = `
CREATE TABLE works (
  id          INTEGER PRIMARY KEY,  -- hm.db の images.id。URL 用で、参照には使わない
  source      TEXT NOT NULL,
  source_url  TEXT NOT NULL,
  image_url   TEXT NOT NULL,
  title       TEXT,
  title_ja    TEXT,
  artist      TEXT,
  date_text   TEXT,
  year_start  INTEGER,
  year_end    INTEGER,
  medium      TEXT,
  dimensions  TEXT,
  credit      TEXT,
  description TEXT
);

CREATE TABLE work_artists (
  work_id     INTEGER NOT NULL,
  position    INTEGER NOT NULL,
  person_key  TEXT NOT NULL,
  role_bucket TEXT,
  PRIMARY KEY (work_id, position)
);

CREATE TABLE artists (
  person_key   TEXT PRIMARY KEY,
  display_name TEXT NOT NULL,
  name_ja      TEXT,
  birth_year   INTEGER,
  death_year   INTEGER,
  work_count   INTEGER NOT NULL
);

CREATE TABLE import_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

const importIndexes = `
CREATE UNIQUE INDEX works_source_url ON works (source_url);
CREATE INDEX work_artists_person ON work_artists (person_key, work_id);
CREATE INDEX artists_work_count ON artists (work_count);
`

// importTableNames は依存の逆順 (DROP する順)。
var importTableNames = []string{"import_meta", "artists", "work_artists", "works"}

// ownedDDL は所有層。import はここに触れない。作品は source_url で指す
// (works.id は再 import で変わる)。
const ownedDDL = `
CREATE TABLE IF NOT EXISTS collections (
  id          INTEGER PRIMARY KEY,
  slug        TEXT NOT NULL UNIQUE,
  title       TEXT NOT NULL,
  title_en    TEXT,
  description TEXT,
  cover_url   TEXT,
  sort        TEXT NOT NULL DEFAULT 'manual',
  published   INTEGER NOT NULL DEFAULT 0,
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS collection_works (
  collection_id INTEGER NOT NULL REFERENCES collections (id) ON DELETE CASCADE,
  source_url    TEXT NOT NULL,
  position      INTEGER NOT NULL,
  note          TEXT,
  added_at      TEXT NOT NULL,
  PRIMARY KEY (collection_id, source_url)
);

CREATE INDEX IF NOT EXISTS collection_works_url ON collection_works (source_url);

CREATE TABLE IF NOT EXISTS image_cache (
  url_hash     TEXT PRIMARY KEY,      -- sha256(source_url)。R2 のキーと同じ
  image_id     INTEGER NOT NULL,      -- 参考値。再 import で変わる
  source       TEXT NOT NULL,
  source_url   TEXT NOT NULL,
  origin_url   TEXT NOT NULL,
  state        TEXT NOT NULL,
  priority     INTEGER NOT NULL DEFAULT 0,
  variants     TEXT NOT NULL DEFAULT '',
  bytes        INTEGER NOT NULL DEFAULT 0,
  pixel_w      INTEGER NOT NULL DEFAULT 0,
  pixel_h      INTEGER NOT NULL DEFAULT 0,
  attempts     INTEGER NOT NULL DEFAULT 0,
  last_error   TEXT NOT NULL DEFAULT '',
  requested_at TEXT NOT NULL,
  queued_at    TEXT,
  retry_after  TEXT,
  fetched_at   TEXT,
  hits         INTEGER NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS image_cache_queue ON image_cache (source, state, priority, queued_at);
CREATE INDEX IF NOT EXISTS image_cache_state ON image_cache (state);
`
