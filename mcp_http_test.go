package main

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPMCPRegistrationTools(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("MCP HTTP")
	for _, test := range []struct {
		method string
		params any
		want   string
	}{
		{"initialize", map[string]string{"protocolVersion": "2025-03-26"}, "2025-03-26"},
		{"tools/list", map[string]any{}, "video_project_get"},
		{"tools/call", map[string]any{"name": "video_project_get", "arguments": map[string]string{"projectId": p.ID}}, p.ID},
	} {
		b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": test.method, "params": test.params})
		r := httptest.NewRequest("POST", "http://localhost/api/mcp", bytes.NewReader(b))
		r.Header.Set("Authorization", "Bearer "+a.token)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		if w.Code != 200 || !strings.Contains(w.Body.String(), test.want) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("POST", "http://localhost/api/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
