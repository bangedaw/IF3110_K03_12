package spa

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed all:dist
var embedded embed.FS

func Dist() fs.FS {
	sub, err := fs.Sub(embedded, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}

func Handler(fsys fs.FS) (http.Handler, bool) {
	if fsys == nil {
		return nil, false
	}
	if _, err := fs.Stat(fsys, "index.html"); err != nil {
		return nil, false
	}
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "frame-ancestors 'none'; base-uri 'none'; object-src 'none'")
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || name == "index.html" {
			serveIndex(w, r, fsys)
			return
		}
		if info, err := fs.Stat(fsys, name); err == nil && !info.IsDir() {
			if strings.HasPrefix(name, "assets/") {
				h.Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			files.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(name, "assets/") {
			http.NotFound(w, r)
			return
		}
		serveIndex(w, r, fsys)
	}), true
}

func serveIndex(w http.ResponseWriter, r *http.Request, fsys fs.FS) {
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, fsys, "index.html")
}
