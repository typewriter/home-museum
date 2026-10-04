# hm.db のスキーマ設計

日本語訳・正規化した年・正規化した作者を扱うにあたって、`images` 1枚に全部足す
のではなくテーブルを分けた。DDL は `schema.sql`、各フィールドの正規化の設計判断は
`spec_normalization.md` を参照。

## 基本方針: 生と派生を分け、テーブルごとに書き手を1つに固定する

| 層 | テーブル | 書き手 | 再実行 |
|---|---|---|---|
| 生 | `images`, `image_artists` (raw列) | `loader.rb` | LMDB 全走査。Rijksmuseum 込みで30分超 |
| 派生 | `image_dates` | `normalize_dates.rb` | DB内のみ。数分 |
| 派生 | `image_artists.role_bucket` | `normalize_artists.rb` | DB内のみ。数分 |
| 派生 | `artists`, `image_artists.person_key` ほか3列 | `normalize_person.rb` | DB内のみ |
| 派生 | `image_translations` | `apply_translations.rb titles` | CSV件数分。秒 |
| 派生 | `image_artist_names` | `apply_translations.rb artists`/`seed` | 同上 |

派生層はいつでも全消し→再生成できる。**正規化ルールを直したときに LMDB を
読み直す必要が無い**ことがこの分割の主目的で、Rijksmuseum の RDF/XML パース
(47万件で30分超)を毎回踏まずにルールのイテレーションを回せる。

### 副産物: `loader.rb` の insert only 制約が外れた

旧実装は既存 `source_url` をスキップしていた (`# FIXME: update not supported`)。
派生値を `images` に置かない設計にしたことで上書きしても失うものが無くなり、
upsert に変えた。クローラーが拾い直したメタデータ修正が反映されるようになる。

## 決定した設計上の分岐

### 1. 派生テーブルのキーは `images.id` (自然キーではない)

`loader.rb` を upsert にして行を消さないので `id` は安定する。DBをゼロから
作り直したときは派生スクリプトを回し直す必要があるが、派生層は合計でも数分なので
実害が小さいほうを採った。

### 2. 翻訳テーブルは対象ごとに分ける

`translations(entity_type, entity_key, …)` のようなポリモーフィック1枚にはしない。
i18n の単位が違うため:

- タイトルは作品ごと (1作品=1タイトル) → `image_translations(image_id, field, lang)`
- 作者名は作品-作者エントリごと → `image_artist_names(image_artist_id, lang)`

`image_translations` に `field` を持たせてあるのは description 等の追加のため。

### 3. 作者の日本語名は「人物」ではなく「エントリ」にぶら下げる

正規化としては 名寄せ結果の `artists` を作ってそこに `artist_translations` を
ぶら下げるのが正しい。それでも `image_artist_names` (エントリ単位) を選んだ理由:

- **キーが動かない**。`artists.id` は名寄せルールの改良で採番が変わる。翻訳は
  作り直しにくい成果物なので、動くキーにぶら下げるべきでない
- **同姓同名の誤統合が原理的に起きない**。統合しないため。裸の名前しか持たない
  ソース (Met の `artistDisplayName`、Paris Musées の `name`) では、名前文字列を
  キーにすると別人が1行に潰れる
- **作品の文脈を訳出に使える**。漢字圏の人名はローマ字表記が衝突するため
  (Kano Masanobu = 狩野正信 / 狩野雅信)、名前だけでは漢字を確定できない

代償は同じ人物を何度も訳すこと (エントリ数/実人数は Cleveland 5.9倍、
Paris Musées 7.4倍、Smithsonian 3.8倍)。これは**スキーマではなく生成側で吸収**する
— CSV は生表記単位で1行だけ持ち、適用時に同じ表記の全行へ展開する。
「同じ文字列には同じ訳を当てる」だけで「同じ文字列は同じ人物」とは主張しないので、
上記の誤統合は起きない。訳を分けるべき衝突が見つかったときだけ行を分ける。

### 4. 名寄せ (`artists`) の主キーは autoincrement ではなく `person_key`

