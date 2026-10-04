package imagecache

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Presigner は署名付き GET URL を作れる保管先。R2 だけが実装する。
type Presigner interface {
	PresignGet(key string, signTime time.Time, expires time.Duration) string
}

// sigV4 は S3 互換 API のクエリ署名 (AWS Signature Version 4)。
//
// minio-go の PresignedGetObject は使わない。署名時刻が time.Now() に固定で、
// 渡せないため。署名時刻を固定の窓に揃えないと URL が毎回変わり、ブラウザの
// キャッシュが一度も効かない (spec_image_cache.md §9)。
type sigV4 struct {
	host, bucket, accessKey, secret, region string
}

// presign は path-style (https://host/bucket/key) の URL を返す。bucket が空なら
// virtual-hosted style (host にバケットが入っている) として扱う。
func (s sigV4) presign(key string, t time.Time, expires time.Duration) string {
	t = t.UTC()
	amzDate := t.Format("20060102T150405Z")
	day := t.Format("20060102")
	scope := day + "/" + s.region + "/s3/aws4_request"

	path := "/" + uriEncode(key, false)
	if s.bucket != "" {
		path = "/" + uriEncode(s.bucket, false) + path
	}
	q := [][2]string{ // キー名の昇順
		{"X-Amz-Algorithm", "AWS4-HMAC-SHA256"},
		{"X-Amz-Credential", s.accessKey + "/" + scope},
		{"X-Amz-Date", amzDate},
		{"X-Amz-Expires", fmt.Sprint(int64(expires / time.Second))},
		{"X-Amz-SignedHeaders", "host"},
	}
	parts := make([]string, len(q))
	for i, kv := range q {
		parts[i] = uriEncode(kv[0], true) + "=" + uriEncode(kv[1], true)
	}
	query := strings.Join(parts, "&")

	canonical := strings.Join([]string{
		"GET", path, query, "host:" + s.host + "\n", "host", "UNSIGNED-PAYLOAD",
	}, "\n")
	sum := sha256.Sum256([]byte(canonical))
	toSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", amzDate, scope, hex.EncodeToString(sum[:]),
	}, "\n")

	k := hmacSHA256([]byte("AWS4"+s.secret), day)
	k = hmacSHA256(k, s.region)
	k = hmacSHA256(k, "s3")
	k = hmacSHA256(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(k, toSign))

	return "https://" + s.host + path + "?" + query + "&X-Amz-Signature=" + sig
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

// uriEncode は SigV4 の UriEncode。url.PathEscape / QueryEscape はどちらも
// 規則が違う (前者は '=' や ':' を、後者は空白を '+' にする) ので使えない。
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9',
			c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
