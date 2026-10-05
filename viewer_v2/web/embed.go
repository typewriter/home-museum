// Package webdist は Vite がビルドした画面 (dist/) を viewer_v2 のバイナリに埋め込む。
// dist/ には .gitkeep しかコミットしない。npm run build の前でも go build が通るように。
package webdist

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var embedded embed.FS

// Dist は dist/ を根にした FS。
func Dist() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
