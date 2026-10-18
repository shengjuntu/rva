package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type runtimePresetFixture struct {
	t                         *testing.T
	mu                        sync.Mutex
	presets                   bool
	connects                  []string
	prepared                  []string
	diagnosed                 []string
	configurations            []string
	fullConfigurationRequests int
	turns                     []string
	sessions                  map[string]map[string]any
	receipts                  map[string]map[string]any
	sessionPosts              int
	loseSessionAck            bool
	wrongPreset               bool
}

func newRuntimePresetFixture(t *testing.T) *runtimePresetFixture {
	return &runtimePresetFixture{t: t, presets: true, sessions: map[string]map[string]any{}, receipts: map[string]map[string]any{}}
}
func (f *runtimePresetFixture) pair(preset string) (string, string) {
	if preset == "" {
		return "legacy-instance", "legacy-workspace"
	}
	return "instance-" + preset, "workspace-" + preset
}
func (f *runtimePresetFixture) runtime(preset string, probed bool) map[string]any {
	iid, wid := f.pair(preset)
	out := map[string]any{"appId": "rundesk-video-app", "instanceId": iid, "workspaceId": wid, "executionMode": "docker", "environment": map[string]any{"state": "running", "pendingUpdate": false}, "probed": probed, "accountReady": probed, "servers": map[string]any{"video_project": videoTools}}
	if preset != "" {
		out["presetId"] = preset
	}
	return out
}
func (f *runtimePresetFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer fixture-rundesk-key" {
		f.t.Error("RunDesk auth was not preserved")
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	if path == "/meta" {
		meta := v1Meta().(map[string]any)
		if f.presets {
			meta["capabilities"] = append(meta["capabilities"].([]string), "application-runtime-presets")
		}
		writeJSON(w, 200, meta)
		return
	}
	if strings.HasPrefix(path, "/requests/") {
		key := strings.TrimPrefix(path, "/requests/")
		if receipt, ok := f.receipts[key]; ok {
			writeJSON(w, 200, receipt)
		} else {
			writeJSON(w, 404, map[string]string{"code": "request_not_found"})
		}
		return
	}
	var body map[string]any
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body)
	}
	preset, _ := body["presetId"].(string)
	switch path {
	case "/applications/rundesk-video-app/connect":
		f.connects = append(f.connects, preset)
		iid, wid := f.pair(preset)
		if supplied, _ := body["instanceId"].(string); supplied != "" && supplied != iid {
			f.t.Error("connect used another project's instance", body)
		}
		if supplied, _ := body["workspaceId"].(string); supplied != "" && supplied != wid {
			f.t.Error("connect used another project's workspace", body)
		}
		app := map[string]string{"instanceId": iid, "workspaceId": wid}
		out := map[string]any{"application": app, "status": "connected"}
		if preset != "" {
			app["presetId"], out["presetId"] = preset, preset
			if f.wrongPreset {
				out["presetId"] = "another-preset"
			}
		}
		writeJSON(w, 200, out)
		return
	case "/applications/rundesk-video-app/runtime":
		preset = r.URL.Query().Get("presetId")
		writeJSON(w, 200, f.runtime(preset, false))
		return
	case "/applications/rundesk-video-app/runtime/prepare":
		iid, wid := f.pair(preset)
		if body["instanceId"] != iid || body["workspaceId"] != wid || body["probe"] != true {
			f.t.Error("prepare used another project's binding", body)
		}
		f.prepared = append(f.prepared, preset)
		writeJSON(w, 200, f.runtime(preset, true))
		return
	case "/applications/rundesk-video-app/runtime/diagnostics":
		iid, wid := f.pair(preset)
		if body["instanceId"] != iid || body["workspaceId"] != wid {
			f.t.Error("diagnostic used another project's binding", body)
		}
		f.diagnosed = append(f.diagnosed, preset)
		nonce, _ := body["nonce"].(string)
		png, _ := diagnosticImage(nonce)
		out := runDeskDiagnosticResponse{SchemaVersion: 1, AppID: "rundesk-video-app", PresetID: preset, InstanceID: iid, WorkspaceID: wid, ExecutionMode: "docker", Probed: true, AccountReady: true, Servers: map[string][]string{"video_project": videoTools}}
		out.ImageTransport.State, out.ImageTransport.Nonce, out.ImageTransport.ImageSHA256 = "passed", nonce, hashBytes(png)
		out.ImageTransport.MIMEType, out.ImageTransport.Width, out.ImageTransport.Height = "image/png", 32, 32
		writeJSON(w, 200, out)
		return
	case "/applications/rundesk-video-app/runtime/skills":
		preset = r.URL.Query().Get("presetId")
		iid, wid := f.pair(preset)
		f.configurations = append(f.configurations, iid)
		skills := []map[string]any{}
		for _, name := range []string{"video-project", "video-editing", "video-narration", "video-review"} {
			runtimeName := runtimeSkillName(name)
			skills = append(skills, map[string]any{"name": runtimeName, "path": "/" + iid + "/skills/" + runtimeName + "/SKILL.md", "enabled": true})
		}
		writeJSON(w, 200, map[string]any{"appId": "rundesk-video-app", "presetId": preset, "instanceId": iid, "workspaceId": wid, "skills": skills})
		return
	case "/sessions":
		if r.Method == "GET" {
			writeJSON(w, 200, []any{})
			return
		}
		source, _ := body["source"].(map[string]any)
		preset, _ = source["presetId"].(string)
		iid, wid := f.pair(preset)
		if body["instanceId"] != iid || body["workspaceId"] != wid {
			f.t.Error("session source and binding disagree", body)
		}
		f.sessionPosts++
		sid := "session-" + preset
		if preset == "" {
			sid = "session-legacy"
		}
		session := map[string]any{"id": sid, "instanceId": iid, "workspaceId": wid, "executionMode": "docker", "status": "idle", "source": source}
		f.sessions[sid] = session
		response := map[string]string{"id": sid}
		f.receipts[r.Header.Get("Idempotency-Key")] = map[string]any{"state": "completed", "method": "POST", "path": "/sessions", "httpStatus": 201, "response": response}
		if f.loseSessionAck {
			f.loseSessionAck = false
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				f.t.Error(err)
				return
			}
			conn.Close()
			return
		}
		writeJSON(w, 201, response)
		return
	}
	if strings.HasPrefix(path, "/instances/") && strings.HasSuffix(path, "/configuration") {
		f.fullConfigurationRequests++
		writeJSON(w, 403, map[string]string{"error": "preset keys cannot read broad instance configuration", "code": "forbidden"})
		return
	}
	if strings.HasPrefix(path, "/sessions/") {
		suffix := strings.TrimPrefix(path, "/sessions/")
		sid := strings.Split(suffix, "/")[0]
		session, ok := f.sessions[sid]
		if !ok {
			f.t.Error("unknown session", sid)
			writeJSON(w, 404, nil)
			return
		}
		if strings.HasSuffix(path, "/turns") {
			f.turns = append(f.turns, sid)
			writeJSON(w, 200, map[string]string{"id": sid, "runId": "run-" + sid})
			return
		}
		writeJSON(w, 200, session)
		return
	}
	f.t.Error("unexpected RunDesk path", r.Method, path)
	writeJSON(w, 404, nil)
}
func setupRuntimePresetApp(t *testing.T) (*App, *runtimePresetFixture, *httptest.Server) {
	t.Helper()
	a := testApp(t)
	f := newRuntimePresetFixture(t)
	s := httptest.NewServer(f)
	a.rd, a.rdToken = s.URL, "fixture-rundesk-key"
	a.cfg = Config{InstallationID: "fixture-installation", ManagedByRunDesk: true, InstanceID: "legacy-instance", WorkspaceID: "legacy-workspace", RunDeskURL: s.URL}
	if err := atomicJSON(filepath.Join(a.data, "config.json"), a.cfg); err != nil {
		t.Fatal(err)
	}
	return a, f, s
}

