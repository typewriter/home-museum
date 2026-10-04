# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## プロジェクト概要

おうちの美術館 (Uchibi, Home museum) — パブリックドメインの美術作品を、展覧会のように作者別・コレクション別に見せる Web サービス。独立したコンポーネント 2 つで構成される。

| ディレクトリ | 役割 | 技術 |
| --- | --- | --- |
| `importer/` | 美術館 API のクロール → SQLite (`hm.db`) への投入と正規化 | Ruby 4.0 / LMDB / SQLite3 |
| `viewer_v2/` | 公開アプリ (API・画像キャッシュ・画面を 1 バイナリに) | Go 1.26 / SQLite / React + Vite + TypeScript |

2 つを繋ぐのは `viewer_v2 export` が hm.db から作る `works.db` だけ。viewer_v2 は hm.db を読み取り専用で開き、書かない。

## コマンド

### importer

スクリプトごとの引数・所要時間・再開方式は `importer/README.md` にまとめてある。

各クローラーは自身の LMDB ストア (`kv_store.rb` の `KVStore` でラップ) に生 JSON を蓄積し、`loader.rb` がそれらを読んで `hm.db` に投入する。中断しても壊れないが、再開方式はソースごとに違う (取得済み ID のスキップ / `*.resume` ファイル / offset)。**AIC と Wikimedia は再開の仕組みが無く、毎回先頭から取り直す**(Wikimediaは書き込みの重複だけ避ける。詳細は `importer/README.md`)。

```bash
cd importer
bundle install
ruby crawlers/aic.rb           # → importer/crawlers/aic.lmdb        (Art Institute of Chicago)
ruby crawlers/met.rb           # → importer/crawlers/met.lmdb        (The Metropolitan Museum of Art)
ruby crawlers/wikimedia.rb     # Wikidata ダンプをストリーム処理 (数時間〜)。作者の生没年は続けて `wikimedia.rb enrich` で取る
PARIS_TOKEN=xxx ruby crawlers/parismusees.rb  # → importer/crawlers/parismusees.lmdb (GraphQL、要 auth-token)

ruby loader.rb                    # 全ソースを LMDB → hm.db (引数でソース名を絞れる)
ruby loader.rb cleveland aic      # ソース指定
ruby loader.rb aic=/path/to.lmdb  # LMDB のパスを明示
```

`hm.db` は「生」と「派生」の2層に分かれており、テーブルごとに書き手が1つに固定されている。詳細は `importer/docs/spec_schema.md` (設計ドキュメントは `importer/docs/` に集約)。

```bash
ruby normalize_dates.rb            # images → image_dates (正規化した制作年)
ruby normalize_artists.rb          # → image_artists.role_bucket (役割の分類)
ruby apply_translations.rb seed    # 館が持つ原語表記 → image_artist_names
ruby apply_translations.rb titles  # titles_ja_*.csv → image_translations
ruby apply_translations.rb artists # artist_names_ja_*.csv → image_artist_names
ruby apply_translations.rb stale   # 訳出時から原文が変わった行を検出
ruby normalize_person.rb           # → artists, image_artists.person_key ほか (名寄せ。段階1〜7)
```

後半は基本的に互いに独立で順不同。例外は2つ: `seed` → `artists` (館由来の訳を LLM 訳で上書きしないため) と、`normalize_artists.rb` → `normalize_person.rb` (名寄せは `role_bucket` で Sitter/Patron を除いた母数に対して行うため)。**いずれも LMDB を読まないので数分で終わる**。正規化ルールを直したときに `loader.rb` (Rijksmuseum 込みで30分超) を回し直す必要は無い。

#### タイトル日本語訳 (`with_llms/title_translation_batch.rb`)

LLM を使う翻訳・名寄せ判定のスクリプト/プロンプト/Goツールは `importer/with_llms/` にまとめてある。`titles_ja_<source>.csv`(こちらも `with_llms/` 配下)を分割して埋めるツール。第1引数のソース名 (`Loaders::SOURCES` のキー) で入力 LMDB も出力 CSV も切り替わる。

```bash
ruby with_llms/title_translation_batch.rb sources              # 扱えるソース名の一覧
ruby with_llms/title_translation_batch.rb cleveland scan       # 対象一覧キャッシュを作る
ruby with_llms/title_translation_batch.rb cleveland status     # 進捗
ruby with_llms/title_translation_batch.rb cleveland next 50    # → .title_translation_batch_cleveland.json
ruby with_llms/title_translation_batch.rb cleveland append F   # 翻訳結果JSONを CSV へ追記
```

進捗の唯一の状態は CSV そのもの (`source_url` の有無) なので、中断しても再開できる。`next`/`append` のたびに LMDB を全走査すると Rijksmuseum (47 万件の RDF/XML パースで 1 回 30 分超) が成立しないため、対象一覧は `.title_translation_targets_<source>.jsonl` にキャッシュする。**クローラーを再実行して母数が増えたら `scan` で作り直す**こと。実際に翻訳を回すのは `with_llms/title_translator/` (Gemini API) か `with_llms/title_translation_prompt.md` (サブエージェント)。

#### 作者の名寄せ (`normalize_person.rb` / `with_llms/author_merge_batch.rb`)

