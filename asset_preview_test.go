package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestMCPVisualEvidenceIsRealImage(t *testing.T) {
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip("ffmpeg unavailable")
	}
	a := testApp(t)
	p, _ := a.create("素材")
	asset := Asset{ID: uid(), Name: "红色图片.png", MIME: "image/png"}
	asset.File = asset.ID + ".png"
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	f, e := os.Create(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File))
	if e != nil {
		t.Fatal(e)
	}
	if e = png.Encode(f, img); e != nil {
		t.Fatal(e)
	}
	f.Close()
	a.mu.Lock()
	a.projects[p.ID].Assets = append(a.projects[p.ID].Assets, asset)
	a.mu.Unlock()
	var output bytes.Buffer
	request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "video_asset_inspect", "arguments": map[string]any{"projectId": p.ID, "assetId": asset.ID}}})
	runMCP(bytes.NewReader(request), &output, "http://localhost", a.token, &http.Client{Transport: localMCPTransport{handler: a.routes()}})
	var result struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Type string `json:"type"`
				Data string `json:"data"`
			} `json:"content"`
		} `json:"result"`
	}
	if e = json.Unmarshal(output.Bytes(), &result); e != nil {
		t.Fatal(e)
	}
	if result.Result.IsError || len(result.Result.Content) != 2 || result.Result.Content[1].Type != "image" || result.Result.Content[1].Data == "" {
		t.Fatal(output.String())
	}
}
func TestMCPProductionToolsHaveWorkingContracts(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("正文")
	var output bytes.Buffer
	request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "video_production_text", "arguments": map[string]any{"projectId": p.ID, "revision": p.Revision, "text": "第一条事实。"}}})
	runMCP(bytes.NewReader(request), &output, "http://localhost", a.token, &http.Client{Transport: localMCPTransport{handler: a.routes()}})
	q, e := a.get(p.ID)
	if e != nil || q.Production == nil || q.Production.Text != "第一条事实。" {
		t.Fatal(output.String(), e)
	}
}
