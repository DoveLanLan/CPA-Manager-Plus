package panel

import (
	"compress/gzip"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
)

type Service struct {
	PanelPath string
	Embedded  fs.FS
}

func New(panelPath string, embedded fs.FS) *Service {
	return &Service{PanelPath: panelPath, Embedded: embedded}
}

func (s *Service) ServeManagementHTML(w http.ResponseWriter, r *http.Request, writeError func(http.ResponseWriter, int, error)) {
	if s.PanelPath != "" {
		if file, err := os.Open(s.PanelPath); err == nil {
			defer file.Close()
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			writeHTML(w, r, file)
			return
		}
	}
	data, err := fs.ReadFile(s.Embedded, "web/management.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("Content-Type", mime.TypeByExtension(".html"))
	writeHTMLBytes(w, r, data)
}

func acceptsGzip(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept-Encoding")), "gzip")
}

func writeHTML(w http.ResponseWriter, r *http.Request, file *os.File) {
	if acceptsGzip(r) {
		w.Header().Set("Vary", "Accept-Encoding")
		w.Header().Set("Content-Encoding", "gzip")
		gzipWriter := gzip.NewWriter(w)
		defer gzipWriter.Close()
		_, _ = io.Copy(gzipWriter, file)
		return
	}
	if stat, err := file.Stat(); err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
	}
	_, _ = io.Copy(w, file)
}

func writeHTMLBytes(w http.ResponseWriter, r *http.Request, data []byte) {
	if acceptsGzip(r) {
		w.Header().Set("Vary", "Accept-Encoding")
		w.Header().Set("Content-Encoding", "gzip")
		gzipWriter := gzip.NewWriter(w)
		defer gzipWriter.Close()
		_, _ = gzipWriter.Write(data)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	_, _ = w.Write(data)
}
