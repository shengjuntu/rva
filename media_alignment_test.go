package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func alignmentCall(a *App, method, id string, body any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "/api/projects/"+id+"/media-alignment", bytes.NewReader(raw))
	r.SetPathValue("id", id)
	w := httptest.NewRecorder()
	if method == "GET" {
		a.mediaAlignmentGet(w, r)
	} else {
		a.mediaAlignmentUpdate(w, r)
	}
	return w
}

func alignmentProject(t *testing.T, a *App, text string) (Project, Asset) {
	t.Helper()
	p, err := a.create("alignment")
	if err != nil {
		t.Fatal(err)
	}
	asset := Asset{ID: uid(), Name: "image.png", MIME: "image/png", File: "picture.png", Size: 5}
	if err = os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File), []byte("image"), 0600); err != nil {
		t.Fatal(err)
	}
	p.NarrationText = text
	p.Assets = []Asset{asset}
	if err = a.save(&p); err != nil {
		t.Fatal(err)
	}
	a.projects[p.ID] = &p
	return p, asset
}

func alignmentInput(p Project, asset Asset, quote string) MediaAlignmentRequest {
	return MediaAlignmentRequest{Revision: p.Revision, SourceHash: narrationSourceHash(p), Units: []MediaAlignmentUnit{{ID: "product", Quote: quote, VisualIntent: "展示产品外观", AssetIDs: []string{asset.ID}, Role: "illustration", Reason: "图片展示文本提到的产品；不证明未来计划", Status: "selected"}}}
}

func decodeAlignment(t *testing.T, w *httptest.ResponseRecorder) MediaAlignmentView {
	t.Helper()
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var view MediaAlignmentView
	if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	return view
}

func TestMediaAlignmentPersistsPartialCoverageWithoutRewritingText(t *testing.T) {
	a := testApp(t)
	p, asset := alignmentProject(t, a, "新产品发布。明年量产。")
	in := alignmentInput(p, asset, "新产品发布")
	v := decodeAlignment(t, alignmentCall(a, "PUT", p.ID, in))
	if v.State != "blocked" || v.Coverage.Complete || v.Coverage.CoveredCharacters != 5 || v.Coverage.TotalCharacters != 9 || v.Alignment.Units[0].Occurrence != 1 {
		t.Fatal(v)
	}
	if len(v.Coverage.Uncovered) != 1 || v.Coverage.Uncovered[0].Quote != "明年量产" || v.Coverage.Uncovered[0].StartRune != 6 || v.Coverage.Uncovered[0].EndRune != 10 {
		t.Fatal(v.Coverage)
	}
	q, _ := a.get(p.ID)
	if q.NarrationText != p.NarrationText || q.MediaAlignment == nil || q.Revision != p.Revision+1 {
		t.Fatal(q)
	}
	in.Revision = q.Revision
	in.Units = append(in.Units, MediaAlignmentUnit{ID: "future", Quote: "明年量产", VisualIntent: "时间线文字卡", Role: "textcard", Reason: "表达未来计划，不使用工厂图片冒充已量产", Status: "selected"})
	v = decodeAlignment(t, alignmentCall(a, "PUT", p.ID, in))
	if v.State != "ready" || !v.Coverage.Complete || v.Coverage.SelectedUnits != 2 {
		t.Fatal(v)
	}
	reloaded, err := newApp(a.data)
	if err != nil {
		t.Fatal(err)
	}
	v = decodeAlignment(t, alignmentCall(reloaded, "GET", p.ID, nil))
	if v.State != "ready" || len(v.Alignment.Units) != 2 || len(v.Assets) != 1 || v.Assets[0].Origin != "unrecorded" {
		t.Fatal(v)
	}
}

