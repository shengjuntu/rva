package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestCapabilityDiscoverySeparatesConfiguredAndProbed(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v1/models" {
			t.Error("discovery synthesized or used unexpected endpoint", r.URL.Path)
		}
		writeJSON(w, 200, map[string]any{"data": []map[string]string{{"id": "IndexTeam/IndexTTS-2.5"}}})
	}))
	defer server.Close()
	a := testApp(t)
	a.tts = indexConfig(server.URL)
	plain := a.capabilityEnvelope(context.Background(), false)
	if calls.Load() != 0 {
		t.Fatal("default discovery contacted provider")
	}
	index := plain.Capabilities[1].Providers[1]
	if !index.Configured || index.Ready || (index.State != "configured" && index.State != "blocked") {
		t.Fatal(index)
	}
	probed := a.capabilityEnvelope(context.Background(), true)
	index = probed.Capabilities[1].Providers[1]
	if calls.Load() != 1 || index.Verified != "model_list" {
		t.Fatal(index, calls.Load())
	}
	raw, _ := json.Marshal(probed)
	if strings.Contains(string(raw), a.tts.APIKey) || strings.Contains(string(raw), server.URL) {
		t.Fatal("credentials/endpoint leaked")
	}
	for _, c := range probed.Capabilities {
		if c.ID == "image_generation" || c.ID == "video_generation" || c.ID == "text_media_alignment" {
			if c.Implemented || c.Ready || len(c.Tools) > 0 {
				t.Fatal("advertised unavailable generator", c)
			}
		}
	}
}
func TestAssetImportConcurrentRetriesAndConflicts(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("素材")
	in := AssetImportRequest{Name: "caption.txt", RequestID: uid(), Data: base64.StdEncoding.EncodeToString([]byte("新闻正文"))}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- call(a, "POST", "/api/projects/"+p.ID+"/assets/import", in).Code }()
	}
	wg.Wait()
	close(codes)
	for c := range codes {
		if c != 200 && c != 201 {
			t.Fatal(c)
		}
	}
	q, _ := a.get(p.ID)
	if len(q.Assets) != 1 || q.Revision != p.Revision+1 {
		t.Fatal("duplicate import", q.Revision, len(q.Assets))
	}
	in.Data = base64.StdEncoding.EncodeToString([]byte("不同正文"))
	if w := call(a, "POST", "/api/projects/"+p.ID+"/assets/import", in); w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	reload, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	in.Data = base64.StdEncoding.EncodeToString([]byte("新闻正文"))
	if w := call(reload, "POST", "/api/projects/"+p.ID+"/assets/import", in); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestUnifiedJobsPersistAndReplayRenderReceipt(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("任务")
	request := RenderRequest{RequestID: uid(), Revision: p.Revision, Kind: "draft"}
	raw, _ := json.Marshal(request)
	render := Render{ID: uid(), RequestID: request.RequestID, RequestHash: hashBytes(raw), State: "completed", Revision: p.Revision}
	speech := SpeechJob{ID: uid(), State: "interrupted", Total: 4, Progress: 2}
	p.Renders = append(p.Renders, render)
	p.Speech = append(p.Speech, speech)
	a.mu.Lock()
	a.projects[p.ID] = &p
	e := a.save(&p)
	a.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	reload, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	w := call(reload, "POST", "/api/projects/"+p.ID+"/jobs", JobStartRequest{Type: "render", Revision: request.Revision, RequestID: request.RequestID, Options: JobOptions{Kind: "draft"}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var j ProductionJob
	json.Unmarshal(w.Body.Bytes(), &j)
	if j.ID != "render:"+render.ID || j.State != "succeeded" || j.ProgressKnown || len(j.Artifacts) != 3 {
		t.Fatal(j)
	}
	w = call(reload, "POST", "/api/projects/"+p.ID+"/jobs", JobStartRequest{Type: "render", Revision: request.Revision, RequestID: request.RequestID})
	if w.Code != 409 {
		t.Fatal("different request reused", w.Code)
	}
	w = call(reload, "GET", "/api/projects/"+p.ID+"/jobs/tts:"+speech.ID, nil)
	json.Unmarshal(w.Body.Bytes(), &j)
	if w.Code != 200 || j.State != "interrupted" || !j.ProgressKnown || j.CompletedUnits != 2 {
		t.Fatal(w.Body.String())
	}
	other, _ := reload.create("另一个项目")
	if w = call(reload, "GET", "/api/projects/"+other.ID+"/jobs/render:"+render.ID, nil); w.Code != 404 {
		t.Fatal("cross-project task leak", w.Code)
	}
}
func TestJobCancellationIsIdempotentAndScoped(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("取消")
	rid, sid := uid(), uid()
	p.Renders = []Render{{ID: rid, State: "running"}}
	p.Speech = []SpeechJob{{ID: sid, State: "running"}}
	var renders, speeches atomic.Int32
	a.mu.Lock()
	a.projects[p.ID] = &p
	a.jobs[rid] = func() { renders.Add(1) }
	a.speechJobs[sid] = func() { speeches.Add(1) }
	a.mu.Unlock()
	for i := 0; i < 2; i++ {
		w := call(a, "POST", "/api/projects/"+p.ID+"/jobs/render:"+rid+"/cancel", map[string]any{})
		if w.Code != 200 || !strings.Contains(w.Body.String(), "cancel_requested") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if renders.Load() != 1 || speeches.Load() != 0 {
		t.Fatal("canceled wrong task or canceled twice")
	}
	a.mu.Lock()
	a.projects[p.ID].Renders[0].State = "completed"
	a.mu.Unlock()
	w := call(a, "POST", "/api/projects/"+p.ID+"/jobs/render:"+rid+"/cancel", map[string]any{})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "succeeded") {
		t.Fatal(w.Body.String())
	}
}
func TestJobRejectsUnavailableAndUnknownOptions(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("能力")
	w := call(a, "POST", "/api/projects/"+p.ID+"/jobs", JobStartRequest{Type: "image_generation", Revision: p.Revision, RequestID: uid()})
	if w.Code != 501 {
		t.Fatal(w.Code)
	}
	w = call(a, "POST", "/api/projects/"+p.ID+"/jobs", map[string]any{"type": "render", "revision": p.Revision, "requestId": uid(), "options": map[string]any{"invented": true}})
	if w.Code != 400 {
		t.Fatal("ignored option", w.Code)
	}
	var result bytes.Buffer
	request, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "video_job_start", "arguments": map[string]any{"projectId": p.ID, "type": "render", "revision": p.Revision, "requestId": uid(), "options": map[string]any{"invented": true}}}})
	runMCP(bytes.NewReader(request), &result, "http://localhost", a.token, &http.Client{Transport: localMCPTransport{handler: a.routes()}})
	if !strings.Contains(result.String(), `"isError":true`) {
		t.Fatal("MCP ignored unknown option", result.String())
	}
	q, _ := a.get(p.ID)
	if len(q.Speech)+len(q.Renders) != 0 {
		t.Fatal("unavailable capability created fake job")
	}
}
func TestProjectCreationReceiptSurvivesRename(t *testing.T) {
	a := testApp(t)
	requestID := uid()
	p, e := a.createRequested("初始名称", requestID)
	if e != nil {
		t.Fatal(e)
	}
	p.Name = "用户重命名"
	p, e = a.edit(p.ID, p)
	if e != nil {
		t.Fatal(e)
	}
	again, e := a.createRequested("初始名称", requestID)
	if e != nil || again.ID != p.ID || again.Name != p.Name {
		t.Fatal(again, e)
	}
	if _, e = a.createRequested("另一个请求", requestID); e == nil {
		t.Fatal("conflicting project creation accepted")
	}
}
func TestNewGlobalMCPToolsDoNotRequireProject(t *testing.T) {
	a := testApp(t)
	for _, name := range []string{"video_capabilities_get", "video_project_create"} {
		arguments := map[string]any{}
		if name == "video_project_create" {
			arguments["name"] = "MCP项目"
			arguments["requestId"] = uid()
		}
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": name, "arguments": arguments}})
		var result bytes.Buffer
		runMCP(bytes.NewReader(raw), &result, "http://localhost", a.token, &http.Client{Transport: localMCPTransport{handler: a.routes()}})
		if strings.Contains(result.String(), `"isError":true`) {
			t.Fatal(result.String())
		}
	}
}

