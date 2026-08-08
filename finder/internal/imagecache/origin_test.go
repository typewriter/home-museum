package imagecache

import "testing"

// IIIF の size 部分だけを差し替えられているか。ここが壊れると館に原寸を
// 要求してしまい、負荷を抑えるという目的を静かに失う。
func TestOriginURL(t *testing.T) {
	cases := []struct {
		name  string
		ref   Ref
		width int
		want  string
	}{
		{
			name:  "aic は full/full を幅指定に置き換える",
			ref:   Ref{Source: "aic", ImageURL: "https://www.artic.edu/iiif/2/03c0fd45/full/full/0/default.jpg"},
			width: 1600,
			want:  "https://www.artic.edu/iiif/2/03c0fd45/full/1600,/0/default.jpg",
		},
		{
			name:  "rijksmuseum は full/max を幅指定に置き換える",
			ref:   Ref{Source: "rijksmuseum", ImageURL: "https://iiif.micr.io/owHmZ/full/max/0/default.jpg"},
			width: 1600,
			want:  "https://iiif.micr.io/owHmZ/full/1600,/0/default.jpg",
		},
		{
			name:  "既に幅指定なら上書きする",
			ref:   Ref{Source: "rijksmuseum", ImageURL: "https://iiif.micr.io/owHmZ/full/2400,/0/default.jpg"},
			width: 1600,
			want:  "https://iiif.micr.io/owHmZ/full/1600,/0/default.jpg",
		},
		{
			name:  "IIIF でない館はそのまま",
			ref:   Ref{Source: "cleveland", ImageURL: "https://openaccess-cdn.clevelandart.org/1919.55/1919.55_print.jpg"},
			width: 1600,
			want:  "https://openaccess-cdn.clevelandart.org/1919.55/1919.55_print.jpg",
		},
		{
			name:  "IIIF 風でない URL の館は触らない",
			ref:   Ref{Source: "aic", ImageURL: "https://example.org/a.jpg"},
			width: 1600,
			want:  "https://example.org/a.jpg",
		},
		{
			name:  "image_url が空なら空",
			ref:   Ref{Source: "aic"},
			width: 1600,
			want:  "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := OriginURL(c.ref, c.width); got != c.want {
				t.Errorf("OriginURL()\n got = %q\nwant = %q", got, c.want)
			}
		})
	}
}

// 保管キーは source_url のハッシュから決まる。images.id に依存しないことが
// 設計の要点なので、同じ source_url なら常に同じキーになることを確かめる。
func TestKeyIsStableForSourceURL(t *testing.T) {
	const u = "https://clevelandart.org/art/1919.55"
	a := Key("cleveland", Hash(u), 1600)
	b := Key("cleveland", Hash(u), 1600)
	if a != b {
		t.Fatalf("同じ source_url で異なるキー: %q vs %q", a, b)
	}
	if got, want := a, "img/cleveland/"+Hash(u)[:2]+"/"+Hash(u)+"/1600.webp"; got != want {
		t.Errorf("Key() = %q, want %q", got, want)
	}
	if Key("cleveland", Hash(u), 400) == a {
		t.Error("幅が違えば別のキーになるべき")
	}
}

func TestLooksLikeImage(t *testing.T) {
	for _, ct := range []string{"image/jpeg", "image/jpeg; charset=binary", "", "application/octet-stream"} {
		if !looksLikeImage(ct) {
			t.Errorf("looksLikeImage(%q) = false, want true", ct)
		}
	}
	for _, ct := range []string{"text/html", "text/html; charset=utf-8", "application/json"} {
		if looksLikeImage(ct) {
			t.Errorf("looksLikeImage(%q) = true, want false", ct)
		}
	}
}