名寄せは日本語名の前提ではなくなったので、用途は「作者軸で作品を横断的に集める」
ことだけ。ただし**作者一覧を出すのは必須要件**なので、クラスタの代表表記を置く場所が
要る。`artists` は作る。

```sql
CREATE TABLE artists (
  person_key   TEXT PRIMARY KEY,   -- 'ulan:500031075' | 'cluster:aic|27558'
  display_name TEXT NOT NULL,
  birth_year   INTEGER,
  death_year   INTEGER,
  image_count  INTEGER NOT NULL
);
```

当初は「導入すると `artists` だけ派生層から外れる (id を持ち続ける必要があるため
`DELETE; INSERT` ができなくなる)」を最大の代償として後回しにしていたが、これは
**autoincrement の `id` を主キーにした場合の話**だった。`person_key` を自然キーに
すれば起きない。

- エントリの約6割を占める `ulan:` / `wd:` は**外部で安定した識別子**で、名寄せ
  ルールを改良しても値が変わらない
- `cluster:` 側も連番ではなく中身から決定的に導く (`spec_normalization.md`
  「`person_key` を連番にしなかった」)。同じルールで再実行すれば必ず同じキーになる

人物単位でぶら下がるものが無い (§3 の決定どおり日本語名はエントリ単位の
`image_artist_names` に置く) ので、`artists` は `image_dates` と同じ普通の派生
テーブルとして扱える。

### 5. `display_name` は「役割接頭辞を除いてから最頻」

素朴に最頻の `name_raw` を採ると作者一覧が壊れる。ティツィアーノのクラスタでの実測:

```
After Titian (Tiziano Vecellio)   28   ← 最頻。これが一覧に並んでしまう
Titiaan                           28
Titian (Tiziano Vecellio)          4
```

Met は `After` / `Attributed to` / `Possibly after a design by` を名前文字列そのものに
埋め込んでおり、`role_bucket` では捕まらない (Met の `role_raw` は `Maker` 等で、
`after` はバケットに落ちていない)。接頭辞を除いた表記に正規化してから最頻を採ると
Met 側が `Titian (Tiziano Vecellio)` に寄り、38対28 で `Titiaan` に勝つ。

### 6. 信頼度は `artists` ではなく `image_artists` 側に持つ

作者一覧を信頼度で絞るときは `image_artists.match_confidence` を JOIN して使う。
`artists` に信頼度を持たせないので、精度の低い手法 (LLM 等) を後から足しても
`artists` の定義を変えずに済む。JOIN を毎回書くのが面倒になったら
`artists.min_confidence` を `ALTER TABLE` で足せばよい (派生テーブルなので追加
コストは再生成の数分だけ)。

## 作業手順

```bash
ruby loader.rb                       # LMDB → images, image_artists (全ソース)
ruby normalize_dates.rb              # → image_dates
ruby normalize_artists.rb            # → image_artists.role_bucket
ruby apply_translations.rb seed      # 館が持つ原語表記 → image_artist_names
ruby apply_translations.rb titles    # titles_ja_*.csv → image_translations
ruby apply_translations.rb artists   # artist_names_ja_*.csv → image_artist_names
ruby apply_translations.rb stale     # 原文が変わった訳を検出
ruby normalize_person.rb             # → artists, image_artists.person_key ほか
```

後半4つは互いに独立で順不同。`loader.rb` の後に回せばよい。

`normalize_person.rb` だけは `normalize_artists.rb` の後に置く。名寄せの母数を
決めるのに `role_bucket` を見るため。

`seed` と `artists` の順序だけは意味がある: 館が持つ原語表記 (`method='source'`) は
LLM 訳より信頼できるので、`artists` は `method='source'` の行を上書きしない。

## 鮮度検知

`image_translations.source_text` と `image_artist_names.name_original` に訳出時の
原文スナップショットを持たせてある。`apply_translations.rb stale` が現在値と
突き合わせて、再翻訳が要る行を機械的に出す。

`image_artist_names` 側は原文の変化だけでなく **`position` のずれ**の検知も兼ねる。
ソースが `creators[]` の順序を変えると別の作者に訳が付いてしまうため。
