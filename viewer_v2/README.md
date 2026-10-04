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

```bash
sudo apt install libvips-tools               # 画像の変換に要る
go run . serve -images local:./imagecache    # 動作確認用 (R2 を使わない)
go run . serve -images r2                    # R2_ACCOUNT_ID / R2_BUCKET / R2_ACCESS_KEY_ID / R2_SECRET_ACCESS_KEY
```

画像キャッシュの設計は [`spec_image_cache.md`](spec_image_cache.md)、コレクションの設計は
[`spec_collections.md`](spec_collections.md)。

## 画面 (web/)

React + Vite + TypeScript。ビルドした `web/dist/` を Go のバイナリに埋め込む (`web/embed.go`)。

```bash
cd web
npm ci
npm run dev     # http://localhost:5173 。/api と /img は 127.0.0.1:8080 の serve に任せる
npm run build   # dist/ を作る。go build の前に要る (無ければ serve は 503 を返す)
```

**経路はライブラリを使わず自前で持つ** (`web/src/shared/router.tsx`)。作者のキー
(person_key) は `/` を含むことがあり (`cluster:rijksmuseum|https://id.rijksmuseum.nl/...`)、
`%2F` のまま 1 区切りとして扱いたい。ルーターライブラリの多くはパスをデコードしてから
照合するので、そこで区切りが増える。

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

## VPS へのデプロイ

`.env` に `R2_ACCOUNT_ID` / `R2_BUCKET` / `R2_ACCESS_KEY_ID` / `R2_SECRET_ACCESS_KEY` と
`VIEWER_BASE_URL` (例: `https://uchibi.nyamikan.net`) を書く。

```bash
docker compose -f docker-compose.yml -f compose.https.yml up -d --build
```

HTTPS の受け口は複数サービス共通の `../caddy-host` (このリポジトリの外)。その Caddyfile に
次を足す。パスは `/api` や `/img` を絶対パスで持っているので、ドメインの直下に置く。

```caddyfile
{$VIEWER_DOMAIN} {
	@admin path /admin /admin/* /api/admin/*
	basic_auth @admin {
		{$VIEWER_ADMIN_USER} {$VIEWER_ADMIN_HASH}   # caddy hash-password で作る
	}
	reverse_proxy viewer:8080
}
```

**管理画面の認証は Caddy にだけ任せている。** viewer_v2 自身は認証を持たないので、
コンテナのポートを外に出さないこと (`docker-compose.yml` は 127.0.0.1 にしか開けない)。
basic 認証の資格情報はクロスサイトのリクエストにもブラウザが付けるので、書き込み系の
管理 API はアプリ側で同一オリジンに限っている (`internal/web/api_admin.go`)。

### 作品データの入れ替え

**import の前に VPS のスナップショットを取る。** viewer_v2 はバックアップの機能を持たず、
コレクション (人の手でしか作れない) を守るのはこのスナップショットだけになる。

```bash
# ローカル
go run . export -hm ../importer/hm.db -out works.db
scp works.db vps:home-museum/viewer_v2/data/

# VPS (サービスを止めてから)
docker compose stop viewer
docker compose run --rm viewer /app/viewer_v2 import works.db
docker compose start viewer
rm data/works.db
```

### finder からの移行 (1 回だけ)

finder の `cache.db` を `data/` に置いてから、画像キャッシュの状態を移す。移し終えたら
finder のコンテナを止めてよい (`cd ../finder && docker compose down`)。

```bash
docker compose run --rm viewer /app/viewer_v2 migrate-cache finder-cache.db
```
