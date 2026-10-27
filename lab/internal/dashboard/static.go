package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// The panel's files. web/app is the React build (`npm run build` in lab/web,
// done by the Docker image); without it, web/placeholder says how to get it.
const (
	appDir         = "web/app"
	placeholderDir = "web/placeholder"
	indexFile      = "index.html"
	assetsPrefix   = "assets/"
	// Built assets carry a content hash in their names, so they never change.
	immutableCache = "public, max-age=31536000, immutable"
	// The page itself is asked for again each time, to pick up a new build.
	revalidateCache = "no-cache"
)

//go:embed all:web
var webFiles embed.FS

// Every other path gets the app's page: the app's own router draws
// /rounds/r0004 and the rest.
type appHandler struct {
	files      fs.FS
	fileServer http.Handler
	page       []byte
}

func newAppHandler() appHandler {
	files := panelFiles()

	page, err := fs.ReadFile(files, indexFile)
	if err != nil {
		// Both directories are embedded at build time with an index.html.
		panic(err)
	}

	return appHandler{files: files, fileServer: http.FileServerFS(files), page: page}
}

func panelFiles() fs.FS {
	app, err := fs.Sub(webFiles, appDir)
	if err == nil {
		_, err = fs.Stat(app, indexFile)
	}

	if err == nil {
		return app
	}

	placeholder, err := fs.Sub(webFiles, placeholderDir)
	if err != nil {
		panic(err)
	}

	return placeholder
}

func (h appHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")

	if strings.HasPrefix(name, assetsPrefix) {
		w.Header().Set("Cache-Control", immutableCache)
		h.fileServer.ServeHTTP(w, r)

		return
	}

	info, err := fs.Stat(h.files, name)

	isFile := name != "" && name != indexFile && err == nil && !info.IsDir()
	if isFile {
		h.fileServer.ServeHTTP(w, r)

		return
	}

	w.Header().Set("Cache-Control", revalidateCache)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)

	// A failed write means the client went away.
	_, _ = w.Write(h.page)
}

func (p panelHandlers) apiNotFound(w http.ResponseWriter, r *http.Request) {
	p.writeError(w, r, errNotFound)
}