func TestProductionProfileCreationFreezesIntentAndRequestIdentity(t *testing.T) {
	a := testApp(t)
	a.productionProfile = "preset-a"
	requestID := uid()
	p, err := a.createRequestedProfile("默认环境", requestID, nil)
	if err != nil || p.ProductionProfile == nil || *p.ProductionProfile != "preset-a" {
		t.Fatal(p, err)
	}
	a.productionProfile = "preset-b"
	again, err := a.createRequestedProfile("默认环境", requestID, nil)
	if err != nil || again.ID != p.ID || *again.ProductionProfile != "preset-a" {
		t.Fatal("default change broke creation receipt", again, err)
	}
	explicit := "preset-b"
	if _, err := a.createRequestedProfile("默认环境", requestID, &explicit); err != errCreateRequestConflict {
		t.Fatal("changed explicit preset reused receipt", err)
	}
	empty := ""
	legacy, err := a.createRequestedProfile("保留默认", uid(), &empty)
	if err != nil || legacy.ProductionProfile == nil || *legacy.ProductionProfile != "" {
		t.Fatal(legacy, err)
	}
	for _, bad := range []string{"../image", "image:tag", "https://example.test", " preset-a", strings.Repeat("a", 129)} {
		if _, err := a.createRequestedProfile("invalid", uid(), &bad); err == nil {
			t.Fatal("invalid preset ID accepted", bad)
		}
	}
	// PUT updates authored project content, never the requested preset or binding.
	p.ProductionProfile = &explicit
	p.RuntimeBinding = &ProjectRuntimeBinding{PresetID: "other", InstanceID: "foreign", WorkspaceID: "foreign"}
	edited, err := a.edit(p.ID, p)
	if err != nil || *edited.ProductionProfile != "preset-a" || edited.RuntimeBinding != nil {
		t.Fatal("ordinary edit replaced runtime intent", edited, err)
	}
}

