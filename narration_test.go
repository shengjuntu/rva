package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestIndependentNarrationSpeech(t *testing.T) {
	a := testApp(t)
	p := speechTestProject(t, a)
	original := p.Shots
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/wav")
		w.Write(indexTestWAV())
	}))
	defer s.Close()
	a.tts = indexConfig(s.URL)
	body, _ := json.Marshal(map[string]any{"revision": p.Revision, "text": "Free narration.\nSecond line."})
	w := call(a, "PUT", "/api/projects/"+p.ID+"/narration", json.RawMessage(body))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &p)
	body, _ = json.Marshal(SpeechRequest{Source: "narration", Revision: p.Revision, RequestID: uid()})
	w = call(a, "POST", "/api/projects/"+p.ID+"/speech", json.RawMessage(body))
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var j SpeechJob
	json.Unmarshal(w.Body.Bytes(), &j)
	j = waitSpeech(t, a, p.ID, j.ID)
	if j.State != "completed" {
		t.Fatal(j.Error)
	}
	body, _ = json.Marshal(map[string]any{"revision": p.Revision})
	url := "/api/projects/" + p.ID + "/speech/" + j.ID + "/materialize"
	w = call(a, "POST", url, json.RawMessage(body))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct {
		Revision int
		Anchors  []EditAnchor
		Asset    Asset
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if result.Asset.ID != j.ID || len(result.Anchors) != 4 {
		t.Fatal(w.Body.String())
	}
	q, _ := a.get(p.ID)
	if !reflect.DeepEqual(q.Shots, original) || q.EditDSL != nil || q.Audio != nil {
		t.Fatal("materialization changed editing")
	}
	body, _ = json.Marshal(map[string]any{"revision": q.Revision})
	w = call(a, "POST", url, json.RawMessage(body))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	again, _ := a.get(p.ID)
	if again.Revision != q.Revision || len(again.Assets) != len(q.Assets) {
		t.Fatal("not idempotent")
	}
	body, _ = json.Marshal(map[string]any{"revision": q.Revision, "text": "Changed"})
	w = call(a, "PUT", "/api/projects/"+p.ID+"/narration", json.RawMessage(body))
	json.Unmarshal(w.Body.Bytes(), &q)
	body, _ = json.Marshal(map[string]any{"revision": q.Revision})
	w = call(a, "POST", url, json.RawMessage(body))
	if w.Code != 409 {
		t.Fatal("accepted stale speech", w.Code)
	}
}