func TestMediaAlignmentMCPPersistsAndRetrievesTheSameDocument(t *testing.T) {
	a := testApp(t)
	p, asset := alignmentProject(t, a, "以文字为核心。")
	input := alignmentInput(p, asset, "以文字为核心")
	args := map[string]any{"projectId": p.ID, "revision": p.Revision, "sourceHash": input.SourceHash, "units": input.Units}
	raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "video_media_alignment_update", "arguments": args}})
	var out bytes.Buffer
	runMCP(bytes.NewReader(raw), &out, "http://localhost", a.token, &http.Client{Transport: localMCPTransport{handler: a.routes()}})
	if strings.Contains(out.String(), `"isError":true`) {
		t.Fatal(out.String())
	}
	q, _ := a.get(p.ID)
	if q.MediaAlignment == nil || q.MediaAlignment.Units[0].Quote != input.Units[0].Quote {
		t.Fatal(out.String(), q.MediaAlignment)
	}
	raw, _ = json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "video_media_alignment_get", "arguments": map[string]any{"projectId": p.ID}}})
	out.Reset()
	runMCP(bytes.NewReader(raw), &out, "http://localhost", a.token, &http.Client{Transport: localMCPTransport{handler: a.routes()}})
	if strings.Contains(out.String(), `"isError":true`) || !strings.Contains(out.String(), "visualIntent") || !strings.Contains(out.String(), "sourceHash") {
		t.Fatal(out.String())
	}
}

func TestPreparationRequiresResolvedCoverageAndCurrentSpeech(t *testing.T) {
	a := testApp(t)
	p, asset := alignmentProject(t, a, "原文。")
	in := alignmentInput(p, asset, "原文")
	in.Units[0].Status = "planned"
	v := decodeAlignment(t, alignmentCall(a, "PUT", p.ID, in))
	if v.State != "blocked" || v.Coverage.Complete || v.Coverage.PlannedUnits != 1 || v.Coverage.CoveredCharacters != 0 {
		t.Fatal(v)
	}
	q, _ := a.get(p.ID)
	in.Revision, in.Units[0].Status = q.Revision, "selected"
	decodeAlignment(t, alignmentCall(a, "PUT", p.ID, in))
	cards, _ := readBoard(t, a, p.ID)
	if cards["preparation"].State == "ready" {
		t.Fatal("alignment without speech marked ready", cards["preparation"])
	}
	q, _ = a.get(p.ID)
	projection := clone(&q)
	projection.Shots = narrationShots(q.NarrationText)
	q.Speech = []SpeechJob{{ID: uid(), Source: "narration", State: "completed", ScriptHash: speechScriptHash(projection), Duration: 1}}
	a.projects[p.ID] = &q
	cards, _ = readBoard(t, a, p.ID)
	if cards["preparation"].State != "ready" {
		t.Fatal(cards["preparation"])
	}
	q.NarrationText = "改稿。"
	a.projects[p.ID] = &q
	cards, _ = readBoard(t, a, p.ID)
	if cards["preparation"].State != "stale" {
		t.Fatal(cards["preparation"])
	}
}