func TestProductionProfileCreationHTTPAndMCP(t *testing.T) {
	a := testApp(t)
	a.productionProfile = "preset-default"
	for _, transport := range []string{"http", "mcp"} {
		for _, selected := range []string{"omitted", "preset-other", ""} {
			arguments := map[string]any{"name": "create-" + transport + "-" + selected, "requestId": uid()}
			if selected != "omitted" {
				arguments["productionProfile"] = selected
			}
			var project Project
			if transport == "http" {
				w := call(a, "POST", "/api/projects", arguments)
				if w.Code != 201 {
					t.Fatal(w.Code, w.Body.String())
				}
				if err := json.Unmarshal(w.Body.Bytes(), &project); err != nil {
					t.Fatal(err)
				}
			} else {
				raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": map[string]any{"name": "video_project_create", "arguments": arguments}})
				var buffer bytes.Buffer
				runMCP(bytes.NewReader(raw), &buffer, "http://localhost", a.token, &http.Client{Transport: localMCPTransport{handler: a.routes()}})
				var response struct {
					Result struct {
						IsError bool `json:"isError"`
						Content []struct {
							Text string `json:"text"`
						} `json:"content"`
					} `json:"result"`
				}
				if err := json.Unmarshal(buffer.Bytes(), &response); err != nil || response.Result.IsError || len(response.Result.Content) != 1 {
					t.Fatal(buffer.String(), err)
				}
				if err := json.Unmarshal([]byte(response.Result.Content[0].Text), &project); err != nil {
					t.Fatal(err)
				}
			}
			want := selected
			if selected == "omitted" {
				want = "preset-default"
			}
			if project.ProductionProfile == nil || *project.ProductionProfile != want || project.RuntimeBinding != nil {
				t.Fatal(transport, selected, project)
			}
		}
	}
}

