# コレクションの設計

「浮世絵」「印象派」「フレスコの宗教画」のような主題別の作品集を finder で作る。
音楽のプレイリストに当たるもの。

データとしては **1 コレクションが複数の作品を持つ**だけ。テーブルは 2 枚。

検索やルールは**作品を集める作業の道具**であって、コレクションの定義ではない。
「`medium LIKE '%fresco%'` で絞って上から選ぶ」はやるが、その条件は保存しない。
保存するのは選んだ結果の作品リストだけで、後から hm.db が変わってもメンバーは
勝手に増減しない。

> 旧実装 (`server/collection_generator.rb`) はルール (作家名への `artist LIKE` の
> 和集合) を正本にして毎回再生成していた。そちらは採らない。

## 1. 置き場所は 4 つ目のファイル `collections.db`

コレクションは、このリポジトリで初めての**人の手でしか作れない成果物**になる。
派生層は数分、`index.db` は 1 分、`cache.db` も時間さえかければ埋め直せるが、
コレクションは消したら戻らない。だから既存のどのファイルにも相乗りさせない。

| ファイル | 書き手 | 失ったときの復旧 |
|---|---|---|
| `importer/hm.db` | importer のスクリプト群 | LMDB から再生成 (30 分〜) |
| `finder/index.db` | `finder index` | 1 分で作り直せる |
| `finder/cache.db` | `finder serve -images` | 館から取り直す (数ヶ月) |
| `finder/collections.db` | **`finder serve` (人の操作)** | **復旧できない** |

- `hm.db` に置かない — spec_schema.md の「テーブルごとに書き手を 1 つに固定する」。
  ここを崩すと finder が hm.db に書く最初のプロセスになる
- `index.db` に置かない — `finder index` は毎回ゼロから作って rename している。
  索引の作り直しがコレクションの消去になってしまう
- `cache.db` に相乗りしない — 消してよいものと消してはいけないものを混ぜない

## 2. 読み取り専用接続に読み書きで ATTACH してよい (実測)

`store.Open` は hm.db を `mode=ro` で開いている。ここに書ける DB を足せるかを
実測した (`modernc.org/sqlite` v1.56.0):

```
mode=ro で開いた接続に collections.db を ATTACH

  ro 接続から co. へ INSERT   → 成功
  ro 接続から main へ INSERT  → attempt to write a readonly database (8)
  存在しないファイルの ATTACH → その場で作られる
```

**ATTACH 先は main 接続の読み取り専用フラグを継承しない。** DSN に `mode=ro` を
書いたかどうかで決まる。よって既存プールに `co.` として ATTACH するだけでよく、
書き込み用の接続を別に用意する必要はない。`images` との JOIN も同じ接続でできる。

> `docker-compose.yml` のコメント「ATTACH した index.db もメイン接続のフラグを
> 継承するので読み取り専用になる」は誤り。実際に効いているのは bind mount の
> `:ro` と `store.go` の `roDSN()` が付ける `mode=ro`。コメントを直すこと。
> `collections.db` は書けるディレクトリに置く必要があるので、compose では
> `cache.db` と同じ扱いにする。

## 3. メンバーのキーは `images.id` ではなく `source_url`

spec_image_cache.md §1 と同じ判断。あちらは「`images.id` を使うと hm.db の
再構築で数ヶ月ぶんのキャッシュが迷子になる」だった。コレクションは画像キャッシュ
より復旧しにくいので、なおさら `id` に紐づけない。

`loader.rb` が upsert になった今でも、`hm.db` をゼロから作り直せば `id` は変わる。
spec_schema.md §1 がそれを許容できたのは派生層が数分で再生成できるからで、
コレクションにはその逃げ道がない。

`images.source_url` は `UNIQUE INDEX images_source_url` を持つ自然キーなので、
JOIN のコストは問題にならない。

```sql
JOIN images i ON i.source_url = ci.source_url
```

副作用として **hm.db にまだ無い作品も指せる**。解決できないメンバーは
コレクション画面に件数を出すだけにして、自動では消さない。

## 4. スキーマ

