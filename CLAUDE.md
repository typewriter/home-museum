# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## プロジェクト概要

Uchibi (Home museum) — パブリックドメインの美術作品 40 万点以上を全画面スライドショーで表示する Web サービス。独立したコンポーネントで構成される。

| ディレクトリ | 役割 | 技術 |
| --- | --- | --- |
| `importer/` | 美術館 API のクロール → SQLite への投入 | Ruby 4.0 / LMDB / SQLite3 |
| `finder/` | `importer/hm.db` を探索するデータ点検ツール (サーバー・フロントエンド一体) | Go 1.26 / SQLite FTS5 |
| `server/` | JSON API + 画像キャッシュ/リサイズ (**旧実装**) | Sinatra / ActiveRecord / RMagick |
| `viewer/` | SPA フロントエンド (**旧実装**) | Vue 2 + TypeScript + UIkit 3 |

`server/` と `viewer/` は旧世代のもの。現在の作業の中心は `importer/` のデータクレンジングと、それを見るための `finder/`。

`server` / `viewer` を繋ぐのは `server/hm.db` (SQLite ファイル、リポジトリには含まれない) と `server/public/images/` (画像キャッシュディレクトリ) のみ。コード上の依存関係はない。`finder/` は `importer/hm.db` を**読み取り専用**で見るだけで、他のどのコンポーネントにも依存しない。

## コマンド

### viewer

```bash
cd viewer
npm install
npm run serve   # 開発サーバー (HMR)
npm run build   # dist/ へ本番ビルド
npm run lint    # ESLint + Prettier (--fix 込み)
```

注意: `vue.config.js` に devServer proxy が無く、各 View の `server` データは空文字 (相対 URL) のため、`npm run serve` 単体では API に到達できない。API を併用する場合は docker-compose を使うか、`server` フィールドに API のオリジンを一時的に設定する。

### server

```bash
cd server
bundle install
ruby initializer.rb            # hm.db にスキーマを作成 (冪等)
bundle exec rackup --port 8080 # config.ru 経由で起動
bundle exec ruby collection_generator.rb  # collections / collection_images を再生成
```

環境変数: `DATABASE_PATH` (既定 `./hm.db`)、`IMAGE_PATH` (既定 `./public/images`)。

### importer

スクリプトごとの引数・所要時間・再開方式は `importer/README.md` にまとめてある。

各クローラーは自身の LMDB ストア (`kv_store.rb` の `KVStore` でラップ) に生 JSON を蓄積し、`loader.rb` がそれらを読んで `hm.db` に投入する。中断しても壊れないが、再開方式はソースごとに違う (取得済み ID のスキップ / `*.resume` ファイル / offset)。**AIC だけは再開の仕組みが無く、毎回先頭から取り直す**。

```bash
cd importer
bundle install
ruby crawlers/aic.rb           # → importer/crawlers/aic.lmdb        (Art Institute of Chicago)
ruby crawlers/met.rb           # → importer/crawlers/met.lmdb        (The Metropolitan Museum of Art)
PARIS_TOKEN=xxx ruby crawlers/parismusees.rb  # → importer/crawlers/parismusees.lmdb (GraphQL、要 auth-token)

ruby loader.rb                    # 全ソースを LMDB → hm.db (引数でソース名を絞れる)
ruby loader.rb cleveland aic      # ソース指定
ruby loader.rb aic=/path/to.lmdb  # LMDB のパスを明示
```

`hm.db` は「生」と「派生」の2層に分かれており、テーブルごとに書き手が1つに固定されている。詳細は `importer/spec_schema.md`。

```bash
ruby normalize_dates.rb            # images → image_dates (正規化した制作年)
ruby normalize_artists.rb          # → image_artists.role_bucket (役割の分類)
ruby apply_translations.rb seed    # 館が持つ原語表記 → image_artist_names
ruby apply_translations.rb titles  # titles_ja_*.csv → image_translations
ruby apply_translations.rb artists # artist_names_ja_*.csv → image_artist_names
ruby apply_translations.rb stale   # 訳出時から原文が変わった行を検出
```

後半は互いに独立で順不同 (`seed` → `artists` の順序だけは意味がある。館由来の訳を LLM 訳で上書きしないため)。**いずれも LMDB を読まないので数分で終わる**。正規化ルールを直したときに `loader.rb` (Rijksmuseum 込みで30分超) を回し直す必要は無い。

#### タイトル日本語訳 (`with_llms/title_translation_batch.rb`)

LLM を使う翻訳・名寄せ判定のスクリプト/プロンプト/Goツールは `importer/with_llms/` にまとめてある。`spec_normalization_title.md` の設計に基づき、`titles_ja_<source>.csv`(こちらも `with_llms/` 配下)を分割して埋めるツール。第1引数のソース名 (`Loaders::SOURCES` のキー) で入力 LMDB も出力 CSV も切り替わる。

