# importer

美術館 API をクロールして、パブリックドメイン作品のメタデータを SQLite (`hm.db`) に投入する。

```
美術館 API ──[クローラー]──> <source>.lmdb ──[loader.rb]──> hm.db
                                    ↑                          ↑
                                    └──[翻訳ツール]──> CSV ──[apply_translations.rb]
                                                              ↑
                                              [normalize_*.rb]─┘
```

クロール結果は生 JSON のまま LMDB に溜め、`loader.rb` が共通スキーマへ変換して `hm.db` に入れる。日本語訳や正規化した年・作者は**別テーブル**に分かれていて、それぞれ専用スクリプトが後から埋める。設計の理由は [`spec_schema.md`](docs/spec_schema.md)。

## セットアップ

```bash
bundle install     # lmdb, sqlite3, rexml
```

Ruby 4.0.6 (`.ruby-version`)。`hm.db` の場所は環境変数 `DATABASE_PATH` で変えられる (既定はこのディレクトリの `hm.db`)。

## スクリプト一覧

### 1. クローラー — 美術館 API → `crawlers/<source>.lmdb`

クローラー本体・LMDB・再開状態ファイルは `crawlers/` にまとめてある。引数は取らない。
すべて中断可能だが、**再開の仕方はソースごとに違う**。

| コマンド | 出力 | 現在の件数 | 再開方式 | 備考 |
|---|---|---|---|---|
| `ruby crawlers/aic.rb` | `crawlers/aic.lmdb` | 132,021 | **なし**(毎回先頭から) | 上書きなので壊れないが、やり直しは全件。10秒/100件 |
| `ruby crawlers/met.rb` | `crawlers/met.lmdb` | 266,498 | 取得済み ID をスキップ | 0.5秒/件 |
| `PARIS_TOKEN=xxx ruby crawlers/parismusees.rb` | `crawlers/parismusees.lmdb` | 314,843 | 取得済み件数を offset に使う | GraphQL。**トークン必須** |
| `ruby crawlers/rijksmuseum.rb` | `crawlers/rijksmuseum.lmdb` | 838,386 | `crawlers/rijksmuseum.resume` に resumptionToken | OAI-PMH / RDF-XML |
| `ruby crawlers/smithsonian.rb` | `crawlers/smithsonian.lmdb` | 96,529 | `crawlers/smithsonian.resume` に完了シャード URL | S3 のバルクメタデータ。API キー不要 |
| `ruby crawlers/cleveland.rb` | `crawlers/cleveland.lmdb` | 66,833 | `crawlers/cleveland.resume` | 5〜10秒/ページ |
| `ruby crawlers/wikimedia.rb` | `crawlers/wikimedia.lmdb` | 358,290 (+ 作者 26,697) | **なし**(毎回先頭から。書き込み済みの QID は飛ばす) | Wikidata のダンプ (圧縮 40GB 超) をストリーム処理。**数時間〜半日以上** |

Wikimedia は抽出後に `ruby crawlers/wikimedia.rb enrich` で作者の名前・生没年を補う (Wikidata API を 50 件ずつ 30 秒間隔。中断しても再実行で続きから)。

LMDB の mapsize (`KVStore::MAP_SIZE`) は 64GB。**仮想アドレス空間の予約であって実ディスク消費ではない**ので、大きくても害はない。使い切ると `LMDB::Error::MAP_FULL` でクローラーが落ちるが、既に書けている分は無事なので定数を上げて再開すればよい。一時的に上げたいだけなら環境変数 `LMDB_MAP_SIZE` (バイト数) で上書きできる。

> Rijksmuseum は 84 万件 × 平均 9.2KB (RDF/XML 生のまま) で、初期値の 10GB を実際に使い切った。値を gzip すれば約 1/5 になる余地はある。

### 2. 投入 — LMDB → `hm.db`

```bash
ruby loader.rb                    # 全ソース
ruby loader.rb cleveland aic      # ソース指定
ruby loader.rb aic=/path/to.lmdb  # LMDB のパスを明示
```

`images` と `image_artists` の**生の列だけ**を書く。既存行は upsert され、値が変わった行だけ `updated_at` が更新される。何度実行しても安全。

所要時間は Cleveland 40,442件 で 8 秒、AIC 58,828件 で 15 秒。ただし **Rijksmuseum だけは 1 件ごとに RDF/XML をパースするため 30 分超**かかる。

### 3. 正規化・翻訳の適用 — `hm.db` → `hm.db`

**いずれも LMDB を読まないので数分で終わる。**ルールを直したら気軽に回し直してよい。

| コマンド | 書くもの | 内容 |
|---|---|---|
| `ruby normalize_dates.rb [SOURCE...]` | `image_dates` | 制作年を `date_start` / `date_end` / `date_precision` に |
| `ruby normalize_artists.rb [SOURCE...]` | `image_artists.role_bucket` | 役割を `creator` / `creator_uncertain` / `after` / `non_creator` に分類。Sitter や Patron を作者から外すのが主目的 |
| `ruby apply_translations.rb seed` | `image_artist_names` | 館が持つ原語表記(Cleveland の漢字名)を日本語名として取り込む |
| `ruby apply_translations.rb titles [SOURCE...]` | `image_translations` | `titles_ja_<source>.csv` を適用 |
| `ruby apply_translations.rb artists [SOURCE...]` | `image_artist_names` | `artist_names_ja_<source>.csv` を適用 |
| `ruby apply_translations.rb stale` | (表示のみ) | 訳出時から原文が変わった行を検出 |
| `ruby normalize_person.rb` | `artists`, `image_artists.person_key` ほか3列 | 同一人物の名寄せ。典拠ID (ULAN/Wikidata/VIAF/RKD) を起点に段階1〜7 を適用する |
| `ruby with_llms/author_merge_batch.rb status \| next N \| append F` | `author_merges.csv`, 上の3列 | 機械ルールで統合できなかった作者を LLM に判定させる (段階7)。指示は [`with_llms/author_migration_prompt.md`](with_llms/author_migration_prompt.md) |

