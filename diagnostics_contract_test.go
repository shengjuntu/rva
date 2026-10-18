package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Exercise the two products' actual wire protocol. The Docker transport and
// account metadata are fixtures; the Python program and App MCP PNG are real.
func TestRootRuntimeProbeProgramWithActualAppMCP(t *testing.T) {
	scriptPath := os.Getenv("VIDEO_RUNTIME_PROBE_SCRIPT")
	if scriptPath == "" {
		t.Skip("set VIDEO_RUNTIME_PROBE_SCRIPT for cross-product protocol test")
	}
	for _, name := range []string{"python3", "ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skip(err)
		}
	}
	script, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	a := testApp(t)
	p, _ := a.create("actual runtime protocol")
	p.SessionID = "diagnostic-session"
	p.WorkspaceID = "diagnostic-workspace"
	a.save(&p)
	a.projects[p.ID] = &p
	a.cfg.InstanceID = "diagnostic-instance"
	a.cfg.WorkspaceID = p.WorkspaceID
	a.cfg.ManagedByRunDesk = true
	appServer := httptest.NewServer(a.routes())
	defer appServer.Close()
	a.public = appServer.URL
	a.mcpURL = appServer.URL + "/api/mcp"
	workspace := t.TempDir()
	var executed bool
	rd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/applications/rundesk-video-app/runtime":
			// Exactly the real GET contract: it is not an account/MCP probe.
			writeJSON(w, 200, map[string]any{"appId": "rundesk-video-app", "instanceId": a.cfg.InstanceID, "workspaceId": p.WorkspaceID, "executionMode": "docker", "probed": false, "environment": map[string]any{"id": "env", "state": "running", "pendingUpdate": false}})
		case "GET /api/v1/sessions/diagnostic-session":
			writeJSON(w, 200, map[string]any{"instanceId": a.cfg.InstanceID, "workspaceId": p.WorkspaceID, "executionMode": "docker"})
		case "POST /api/v1/applications/rundesk-video-app/runtime/diagnostics":
			var request struct {
				Nonce string `json:"nonce"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			payload, _ := json.Marshal(map[string]any{"workspace": workspace, "nonce": request.Nonce, "mcpConfig": map[string]any{"url": a.mcpURL, "http_headers": map[string]string{"Authorization": "Bearer " + a.token}}})
			ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "python3", "-I", "-c", string(script))
			cmd.Stdin = bytes.NewReader(payload)
			raw, err := cmd.Output()
			if err != nil {
				t.Errorf("actual runtime Python failed: %v", err)
				w.WriteHeader(502)
				return
			}
			var answer map[string]any
			if err := json.Unmarshal(raw, &answer); err != nil {
				t.Error(err)
				w.WriteHeader(502)
				return
			}
			executed = true
			answer["appId"] = "rundesk-video-app"
			answer["instanceId"] = a.cfg.InstanceID
			answer["workspaceId"] = p.WorkspaceID
			answer["executionMode"] = "docker"
			answer["probed"] = true
			answer["accountReady"] = true
			answer["servers"] = map[string]any{"video_project": videoTools}
			answer["errors"] = map[string]string{}
			writeJSON(w, 200, answer)
		default:
			w.WriteHeader(404)
		}
	}))
	defer rd.Close()
	a.rd = rd.URL
	a.cfg.RunDeskURL = rd.URL
	result, err := a.diagnoseRuntime(context.Background(), p.ID)
	if err != nil || !executed || !result.Ready || result.ModelVisionVerified {
		t.Fatal(result, err, executed)
	}
	found := false
	for _, row := range result.Rows {
		if row.ID == "mcp_image_transport" {
			found = true
			if row.State != "passed" {
				t.Fatal(row)
			}
		}
	}
	if !found {
		t.Fatal("actual image transport evidence missing")
	}
}