```bash
ruby with_llms/title_translation_batch.rb sources              # 扱えるソース名の一覧
ruby with_llms/title_translation_batch.rb cleveland scan       # 対象一覧キャッシュを作る
ruby with_llms/title_translation_batch.rb cleveland status     # 進捗
ruby with_llms/title_translation_batch.rb cleveland next 50    # → .title_translation_batch_cleveland.json
ruby with_llms/title_translation_batch.rb cleveland append F   # 翻訳結果JSONを CSV へ追記
```

進捗の唯一の状態は CSV そのもの (`source_url` の有無) なので、中断しても再開できる。`next`/`append` のたびに LMDB を全走査すると Rijksmuseum (47 万件の RDF/XML パースで 1 回 30 分超) が成立しないため、対象一覧は `.title_translation_targets_<source>.jsonl` にキャッシュする。**クローラーを再実行して母数が増えたら `scan` で作り直す**こと。実際に翻訳を回すのは `with_llms/title_translator/` (Gemini API) か `with_llms/title_translation_prompt.md` (サブエージェント)。

### finder

```bash
cd finder
go run . index   # 索引 index.db を作る (約 1 分 / 約 270 MB)。hm.db 更新後は作り直す
go run . serve   # http://127.0.0.1:8081
```

`hm.db` は `mode=ro` で開き、**finder は一切書かない**。finder 自身の派生成果物 (FTS5 索引と解決済みの制作年) は別ファイル `finder/index.db` に置き、接続フックで `ATTACH` して `ix.` で参照する。これは `spec_schema.md` の「テーブルごとに書き手を 1 つに固定する」原則を崩さないための分割で、生の列は毎回 `hm.db` から直接読むため**索引が古くても表示値は古くならない** (古くなるのは検索のヒット範囲と絞り込みの選択肢だけ。画面上部に警告が出る)。

欧文と日本語で FTS を分けてある。`unicode61` は CJK を 1 語に切ってしまい「聖母子」から「聖母」を引けないため、日本語だけ `trigram` で別に張っている。詳細は `finder/README.md`。

#### 画像キャッシュ (`-images`)

```bash
sudo apt install libvips-tools webp        # 変換に必須
go run . serve -images local:./imagecache  # 動作確認用
go run . serve -images r2                  # 本番 (要 R2_* 環境変数)
```

**館へは館ごとに 10 秒に 1 回しか取りに行かない**。取得したものはリサイズして WebP にし (1600px / 400px)、Cloudflare R2 に無期限で保管する。状態は `finder/cache.db`。設計の根拠は `finder/spec_image_cache.md`。

要点だけ:

- 保管キーは `sha256(source_url)`。`images.id` を使うと hm.db の再構築で数ヶ月かけて溜めたキャッシュが全部迷子になる
- 404 / 410 / 画像でないものは `gone` として記録し**二度と取りに行かない**。このネガティブキャッシュが無いと失敗画像で館を叩き続ける
- IIIF の aic / rijksmuseum (全体の 55%) には原寸ではなく 1600px を要求する。館の転送量が 58% 減る。AIC はブラウザ相当の UA と `Referer` の両方が無いと 403
- 変換は `vipsthumbnail` に外出し。Go に実用的な lossy WebP エンコーダが純 Go で存在しないため。おかげで finder 本体は cgo なしのまま
- **Go で WebP を再デコードしてはいけない**。`x/image/webp` は VP8 の限定レンジ YUV をフルレンジとして扱うため約 10dB ずれる。400px は 1600px からではなく必ず原本から作る

### 全体 (Docker)

```bash
docker-compose up --build   # http://localhost:8080
```

`viewer` コンテナ (nginx) が `/v1` を `api:8080` にプロキシし、それ以外は SPA を返す。`server/public` は api と nginx の両コンテナにマウントされ、api が書き出した画像を nginx が直接配信する構成。この共有ボリュームが壊れると画像が 404 になる。

### テスト

テストフレームワークは導入されていない (Ruby 側に rspec/minitest なし、viewer に `@vue/cli-plugin-unit-*` なし)。

## アーキテクチャ上の要点

### データフロー

```
美術館 API → {aic,met,…}.lmdb → loader.rb → hm.db (images, image_artists)
                                                  ↓
                        normalize_dates.rb     → image_dates
                        normalize_artists.rb   → image_artists.role_bucket
                        apply_translations.rb  → image_translations, image_artist_names
                                                  ↓
                             collection_generator.rb → collections, collection_images
                                                  ↓
                                    server/app.rb → /v1/* → viewer
                                                  ↓
                              ImageStore が原本 URL から遅延取得 → server/public/images/
```

