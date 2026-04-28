package adminui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed index.html app.css app.js
var assets embed.FS

func Handler() http.Handler {
	sub, err := fs.Sub(assets, ".")
	if err != nil {
		panic(err)
	}
	fileServer := http.StripPrefix("/admin", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/admin")
		if path == "" || path == "/" {
			http.ServeFileFS(w, r, sub, "index.html")
			return
		}
		fileServer.ServeHTTP(w, r)
	})
}
