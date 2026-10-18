package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"net/http/httptest"
	"testing"
)

func rootMCPCall(t *testing.T, a *App, name string, args map[string]any) map[string]any {
	t.Helper()
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	r := httptest.NewRequest("POST", "http://localhost/api/mcp", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+a.token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var response struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response.Result
}

func TestRootDiagnosticImageMCPPayload(t *testing.T) {
	a := testApp(t)
	nonce := uid()
	result := rootMCPCall(t, a, "rundesk_diagnostic_image", map[string]any{"nonce": nonce})
	if result["isError"] == true {
		t.Fatal(result)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) != 2 {
		t.Fatal(result)
	}
	metadata := map[string]any{}
	text := content[0].(map[string]any)
	if err := json.Unmarshal([]byte(text["text"].(string)), &metadata); err != nil {
		t.Fatal(err)
	}
	block := content[1].(map[string]any)
	b, err := base64.StdEncoding.Strict().DecodeString(block["data"].(string))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(b))
	if err != nil || cfg.Width != 32 || cfg.Height != 32 || block["mimeType"] != "image/png" {
		t.Fatal(cfg, err)
	}
	expected, err := diagnosticImage(nonce)
	if err != nil || !bytes.Equal(b, expected) || metadata["nonce"] != nonce || metadata["imageSHA256"] != hashBytes(b) || metadata["modelVisionVerified"] != false {
		t.Fatal(metadata, err)
	}
	other, _ := diagnosticImage(uid())
	if bytes.Equal(other, b) {
		t.Fatal("different nonce returned identical payload")
	}
	for _, args := range []map[string]any{{}, {"nonce": "../outside"}, {"nonce": nil}, {"nonce": nonce, "projectId": uid()}} {
		if rootMCPCall(t, a, "rundesk_diagnostic_image", args)["isError"] != true {
			t.Fatal("invalid diagnostic arguments accepted", args)
		}
	}
}

func TestRootLocateMCPPreservesOptionalOccurrence(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("原文定位")
	p.NarrationText = "芯片发布。准备量产。"
	d := simpleEdit()
	d.TextSourceHash = narrationSourceHash(p)
	d.Clips[0].TextRefs = []TextReference{{Quote: "芯片发布。"}}
	p.EditDSL = &d
	p.EditActive = true
	a.save(&p)
	a.projects[p.ID] = &p
	result := rootMCPCall(t, a, "video_edit_locate", map[string]any{"projectId": p.ID, "quote": "芯片发布。"})
	if result["isError"] == true {
		t.Fatal(result)
	}
	content := result["content"].([]any)
	var located EditLocateResult
	if err := json.Unmarshal([]byte(content[0].(map[string]any)["text"].(string)), &located); err != nil {
		t.Fatal(err)
	}
	if len(located.Matches) != 1 || located.Matches[0].ClipID != d.Clips[0].ID || located.Query.Occurrence != 1 {
		t.Fatal(located)
	}
	for _, value := range []any{0, -1, nil, 1.2} {
		if rootMCPCall(t, a, "video_edit_locate", map[string]any{"projectId": p.ID, "quote": "芯片发布。", "occurrence": value})["isError"] != true {
			t.Fatal("invalid occurrence accepted", value)
		}
	}
}
