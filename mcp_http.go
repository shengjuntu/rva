package main

import (
	"bytes"
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// Stateless Streamable HTTP JSON responses, reusing the same tool dispatcher as stdio.
func (a *App) mcpHTTP(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+a.token)) != 1 {
		fail(w, 401, io.EOF)
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		w.WriteHeader(415)
		return
	}
	raw, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
	if e != nil {
		fail(w, 413, e)
		return
	}
	var msg struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      json.RawMessage `json:"id"`
		Method  string          `json:"method"`
	}
	if json.Unmarshal(raw, &msg) != nil || msg.JSONRPC != "2.0" || msg.Method == "" {
		writeJSON(w, 400, map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32600, "message": "Invalid Request"}})
		return
	}
	if len(msg.ID) == 0 {
		w.WriteHeader(202)
		return
	}
	// Tools are routed directly to our authenticated business handlers, without
	// depending on the public address being reachable from inside this process.
	var compact bytes.Buffer
	if e = json.Compact(&compact, raw); e != nil {
		fail(w, 400, e)
		return
	}
	var result bytes.Buffer
	client := &http.Client{Transport: localMCPTransport{handler: a.routes()}}
	runMCP(&compact, &result, "http://localhost", a.token, client)
	w.Header().Set("Content-Type", "application/json")
	if result.Len() == 0 {
		w.WriteHeader(400)
		return
	}
	w.Write(result.Bytes())
}

type localMCPTransport struct{ handler http.Handler }
type mcpResponse struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *mcpResponse) Header() http.Header { return w.header }
func (w *mcpResponse) WriteHeader(n int) {
	if w.status == 0 {
		w.status = n
	}
}
func (w *mcpResponse) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.body.Write(b)
}
func (t localMCPTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	w := &mcpResponse{header: make(http.Header)}
	t.handler.ServeHTTP(w, r)
	if w.status == 0 {
		w.status = 200
	}
	return &http.Response{StatusCode: w.status, Header: w.header, Body: io.NopCloser(bytes.NewReader(w.body.Bytes())), Request: r}, nil
}
