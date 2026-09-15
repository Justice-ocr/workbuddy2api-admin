package main

import (
	"fmt"
	"net/http"
	"time"
)

type adminResponse struct {
	http.ResponseWriter
	status int
}

func (w *adminResponse) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}
func (w *adminResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (w *adminResponse) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *adminResponse) Flush() {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}

// No raw request/response bodies, query strings, identities or authorization headers.
func (a *adminServer) observeAPI(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		label := ""
		if r.URL.Path == "/v1/chat/completions" {
			label = "对话请求"
		}
		if r.URL.Path == "/v1/models" {
			label = "模型目录"
		}
		if label == "" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		out := &adminResponse{ResponseWriter: w}
		defer func() { a.event(label, fmt.Sprintf("HTTP %d · %d ms", out.status, time.Since(start).Milliseconds())) }()
		next.ServeHTTP(out, r)
	})
}
