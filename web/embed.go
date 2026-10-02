package web

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/astaxie/beego"
)

//go:embed static
var StaticFS embed.FS

//go:embed views
var ViewsFS embed.FS

// slashFileSystem normalizes path separators so embed.FS works on Windows
// (beego uses filepath.Join which produces backslashes).
type slashFileSystem struct {
	fs http.FileSystem
}

func (s slashFileSystem) Open(name string) (http.File, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Clean("/" + strings.TrimPrefix(name, "/"))
	if name == "/" {
		name = "."
	} else {
		name = strings.TrimPrefix(name, "/")
	}
	return s.fs.Open(name)
}

// ViewsHTTPFS returns an http.FileSystem for embedded view templates.
// Paths are rooted at "views/..." to match beego SetViewsPath("views").
func ViewsHTTPFS() http.FileSystem {
	return slashFileSystem{fs: http.FS(ViewsFS)}
}

// diskFirstFS prefers a file on disk (for hot-added files such as npc
// binaries and install scripts) and falls back to the embedded FS.
type diskFirstFS struct {
	embedded http.FileSystem
	diskRoot string
}

func (d diskFirstFS) Open(name string) (http.File, error) {
	if d.diskRoot != "" {
		f, err := os.Open(filepath.Join(d.diskRoot, name))
		if err == nil {
			if st, serr := f.Stat(); serr == nil && !st.IsDir() {
				return f, nil
			}
			f.Close()
		}
	}
	return d.embedded.Open(name)
}

// StaticHTTPFS returns an http.FileSystem rooted at the embedded static/ directory
// (so Open("css/style.css") maps to static/css/style.css), with on-disk
// web/static/ taking priority so admins can drop extra files (e.g. npc
// binaries) into web/static/ and serve them over /static/ without a rebuild.
func StaticHTTPFS() http.FileSystem {
	sub, err := fs.Sub(StaticFS, "static")
	if err != nil {
		panic("web: embed static sub FS: " + err.Error())
	}
	return slashFileSystem{fs: diskFirstFS{embedded: http.FS(sub), diskRoot: filepath.Join("web", "static")}}
}

// ReadStaticFile reads a file from the embedded static FS.
// name is relative to static/, e.g. "page/error.html".
func ReadStaticFile(name string) ([]byte, error) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Clean("/" + strings.TrimPrefix(name, "/"))
	name = strings.TrimPrefix(name, "/")
	return StaticFS.ReadFile(path.Join("static", name))
}

// InitBeegoAssets configures Beego to serve views and static files exclusively
// from the embedded filesystem. Disk web/ directories are never used.
func InitBeegoAssets() {
	beego.SetTemplateFSFunc(func() http.FileSystem {
		return ViewsHTTPFS()
	})
	beego.SetViewsPath("views")

	// Drop default (and any prior) disk-based static mappings.
	beego.DelStaticPath("/static")
	base := strings.TrimSuffix(beego.AppConfig.String("web_base_url"), "/")
	staticPrefix := base + "/static"
	if !strings.HasPrefix(staticPrefix, "/") {
		staticPrefix = "/" + staticPrefix
	}
	staticPrefix = path.Clean(staticPrefix)
	if base != "" {
		// Also clear a bare /static mapping when web_base_url is set.
		beego.DelStaticPath(staticPrefix)
	}

	fileServer := http.StripPrefix(staticPrefix, http.FileServer(StaticHTTPFS()))
	// options[0]=true registers as prefix match: staticPrefix/?:all(.*)
	beego.Handler(staticPrefix, fileServer, true)
}
