# author_merger

`author_merge_batch.rb` と Gemini API を繋ぎ、機械ルール (`normalize_person.rb` の
段階1〜6) で統合できなかった作者の名寄せ先を判定するツール。判定は
`importer/author_merges.csv` に蓄積され、`hm.db` にも即時反映される。

サブエージェントに手作業でやらせる場合の指示は `../author_migration_prompt.md`。
どちらを使っても入出力と検証は同じ (Ruby 側が唯一の実装)。

## 前提

- Go 1.26 以降 (モジュールはこのディレクトリ直下)
- Ruby (`../author_merge_batch.rb` を実行するため)
- Gemini API キーを `GEMINI_API_KEY` (または `GOOGLE_API_KEY`) に設定
- `hm.db` に `artists` と `image_artists.person_key` が入っていること
  (先に `ruby ../normalize_person.rb` を回す)

## 実行

```bash
cd importer/with_llms/author_merger
GEMINI_API_KEY=xxx go run . -batches 10 -size 20
```

まず少件数で出力を確かめる場合は `-dry-run` を使う (判定結果 JSON を書いたところで
止まり、CSV にも DB にも触らない)。

```bash
GEMINI_API_KEY=xxx go run . -batches 1 -size 5 -dry-run
```

残り全件を処理するなら `-batches 0`。ただし未統合の作者は45,000人ほどいる。
取り出し順は作品点数の降順に固定してあるので、**上位数百件で作品数の大半を
カバーできる**。全件を回す必要はない。

進捗は `ruby ../author_merge_batch.rb status` で見られる。判定済みかどうかの状態は
`author_merges.csv` そのものなので、途中で止めても再実行すれば続きから進む。

## フラグ

| フラグ | 既定 | 説明 |
| --- | --- | --- |
| `-batches` | `10` | 実行するバッチ数。`0` なら `remaining=0` まで走り切る |
| `-size` | `20` | 1バッチ = 1リクエストの件数 |
| `-model` | `gemini-3.6-flash` | モデル名。環境変数 `GEMINI_MODEL` でも指定可 |
| `-thinking` | `high` | 思考レベル。`minimal` / `low` / `medium` / `high` / `default` |
| `-tier` | `flex` | サービスティア。`flex` / `standard` / `default`。`flex` はレイテンシが高い代わりに安い |
| `-timeout` | `15m` | 1リクエストのタイムアウト。`0` で無効 |
| `-temperature` | `1.0` | 生成温度。思考モデルは低温にすると反復・ループを起こしうるため既定のまま推奨 |
| `-attempts` | `3` | 1バッチあたりのAPI試行回数の上限 |
| `-dry-run` | `false` | 判定結果JSONを書くところまでで停止し、CSV/DBへ反映しない |
| `-importer` | `..` | `author_merge_batch.rb` のあるディレクトリ |
| `-out` | `<importer>/scratch/author_merges` | 判定結果JSONの出力先 |
| `-ruby` | `ruby` | ruby コマンド |

## 1バッチの流れ

```
ruby author_merge_batch.rb next N   → .author_merge_batch.json
Gemini API (structured output)      → decisions-<unixnano>.json
ruby author_merge_batch.rb append F → author_merges.csv へ追記し hm.db へ適用
```

- 判定方針と `confidence` の基準は `prompt.md` に置き、`go:embed` で system instruction
  として送っている。方針を変えたいときはこのファイルを編集する。
- **統合先は「その要素の `candidates` に含まれるキー」しか認めない。** 候補外のキーを
  返してきた回答は採用せず、その件だけを入力にして訊き直す (`-attempts` 回まで、
  5秒刻みのバックオフ)。全件揃わなければそのバッチは中断し、CSV には一切書かない。
- `merge_into` が空のときは `confidence` も null にして Ruby 側へ渡す。

## `-size` を大きくしないほうがよい理由

この作業は正解の74%が「名寄せ先なし」で、同じ答えが続くと機械的に流れやすい。
1件あたりの負荷も高い (対象と候補8件を読み、生没年を照合する)。既定を20にしてあるのは
そのため。タイトル翻訳 (50件) と同じ感覚で増やすと精度が落ちる。

## 判定できない類型がある

`candidates` は文字列の類似度だけで集めているので、**言語違いや雅号は原理的に
候補に出てこない** (`Titian` / `Tiziano Vecellio`、`El Greco` /
`Domenikos Theotokopoulos` など)。この段階では拾えない。

その場合はモデルに `reason` の先頭を `別名候補:` で始めて書き残させている。
別名を検索して統合する工程は未実装。
