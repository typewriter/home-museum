package imagecache

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// Ref は 1 作品ぶんの取得に必要な情報。web 層が hm.db から拾って渡す。
type Ref struct {
	ID        int64
	Source    string
	SourceURL string
	ImageURL  string
}

// Hash は保管キーの素。images.id ではなく source_url を使う理由は
// spec_image_cache.md §1。
func Hash(sourceURL string) string {
	sum := sha256.Sum256([]byte(sourceURL))
	return hex.EncodeToString(sum[:])
}

// Key は保管先のオブジェクトキー。1 プレフィックスに数十万個を置かないよう
// ハッシュ先頭 2 文字で階層を切る。
func Key(source, hash string, width int) string {
	return fmt.Sprintf("img/%s/%s/%s/%d.webp", source, hash[:2], hash, width)
}

// iiifSize は IIIF Image API の size 部分 (…/full/{size}/{rotation}/…) を捕まえる。
// 現在 hm.db に入っているのは full/full (AIC) と full/max (Rijksmuseum)。
var iiifSize = regexp.MustCompile(`/full/(full|max|\d+,|,\d+|!?\d+,\d+)/(\d+)/`)

// OriginURL は実際に館へ取りに行く URL を組み立てる。
//
// IIIF の館 (aic / rijksmuseum、全体の 55%) は原寸ではなく必要な幅を要求する。
// 館の転送量が 58% 減り、生成される WebP もむしろ小さい (spec_image_cache.md §4)。
// 館ごとの癖はこの関数だけに閉じ込める。
func OriginURL(r Ref, width int) string {
	if r.ImageURL == "" {
		return ""
	}
	switch r.Source {
	case "aic", "rijksmuseum":
		if m := iiifSize.FindStringSubmatchIndex(r.ImageURL); m != nil {
			return r.ImageURL[:m[2]] + fmt.Sprintf("%d,", width) + r.ImageURL[m[3]:]
		}
	}
	return r.ImageURL
}

// browserUA は AIC 用。素性を名乗る既定の UA では通らないので已むを得ず使う。
const browserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

// requestHeaders は館ごとに必要な HTTP ヘッダ。既定の User-Agent を上書きできる。
//
// AIC の IIIF エンドポイントは **ブラウザ相当の User-Agent と Referer の両方**が
// 揃わないと 403 を返す (実測: UA だけ 403 / Referer だけ 403 / 両方で 200)。
// HEAD も拒否するので、サイズの事前確認はできない。
// 他の 5 館は素性を名乗る既定の UA で通るので、上書きしない。
func requestHeaders(source string) map[string]string {
	h := map[string]string{
		"Accept": "image/*,*/*;q=0.8",
	}
	if source == "aic" {
		h["User-Agent"] = browserUA
		h["Referer"] = "https://www.artic.edu/"
	}
	return h
}

// looksLikeImage は Content-Type が画像かどうか。館によっては 200 で
// HTML のエラーページを返すことがあるので、保存前に弾く。
func looksLikeImage(contentType string) bool {
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	switch ct {
	case "image/jpeg", "image/jpg", "image/png", "image/tiff", "image/webp",
		"image/gif", "application/octet-stream", "":
		// octet-stream と空は Smithsonian の ids.si.edu が返す。中身は
		// libvips に判定させる。
		return true
	}
	return false
}
