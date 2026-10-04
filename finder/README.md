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

go run . collections export   # コレクションを git 用のテキストに書き出す
go run . collections import   # 書き戻す (collections.db を作り直す)
```

| フラグ | 既定 | 環境変数 |
|---|---|---|
| `-db` | `../importer/hm.db` | `DATABASE_PATH` |
| `-index` | `./index.db` | `FINDER_INDEX` |
| `-collections` | `./collections.db` (空にすると機能ごと無効) | `FINDER_COLLECTIONS` |
| `-collections-dir` | `./collections` (export / import の対象) | `FINDER_COLLECTIONS_DIR` |
| `-addr` | `127.0.0.1:8081` | `FINDER_ADDR` |
| `-base-path` | 空 (ルート直下)。例: `/art-finder` | `FINDER_BASE_PATH` |
| `-timeout` | `30s` | — |
| `-images` | 空 (無効)。`r2` / `local:<dir>` | `FINDER_IMAGE_STORE` |
| `-cache` | `./cache.db` (画像キャッシュの状態) | `FINDER_CACHE` |
| `-image-interval` | `10s` (館ごとの取得間隔) | — |
| `-image-quality` | `80` (WebP の品質) | — |

起動には索引 (`index.db`) が要る。先に `go run . index` を実行すること。

## hm.db には書かない

`hm.db` は `mode=ro` で開く。**finder が作る派生成果物は別ファイル `index.db`**
に置き、接続のたびに `ATTACH` して `ix.` で参照する。`spec_schema.md` の
「テーブルごとに書き手を 1 つに固定する」原則を崩さないため。

生の列は毎回 `hm.db` から直接読むので、索引が古くても**表示される値は古くならない**。
古くなるのは検索のヒット範囲と絞り込みの選択肢だけで、それは画面上部の警告で分かる。

```
importer/hm.db        ──(mode=ro / ATTACH)──┐
                                            │
finder/index.db       ──(mode=ro / ATTACH)──┼── finder serve
      ↑                                     │
   finder index                             │
   (いつ捨ててもよい)                        │
                                            │
finder/collections.db ──(読み書き / ATTACH)──┘
      ↑
   人の操作 (これだけ作り直せない)
```

`index.db` と `collections.db` はどちらも `.gitignore` 済み。ただし
**`collections.db` だけは復旧できない**ので、`go run . collections export` で
`collections/` 以下のテキストに出して git に置く。

## 検索の仕組み

**欧文と日本語で索引を分けている。** `unicode61` トークナイザは CJK を 1 語として
切ってしまい「聖母子」の中の「聖母」を引けないため、日本語は `trigram` で別に張る。

- 欧文は `unicode61 remove_diacritics 2`。**`Cezanne` で `Cézanne` に当たる**。
  語は前方一致 (`"monet"*`) で扱う
- 日本語は 3 文字以上なら `search_ja`、2 文字以下は trigram に載らないので
  `image_translations.text` への LIKE にフォールバックする (画面に注記が出る)
- 「Monet 睡蓮」のように混ざった入力は語ごとに振り分けて AND を取る

FTS5 の式を直接書きたいときは「FTS5 の式をそのまま渡す」にチェックを入れる
(`title:madonna NOT print` など)。

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
| `/collections/` | コレクションの一覧と新規作成 |
| `/collections/{slug}` | メンバー一覧。並べ替え・覚書・外す |
| `/stats` | 派生層のカバレッジ (ソース × テーブル)、`date_precision` / `role_bucket` / 世紀別の分布、索引の状態。10 分キャッシュ (`?refresh=1` で再集計) |

## コレクション

「浮世絵」「フレスコの宗教画」のような主題別の作品集。設計は
[`spec_collections.md`](spec_collections.md)。

**検索条件は保存せず、選んだ作品だけを保存する。** あとから `hm.db` が変わっても
メンバーは勝手に増減しない。

集め方は 3 つ:

- 作品検索で絞り込んで**「この検索結果をまとめて追加」** (上限 10,000 件)
- 検索結果の行ごとの「＋」
- 作品ページ (`/works/{id}`) と作者ページから

メンバーは `images.id` ではなく `source_url` で持つ (§3)。`hm.db` にまだ無い作品も
指せるので、館の URL を控えておいてクロール後に合流させられる。解決できない
メンバーは「未解決」として件数が出る。

### git に出す

`collections.db` は壊れたら復旧できない唯一のファイルなので、テキストに出して
git に置く。

```bash
go run . collections export   # → collections/collections.yaml + <slug>.csv
go run . collections import   # ← YAML を正本として collections.db を作り直す
```

- `collections.yaml` — コレクションのメタ情報。全部で 1 ファイル
- `collections/<slug>.csv` — `source_url, position, note`。行単位の diff が読める

保存のたびに自動では書き出さない (git の作業ツリーが常に汚れるため)。

## 画像キャッシュ

`-images` を指定したときだけ有効。設計の根拠は [`spec_image_cache.md`](spec_image_cache.md)。

```bash
sudo apt install libvips-tools webp        # 変換に libvips が要る

