-- hm.db のスキーマ。設計の全体像は spec_schema.md を参照。
--
-- テーブルは「生」と「派生」の2層に分かれ、各テーブルの書き手は1つに固定されている。
-- 派生層は全消し→再生成できるので、正規化ルールを直したら該当スクリプトを回し直せばよい。
--
--   images, image_artists (raw列)  ← loader.rb        (LMDB を読む唯一のスクリプト)
--   image_dates                    ← normalize_dates.rb
--   image_artists.role_bucket      ← normalize_artists.rb
--   image_translations             ← apply_translations.rb titles
--   image_artist_names             ← apply_translations.rb artists / seed

-- ===================== 生: loader.rb だけが書く =====================

CREATE TABLE IF NOT EXISTS images (
  id                 INTEGER PRIMARY KEY,
  source             TEXT NOT NULL,   -- Loaders::SOURCES のキー ('aic' 等)。表示名ではない
  source_id          TEXT,            -- 館側のネイティブID
  source_url         TEXT NOT NULL,   -- 自然キー。翻訳CSVのJOINキーでもある
  image_url          TEXT,
  title              TEXT,
  artist             TEXT,            -- 館が用意した表示用の作者文字列 (構造は image_artists が持つ)
  date               TEXT,            -- 館が用意した表示用の年テキスト
  -- 館が既に構造化して持っている年。持たない館は NULL (正規化結果は image_dates)
  date_raw_start     INTEGER,
  date_raw_end       INTEGER,
  date_raw_precision TEXT,            -- 館側の精度ヒントの生値 (AIC date_qualifier_title 等)
  category           TEXT,
  style              TEXT,
  medium             TEXT,
  origin             TEXT,
  dimensions         TEXT,
  credit             TEXT,
  description        TEXT,
  first_seen_at      TEXT NOT NULL,
  updated_at         TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS images_source_url ON images (source_url);
CREATE INDEX IF NOT EXISTS images_source ON images (source);
CREATE INDEX IF NOT EXISTS images_style ON images (style);

-- 1作品 n作者。(image_id, position) が自然キーで、id はこれに対して安定させる。
-- image_artist_names がこの id にぶら下がるため、delete→insert で採番し直してはいけない。
CREATE TABLE IF NOT EXISTS image_artists (
  id                     INTEGER PRIMARY KEY,
  image_id               INTEGER NOT NULL REFERENCES images (id) ON DELETE CASCADE,
  position               INTEGER NOT NULL,
  name_raw               TEXT NOT NULL,  -- 館の表記そのまま (国籍・生没年込みのこともある)
  role_raw               TEXT,           -- Smithsonian label / Cleveland role / Met constituent role
  qualifier_raw          TEXT,           -- Cleveland qualifier (after, attributed to …)
  birth_year             INTEGER,        -- 館が構造化して持つ場合のみ。AIC/Smithsonian は NULL
  death_year             INTEGER,
  source_artist_id       TEXT,           -- 館内部の作者ID (Cleveland creators[].id 等)
  authority_urls         TEXT,           -- 外部典拠URLのJSON配列 (ULAN/Wikidata/VIAF/RKD)
  name_original_language TEXT,           -- 原語表記 (Cleveland name_in_original_language)
  role_bucket            TEXT,           -- ← normalize_artists.rb が埋める派生列
  UNIQUE (image_id, position)
);

CREATE INDEX IF NOT EXISTS image_artists_image ON image_artists (image_id);
CREATE INDEX IF NOT EXISTS image_artists_name ON image_artists (name_raw);

-- ===================== 派生: 各スクリプトが作り直せる =====================

-- 正規化した制作年。spec_normalization_year.md
CREATE TABLE IF NOT EXISTS image_dates (
  image_id       INTEGER PRIMARY KEY REFERENCES images (id) ON DELETE CASCADE,
  date_start     INTEGER,        -- BCE は負数 (天文学的年表記)
  date_end       INTEGER,
  date_precision TEXT NOT NULL,  -- exact|circa|before|after|range|century|decade|unknown
  rule_version   TEXT
);

CREATE INDEX IF NOT EXISTS image_dates_start ON image_dates (date_start);
CREATE INDEX IF NOT EXISTS image_dates_precision ON image_dates (date_precision);

-- 作品単位のテキスト翻訳 (いまは title のみ)。spec_normalization_title.md
CREATE TABLE IF NOT EXISTS image_translations (
  image_id      INTEGER NOT NULL REFERENCES images (id) ON DELETE CASCADE,
  field         TEXT NOT NULL,   -- 'title' (将来 'description')
  lang          TEXT NOT NULL,
  text          TEXT NOT NULL,
  confidence    TEXT,
  method        TEXT,            -- llm | manual | source
  source_text   TEXT,            -- 訳出時の原文スナップショット (鮮度検知用)
  translated_at TEXT,
  PRIMARY KEY (image_id, field, lang)
);

-- 作者エントリ単位の名前訳。人物単位ではないので同姓同名の誤統合が起きない。
-- 名寄せ (artists テーブル) を導入したら、その訳を優先しつつ本テーブルは
-- 名寄せできなかったエントリのフォールバックとして残す。spec_normalization_author.md
CREATE TABLE IF NOT EXISTS image_artist_names (
  image_artist_id INTEGER NOT NULL REFERENCES image_artists (id) ON DELETE CASCADE,
  lang            TEXT NOT NULL,
  name            TEXT NOT NULL,
  confidence      TEXT,
  method          TEXT,          -- source | llm | manual (source が最優先)
  name_original   TEXT,          -- 訳出時の name_raw スナップショット (position ずれの検知用)
  translated_at   TEXT,
  PRIMARY KEY (image_artist_id, lang)
);
