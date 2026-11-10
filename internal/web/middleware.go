package web

import (
	"errors"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

// instrument is the outermost layer: it logs every request with its status,
// turns a panic into a 500 instead of a dropped connection, and sets the two
// response headers that cost nothing and close a class of browser tricks.
func instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		// nosniff stops a browser treating a saved solution or a test log as a
		// script; DENY stops another site framing the workspace and clicking
		// through it.
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")

		defer func() {
			if v := recover(); v != nil {
				// net/http recovers too, but it closes the connection without a
				// response and the browser shows a blank page. ErrAbortHandler
				// is the sanctioned way to drop a connection, so it passes.
				if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
					panic(v)
				}
				log.Printf("panic: %s %s: %v\n%s", r.Method, r.URL.Path, v, debug.Stack())
				if !sw.wrote {
					http.Error(sw, "internal error", http.StatusInternalServerError)
				}
			}
			if !strings.HasPrefix(r.URL.Path, "/static/") {
				log.Printf("%s %s %d %s", r.Method, r.URL.Path, sw.status(), time.Since(start).Round(time.Millisecond))
			}
		}()
		next.ServeHTTP(sw, r)
	})
}

// statusWriter remembers the status for the log. Unwrap keeps
// http.ResponseController working through it, which the event stream relies
// on for Flush and for clearing its deadlines.
type statusWriter struct {
	http.ResponseWriter
	code  int
	wrote bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.code = code
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.code = http.StatusOK
		w.wrote = true
	}
	return w.ResponseWriter.Write(b)
}

// Flush is forwarded explicitly because the event stream asserts http.Flusher
// on the writer it is handed, and an embedded interface does not satisfy a
// type assertion for a method the outer type never declares.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		if !w.wrote {
			w.code = http.StatusOK
			w.wrote = true
		}
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) status() int {
	if !w.wrote {
		return http.StatusOK
	}
	return w.code
}

// limitForm bounds a form-encoded body the way decodeBody bounds JSON, and
// reports a body that is too large as 413 rather than as a missing field.
func limitForm(w http.ResponseWriter, r *http.Request) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	if err := r.ParseForm(); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "malformed form body", http.StatusBadRequest)
		}
		return false
	}
	return true
}