引数の `SOURCE` は `aic` / `met` / `parismusees` / `rijksmuseum` / `smithsonian` / `cleveland` / `wikimedia`。省略すると全ソース。

順序は基本的に自由だが、例外が2つ。

- **`seed` は `artists` より先**に実行する。館由来の訳 (`method='source'`) のほうが LLM 訳より確実なので、`artists` は `method='source'` の行を上書きしない。
- **`normalize_person.rb` は `normalize_artists.rb` より後**に実行する。名寄せの母数を決めるのに `role_bucket` を見て、Sitter や Patron を作者一覧から除くため。

### 4. 日本語訳 CSV を作る

翻訳そのものは重い外部処理なので、DB とは切り離して CSV を作る。CSV が真の source of truth で、`hm.db` はそこからいつでも作り直せる。

```bash
ruby with_llms/title_translation_batch.rb sources            # 扱えるソース名
ruby with_llms/title_translation_batch.rb cleveland scan     # 対象一覧キャッシュを作る
ruby with_llms/title_translation_batch.rb cleveland status   # 進捗
ruby with_llms/title_translation_batch.rb cleveland next 50  # 未翻訳50件を JSON に書き出す
ruby with_llms/title_translation_batch.rb cleveland append F # 翻訳結果 JSON を CSV へ追記
```

進捗の唯一の状態は CSV そのもの (`source_url` の有無) なので、中断しても再開できる。`next`/`append` のたびに LMDB を全走査すると Rijksmuseum が成立しないため、対象一覧は `.title_translation_targets_<source>.jsonl` にキャッシュされる。**クローラーを再実行して母数が増えたら `scan` で作り直すこと。**

実際に翻訳を回すには、サブエージェントに次のプロンプトを渡す。

- [`with_llms/title_translation_prompt.md`](with_llms/title_translation_prompt.md)

作者名の CSV (`artist_names_ja_<source>.csv`) を作るツールはまだ無い。適用側 (`apply_translations.rb artists`) は先に用意してある。

### 5. ライブラリ (直接実行しない)

| ファイル | 役割 |
|---|---|
| `loaders.rb` | 各館の生 JSON → 共通スキーマの変換。**LMDB を読むのはここだけ** |
| `kv_store.rb` | LMDB の薄いラッパー (`KVStore`) |
| `db.rb` | `hm.db` への接続と `schema.sql` の冪等適用 |
| `schema.sql` | DDL。テーブル定義はここ 1 箇所 |

## 典型的な作業手順

**クロールし直したとき**

```bash
ruby crawlers/cleveland.rb          # 時間がかかる。中断可
ruby loader.rb cleveland
ruby normalize_dates.rb cleveland
ruby normalize_artists.rb cleveland
ruby normalize_person.rb             # normalize_artists.rb より後。全ソース横断なので引数なし
ruby apply_translations.rb seed
ruby apply_translations.rb titles cleveland
ruby with_llms/title_translation_batch.rb cleveland scan   # 母数が増えたので作り直す
```

**正規化ルールを直したとき** — `loader.rb` は不要

```bash
ruby normalize_dates.rb
```

**再翻訳したとき**

```bash
ruby apply_translations.rb titles cleveland
```

**原文が変わっていないか確認したいとき**

```bash
ruby apply_translations.rb stale
```

## `hm.db` の引き方

テーブルと書き手の対応は [`spec_schema.md`](docs/spec_schema.md)。表示側はこの形で引く。

```sql
select coalesce(t.text, i.title) as title,
       coalesce(n.name, a.name_raw) as artist,
       d.date_start, d.date_end, d.date_precision
  from images i
  left join image_translations t
         on t.image_id = i.id and t.field = 'title' and t.lang = 'ja'
  left join image_dates d on d.image_id = i.id
  left join image_artists a
         on a.image_id = i.id and a.role_bucket in ('creator', 'creator_uncertain')
  left join image_artist_names n
         on n.image_artist_id = a.id and n.lang = 'ja'
 where i.id = ?;
```

## 設計ドキュメント

| ファイル | 内容 |
|---|---|
| [`spec_schema.md`](docs/spec_schema.md) | テーブルの分け方、書き手の固定、決定した設計上の分岐 |
| [`spec_normalization.md`](docs/spec_normalization.md) | 制作年・作者の役割・名寄せ・日本語訳の設計判断 (なぜそうしたか / しなかったか) |

## 注意点

- `*.lmdb/` と `*.db` はリポジトリに含めない (`.gitignore` 済み)。LLM の成果物 (`with_llms/titles_ja_*.csv`、`author_merges.csv`) も作り直せない正本だが、ローカルに置いて追跡しない。消さないこと
- `sqlite3` gem は 2.x。`execute` へのバインド変数は可変長引数ではなく**配列**で渡す
- SQLite では二重引用符は文字列ではなく**識別子**を意味する。`where method = "source"` は `images.source` 列との比較になってしまうので、文字列リテラルには単一引用符を使う
