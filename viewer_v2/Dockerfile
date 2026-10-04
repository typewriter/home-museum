# viewer_v2 のイメージ。viewer.db は同梱せず、/var/lib/viewer を volume で受け取る。
#
#   cd viewer_v2 && docker compose up -d --build

FROM node:24-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /web/dist ./web/dist
# modernc.org/sqlite は純 Go なので cgo は要らない。
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/viewer_v2 .

FROM debian:trixie-slim

# 画像変換は vipsthumbnail / vipsheader に外出ししてある (spec_image_cache.md §5)。
# 実行時にも Converter.Check が同じ検査をして起動を止めるが、ビルド時に落とした
# ほうが原因が分かりやすい。
RUN apt-get update \
 && apt-get install -y --no-install-recommends libvips-tools ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && vips --vips-config | grep -q "WebP load/save with libwebp: true"

COPY --from=build /out/viewer_v2 /app/viewer_v2

# /var/lib/viewer にはホストのディレクトリを bind mount する。所有者はホスト側に
# なるので、compose は user: でホストの UID を渡す。
RUN mkdir -p /var/lib/viewer && chown 10001:10001 /var/lib/viewer
USER 10001:10001
WORKDIR /var/lib/viewer

ENV VIEWER_DB=/var/lib/viewer/viewer.db \
    VIEWER_IMAGE_STORE=r2

EXPOSE 8080
CMD ["sh", "-c", "exec /app/viewer_v2 serve -addr 0.0.0.0:${PORT:-8080}"]
