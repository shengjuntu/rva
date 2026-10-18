package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type generationMCPResult struct {
	IsError bool `json:"isError"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
		Data string `json:"data"`
		MIME string `json:"mimeType"`
	} `json:"content"`
}

func callGenerationMCP(t *testing.T, a *App, name string, args map[string]any) generationMCPResult {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": args}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://localhost/api/mcp", bytes.NewReader(raw))
	r.Header.Set("Authorization", "Bearer "+a.token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var reply struct {
		Result generationMCPResult `json:"result"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		t.Fatal(err, w.Body.String())
	}
	return reply.Result
}

// Exercise the complete HTTP MCP -> typed dispatcher -> App -> binary adapter
// path. The adapter is local test code; no paid provider or model is invoked.
func TestGenerationAndVideoInspectHTTPMCP(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("MCP generation")
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 16, 16))); err != nil {
		t.Fatal(err)
	}
	ref := Asset{ID: uid(), MIME: "image/png", Name: "reference.png", Size: int64(imageBytes.Len())}
	ref.File = ref.ID + ".png"
	if err := os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", ref.File), imageBytes.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.projects[p.ID].Assets = append(a.projects[p.ID].Assets, ref)
	a.mu.Unlock()
	videoBytes, err := os.ReadFile("examples/edit-preview.mp4")
	if err != nil {
		t.Fatal(err)
	}
	var posts atomic.Int32
	requests := make(chan map[string]any, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		posts.Add(1)
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		select {
		case requests <- payload:
		default:
		}
		_, _ = w.Write(videoBytes)
	}))
	defer provider.Close()
	t.Setenv("VIDEO_VIDEO_GEN_URL", provider.URL)
	t.Setenv("VIDEO_VIDEO_GEN_MODEL", "test-adapter")
	args := map[string]any{"projectId": p.ID, "revision": p.Revision, "requestId": uid(), "type": "video_generation", "options": map[string]any{"prompt": "animate this picture", "duration": 5, "aspectRatio": "9:16", "referenceAssetIds": []string{ref.ID}}}
	result := callGenerationMCP(t, a, "video_job_start", args)
	if result.IsError {
		t.Fatal(result)
	}
	a.wg.Wait()
	var payload map[string]any
	select {
	case payload = <-requests:
	case <-time.After(time.Second):
		t.Fatal("MCP generation never reached configured adapter")
	}
	if payload["duration"] != 5.0 || payload["aspectRatio"] != "9:16" {
		t.Fatal("MCP dropped generation options", payload)
	}
	refs, ok := payload["referenceImages"].([]any)
	if !ok || len(refs) != 1 {
		t.Fatal(payload)
	}
	sent := refs[0].(map[string]any)
	data, err := base64.StdEncoding.DecodeString(sent["data"].(string))
	if err != nil || !bytes.Equal(data, imageBytes.Bytes()) || sent["sha256"] != hashBytes(imageBytes.Bytes()) {
		t.Fatal("MCP reference bytes changed", err)
	}
	q, _ := a.get(p.ID)
	if len(q.Generation) != 1 || q.Generation[0].State != "completed" {
		t.Fatal(q.Generation)
	}
	assetID := q.Generation[0].AssetID
	result = callGenerationMCP(t, a, "video_asset_video_inspect", map[string]any{"projectId": p.ID, "assetId": assetID, "times": []float64{0, 0.1}})
	if result.IsError {
		t.Fatal(result)
	}
	images := 0
	for _, block := range result.Content {
		if block.Type != "image" {
			continue
		}
		images++
		data, err := base64.StdEncoding.DecodeString(block.Data)
		if err != nil || block.MIME != "image/png" {
			t.Fatal("image block encoded incorrectly", err)
		}
		if _, err := png.Decode(bytes.NewReader(data)); err != nil {
			t.Fatal("MCP image was not an actual PNG", err)
		}
	}
	if images != 2 || len(result.Content) != 5 {
		t.Fatalf("expected two real MCP image blocks, got %d: %+v", images, result)
	}
	for _, args := range []map[string]any{
		{"projectId": p.ID, "assetId": assetID, "times": nil},
		{"projectId": p.ID, "assetId": assetID, "times": []any{nil}},
		{"projectId": p.ID, "assetId": assetID, "times": []any{0, nil}},
		{"projectId": p.ID, "assetId": assetID},
		{"projectId": p.ID, "assetId": assetID, "times": []float64{0}, "ignoredField": true},
		{"projectId": p.ID, "assetId": assetID, "times": []float64{0, 0}},
	} {
		result := callGenerationMCP(t, a, "video_asset_video_inspect", args)
		if !result.IsError {
			t.Fatal("MCP accepted invalid video inspection args", args, result)
		}
	}
	for _, args := range []map[string]any{
		{"projectId": p.ID, "revision": q.Revision, "requestId": uid(), "type": "video_generation", "options": map[string]any{"prompt": "x", "unknownOption": true}},
		{"projectId": p.ID, "revision": q.Revision, "requestId": uid(), "type": "video_generation", "options": map[string]any{"prompt": "x"}, "unknownArgument": true},
	} {
		result := callGenerationMCP(t, a, "video_job_start", args)
		if !result.IsError {
			t.Fatal("MCP silently dropped an unknown generation field", args, result)
		}
	}
	if posts.Load() != 1 {
		t.Fatal("invalid MCP requests reached provider", posts.Load())
	}
}