```sql
-- collections.db。書き手は finder serve (人の操作) だけ。

CREATE TABLE collections (
  id          INTEGER PRIMARY KEY,
  slug        TEXT NOT NULL UNIQUE,   -- 'ukiyoe'。URL とエクスポート名を兼ねる
  title       TEXT NOT NULL,          -- 「浮世絵」
  title_en    TEXT,
  description TEXT,
  cover_url   TEXT,                   -- 代表画像の source_url (images.id ではない §3)
  sort        TEXT NOT NULL,          -- manual | added_desc | year_asc | year_desc | title
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL
);

CREATE TABLE collection_images (
  collection_id INTEGER NOT NULL REFERENCES collections (id) ON DELETE CASCADE,
  source_url    TEXT NOT NULL,        -- §3
  position      INTEGER,              -- 手動順。sort='manual' のときだけ使う (§5)
  note          TEXT,                 -- なぜ入れたか。後から見た自分のため
  added_at      TEXT NOT NULL,
  PRIMARY KEY (collection_id, source_url)
);

CREATE INDEX collection_images_url ON collection_images (source_url);
```

- 主キーが `(collection_id, source_url)` なので、同じ作品の二重追加は upsert になる
- `collection_images_url` は逆引き用 (作品詳細に「所属コレクション」を出す)
- `id` と `slug` を分けてあるのは、slug を変えてもメンバーを書き換えずに済むため

### `position` は素朴な連番にする

並べ替えのたびに、そのコレクションの全行を 0, 1, 2 … に振り直す。

```sql
UPDATE collection_images SET position = r.n FROM (
  SELECT source_url, row_number() OVER (ORDER BY position) - 1 AS n
  FROM collection_images WHERE collection_id = ?
) r WHERE collection_id = ? AND source_url = r.source_url;
```

当初は `REAL` にして、2 件の間に挿すときは `(前 + 後) / 2` を入れ、他の行に
触らない方式 (fractional indexing) を考えていた。**採らない。** double の仮数部は
53 bit しかなく、実測で**同じ隙間へ 52 回挿すと中点が計算できなくなる**
(`1.0000000000000002` で行き止まり)。position が重複して並び順が壊れる。
「この 2 枚の間にもう 1 枚」は手で並べていると起きやすい操作なので、現実的な故障。

避けようとしていたコストは最初から無かった。5,000 件の採番し直しは上の
`row_number()` 1 発で **6.3 ms**。コレクション 1 本はせいぜい数千件なので誤差。

## 5. 集める操作

コレクションの中身は「検索して選ぶ」でしか増えない。既存画面に足すのはこれだけ:

| 場所 | 操作 |
|---|---|
| 検索結果 | 行ごとの「＋」と、**現在の検索結果をまとめて追加** |
| 作品詳細 `/works/{id}` | 所属コレクションの表示と付け外し |
| 作者詳細 `/artists/detail` | この作者の全作品を追加 (浮世絵はこれが主導線) |

「まとめて追加」がルールの居場所。`medium LIKE '%fresco%'` で絞り込んだ結果を
一括で入れてから、要らないものを外す。**条件は保存せず、その時点の作品 ID を
展開して行にする**ので、後から hm.db が変わってもメンバーは動かない。

一括追加は件数の上限 (`CountCap` の 10,000) を超えうるので、実行前に件数を
確認させる。

コレクション自体の画面:

| パス | 内容 |
|---|---|
| `/collections/` | 一覧。件数・未解決件数・更新日時 |
| `/collections/{slug}` | メンバー一覧。`sort` に従う。手動順のときは並べ替え |

選別にはサムネイルが要る。検索画面が既定でサムネを出さないのは 1 ページ 50 件が
館ごと 10 秒では数分かかるためだが (spec_image_cache.md §8)、コレクション画面は
1 ページ 12〜20 件に落として既定で出す。202 とプレースホルダの既存の仕組みに任せる。

## 6. git へのエクスポート

`collections.db` が壊れたら復旧できない (§1) ので、テキストに出して git に置く。

```bash
finder collections export   # → finder/collections/*.csv + collections.yaml
finder collections import   # ← 逆
```

- `collections.yaml` — コレクションのメタ情報。多くて数十本なので 1 ファイル
- `collections/<slug>.csv` — `source_url, position, note` の 3 列。
  `titles_ja_*.csv` と同じ扱いで、行単位の diff が読める

YAML パーサは `go.yaml.in/yaml/v3` が minio 経由で既に依存グラフにいる (indirect)。
direct に上げるだけで新規依存は増えない。

保存のたびに自動で書き出さない。1 クリックごとにファイルが変わると git の
作業ツリーが常に汚れる。

## 7. 積み残し

- **入れ子・タグ**。「印象派」⊂「19 世紀の絵画」は作らない。必要になったら
  `parent_id` ではなくタグで解く (階層は 1 つの軸しか持てない)
- **多言語**。`title` / `title_en` の 2 列で始める。テーブルに分けるのは
  3 言語目が出てから
- **公開範囲**。viewer を作り直すときに決める。今は列を作らない
