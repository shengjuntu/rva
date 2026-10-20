package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// Exercise real HTTP MCP, the durable job store and actual audio processing.
// Only the external IndexTTS inference response is a fixture.
func TestIndependentTextSpeechHTTPMCPPreservesEditorAndReceipts(t *testing.T) {
	var mu sync.Mutex
	var synthesized []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-index-secret" || r.URL.Path != "/v1/audio/speech" {
			t.Error("independent speech bypassed configured provider/authentication")
		}
		var request struct{ Input, Voice string }
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Voice != "news_zh" {
			t.Errorf("provider request: %+v, %v", request, err)
		}
		mu.Lock()
		synthesized = append(synthesized, request.Input)
		mu.Unlock()
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(indexTestWAV())
	}))
	defer server.Close()
	a := testApp(t)
	a.tts = indexConfig(server.URL)
	p := speechTestProject(t, a)
	base := "/api/projects/" + p.ID
	w := call(a, "PUT", base+"/narration", map[string]any{"revision": p.Revision, "text": "The project's authored narration must stay."})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &p)
	before := p
	texts := []string{"第一份独立素材。\n第二句素材。", "A different standalone voice clip."}
	var first ProductionJob
	var firstArgs map[string]any
	for i, text := range texts {
		args := map[string]any{"projectId": p.ID, "revision": p.Revision, "requestId": uid(), "type": "tts", "options": map[string]any{"source": "text", "text": text}}
		result := callGenerationMCP(t, a, "video_job_start", args)
		if result.IsError || len(result.Content) != 1 {
			t.Fatal(result)
		}
		var created ProductionJob
		if err := json.Unmarshal([]byte(result.Content[0].Text), &created); err != nil || created.Source != "text" || created.ScriptHash == "" {
			t.Fatal(created, err)
		}
		job := waitSpeech(t, a, p.ID, created.NativeID)
		if job.State != "completed" || job.Source != "text" {
			t.Fatal(job)
		}
		var snapshot Project
		raw, err := os.ReadFile(filepath.Join(a.speechDir(p.ID, job.ID), "snapshot.json"))
		if err != nil || json.Unmarshal(raw, &snapshot) != nil || snapshot.NarrationText != text || job.ScriptHash != speechScriptHash(snapshot) {
			t.Fatal("source snapshot/hash was not preserved", err)
		}
		var manifest SpeechManifest
		raw, err = os.ReadFile(filepath.Join(a.speechDir(p.ID, job.ID), "manifest.json"))
		if err != nil || json.Unmarshal(raw, &manifest) != nil || manifest.Source != "text" || manifest.ScriptHash != job.ScriptHash || len(manifest.Cues) == 0 || manifest.Duration <= 0 {
			t.Fatal("missing measured standalone provenance", manifest, err)
		}
		w = call(a, "GET", base+"/jobs/"+created.ID, nil)
		var view ProductionJob
		json.Unmarshal(w.Body.Bytes(), &view)
		if w.Code != 200 || view.State != "succeeded" || len(view.Artifacts) != 4 || view.ScriptHash != job.ScriptHash {
			t.Fatal(w.Code, w.Body.String())
		}
		for _, artifact := range view.Artifacts {
			w = call(a, "GET", artifact.URL, nil)
			if w.Code != 200 || hashBytes(w.Body.Bytes()) != artifact.SHA256 || int64(w.Body.Len()) != artifact.Size {
				t.Fatal("archived artifact cannot be downloaded/verified", artifact.Name, w.Code)
			}
		}
		for _, suffix := range []string{"adopt", "materialize"} {
			w = call(a, "POST", base+"/speech/"+job.ID+"/"+suffix, map[string]any{"revision": p.Revision})
			if w.Code != 409 {
				t.Fatal("standalone audio changed the editor through "+suffix, w.Code)
			}
		}
		if i == 0 {
			first, firstArgs = created, args
		}
	}
	after, _ := a.get(p.ID)
	after.Speech, before.Speech = nil, nil
	after.Updated = before.Updated
	if !reflect.DeepEqual(before, after) {
		t.Fatal("independent synthesis changed project editorial state")
	}
	mu.Lock()
	if !reflect.DeepEqual(synthesized, []string{"第一份独立素材。", "第二句素材。", texts[1]}) {
		t.Fatal("provider received project narration or wrong inputs", synthesized)
	}
	mu.Unlock()
	// An unresolved request keeps its identity even after restart and provider
	// defaults change. It must not make another upstream inference request.
	restarted, err := newApp(a.data)
	if err != nil {
		t.Fatal(err)
	}
	restarted.token = a.token
	result := callGenerationMCP(t, restarted, "video_job_start", firstArgs)
	var retried ProductionJob
	if result.IsError || len(result.Content) != 1 || json.Unmarshal([]byte(result.Content[0].Text), &retried) != nil || retried.ID != first.ID {
		t.Fatal("receipt changed on restart", result)
	}
	mu.Lock()
	if len(synthesized) != 3 {
		t.Fatal("receipt retry regenerated audio", synthesized)
	}
	mu.Unlock()
	firstArgs["options"].(map[string]any)["text"] = "Changed text with the original request identity."
	if result := callGenerationMCP(t, restarted, "video_job_start", firstArgs); !result.IsError {
		t.Fatal("same requestId accepted a different standalone input")
	}
}

