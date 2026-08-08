package imagecache

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// ErrNotFound は保管先にオブジェクトが無いこと。
var ErrNotFound = errors.New("オブジェクトがありません")

// Blob は画像の保管先。R2 とローカルディレクトリを差し替えられるように
// 最小限の 3 操作だけに絞る (spec_image_cache.md §9)。
type Blob interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
	Stat(ctx context.Context, key string) (int64, error)
	Describe() string
}

// OpenBlob は "r2" または "local:<dir>" から保管先を作る。
//
// R2 は環境変数で設定する:
//
//	R2_ACCOUNT_ID (または R2_ENDPOINT), R2_BUCKET,
//	R2_ACCESS_KEY_ID, R2_SECRET_ACCESS_KEY
func OpenBlob(spec string) (Blob, error) {
	switch {
	case spec == "" || spec == "none":
		return nil, nil
	case strings.HasPrefix(spec, "local:"):
		dir := strings.TrimPrefix(spec, "local:")
		if dir == "" {
			return nil, errors.New("local: の後ろにディレクトリを指定してください")
		}
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return nil, err
		}
		return &localBlob{root: abs}, nil
	case spec == "r2":
		return openR2()
	default:
		return nil, fmt.Errorf("保管先の指定が不正です: %q (r2 または local:<dir>)", spec)
	}
}

// ---- ローカルディレクトリ ----

type localBlob struct{ root string }

func (l *localBlob) path(key string) string {
	return filepath.Join(l.root, filepath.FromSlash(key))
}

func (l *localBlob) Put(ctx context.Context, key string, r io.Reader, size int64, ct string) error {
	p := l.path(key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// 書きかけを見せないよう一時ファイル経由で置く。
	tmp, err := os.CreateTemp(filepath.Dir(p), ".put-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (l *localBlob) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	f, err := os.Open(l.path(key))
	if os.IsNotExist(err) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, 0, err
	}
	return f, fi.Size(), nil
}

func (l *localBlob) Stat(ctx context.Context, key string) (int64, error) {
	fi, err := os.Stat(l.path(key))
	if os.IsNotExist(err) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func (l *localBlob) Describe() string { return "local:" + l.root }

// ---- Cloudflare R2 (S3 互換) ----

type r2Blob struct {
	cl     *minio.Client
	bucket string
	label  string
}

func openR2() (Blob, error) {
	bucket := os.Getenv("R2_BUCKET")
	access := os.Getenv("R2_ACCESS_KEY_ID")
	secret := os.Getenv("R2_SECRET_ACCESS_KEY")
	endpoint := os.Getenv("R2_ENDPOINT")
	if endpoint == "" {
		if acct := os.Getenv("R2_ACCOUNT_ID"); acct != "" {
			endpoint = acct + ".r2.cloudflarestorage.com"
		}
	}
	// スキーム付きで書かれていても受ける。
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		endpoint = u.Host
	}

	var missing []string
	for name, v := range map[string]string{
		"R2_BUCKET": bucket, "R2_ACCESS_KEY_ID": access,
		"R2_SECRET_ACCESS_KEY": secret, "R2_ACCOUNT_ID または R2_ENDPOINT": endpoint,
	} {
		if v == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("R2 の設定が足りません: %s", strings.Join(missing, ", "))
	}

	cl, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(access, secret, ""),
		Secure: true,
		Region: "auto", // R2 は固定で auto
	})
	if err != nil {
		return nil, fmt.Errorf("R2 クライアントを作れません: %w", err)
	}
	return &r2Blob{cl: cl, bucket: bucket, label: "r2://" + bucket + " (" + endpoint + ")"}, nil
}

func (r *r2Blob) Put(ctx context.Context, key string, rd io.Reader, size int64, ct string) error {
	_, err := r.cl.PutObject(ctx, r.bucket, key, rd, size, minio.PutObjectOptions{
		ContentType: ct,
		// 無期限キャッシュ。内容が変わるときはキーが変わる (source_url のハッシュ)。
		CacheControl: "public, max-age=31536000, immutable",
	})
	return err
}

func (r *r2Blob) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	obj, err := r.cl.GetObject(ctx, r.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, 0, err
	}
	st, err := obj.Stat()
	if err != nil {
		obj.Close()
		if minio.ToErrorResponse(err).StatusCode == 404 {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	return obj, st.Size, nil
}

func (r *r2Blob) Stat(ctx context.Context, key string) (int64, error) {
	st, err := r.cl.StatObject(ctx, r.bucket, key, minio.StatObjectOptions{})
	if err != nil {
		if minio.ToErrorResponse(err).StatusCode == 404 {
			return 0, ErrNotFound
		}
		return 0, err
	}
	return st.Size, nil
}

func (r *r2Blob) Describe() string { return r.label }
