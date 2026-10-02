package httpapi

import "net/http"

type poolRedirectStartedKey struct{}

// Preserve ResponseController access to the original writer.
type poolResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *poolResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *poolResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *poolResponseWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