go run . serve -images local:./imagecache  # 動作確認用 (ローカルに置く)

# 本番は Cloudflare R2
export R2_ACCOUNT_ID=... R2_BUCKET=... R2_ACCESS_KEY_ID=... R2_SECRET_ACCESS_KEY=...
go run . serve -images r2
```

**館へは館ごとに 10 秒に 1 回しか取りに行かない (AIC だけ 30 秒)。** 取得したものは
1600px と 400px の WebP にして無期限で保管する。404 などで取れなかったものも記録し、
二度と取りに行かない。

未取得の画像はプレースホルダが出て、取れ次第差し替わる。一覧のサムネイルは
「サムネイルを出す」を有効にしたときだけ出る (1 ページ分が埋まるのに数分かかるため)。
進捗は `/stats` の「画像キャッシュ」節で見られる。

## VPS へのデプロイ (HTTPS)

HTTPS の受け口 (Caddy) は複数サービス共通で `../caddy-host` にある。finder
専用ではないので、まずそちらを一度だけ起動してから finder を上乗せする。

```bash
# 1. 受け口 (初回だけ、以後は他サービスを足しても再起動不要)
cd ../caddy-host
echo 'DOMAIN=finder.example.com' > .env
docker compose up -d --build

# 2. finder (R2_* は finder/.env に書いておく)
cd ../finder
docker compose -f docker-compose.yml -f compose.https.yml up -d --build
```

`compose.https.yml` は `docker-compose.yml` への上乗せで、finder を
caddy-host が作る外部ネットワーク `homemuseum` に参加させ、`FINDER_BASE_PATH`
(既定 `/art-finder`) を教えるだけ。証明書の取得・更新やパスベースのルーティング
定義は `../caddy-host/Caddyfile` 側にある。ルート `/` は finder 以外の将来の
アプリのために空けてある。

パス以外 (ホストベースルーティングなど) で公開する場合は `FINDER_BASE_PATH` を
空のままにしてよい。

## 既知の制約

- **主題 (宗教画・風景画・肖像) の軸が hm.db に無い**。`style` は館ごとに意味が
  違う (Met は部門名 "European Sculpture and Decorative Arts"、Cleveland は
  "Medieval Art"、Paris Musées は仏語のジャンル名) ので、横断ファセットとしては
  そのまま使えない。「中世の宗教画」は今のところ
  「キーワード + 年レンジ + ソース別 style」で近似するしかない
- 件数は 10,000 件で打ち切って `10,000+` と出す。全件を毎回数え切ると遅いため
- `/stats` は初回 1〜2 分かかる (`images` と `image_artists` の全走査を含む)
- 日本語検索は `image_translations` が埋まっていないと機能しない。
  `ruby apply_translations.rb titles` の後に `go run . index` で作り直すこと
