package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func v1Meta() any {
	return map[string]any{"apiVersions": []string{"v1"}, "capabilities": []string{"api-v1", "idempotency", "configuration-summary"}}
}

func TestDurableOperationLostAckAndRestart(t *testing.T) {
	a := testApp(t)
	posts := 0
	key := ""
	original := ""
	rd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			if posts == 0 {
				writeJSON(w, 404, map[string]string{"code": "request_not_found"})
				return
			}
			if !strings.HasSuffix(r.URL.Path, key) {
				t.Error("changed key")
			}
			writeJSON(w, 200, map[string]any{"method": "POST", "path": "/sessions", "state": "completed", "httpStatus": 201, "response": map[string]string{"id": "session-1"}})
			return
		}
		posts++
		key = r.Header.Get("Idempotency-Key")
		if key == "" {
			t.Error("missing key")
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		original = body["title"]
		// The server accepts the operation before its response is lost.
		connection, _, _ := w.(http.Hijacker).Hijack()
		connection.Close()
	}))
	defer rd.Close()
	a.rd = rd.URL
	var out map[string]string
	if e := a.rdOperation(context.Background(), "session:p1", "/sessions", map[string]string{"title": "original"}, &out, false, true); e == nil {
		t.Fatal("lost response reported success")
	}
	b, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	b.rd = rd.URL
	if e = b.rdOperation(context.Background(), "session:p1", "/sessions", map[string]string{"title": "renamed"}, &out, false, true); e != nil {
		t.Fatal(e)
	}
	if posts != 1 || original != "original" || out["id"] != "session-1" {
		t.Fatal(posts, original, out)
	}
	if e = b.rdOperation(context.Background(), "session:p1", "/sessions", map[string]string{"title": "changed"}, &out, true, true); e == nil {
		t.Fatal("changed body reused key")
	}
}

func TestReceiptStatesNeverResubmit(t *testing.T) {
	for _, state := range []string{"processing", "unconfirmed", "failed"} {
		t.Run(state, func(t *testing.T) {
			a := testApp(t)
			posts := 0
			rd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					posts++
					t.Error("unexpected mutation")
				}
				status := 200
				receiptState := state
				if state == "failed" {
					status = 409
					receiptState = "completed"
				}
				writeJSON(w, 200, map[string]any{"method": "POST", "path": "/sessions/s1/turns", "state": receiptState, "httpStatus": status, "response": map[string]string{"error": "busy", "code": "session_busy"}})
			}))
			defer rd.Close()
			a.rd = rd.URL
			var out any
			if e := a.rdOperation(context.Background(), "turn:p1", "/sessions/s1/turns", map[string]string{"text": "one"}, &out, true, true); e == nil {
				t.Fatal("receipt reported success")
			}
			if posts != 0 {
				t.Fatal(posts)
			}
		})
	}
}

func TestPendingMessageRecoveryAndFencedStop(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("kept project")
	p.SessionID = "session1"
	p.ToolsVersion = version
	p.AgentRunID = "old-run"
	a.save(&p)
	a.projects[p.ID] = &p
	posts := 0
	receipt := false
	stops := 0
	rd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/requests/"):
			if !receipt {
				writeJSON(w, 404, map[string]string{"code": "request_not_found"})
				return
			}
			writeJSON(w, 200, map[string]any{"method": "POST", "path": "/sessions/session1/turns", "state": "completed", "httpStatus": 200, "response": map[string]string{"id": "session1", "runId": "owned-run"}})
		case strings.HasSuffix(r.URL.Path, "/turns"):
			posts++
			receipt = true
			connection, _, _ := w.(http.Hijacker).Hijack()
			connection.Close()
		case strings.HasSuffix(r.URL.Path, "/stop"):
			var in map[string]string
			json.NewDecoder(r.Body).Decode(&in)
			if in["expectedRunId"] != "owned-run" {
				t.Error("stopped another run", in)
			}
			stops++
			writeJSON(w, 409, map[string]string{"code": "run_conflict", "error": "new run active"})
		default:
			writeJSON(w, 200, map[string]string{"id": "session1", "runId": "different-run", "turnId": "different-turn", "status": "running"})
		}
	}))
	defer rd.Close()
	a.rd = rd.URL
	url := "/api/projects/" + p.ID
	msg := map[string]string{"text": "original message", "requestId": "request-one"}
	w := call(a, "POST", url+"/messages", msg)
	if w.Code != 502 {
		t.Fatal(w.Code, w.Body.String())
	}
	b, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	b.rd = rd.URL
	b.token = a.token
	w = call(b, "GET", url+"/agent", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	got, _ := b.get(p.ID)
	if got.AgentSubmission.State != "accepted" || got.AgentRunID != "owned-run" || posts != 1 {
		t.Fatal(got.AgentSubmission, posts)
	}
	w = call(b, "POST", url+"/messages", msg)
	if w.Code != 200 || posts != 1 {
		t.Fatal(w.Code, posts)
	}
	w = call(b, "POST", url+"/messages", map[string]string{"text": "different", "requestId": "request-one"})
	if w.Code != 409 {
		t.Fatal("changed input accepted")
	}
	w = call(b, "POST", url+"/messages", map[string]string{"text": "steer outsider", "requestId": "request-two", "expectedTurnId": "different-turn"})
	if w.Code != 409 {
		t.Fatal("foreign run steered")
	}
	w = call(b, "POST", url+"/agent/stop", map[string]string{"expectedRunId": "owned-run"})
	if w.Code != 409 || stops != 1 {
		t.Fatal(w.Code, stops)
	}
}