- **生の層 (`images`, `image_artists`) を書くのは `loader.rb` だけ**。派生値は別テーブルに分けてあるので `loader.rb` は既存行を upsert してよい (旧実装の insert only 制約は解消済み)。
- 逆に **`loader.rb` に「解釈」を書いてはいけない**。館のデータをそのまま写す以上のこと (precision の分類、役割のバケット分け、名寄せ) は派生層の仕事。この線引きが崩れると、ルールを直すたびに LMDB 全走査が必要になる。
- importer 側の DDL は `importer/schema.sql` に一本化されており、`db.rb` が接続のたびに冪等に適用する。ただし `server/initializer.rb` には依然として `images` の旧 DDL が残っている (server は別途作り直し予定)。
- 各ソースのフィールドは `importer/loaders.rb` (`Loaders`) で共通スキーマにマッピングされる。パブリックドメインかつ画像 URL を持つレコードのみ通す。`loader.rb` と `with_llms/title_translation_batch.rb` は同じ抽出条件・同じ `source_url` を見る必要がある (でないと翻訳 CSV が `images` に JOIN できない) ため、実装はここ 1 箇所だけに置く。`Loaders::SOURCES` がソース名 → LMDB ファイル名・表示ラベルの対応表 (`images.source` にはキーのほうが入る)。

### コレクション生成 (`server/collection_generator.rb`)

コレクションは、作家名リストに対する `artist LIKE '%name%'` の和集合として定義される。ここでの `%` と `_` は **意図的な SQL LIKE ワイルドカード**であり、タイポではない。

- `"Paul C_zanne"` — ダイアクリティカルマーク (Cézanne) の表記揺れを吸収
- `"Rubens%van Rijn"` / `"Rembrandt%van Rijn"` — 館ごとに異なる姓名の区切り方を吸収
- `# CUT` コメント — 意図的に途中で切って前方一致的に広く拾っている印

そのため作家名を「修正」してはいけない。カバー画像は `index_art` を `title LIKE` で引いて決定する。実行するとコレクションごとの作品数が STDERR に出力される。

### ランダム表示の同期設計 (`server/app.rb`)

`Random.new(Time.now.to_i / 60 - i)` で乱数シードを 1 分単位に量子化しているため、**同じ分にアクセスした全クライアントが同じ作品を得る**。viewer は初回ロードで `?i=1` (1 分前のシード = 現在表示すべき作品) を取得し、次の作品はパラメータなし (現在の分のシード) で先読みして 60 秒後に切り替える。この 60 秒という値は `app.rb` の除数と `Random.vue` の `setTimeout` の両方に埋め込まれており、片方だけ変えると同期が崩れる。

エンドポイント: `/v1/collection`、`/v1/random`、`/v1/random/collection/:id`、`/v1/random/style/:style`。

### 画像キャッシュ (`server/image_store.rb`)

初回リクエスト時に美術館側の URL から原本を取得して `{id}.jpg` として保存し、リサイズ版を `{id}.{size}.jpg` として生成する (`size` が `"max"` なら原本をそのまま返す)。返却するパスは先頭の `.` を除去して Web パス化される。つまり **API のレスポンスタイムは初回だけ外部ネットワークに依存する**。308 リダイレクトのみ手動で追従している。

### viewer

- Vue 2 + Options API。`vue-class-component` / `vue-property-decorator` は依存に入っているが未使用。
- ルーティングは `/:lang/` プレフィックスの有無で 2 系統を定義 (`router/index.ts`)。ロケールは各 View の `updateLang()` が `$i18n.locale` に直接代入する方式で、`lang` が無ければ `ja`。
- i18n メッセージは `main.ts` 内にインライン定義。ロケールファイルは無い。
- `Home.vue` は API レスポンスに `id: 9999` の擬似コレクション「ランダム (Random)」を追加し、これだけコレクション指定なしの `/random` にリンクする。
- UIkit の SCSS 変数は `src/styles/variables-hm.scss` で上書き (ダークテーマ寄り)。`App.vue` から UIkit theme SCSS を読み込む。
- `Random.vue` は even/odd 2 枚の div を交互にクロスフェードさせ、非表示側に次の画像をプリロードする。

## 制約

- `server/` は **Ruby 2.7 前提** (`server/Dockerfile` = `ruby:2.7-slim`)。`image_store.rb` は `File.exists?` と open-uri の Kernel#open による URL オープンを使っており、後者は Ruby 3.0 で削除されているため、3.x で動かすには `URI.open` への書き換えが必要。
- `importer/` は Ruby 4.0.6 (`.ruby-version`) で稼働。LevelDB (`leveldb-ruby`) は native extension がビルドできず廃止し、LMDB (`lmdb` gem) + 薄いラッパー `kv_store.rb` (`KVStore`) に置き換えた。`sqlite3` gem も 1.4 系 → 2.x に更新しており、`execute` へのバインド変数は可変長引数ではなく配列で渡す必要がある点に注意 (`loader.rb` 参照)。
- `config.ru` で `set :protection, :except => [:http_origin]` を設定しており、nginx 経由のプロキシで Origin ヘッダが変わっても弾かれないようにしている。
- Paris Musées の API は `PARIS_TOKEN` 環境変数 (auth-token ヘッダ) が必須。