func TestIndependentTextSpeechValidationAndLegacyReceipt(t *testing.T) {
	for _, in := range []SpeechRequest{
		{Source: "text"},
		{Source: "text", Text: " \n"},
		{Source: "text", Text: "before\x00after"},
		{Source: "text", Text: strings.Repeat("a", 60001)},
		{Source: "text", Text: strings.Repeat("line.\n", 301)},
		{Source: "shots", Text: "Must not silently replace saved shots."},
		{Source: "narration", Text: "Must not silently replace saved narration."},
	} {
		in.RequestID = uid()
		if err := in.normalize(defaultSpeechConfig()); err == nil {
			t.Fatalf("invalid independent input accepted: source=%q bytes=%d", in.Source, len(in.Text))
		}
	}
	a := testApp(t)
	p, _ := a.create("legacy receipt isolation")
	old := SpeechJob{ID: uid(), RequestID: uid(), Revision: p.Revision, Voice: "cmn", Rate: 170, State: "completed"}
	a.projects[p.ID].Speech = append(a.projects[p.ID].Speech, old)
	w := call(a, "POST", "/api/projects/"+p.ID+"/speech", SpeechRequest{Source: "text", Text: "New standalone text.", Revision: p.Revision, RequestID: old.RequestID})
	if w.Code != 409 {
		t.Fatal("legacy receipt was reused for unrelated explicit text", w.Code, w.Body.String())
	}
	w = call(a, "POST", "/api/projects/"+p.ID+"/jobs", JobStartRequest{Type: "render", Revision: p.Revision, RequestID: uid(), Options: JobOptions{Text: "Must be rejected."}})
	if w.Code != 400 {
		t.Fatal("render silently ignored synthesis input", w.Code, w.Body.String())
	}
}

func TestIndependentTextSpeechMCPDiscoverability(t *testing.T) {
	a := testApp(t)
	r := httptest.NewRequest("POST", "http://localhost/api/mcp", bytes.NewBufferString(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	r.Header.Set("Authorization", "Bearer "+a.token)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	var result struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				InputSchema struct {
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	found := 0
	for _, tool := range result.Result.Tools {
		if tool.Name != "video_job_start" && tool.Name != "video_speech_start" {
			continue
		}
		properties := tool.InputSchema.Properties
		if tool.Name == "video_job_start" {
			var options struct {
				Properties map[string]json.RawMessage `json:"properties"`
			}
			if err := json.Unmarshal(properties["options"], &options); err != nil {
				t.Fatal(err)
			}
			properties = options.Properties
		}
		var source struct{ Enum []string }
		var text struct{ Type string }
		json.Unmarshal(properties["source"], &source)
		json.Unmarshal(properties["text"], &text)
		if !reflect.DeepEqual(source.Enum, []string{"narration", "shots", "text"}) || text.Type != "string" {
			t.Fatal("MCP discovery omits standalone text input", tool.Name, source, text)
		}
		found++
	}
	if found != 2 {
		t.Fatal("missing speech tools", found)
	}
}