func TestMediaAlignmentRequiresExactUnambiguousSourceReference(t *testing.T) {
	a := testApp(t)
	p, asset := alignmentProject(t, a, "芯片发布。芯片发布。")
	in := alignmentInput(p, asset, "芯片发布")
	w := alignmentCall(a, "PUT", p.ID, in)
	if w.Code != 422 || !strings.Contains(w.Body.String(), "occurrence") {
		t.Fatal(w.Code, w.Body.String())
	}
	in.Units[0].Occurrence = 2
	v := decodeAlignment(t, alignmentCall(a, "PUT", p.ID, in))
	if v.Coverage.CoveredCharacters != 4 || v.Coverage.Complete {
		t.Fatal(v)
	}
	q, _ := a.get(p.ID)
	in.Revision = q.Revision
	in.Units[0].Quote = "新芯片发布"
	if w = alignmentCall(a, "PUT", p.ID, in); w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
	in.Units[0].Quote = "芯片发布"
	in.Units[0].Occurrence = 3
	if w = alignmentCall(a, "PUT", p.ID, in); w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestMediaAlignmentStalesOnChosenBytesSourceAndNarration(t *testing.T) {
	a := testApp(t)
	p, asset := alignmentProject(t, a, "芯片发布。")
	v := decodeAlignment(t, alignmentCall(a, "PUT", p.ID, alignmentInput(p, asset, "芯片发布")))
	if v.State != "ready" {
		t.Fatal(v)
	}
	// Equal-length mutations must also invalidate the byte fingerprint.
	os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File), []byte("other"), 0600)
	v = decodeAlignment(t, alignmentCall(a, "GET", p.ID, nil))
	if v.State != "stale" || len(v.StaleReasons) == 0 {
		t.Fatal(v)
	}
	os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File), []byte("image"), 0600)
	q, _ := a.get(p.ID)
	q.Assets[0].Source = &NewsImageSource{PageURL: "https://example.test/new-source"}
	a.projects[p.ID] = &q
	v = decodeAlignment(t, alignmentCall(a, "GET", p.ID, nil))
	if v.State != "stale" {
		t.Fatal(v)
	}
	q.Assets[0].Source = nil
	q.NarrationText = "旧芯片发布。"
	a.projects[p.ID] = &q
	v = decodeAlignment(t, alignmentCall(a, "GET", p.ID, nil))
	if v.State != "stale" || v.SourceHash == v.Alignment.SourceHash {
		t.Fatal(v)
	}
}

func TestMediaAlignmentKeepsUnrelatedAssetsAndReportsGeneratedProvenance(t *testing.T) {
	a := testApp(t)
	p, asset := alignmentProject(t, a, "原理示意。")
	p.Generation = []GenerationJob{{ID: uid(), Type: "image_generation", State: "completed", AssetID: asset.ID, Prompt: "原理示意", Model: "configured-model"}}
	a.projects[p.ID] = &p
	decodeAlignment(t, alignmentCall(a, "PUT", p.ID, alignmentInput(p, asset, "原理示意")))
	q, _ := a.get(p.ID)
	q.Assets = append(q.Assets, Asset{ID: uid(), Name: "unused.png", MIME: "image/png", File: "unused.png"})
	a.projects[p.ID] = &q
	v := decodeAlignment(t, alignmentCall(a, "GET", p.ID, nil))
	if v.State != "ready" || len(v.Assets) != 1 || v.Assets[0].Origin != "generated" || v.Assets[0].Generation.Prompt != "原理示意" {
		t.Fatal(v)
	}
	q.Generation[0].Prompt = "changed"
	a.projects[p.ID] = &q
	if v = decodeAlignment(t, alignmentCall(a, "GET", p.ID, nil)); v.State != "stale" {
		t.Fatal(v)
	}
}