func TestStopUnknownSubmissionDoesNotStartIt(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("original")
	p.SessionID = "s1"
	p.ToolsVersion = version
	p.AgentSubmission = &AgentSubmission{ID: "pending-one", Text: "must not run", State: "pending"}
	a.save(&p)
	a.projects[p.ID] = &p
	posts := 0
	rd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			posts++
		}
		writeJSON(w, 404, map[string]string{"code": "request_not_found"})
	}))
	defer rd.Close()
	a.rd = rd.URL
	w := call(a, "POST", "/api/projects/"+p.ID+"/agent/stop", map[string]string{})
	if w.Code != 409 || posts != 0 {
		t.Fatal(w.Code, posts)
	}
	got, _ := a.get(p.ID)
	if !got.AgentSubmission.CancelRequested {
		t.Fatal("cancel intent lost")
	}
	call(a, "GET", "/api/projects/"+p.ID+"/agent", nil)
	if posts != 0 {
		t.Fatal("poll started canceled task")
	}
}

func TestPendingCancellationResumesAfterReceiptArrives(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("cancel recovery")
	p.SessionID = "s1"
	p.ToolsVersion = version
	p.AgentSubmission = &AgentSubmission{ID: "cancel-message", Text: "original", State: "pending", CancelRequested: true, Body: json.RawMessage(`{"text":"original"}`)}
	a.save(&p)
	a.projects[p.ID] = &p
	stops := 0
	rd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/requests/"):
			writeJSON(w, 200, map[string]any{"state": "completed", "method": "POST", "path": "/sessions/s1/turns", "httpStatus": 200, "response": map[string]string{"id": "s1", "runId": "original-run"}})
		case strings.HasSuffix(r.URL.Path, "/stop"):
			var body map[string]string
			json.NewDecoder(r.Body).Decode(&body)
			if body["expectedRunId"] != "original-run" {
				t.Error(body)
			}
			stops++
			writeJSON(w, 200, map[string]bool{"ok": true})
		case strings.HasSuffix(r.URL.Path, "/turns"):
			t.Error("canceled task resubmitted")
		default:
			writeJSON(w, 200, map[string]string{"id": "s1", "runId": "original-run", "status": "interrupted"})
		}
	}))
	defer rd.Close()
	a.rd = rd.URL
	op := agentOperation{Key: "original-key", Backend: a.rd, Path: "/sessions/s1/turns", Body: p.AgentSubmission.Body}
	file := a.operationFile("message:" + p.ID + ":" + p.AgentSubmission.ID)
	if e := os.MkdirAll(filepath.Dir(file), 0700); e != nil {
		t.Fatal(e)
	}
	if e := atomicJSON(file, op); e != nil {
		t.Fatal(e)
	}
	w := call(a, "GET", "/api/projects/"+p.ID+"/agent", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	got, _ := a.get(p.ID)
	if got.AgentSubmission.State != "canceled" || stops != 1 {
		t.Fatal(got.AgentSubmission, stops)
	}
}

func TestUnreadableReceiptRemainsUncertain(t *testing.T) {
	a := testApp(t)
	rd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("unexpected write")
		}
		writeJSON(w, 401, map[string]string{"code": "unauthorized", "error": "expired"})
	}))
	defer rd.Close()
	a.rd = rd.URL
	var out any
	err := a.rdOperation(context.Background(), "auth-read", "/sessions", map[string]string{"title": "original"}, &out, true, true)
	remote, ok := err.(*RunDeskError)
	if !ok || !remote.Uncertain || remote.Code != "unauthorized" {
		t.Fatal(err)
	}
}
