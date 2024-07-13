package lazyassets

import (
	"fmt"
	"io"
	"net/http"
	"strings"
)

type TestFn func(f File) bool

func OnlyDirectories(subfolders ...string) TestFn {
	return func(f File) bool {
		for _, subfolder := range subfolders {
			if strings.HasPrefix(f.Path(), subfolder) {
				return true
			}
		}
		return false
	}
}
func OnlySubDirectories() TestFn {
	return func(f File) bool {
		return strings.Contains(f.Path(), "/")
	}
}

// OnlyAssets will have permalinks for images, css and js files
func OnlyAssets() TestFn {
	return func(f File) bool {
		mime := f.MimeType()
		return strings.HasPrefix(mime, "image/") || strings.HasPrefix(mime, "text/css") || strings.HasPrefix(mime, "application/javascript")
	}
}

func OnlyMimeTypes(mimes ...string) TestFn {
	return func(f File) bool {
		for _, mime := range mimes {
			if f.MimeType() == mime {
				return true
			}
		}
		return false
	}
}

func AllFiles() TestFn {
	return func(f File) bool {
		return true
	}
}

type Server struct {
	*Storage

	// PermalinkFiles is a function that returns true if the file should have a permalink
	// If nil, AllFiles is used
	PermalinkFiles TestFn

	// CSSTransformFiles is a function that returns true if the file css file should be transformed
	// If nil, AllFiles is used (meaning all css files)
	CSSTransformFiles TestFn

	// NextHandler is the next handler to call if the path is not found
	// If nil, 404 Not found is returned
	NextHandler http.Handler
}

// Permalink returns the permalink for the given path.
//
//	func (s *Storage) Permalink(p string) (string, *file) {
//		if len(p) == 0 {
//			return "", nil
//		}
//		if p[0] != '/' {
//			p = "/" + p
//		}
//
//		F := s.Find(p)
//		if F == nil {
//			return "", nil
//		}
//		f := F.(*file)
//		if f.isPermalink {
//			return p, f
//		}
//
//		path := withHash(p, f.Hash())
//		F = s.Find(path)
//		if F == nil {
//			return "", nil
//		}
//
//		return path, F.(*file)
//	}

// Routes returns all the routes that are available
// This is an expensive operation as it needs to hash all the files
func (m *Server) Routes() []Route {
	if m.paths == nil {
		return nil
	}

	path := m.paths.All()
	routes := make([]Route, len(path))
	for i, route := range path {

		url := route.Req.URL.String()
		if f := m.Find(url); f != nil {
			url = f.Permalink()
		}

		routes[i] = Route{
			route.Req.URL.String(),
			url,
			route.Req.URL.String(),
		}
	}
	return routes
}

func (m *Server) find(path string) File {
	f := m.Find(path)
	if f != nil {
		return f
	}
	path, hash, err := withoutHash(path)
	if err != nil {
		return nil
	}

	f = m.Find(path)
	h := f.Hash()
	if f == nil || hash != h {
		return nil
	}
	if m.PermalinkFiles != nil && !m.PermalinkFiles(f) {
		return nil
	}
	return f
}

func (m *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	F := m.find(r.URL.Path)
	if F == nil {
		if m.NextHandler != nil {
			m.NextHandler.ServeHTTP(w, r)
			return
		} else {
			http.NotFound(w, r)
		}
		return
	}
	f := F.(*file)
	f.init()

	noMatch := r.Header.Get("If-None-Match")
	if strings.Contains(noMatch, f.h.Etag()) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", f.mime)
	file, err := f.openFn()
	if err != nil {
		http.Error(w, fmt.Errorf("can't read the file: %w", err).Error(), http.StatusInternalServerError)
		return
	}
	if c, ok := file.(io.Closer); ok {
		defer c.Close()
	}
	if r.URL.Path[1:] == f.permalink {
		w.Header().Set("Cache-Control", "public, max-age=31536000")
	}
	w.Header().Set("ETag", `"`+f.h.Etag()+`"`)

	// Transform css files with permalinks
	if strings.HasPrefix(f.mime, "text/css") && (m.CSSTransformFiles == nil || m.CSSTransformFiles(f)) {
		cssFile, err := f.openFn()
		if err != nil {
			http.Error(w, fmt.Errorf("can't read the file: %w", err).Error(), http.StatusInternalServerError)
			return
		}
		defer cssFile.Close()
		data, err := io.ReadAll(cssFile)
		if err != nil {
			http.Error(w, fmt.Errorf("can't read the file: %w", err).Error(), http.StatusInternalServerError)
			return
		}
		w.Write([]byte(formatCSSPermalink(m, string(data))))
		return
	}

	io.Copy(w, file)
}
