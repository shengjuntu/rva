package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestProjectManagementOpeningArchiveAndDeletedCreationReceipt(t *testing.T) {
	a := testApp(t)
	creationID := uid()
	p, err := a.createRequested("可管理项目", creationID)
	if err != nil {
		t.Fatal(err)
	}
	opened := call(a, "POST", "/api/projects/"+p.ID+"/opened", map[string]any{})
	if opened.Code != 200 {
		t.Fatal(opened.Code, opened.Body.String())
	}
	current, _ := a.get(p.ID)
	if current.LastOpened == "" || current.Revision != p.Revision || current.Updated != p.Updated {
		t.Fatal("opening changed content or failed to persist recent access", current)
	}
	if w := call(a, "PATCH", "/api/projects/"+p.ID+"/management", map[string]any{"revision": p.Revision, "name": "更新后", "archived": true}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	current, _ = a.get(p.ID)
	if current.Name != "更新后" || !current.Archived || current.Revision != p.Revision+1 {
		t.Fatal(current)
	}
	if w := call(a, "DELETE", "/api/projects/"+p.ID, map[string]any{"revision": p.Revision, "confirmName": current.Name}); w.Code != 409 {
		t.Fatal("stale deletion passed", w.Code)
	}
	if w := call(a, "DELETE", "/api/projects/"+p.ID, map[string]any{"revision": current.Revision, "confirmName": p.Name}); w.Code != 409 {
		t.Fatal("wrong name passed", w.Code)
	}
	if w := call(a, "DELETE", "/api/projects/"+p.ID, map[string]any{"revision": current.Revision, "confirmName": current.Name}); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if _, err := a.get(p.ID); err == nil {
		t.Fatal("deleted project remains active")
	}
	if _, err := os.Stat(filepath.Join(a.data, "project-trash", "requests", creationID+".json")); err != nil {
		t.Fatal("deletion receipt missing", err)
	}
	if _, err := a.createRequested("可管理项目", creationID); err != errProjectDeleted {
		t.Fatal("same request recreated deleted project", err)
	}
	if _, err := newApp(a.data); err != nil {
		t.Fatal("trash broke restart", err)
	}
}

func TestProjectManagementRefusesUnknownOrRunningTask(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("忙碌")
	if err := a.saveAgent(p.ID, func(q *Project) {
		q.AgentSubmission = &AgentSubmission{Purpose: "draft", ID: uid(), State: "accepted", RunID: "run-1"}
	}); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"", "running"} {
		if err := a.saveAgent(p.ID, func(q *Project) { q.AgentSubmission.ExecutionState = state }); err != nil {
			t.Fatal(err)
		}
		p, _ = a.get(p.ID)
		if w := call(a, "PATCH", "/api/projects/"+p.ID+"/management", map[string]any{"revision": p.Revision, "archived": true}); w.Code != 409 {
			t.Fatal("active or unknown task archived", state, w.Code)
		}
	}
}

func TestProductionStateNeverClaimsUnownedOrAnotherRunMovie(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("归属检查")
	var remote atomic.Value
	remote.Store("running")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/sessions/fixture-session" {
			t.Error("unexpected remote read", r.URL.Path)
		}
		writeJSON(w, 200, map[string]any{"runId": "run-1", "status": remote.Load().(string)})
	}))
	defer upstream.Close()
	a.rd, a.rdToken = upstream.URL, "fixture-token"
	requestID := uid()
	a.mu.Lock()
	q := clone(a.projects[p.ID])
	q.SessionID = "fixture-session"
	q.AgentSubmission = &AgentSubmission{Purpose: "draft", ID: requestID, SessionID: q.SessionID, State: "accepted", RunID: "run-1"}
	q.Workflow = &ProductionWorkflow{EditingBackend: "native", Skills: []string{"video-narration", "video-editing", "video-review"}}
	old := Render{ID: uid(), State: "completed", Revision: q.Revision, Duration: 3, SourceHash: narrationSourceHash(q), ContentHash: productionHash(q)}
	q.Renders = append(q.Renders, old)
	if err := a.save(&q); err != nil {
		t.Fatal(err)
	}
	a.projects[p.ID] = &q
	a.mu.Unlock()
	read := func() productionStateView {
		t.Helper()
		w := call(a, "GET", "/api/projects/"+p.ID+"/production-state", nil)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var view productionStateView
		if err := json.Unmarshal(w.Body.Bytes(), &view); err != nil {
			t.Fatal(err)
		}
		return view
	}
	if v := read(); v.State != "running" || v.RenderID != "" || v.Preview != nil {
		t.Fatal("old render leaked as current progress", v)
	}
	remote.Store("completed")
	if v := read(); v.State != "no_output" || v.RenderID != "" {
		t.Fatal("old render leaked as new output", v)
	}
	a.mu.Lock()
	q = clone(a.projects[p.ID])
	owned := old
	owned.ID, owned.ProductionRequestID = uid(), requestID
	q.Renders = append(q.Renders, owned)
	if err := a.save(&q); err != nil {
		t.Fatal(err)
	}
	a.projects[p.ID] = &q
	a.mu.Unlock()
	if v := read(); v.State != "completed" || v.RenderID != owned.ID {
		t.Fatal("matching completed full output was not selected", v)
	}
	if err := validateProductionOwner(q, uid()); err == nil {
		t.Fatal("unrelated production request claimed current project")
	}
	if err := validateProductionOwner(q, requestID); err != nil {
		t.Fatal(err)
	}
	upstream.Close()
	if v := read(); v.State != "unknown" || v.RenderID != "" {
		t.Fatal("remote outage claimed old success", v)
	}
}
