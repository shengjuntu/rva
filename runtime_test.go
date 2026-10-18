package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRuntimePreflight(t *testing.T) {
	for _, kind := range []string{"ready", "local", "missing_tool", "unauthenticated", "wrong_binding", "pending_update", "stopped", "errors", "unprobed", "wrong_project"} {
		t.Run(kind, func(t *testing.T) {
			a := testApp(t)
			a.cfg = Config{ManagedByRunDesk: true, InstanceID: "i", WorkspaceID: "w"}
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				v := map[string]any{"appId": "rundesk-video-app", "instanceId": "i", "workspaceId": "w", "executionMode": "docker", "environment": map[string]any{"state": "running", "pendingUpdate": false}, "probed": true, "accountReady": true, "servers": map[string]any{"video_project": videoTools}}
				if r.Method == "POST" {
					posts++
					var in map[string]any
					json.NewDecoder(r.Body).Decode(&in)
					if in["instanceId"] != "i" || in["workspaceId"] != "w" || in["probe"] != true {
						t.Error(in)
					}
				}
				switch kind {
				case "local":
					v["executionMode"] = "local"
				case "wrong_binding":
					v["instanceId"] = "other"
				case "missing_tool":
					v["servers"] = map[string]any{"video_project": videoTools[:1]}
				case "unauthenticated":
					v["accountReady"] = false
				case "pending_update":
					v["environment"] = map[string]any{"state": "running", "pendingUpdate": true}
				case "stopped":
					v["environment"] = map[string]any{"state": "stopped"}
				case "errors":
					v["errors"] = map[string]string{"mcp": "failed"}
				case "unprobed":
					v["probed"] = false
				}
				writeJSON(w, 200, v)
			}))
			defer server.Close()
			a.rd = server.URL
			p := Project{WorkspaceID: "w"}
			if kind == "wrong_project" {
				p.WorkspaceID = "other"
			}
			_, e := a.prepareRuntime(context.Background(), &p)
			if (e == nil) != (kind == "ready" || kind == "local") {
				t.Fatalf("unexpected result: %v", e)
			}
			if (kind == "local" || kind == "wrong_binding" || kind == "wrong_project") && posts != 0 {
				t.Fatal("prepared wrong environment")
			}
		})
	}
}

func TestDataLockExcludesSecondProcessHandle(t *testing.T) {
	dir := t.TempDir()
	release, e := lockData(dir)
	if e != nil {
		t.Fatal(e)
	}
	if unlock, e := lockData(dir); e == nil {
		unlock()
		release()
		t.Fatal("second writer acquired lock")
	}
	release()
	unlock, e := lockData(dir)
	if e != nil {
		t.Fatal(e)
	}
	unlock()
}
