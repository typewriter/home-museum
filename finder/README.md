# finder

`importer/hm.db` を探索するための内部ツール。Go 製で、サーバーとフロントエンドが
1 バイナリに入っている (テンプレートと静的ファイルは `go:embed`)。

用途は**データ点検**。生の列 (`source_url`, `name_raw`, `role_bucket`, `person_key`,
訳の `method` / `confidence`) まで隠さず出し、クレンジングの進み具合と正規化ルールの
穴を見つけることを目的にしている。エンドユーザー向けの見た目は持たない。

## 使い方

```bash
cd finder
go run . index          # 索引 (index.db) を作る。約 1 分 / 約 270 MB
go run . serve          # http://127.0.0.1:8081
```

| フラグ | 既定 | 環境変数 |
|---|---|---|
| `-db` | `../importer/hm.db` | `DATABASE_PATH` |
| `-index` | `./index.db` | `FINDER_INDEX` |
| `-addr` | `127.0.0.1:8081` | `FINDER_ADDR` |
| `-sql` | `true` (読み取り専用 SQL コンソール) | — |
| `-timeout` | `30s` | — |

索引が無くても起動する。その場合は全文検索が LIKE にフォールバックし、制作年は
館の生値 (`images.date_raw_*`) だけを使う。画面上部に警告が出る。

## hm.db には書かない

`hm.db` は `mode=ro` で開く。**finder が作る派生成果物は別ファイル `index.db`**
に置き、接続のたびに `ATTACH` して `ix.` で参照する。`spec_schema.md` の
「テーブルごとに書き手を 1 つに固定する」原則を崩さないため。

生の列は毎回 `hm.db` から直接読むので、索引が古くても**表示される値は古くならない**。
古くなるのは検索のヒット範囲と絞り込みの選択肢だけで、それは画面上部の警告で分かる。

```
importer/hm.db  ──(mode=ro / ATTACH)──┐
                                      ├── finder serve
finder/index.db ──(mode=ro / ATTACH)──┘
      ↑
   finder index   (hm.db を読んで作り直す。いつ捨ててもよい)
```

## 検索の仕組み

**欧文と日本語で索引を分けている。** `unicode61` トークナイザは CJK を 1 語として
切ってしまい「聖母子」の中の「聖母」を引けないため、日本語は `trigram` で別に張る。

- 欧文は `unicode61 remove_diacritics 2`。**`Cezanne` で `Cézanne` に当たる**。
  語は前方一致 (`"monet"*`) で扱う
- 日本語は 3 文字以上なら `search_ja`、2 文字以下は trigram に載らないので
  `image_translations.text` への LIKE にフォールバックする (画面に注記が出る)
- 「Monet 睡蓮」のように混ざった入力は語ごとに振り分けて AND を取る

FTS5 の式を直接書きたいときは「FTS5 の式をそのまま渡す」にチェックを入れる
(`title:madonna NOT print` など)。索引を疑うときは「LIKE で引く」。

制作年の絞り込みは**期間の重なり**で判定する。1450–1550 の作品は「1500 年まで」で拾える。
紀元前は負数 (`-500`)。

## 画面

| パス | 内容 |
|---|---|
| `/` | 作品検索。実行した SQL とバインド変数、所要時間を下部に出す |
| `/works/{id}` | 1 作品について `images` / `image_artists` / `image_dates` / `image_translations` / `image_artist_names` / `ix.image_meta` の行を全列そのまま出す。NULL と空文字は区別して表示する |
| `/artists/` | 名寄せ結果 (`artists`)。`person_key` の種別 (ulan / wd / cluster) で絞れる |
| `/artists/detail?key=` | 寄せられた生表記の内訳 (`name_raw` × 判定手法 × 件数) とソース別作品数 |
| `/artists/unmatched` | `person_key IS NULL` の作者表記を件数の多い順に。名寄せの取りこぼしを潰す用 |
| `/stats` | 派生層のカバレッジ (ソース × テーブル)、`date_precision` / `role_bucket` / 世紀別の分布、索引の状態。10 分キャッシュ (`?refresh=1` で再集計) |
| `/sql` | 読み取り専用 SQL コンソール。`ix.` も参照できる。500 行で打ち切り |

## 既知の制約

- **主題 (宗教画・風景画・肖像) の軸が hm.db に無い**。`style` は館ごとに意味が
  違う (Met は部門名 "European Sculpture and Decorative Arts"、Cleveland は
  "Medieval Art"、Paris Musées は仏語のジャンル名) ので、横断ファセットとしては
  そのまま使えない。「中世の宗教画」は今のところ
  「キーワード + 年レンジ + ソース別 style」で近似するしかない
- 件数は 10,000 件で打ち切って `10,000+` と出す。全件を毎回数え切ると遅いため
- `/stats` は初回 10 秒ほどかかる (`images` と `image_artists` の全走査を含む)
- 日本語検索は `image_translations` が埋まっていないと機能しない。
  `ruby apply_translations.rb titles` の後に `go run . index` で作り直すこと
