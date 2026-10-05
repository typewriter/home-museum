package imagecache

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Converter は libvips (vipsthumbnail) を叩いてリサイズ + WebP 変換する。
//
// Go に実用的な lossy WebP エンコーダが純 Go で無いため外部コマンドにしている。
// cwebp ではなく libvips なのは shrink-on-load のため。詳細は
// spec_image_cache.md §5。
type Converter struct {
	VipsThumbnail string // 既定 "vipsthumbnail"
	VipsHeader    string // 既定 "vipsheader"
	Quality       int    // 既定 80
	Effort        int    // 既定 6
}

func (c *Converter) bin(name, def string) string {
	if name != "" {
		return name
	}
	return def
}

// Check は起動時に呼ぶ。コマンドの存在と WebP 対応を確かめ、駄目なら
// 分かる形で失敗させる (実行時に初めて気づくのを避ける)。
func (c *Converter) Check(ctx context.Context) error {
	tb := c.bin(c.VipsThumbnail, "vipsthumbnail")
	if _, err := exec.LookPath(tb); err != nil {
		return fmt.Errorf("%s が見つかりません。`sudo apt install libvips-tools webp` を実行してください: %w", tb, err)
	}
	out, err := exec.CommandContext(ctx, "vips", "--vips-config").CombinedOutput()
	if err != nil {
		return fmt.Errorf("vips --vips-config に失敗しました: %w", err)
	}
	if !strings.Contains(string(out), "WebP load/save with libwebp: true") {
		return fmt.Errorf("libvips が WebP 対応でビルドされていません")
	}
	return nil
}

// Variant は生成できた 1 サイズ。
type Variant struct {
	Width  int
	Path   string
	Bytes  int64
	PixelW int
	PixelH int
}

// Convert は原本ファイルから指定した幅の WebP を作る。出力は dir に置く。
//
// ICC は sRGB に変換してから剥がす。変換せずに剥がすと AdobeRGB の画像が
// 濁る (spec_image_cache.md §7)。
func (c *Converter) Convert(ctx context.Context, src, dir string, widths []int) ([]Variant, error) {
	q := c.Quality
	if q <= 0 {
		q = 80
	}
	e := c.Effort
	if e <= 0 {
		e = 6
	}
	opts := fmt.Sprintf("Q=%d,effort=%d,smart_subsample=true,keep=none", q, e)

	var out []Variant
	for _, w := range widths {
		dst := filepath.Join(dir, strconv.Itoa(w)+".webp")
		cmd := exec.CommandContext(ctx, c.bin(c.VipsThumbnail, "vipsthumbnail"),
			src,
			"--size", strconv.Itoa(w)+"x",
			"--export-profile", "srgb",
			"-o", dst+"["+opts+"]")
		// 1 枚あたり 1 コアに抑える。10 秒に 1 回なので急ぐ必要がない。
		cmd.Env = append(os.Environ(), "VIPS_CONCURRENCY=1", "VIPS_WARNING=0")
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("vipsthumbnail (%dpx) に失敗: %w: %s",
				w, err, strings.TrimSpace(stderr.String()))
		}

		// 出力の検証。変換が 0 バイトのファイルを残すことがある
		// (spec_image_cache.md §10)。
		fi, err := os.Stat(dst)
		if err != nil {
			return nil, fmt.Errorf("%dpx の出力がありません: %w", w, err)
		}
		if fi.Size() == 0 {
			return nil, fmt.Errorf("%dpx の出力が 0 バイトです", w)
		}
		pw, ph, err := c.dims(ctx, dst)
		if err != nil {
			return nil, fmt.Errorf("%dpx の出力を読めません: %w", w, err)
		}
		out = append(out, Variant{Width: w, Path: dst, Bytes: fi.Size(), PixelW: pw, PixelH: ph})
	}
	return out, nil
}

func (c *Converter) dims(ctx context.Context, path string) (int, int, error) {
	get := func(field string) (int, error) {
		out, err := exec.CommandContext(ctx, c.bin(c.VipsHeader, "vipsheader"),
			"-f", field, path).Output()
		if err != nil {
			return 0, err
		}
		return strconv.Atoi(strings.TrimSpace(string(out)))
	}
	w, err := get("width")
	if err != nil {
		return 0, 0, err
	}
	h, err := get("height")
	if err != nil {
		return 0, 0, err
	}
	return w, h, nil
}