func TestMediaAlignmentRejectsInvalidReferencesAndRevision(t *testing.T) {
	a := testApp(t)
	p, asset := alignmentProject(t, a, "产品发布。")
	for _, test := range []struct {
		name string
		edit func(*MediaAlignmentRequest)
		code int
	}{
		{"revision", func(in *MediaAlignmentRequest) { in.Revision++ }, 409},
		{"sourceHash", func(in *MediaAlignmentRequest) { in.SourceHash = "wrong" }, 409},
		{"unknown asset", func(in *MediaAlignmentRequest) { in.Units[0].AssetIDs = []string{uid()} }, 422},
		{"no selected asset", func(in *MediaAlignmentRequest) { in.Units[0].AssetIDs = nil }, 422},
		{"role", func(in *MediaAlignmentRequest) { in.Units[0].Role = "proof" }, 422},
		{"duplicate id", func(in *MediaAlignmentRequest) { in.Units = append(in.Units, in.Units[0]) }, 422},
		{"image range", func(in *MediaAlignmentRequest) {
			in.Units[0].Selections = []MediaAlignmentRange{{AssetID: asset.ID, SourceIn: 0, SourceOut: 1}}
		}, 422},
	} {
		t.Run(test.name, func(t *testing.T) {
			in := alignmentInput(p, asset, "产品发布")
			test.edit(&in)
			if w := alignmentCall(a, "PUT", p.ID, in); w.Code != test.code {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestMediaAlignmentVideoSelectionsUseActualDuration(t *testing.T) {
	a := testApp(t)
	p, asset := alignmentProject(t, a, "视频片段。")
	data, err := os.ReadFile("examples/edit-preview.mp4")
	if err != nil {
		t.Fatal(err)
	}
	asset.MIME, asset.File, asset.Size = "video/mp4", "footage.mp4", int64(len(data))
	os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File), data, 0600)
	p.Assets[0] = asset
	a.projects[p.ID] = &p
	in := alignmentInput(p, asset, "视频片段")
	in.Units[0].AssetIDs = nil
	in.Units[0].Selections = []MediaAlignmentRange{{AssetID: asset.ID, SourceIn: .1, SourceOut: .5}}
	v := decodeAlignment(t, alignmentCall(a, "PUT", p.ID, in))
	if v.State != "ready" || v.Alignment.Units[0].Selections[0].SourceOut != .5 {
		t.Fatal(v)
	}
	q, _ := a.get(p.ID)
	in.Revision = q.Revision
	in.Units[0].Selections[0].SourceOut = 10000
	if w := alignmentCall(a, "PUT", p.ID, in); w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, raw := range []string{`{"assetId":"id","sourceIn":null,"sourceOut":1}`, `{"assetId":"id","sourceOut":1}`, `{"assetId":"id","sourceIn":0,"sourceOut":null}`} {
		var selection MediaAlignmentRange
		if json.Unmarshal([]byte(raw), &selection) == nil {
			t.Fatal("null or omitted second accepted", raw)
		}
	}
}

func TestThreeStageBoardDoesNotTreatFilesOrPreviewAsCompletedProduction(t *testing.T) {
	a := testApp(t)
	p, _ := alignmentProject(t, a, "只有文字和文件。")
	w := call(a, "GET", "/api/projects/"+p.ID+"/production-board", nil)
	var board struct {
		Stages        []ProductionCard `json:"stages"`
		Artifacts     []ProductionCard `json:"artifacts"`
		VersionStatus string           `json:"versionStatus"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &board); err != nil || len(board.Stages) != 3 || len(board.Artifacts) != 8 || board.VersionStatus != "draft" {
		t.Fatal(w.Body.String(), err)
	}
	if board.Stages[0].ID != "preparation" || board.Stages[1].ID != "editing" || board.Stages[2].ID != "refinement" || board.Stages[0].State == "ready" {
		t.Fatal(board)
	}
	d := simpleEdit()
	p.EditDSL, p.EditActive = &d, true
	p.Renders = []Render{{ID: uid(), State: "completed", Engine: "ffmpeg_edit", ContentHash: productionHash(p), SourceHash: narrationSourceHash(p), Range: &RenderRange{Start: 0, End: 1}}}
	a.projects[p.ID] = &p
	cards, _ := readBoard(t, a, p.ID)
	if cards["editing"].State == "ready" {
		t.Fatal("local preview marked as full draft", cards["editing"])
	}
	p.Renders[0].Range = nil
	p.Renders[0].State = "running"
	a.projects[p.ID] = &p
	cards, _ = readBoard(t, a, p.ID)
	if cards["editing"].State != "running" {
		t.Fatal("running render marked as completed draft", cards["editing"])
	}
	p.Renders[0].State = "completed"
	a.projects[p.ID] = &p
	cards, _ = readBoard(t, a, p.ID)
	if cards["editing"].State != "ready" || cards["refinement"].State == "ready" {
		t.Fatal(cards)
	}
}
