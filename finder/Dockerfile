# finder のイメージ。DB は同梱せず、hm.db と index.db は volume で受け取る。
#
#   cd finder && docker compose up --build
#
# build context は finder/ (DB を入れないのでリポジトリのルートである必要が無い)。

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# modernc.org/sqlite は純 Go なので cgo は要らない。
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/finder .

FROM debian:trixie-slim

# 画像変換は vipsthumbnail / vipsheader / vips に外出ししてある。
# WebP 対応の有無はここで確かめる。実行時に Converter.Check が同じ検査をして
# 起動を止めるので、ビルド時に落としたほうが原因が分かりやすい。
RUN apt-get update \
 && apt-get install -y --no-install-recommends libvips-tools ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && vips --vips-config | grep -q "WebP load/save with libwebp: true"

COPY --from=build /out/finder /app/finder

# 書き込み先は画像キャッシュの状態 DB だけ。hm.db / index.db は volume で
# 読み取り専用に渡す (docker-compose.yml)。
#
# /var/lib/finder には cache.db を置いたディレクトリを bind mount するので、
# 実際の所有者はホスト側のものになる。compose は user: でホストの UID を渡す。
# docker run で直接使うときは --user を合わせないと書き込めない。
RUN mkdir -p /var/lib/finder && chown 10001:10001 /var/lib/finder
USER 10001:10001

ENV DATABASE_PATH=/data/hm.db \
    FINDER_INDEX=/data/index.db \
    FINDER_CACHE=/var/lib/finder/cache.db \
    FINDER_COLLECTIONS=/var/lib/finder/collections.db \
    FINDER_COLLECTIONS_DIR=/var/lib/finder/collections

EXPOSE 8080

# PORT を見るのはリバースプロキシ側の都合に合わせられるようにするため。
# -sql=false: 読み取り専用とはいえ全表スキャンでメモリを飛ばせるので公開時は閉じる。
CMD ["sh", "-c", "exec /app/finder serve -addr 0.0.0.0:${PORT:-8080} -sql=false -images r2"]
