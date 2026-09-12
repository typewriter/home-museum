# title_translator

`title_translation_batch.rb` と Gemini API を繋ぎ、タイトル日本語訳 CSV
(`importer/titles_ja_<source>.csv`) をバッチで埋めるツール。

## 前提

- Go 1.26 以降 (モジュールはこのディレクトリ直下)
- Ruby (`../title_translation_batch.rb` を実行するため)
- Gemini API キーを `GEMINI_API_KEY` (または `GOOGLE_API_KEY`) に設定

## 実行

対象の美術館は `-source` で指定する。入力 LMDB (`<source>.lmdb`) も出力 CSV
(`titles_ja_<source>.csv`) もこの名前から決まる。取り違えると別ソースの CSV に
追記してしまうため既定値は無く、未指定はエラーになる。

```bash
cd importer/with_llms/title_translator
GEMINI_API_KEY=xxx go run . -source cleveland -batches 10 -size 50
```

指定できる名前は `ruby ../title_translation_batch.rb sources` で確認できる
(`aic` / `met` / `parismusees` / `rijksmuseum` / `smithsonian` / `cleveland`)。

まず少件数で出力を確かめる場合は `-dry-run` を使う (CSV には追記せず、翻訳結果 JSON
を書いたところで止まる)。

```bash
GEMINI_API_KEY=xxx go run . -source cleveland -batches 1 -size 5 -dry-run
```

残り全件を処理するなら `-batches 0`。

```bash
GEMINI_API_KEY=xxx go run . -source cleveland -batches 0 -size 50
```

初回の `next` は対象一覧のキャッシュを作るため LMDB を全走査する。Rijksmuseum は
RDF/XML を1件ずつパースするので、ここだけ数十分かかる (先に
`ruby ../title_translation_batch.rb rijksmuseum scan` を済ませておくとよい)。

進捗の状態は CSV そのもの (`source_url` の有無) なので、途中で止めても再実行すれば
続きから進む。

## フラグ

| フラグ | 既定 | 説明 |
| --- | --- | --- |
| `-source` | (必須) | 対象ソース名。入力 LMDB と出力 CSV がこれで決まる |
| `-batches` | `10` | 実行するバッチ数。`0` なら `remaining=0` まで走り切る |
| `-size` | `50` | 1バッチ = 1リクエストの件数 |
| `-model` | `gemini-3.6-flash` | モデル名。環境変数 `GEMINI_MODEL` でも指定可 |
| `-thinking` | `high` | 思考レベル。`minimal` / `low` / `medium` / `high` / `default` (`default` はモデル既定に任せる) |
| `-tier` | `flex` | サービスティア。`flex` / `standard` / `default` (`default` は指定なし = API 既定)。`flex` はレイテンシが高い代わりに安い |
| `-timeout` | `15m` | 1リクエストのタイムアウト。`flex` は最大15分待たされるため既定もそれに合わせている。`0` で無効 |
| `-temperature` | `1.0` | 生成温度。思考モデルは低温にすると反復・ループを起こしうるため、既定値のままを推奨 |
| `-attempts` | `3` | 1バッチあたりのAPI試行回数の上限 |
| `-dry-run` | `false` | 翻訳結果JSONを書くところまでで停止し、CSVへ追記しない |
| `-importer` | `..` | `title_translation_batch.rb` のあるディレクトリ |
| `-out` | `<importer>/scratch/translations/<source>` | 翻訳結果JSONの出力先 |
| `-ruby` | `ruby` | ruby コマンド |

## 1バッチの流れ

```
ruby title_translation_batch.rb S next N   → .title_translation_batch_S.json
Gemini API (structured output)             → translations-<unixnano>.json
ruby title_translation_batch.rb S append F → titles_ja_S.csv へ追記
```

- 翻訳方針と `confidence` の基準は `prompt.md` に置き、`go:embed` で system instruction
  として送っている。訳し方を変えたいときはこのファイルを編集する。
- レスポンスは入力の `source_url` 集合と突き合わせ、欠落・空訳・不正な `confidence` を
  弾いたうえで、**残った件だけを入力にして訊き直す** (`-attempts` 回まで、5秒刻みの
  バックオフ)。全件揃わなければそのバッチは中断し、CSV には一切書かない。
- LMDB の読み出しと CSV の検証は Ruby 側が唯一の実装。こちらでは触らない。
