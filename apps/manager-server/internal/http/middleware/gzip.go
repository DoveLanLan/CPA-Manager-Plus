package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

const gzipMinBodySize = 0

func WithGzipJSON(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !acceptsGzip(r) {
			next(w, r)
			return
		}

		recorder := &responseRecorder{
			header: make(http.Header),
			status: http.StatusOK,
		}
		next(recorder, r)

		if recorder.header.Get("Content-Encoding") != "" || !isCompressibleJSON(recorder) {
			copyHeaders(w.Header(), recorder.header)
			if recorder.status != 0 {
				w.WriteHeader(recorder.status)
			}
			if recorder.body.Len() > 0 {
				_, _ = io.Copy(w, &recorder.body)
			}
			return
		}

		var compressed bytes.Buffer
		gzipWriter := gzip.NewWriter(&compressed)
		_, _ = io.Copy(gzipWriter, &recorder.body)
		_ = gzipWriter.Close()

		copyHeaders(w.Header(), recorder.header)
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Vary", joinVary(w.Header().Values("Vary"), "Accept-Encoding"))
		w.Header().Set("Content-Length", strconv.Itoa(compressed.Len()))
		if recorder.status != 0 {
			w.WriteHeader(recorder.status)
		}
		_, _ = io.Copy(w, &compressed)
	}
}

func acceptsGzip(r *http.Request) bool {
	return strings.Contains(strings.ToLower(r.Header.Get("Accept-Encoding")), "gzip")
}

func isCompressibleJSON(recorder *responseRecorder) bool {
	if recorder.status == http.StatusNoContent || recorder.status == http.StatusNotModified {
		return false
	}
	if recorder.body.Len() <= gzipMinBodySize {
		return false
	}

	contentType := recorder.header.Get("Content-Type")
	if contentType == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		mediaType = strings.ToLower(strings.TrimSpace(contentType))
	}
	switch mediaType {
	case "application/json", "application/x-ndjson", "text/plain", "text/csv":
		return true
	default:
		return strings.HasPrefix(mediaType, "text/")
	}
}

func copyHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		dst.Del(key)
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func joinVary(existing []string, value string) string {
	seen := make(map[string]struct{}, len(existing)+1)
	parts := make([]string, 0, len(existing)+1)
	for _, item := range existing {
		for _, part := range strings.Split(item, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			key := strings.ToLower(part)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			parts = append(parts, part)
		}
	}
	key := strings.ToLower(value)
	if _, ok := seen[key]; !ok {
		parts = append(parts, value)
	}
	return strings.Join(parts, ", ")
}

type responseRecorder struct {
	header      http.Header
	body        bytes.Buffer
	status      int
	wroteHeader bool
}

func (r *responseRecorder) Header() http.Header {
	return r.header
}

func (r *responseRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
}

func (r *responseRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.body.Write(p)
}