`normalize_person.rb` は典拠ID (ULAN/Wikidata/VIAF/RKD) を起点に段階1〜6 を機械的に適用し、段階7 として `author_merges.csv` の LLM 判定を適用する。**LLM の判定は作り直せないので `author_merges.csv` が正本**で、DB 側は何度作り直しても失われない。判定は「見逃しより誤統合のほうが高コスト」の非対称で、生没年が不一致なら統合しない。

```bash
ruby with_llms/author_merge_batch.rb status   # 進捗
ruby with_llms/author_merge_batch.rb next 30  # 未判定を作品点数の降順で → .author_merge_batch.json
ruby with_llms/author_merge_batch.rb append F # 判定結果を CSV に追記し DB へ適用
```

判定の指示は `with_llms/author_migration_prompt.md`、Gemini で回す Go 版は `with_llms/author_merger/`。設計判断は `importer/docs/spec_normalization.md`。

### viewer_v2

```bash
cd viewer_v2
go run . export -hm ../importer/hm.db -out works.db   # hm.db → works.db (ローカル)
go run . import -db viewer.db works.db                # works.db → viewer.db の取り込み層
go run . serve -images local:./imagecache             # http://127.0.0.1:8080 (R2 なら -images r2)
cd web && npm ci && npm run dev                        # 画面の開発 (http://localhost:5173)
cd web && npm run build                                # go build の前に要る (dist/ を埋め込む)
```

フラグ・デプロイ・データの入れ替え手順は `viewer_v2/README.md`。設計の判断は `viewer_v2/spec_image_cache.md` と `viewer_v2/spec_collections.md`。

要点だけ:

- **viewer.db は 2 層**。取り込み層 (`works` / `work_artists` / `artists`) は import のたびに DROP して作り直し、所有層 (`collections` / `collection_works` / `image_cache`) は import では触らない。所有層は作品を `works.id` ではなく `source_url` (画像は `sha256(source_url)`) で指す。`works.id` は hm.db の作り直しで変わるため
- **館へは館ごとに 10 秒に 1 回しか取りに行かない** (AIC は 30 秒)。404 / 410 / 画像でないものは `gone` として二度と取りに行かない。取得の順番は collection > admin > visitor の優先度で決め、間隔は変えない
- 画像は R2 の署名付き URL への 302 で返す。署名時刻を 24 時間の窓に揃えて URL を固定し、ブラウザのキャッシュを効かせる。SigV4 は minio-go が署名時刻を渡せないので自前 (`internal/imagecache/presign.go`)
- **Go で WebP を再デコードしてはいけない**。`x/image/webp` は VP8 の限定レンジ YUV をフルレンジとして扱うため約 10dB ずれる。400px は 1600px からではなく必ず原本から作る
- 画面の経路は自前 (`web/src/shared/router.tsx`)。person_key は `/` を含むことがあり、`%2F` のまま 1 区切りとして扱う必要がある
- 管理画面 (`/admin`, `/api/admin/*`) の認証は Caddy の basic_auth。アプリ側は書き込みを `http.CrossOriginProtection` で同一オリジンに限るだけ

### テスト

```bash
cd viewer_v2 && go test ./...                  # imagecache のテストは libvips-tools が無いとスキップ
cd viewer_v2/web && npm run build              # tsc --noEmit を含む
cd importer/with_llms/author_merger && go test ./...
```

Ruby (importer) にはテストフレームワークが無い。

## アーキテクチャ上の要点

### データフロー

```
美術館 API → {aic,met,…}.lmdb → loader.rb → hm.db (images, image_artists)
                                                  ↓
                        normalize_dates.rb     → image_dates
                        normalize_artists.rb   → image_artists.role_bucket
                        normalize_person.rb    → artists, image_artists.person_key
                        apply_translations.rb  → image_translations, image_artist_names
                                                  ↓
                           viewer_v2 export → works.db → (VPS へ) → viewer_v2 import → viewer.db
                                                  ↓
                 viewer_v2 serve: /api/* と画面、/img/* は館から取得 → WebP → R2 → 302
```

- **生の層 (`images`, `image_artists`) を書くのは `loader.rb` だけ**。派生値は別テーブルに分けてあるので `loader.rb` は既存行を upsert してよい (旧実装の insert only 制約は解消済み)。
- 逆に **`loader.rb` に「解釈」を書いてはいけない**。館のデータをそのまま写す以上のこと (precision の分類、役割のバケット分け、名寄せ) は派生層の仕事。この線引きが崩れると、ルールを直すたびに LMDB 全走査が必要になる。
- importer 側の DDL は `importer/schema.sql` に一本化されており、`db.rb` が接続のたびに冪等に適用する。
- 各ソースのフィールドは `importer/loaders.rb` (`Loaders`) で共通スキーマにマッピングされる。パブリックドメインかつ画像 URL を持つレコードのみ通す。`loader.rb` と `with_llms/title_translation_batch.rb` は同じ抽出条件・同じ `source_url` を見る必要がある (でないと翻訳 CSV が `images` に JOIN できない) ため、実装はここ 1 箇所だけに置く。`Loaders::SOURCES` がソース名 → LMDB ファイル名・表示ラベルの対応表 (`images.source` にはキーのほうが入る)。