func TestProjectProductionPresetsUseIndependentHTTPBindings(t *testing.T) {
	a, f, server := setupRuntimePresetApp(t)
	defer server.Close()
	a.productionProfile = "preset-a"
	p, err := a.create("A")
	if err != nil {
		t.Fatal(err)
	}
	// A was created but not connected when the default changed.
	a.productionProfile = "preset-b"
	q, err := a.create("B")
	if err != nil {
		t.Fatal(err)
	}
	for _, project := range []Project{p, q} {
		w := call(a, "POST", "/api/projects/"+project.ID+"/connect", map[string]any{})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	p, _ = a.get(p.ID)
	q, _ = a.get(q.ID)
	if p.RuntimeBinding.PresetID != "preset-a" || q.RuntimeBinding.PresetID != "preset-b" || p.RuntimeBinding.InstanceID == q.RuntimeBinding.InstanceID || p.WorkspaceID == q.WorkspaceID {
		t.Fatal(p.RuntimeBinding, q.RuntimeBinding)
	}
	if a.cfg.InstanceID != "legacy-instance" || a.cfg.WorkspaceID != "legacy-workspace" {
		t.Fatal("a project overwrote global legacy binding", a.cfg)
	}
	for _, project := range []Project{p, q, p} {
		w := call(a, "POST", "/api/runtime/prepare?projectId="+project.ID, map[string]any{})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		w = call(a, "POST", "/api/projects/"+project.ID+"/messages", map[string]any{"text": "制作初稿", "requestId": "message-" + project.ID, "skills": []string{"video-editing"}})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		diagnostic, err := a.diagnoseRuntime(context.Background(), project.ID)
		if err != nil || !diagnostic.Ready || diagnostic.Binding.PresetID != project.RuntimeBinding.PresetID {
			t.Fatal(diagnostic, err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.Join(f.connects, ",") != "preset-a,preset-b" || strings.Join(f.turns, ",") != "session-preset-a,session-preset-b" || strings.Join(f.configurations, ",") != "instance-preset-a,instance-preset-b" || strings.Join(f.diagnosed, ",") != "preset-a,preset-b,preset-a" || f.fullConfigurationRequests != 0 {
		t.Fatal(f.connects, f.turns, f.configurations, f.diagnosed)
	}
}

func TestProductionProfileRejectsOldRunDeskAndWrongPreset(t *testing.T) {
	for _, kind := range []string{"old-service", "wrong-preset"} {
		t.Run(kind, func(t *testing.T) {
			a, f, server := setupRuntimePresetApp(t)
			defer server.Close()
			a.productionProfile = "preset-a"
			f.presets = kind != "old-service"
			f.wrongPreset = kind == "wrong-preset"
			p, _ := a.create("pinned")
			w := call(a, "POST", "/api/projects/"+p.ID+"/connect", map[string]any{})
			if w.Code != 409 {
				t.Fatal(w.Code, w.Body.String())
			}
			p, _ = a.get(p.ID)
			if p.RuntimeBinding != nil || p.SessionID != "" || f.sessionPosts != 0 {
				t.Fatal("rejection fell back or created a session", p)
			}
			if kind == "old-service" && (!strings.Contains(w.Body.String(), "production_profile_unsupported") || len(f.connects) != 0) {
				t.Fatal(w.Body.String(), f.connects)
			}
		})
	}
}

func TestUnboundProductionProfileDoesNotPrepareLegacyEnvironment(t *testing.T) {
	a, f, server := setupRuntimePresetApp(t)
	defer server.Close()
	a.productionProfile = "preset-a"
	p, _ := a.create("not yet connected")
	for _, method := range []string{"GET", "POST"} {
		path := "/api/runtime?projectId=" + p.ID
		if method == "POST" {
			path = "/api/runtime/prepare?projectId=" + p.ID
		}
		w := call(a, method, path, map[string]any{})
		if w.Code != 409 {
			t.Fatal(method, w.Code, w.Body.String())
		}
	}
	if len(f.prepared) != 0 || len(f.connects) != 0 {
		t.Fatal("unbound project used global legacy environment", f.prepared, f.connects)
	}
}

func TestLegacyProjectKeepsOriginalSessionWhenDefaultChanges(t *testing.T) {
	a, f, server := setupRuntimePresetApp(t)
	defer server.Close()
	a.productionProfile = "preset-b"
	p, _ := a.create("legacy")
	p.ProductionProfile = nil
	p.SessionID, p.WorkspaceID = "session-legacy", "legacy-workspace"
	if err := a.save(&p); err != nil {
		t.Fatal(err)
	}
	a.projects[p.ID] = &p
	f.sessions[p.SessionID] = map[string]any{"id": p.SessionID, "instanceId": "legacy-instance", "workspaceId": p.WorkspaceID, "executionMode": "docker", "status": "idle"}
	// The session, rather than a mutable global instance, identifies this old project.
	a.cfg.InstanceID = "unrelated-global-instance"
	w := call(a, "POST", "/api/projects/"+p.ID+"/connect", map[string]any{})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	p, _ = a.get(p.ID)
	if p.RuntimeBinding.PresetID != "" || p.RuntimeBinding.InstanceID != "legacy-instance" || p.SessionID != "session-legacy" || f.sessionPosts != 0 {
		t.Fatal(p.RuntimeBinding, p.SessionID, f.sessionPosts)
	}
	if len(f.connects) != 1 || f.connects[0] != "" {
		t.Fatal("old project adopted startup default", f.connects)
	}
}

func TestProductionPresetLostSessionAckSurvivesRestart(t *testing.T) {
	a, f, server := setupRuntimePresetApp(t)
	defer server.Close()
	a.productionProfile = "preset-a"
	p, _ := a.create("lost session receipt")
	f.loseSessionAck = true
	url := "/api/projects/" + p.ID + "/connect"
	w := call(a, "POST", url, map[string]any{})
	if w.Code != 502 {
		t.Fatal(w.Code, w.Body.String())
	}
	p, _ = a.get(p.ID)
	if p.RuntimeBinding == nil || p.RuntimeBinding.PresetID != "preset-a" || p.SessionID != "" {
		t.Fatal("binding was not saved before side effect", p)
	}
	b, err := newApp(a.data)
	if err != nil {
		t.Fatal(err)
	}
	b.rd, b.rdToken, b.token = server.URL, a.rdToken, a.token
	b.productionProfile = "preset-b"
	w = call(b, "POST", url, map[string]any{})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	p, _ = b.get(p.ID)
	if p.SessionID != "session-preset-a" || p.RuntimeBinding.PresetID != "preset-a" || f.sessionPosts != 1 {
		t.Fatal("lost ack crossed runtime or duplicated session", p.RuntimeBinding, p.SessionID, f.sessionPosts)
	}
	// Frozen pending work also refuses a locally replaced binding/session.
	p.AgentSubmission = &AgentSubmission{ID: "pending-message", Text: "original", State: "pending", SessionID: p.SessionID, RuntimeBinding: cloneRuntimeBinding(p.RuntimeBinding)}
	p.RuntimeBinding = &ProjectRuntimeBinding{PresetID: "preset-b", InstanceID: "instance-preset-b", WorkspaceID: "workspace-preset-b", ManagedByRunDesk: true}
	if _, err := b.recoverAgent(context.Background(), p, true); err == nil {
		t.Fatal("pending request replayed into a different runtime")
	}
	if _, err := os.Stat(b.operationFile("message:" + p.ID + ":pending-message")); !os.IsNotExist(err) {
		t.Fatal("rejected replay created an operation", err)
	}
}