func TestUnifiedTTSRunsActualMediaProcessing(t *testing.T) {
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip("ffmpeg unavailable")
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(indexTestWAV())
	}))
	defer server.Close()
	a := testApp(t)
	a.tts = indexConfig(server.URL)
	p := speechTestProject(t, a)
	in := JobStartRequest{Type: "tts", Revision: p.Revision, RequestID: uid()}
	w := call(a, "POST", "/api/projects/"+p.ID+"/jobs", in)
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var view ProductionJob
	json.Unmarshal(w.Body.Bytes(), &view)
	job := waitSpeech(t, a, p.ID, view.NativeID)
	if job.State != "completed" {
		t.Fatal(job.Error)
	}
	w = call(a, "GET", "/api/projects/"+p.ID+"/jobs/"+view.ID, nil)
	json.Unmarshal(w.Body.Bytes(), &view)
	if view.State != "succeeded" || len(view.Artifacts) == 0 {
		t.Fatal(w.Body.String())
	}
	count := calls.Load()
	w = call(a, "POST", "/api/projects/"+p.ID+"/jobs", in)
	if w.Code != 200 || calls.Load() != count {
		t.Fatal("retry synthesized again")
	}
	for _, artifact := range view.Artifacts {
		if artifact.Name == "narration.wav" {
			res := call(a, "GET", artifact.URL, nil)
			if res.Code != 200 || !bytes.HasPrefix(res.Body.Bytes(), []byte("RIFF")) {
				t.Fatal("completed artifact unavailable", res.Code)
			}
		}
	}
}
func TestConcurrentRenderSubmissionAllocatesOneTask(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("幂等渲染")
	p.Shots = []Shot{{Title: "标题", Text: "正文", Duration: 1}}
	p, e := a.edit(p.ID, p)
	if e != nil {
		t.Fatal(e)
	}
	a.hf = filepath.Join(a.data, "fixture-renderer.js")
	a.font = filepath.Join(a.data, "font.otf")
	a.gsap = filepath.Join(a.data, "gsap.js")
	// The fixture intentionally fails rendering; this test verifies allocation, not MP4 quality.
	for _, path := range []string{a.hf, a.font, a.gsap} {
		if e = os.WriteFile(path, []byte("process.exit(1);"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	in := JobStartRequest{Type: "render", Revision: p.Revision, RequestID: uid(), Options: JobOptions{Kind: "draft"}}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); codes <- call(a, "POST", "/api/projects/"+p.ID+"/jobs", in).Code }()
	}
	wg.Wait()
	close(codes)
	a.wg.Wait()
	for c := range codes {
		if c != 200 && c != 202 {
			t.Fatal(c)
		}
	}
	q, _ := a.get(p.ID)
	if len(q.Renders) != 1 {
		t.Fatal("allocated duplicate render", len(q.Renders))
	}
}

func TestFoundationMCPRejectsSilentArgumentLoss(t *testing.T) {
	for _, params := range []string{
		`{"arguments":{"name":"项目"}}`,
		`{"arguments":{"name":"项目","requestId":"0123456789abcdef01234567","provider":"invented"}}`,
	} {
		if e := validateFoundationArguments("video_project_create", json.RawMessage(params)); e == nil {
			t.Fatal("accepted invalid contract", params)
		}
	}
}
