# viewer_v2

home-museum の作品を、展覧会のように作者別・コレクション別に見せる公開アプリ。
finder (内部のデータ点検ツール) と旧 `viewer/` + `server/` を置き換える。

## データの流れ

```
importer/hm.db ──export (ローカル)──▶ works.db ──コピー──▶ VPS ──import (サービス停止中)──▶ viewer.db
```

```bash
go run . export -hm ../importer/hm.db -out works.db
go run . import -db viewer.db works.db

# finder から移るときに 1 回だけ。R2_* 環境変数が要る
go run . migrate-cache -db viewer.db -images r2 ../finder/cache.db
```

画像キャッシュの設計は [`spec_image_cache.md`](spec_image_cache.md)。

## viewer.db は 2 層を 1 ファイルに持つ

| 層 | テーブル | 書き手 |
|---|---|---|
| 取り込み層 | `works`, `work_artists`, `artists`, `import_meta` | `import` (毎回 DROP して作り直す) |
| 所有層 | `collections`, `collection_works`, `image_cache` | `serve` (人の操作と画像の取得) |

**VPS に hm.db を置かず、export した works.db を運ぶ。** viewer_v2 が使うのは
hm.db の一部の列だけで、hm.db の書き手を importer のスクリプト群に限る原則
(`importer/docs/spec_schema.md`) も崩さずに済む。

**所有層を別ファイルにしない。** finder はコレクションを `collections.db` に分けて
ATTACH していたが、viewer_v2 は作品との JOIN を ATTACH なしで書けるほうを取った。
import が取り込み層のテーブルしか DROP しないことはテスト (`internal/export`) で守る。

**所有層は作品を `works.id` ではなく `source_url` で指す。** `works.id` は hm.db の
`images.id` で、hm.db を作り直すと変わる。import で引けなくなった行は消さずに
件数だけを出す。館側で一時的に消えた作品が次の export で戻ることがあるため。

**作者の作品数 (`artists.work_count`) は export で数え直す。** hm.db の
`artists.image_count` は画像の無い作品や、出版社・刷り師としての関与
(`role_bucket = 'non_creator'`) まで数えていて、公開する作者を選ぶ閾値に使えない。
