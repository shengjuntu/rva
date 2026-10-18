package main

import (
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

func setExchangeRootMap(t *testing.T, roots map[string]string) {
	t.Helper()
	raw, _ := json.Marshal(roots)
	t.Setenv("VIDEO_EXCHANGE_ROOTS", string(raw))
}
func exchangePresetProject(p Project, preset, workspace string) Project {
	p.RuntimeBinding = &ProjectRuntimeBinding{PresetID: preset, InstanceID: "instance-" + preset, WorkspaceID: workspace, ManagedByRunDesk: true}
	p.WorkspaceID = workspace
	p.SessionID = "session-" + preset
	p.ProductionProfile = &preset
	return p
}
func TestExchangeWorkspaceRootMapIsolation(t *testing.T) {
	legacy, a, b := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("VIDEO_EXCHANGE_ROOT", legacy)
	setExchangeRootMap(t, map[string]string{"workspace-a": a, "workspace-b": b})
	for _, tc := range []struct{ preset, workspace, path string }{{"a", "workspace-a", a}, {"b", "workspace-b", b}} {
		p := exchangePresetProject(Project{}, tc.preset, tc.workspace)
		selected, err := exchangeSelectionForProject(p)
		if err != nil || selected.Path != tc.path || len(selected.Fingerprint) != 64 {
			t.Fatal(selected, err)
		}
	}
	missing := exchangePresetProject(Project{}, "c", "workspace-c")
	if _, err := exchangeSelectionForProject(missing); err == nil || !strings.Contains(err.Error(), "不会回退") {
		t.Fatal("missing preset mapping used legacy", err)
	}
	selected, err := exchangeSelectionForProject(Project{})
	if err != nil || selected.Path != legacy {
		t.Fatal("legacy did not keep its dedicated root", selected, err)
	}
	// Mutating directory contents does not change physical-root identity.
	before := selected.Fingerprint
	os.WriteFile(filepath.Join(legacy, "new-file"), []byte("x"), 0600)
	selected, err = exchangeSelectionForProject(Project{})
	if err != nil || selected.Fingerprint != before {
		t.Fatal("root fingerprint included mutable file timestamps", selected, err)
	}
}
func TestExchangeRootMapRejectsOverlapsAndAmbiguity(t *testing.T) {
	legacy, other := t.TempDir(), t.TempDir()
	child := filepath.Join(legacy, "child")
	os.Mkdir(child, 0700)
	t.Setenv("VIDEO_EXCHANGE_ROOT", legacy)
	for _, roots := range []map[string]string{{"a": legacy}, {"a": child}, {"a": other, "b": other}, {"a": filepath.Dir(legacy)}} {
		setExchangeRootMap(t, roots)
		if _, _, err := configuredExchangeRoots(); err == nil {
			t.Fatal("overlapping roots accepted", roots)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"a":"relative"}`, `{"a":null}`, `{"a":1}`, `{} {}`} {
		t.Setenv("VIDEO_EXCHANGE_ROOTS", raw)
		if _, _, err := configuredExchangeRoots(); err == nil {
			t.Fatal("ambiguous root configuration accepted", raw)
		}
	}
	raw, _ := json.Marshal(other)
	t.Setenv("VIDEO_EXCHANGE_ROOTS", `{"a":`+string(raw)+`,"a":`+string(raw)+`}`)
	if _, _, err := configuredExchangeRoots(); err == nil {
		t.Fatal("duplicate workspace key accepted")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(other, alias); err == nil {
		setExchangeRootMap(t, map[string]string{"a": other, "b": alias})
		if _, _, err := configuredExchangeRoots(); err == nil {
			t.Fatal("symlink aliases accepted as independent roots")
		}
	}
}

func TestExchangeRootRejectsNestedFilesystemAlias(t *testing.T) {
	a, err := normalizedExchangeRoot(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	b, err := normalizedExchangeRoot(t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if exchangeRootsOverlap(a, b) {
		t.Fatal("fixture roots unexpectedly overlap")
	}
	// Model a nested bind mount: B has a different root inode and unrelated
	// canonical pathname, but one of B's mounted ancestors is A's real inode.
	// Real bind-mount setup requires CAP_SYS_ADMIN and is not available in CI.
	b.Ancestors = append(b.Ancestors, a.Info)
	if !exchangeRootsOverlap(a, b) || !exchangeRootsOverlap(b, a) {
		t.Fatal("nested physical alias escaped root-disjointness checks")
	}
}

type exchangePresetHTTPFixture struct {
	t              *testing.T
	mu             sync.Mutex
	projects       map[string]Project
	roots          map[string]string
	challenges     []string
	afterChallenge func()
}

func (f *exchangePresetHTTPFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	if path == "/meta" {
		meta := v1Meta().(map[string]any)
		meta["capabilities"] = append(meta["capabilities"].([]string), "application-runtime-presets")
		writeJSON(w, 200, meta)
		return
	}
	if strings.HasPrefix(path, "/sessions/") {
		p, ok := f.projects[strings.TrimPrefix(path, "/sessions/")]
		if !ok {
			writeJSON(w, 404, map[string]string{"code": "session_not_found"})
			return
		}
		b := p.RuntimeBinding
		writeJSON(w, 200, map[string]any{"instanceId": b.InstanceID, "workspaceId": b.WorkspaceID, "source": map[string]string{"appId": "rundesk-video-app", "taskId": p.ID, "presetId": b.PresetID}, "executionMode": "docker"})
		return
	}
	preset := r.URL.Query().Get("presetId")
	var body struct {
		PresetID    string `json:"presetId"`
		InstanceID  string `json:"instanceId"`
		WorkspaceID string `json:"workspaceId"`
		Nonce       string `json:"nonce"`
		Challenge   struct {
			Path  string `json:"path"`
			Nonce string `json:"nonce"`
		} `json:"challenge"`
	}
	if r.Method == "POST" {
		json.NewDecoder(r.Body).Decode(&body)
		preset = body.PresetID
	}
	var project Project
	for _, p := range f.projects {
		if p.RuntimeBinding.PresetID == preset {
			project = p
			break
		}
	}
	if project.RuntimeBinding == nil {
		writeJSON(w, 403, map[string]string{"code": "preset_not_authorized"})
		return
	}
	b := project.RuntimeBinding
	if path == "/applications/rundesk-video-app/runtime" {
		writeJSON(w, 200, map[string]any{"appId": "rundesk-video-app", "presetId": preset, "instanceId": b.InstanceID, "workspaceId": b.WorkspaceID, "executionMode": "docker", "environment": map[string]any{"state": "running", "pendingUpdate": false}})
		return
	}
	if path != "/applications/rundesk-video-app/runtime/diagnostics" {
		f.t.Error("unexpected exchange probe path", path)
		writeJSON(w, 404, nil)
		return
	}
	if body.InstanceID != b.InstanceID || body.WorkspaceID != b.WorkspaceID || body.Challenge.Nonce != body.Nonce || !exchangeRelative(body.Challenge.Path) || !strings.HasPrefix(body.Challenge.Path, "outputs/"+project.SessionID+"/") {
		f.t.Error("probe did not freeze selected project", body)
		writeJSON(w, 400, nil)
		return
	}
	root := f.roots[body.WorkspaceID]
	request, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(body.Challenge.Path)))
	if err != nil {
		writeJSON(w, 409, map[string]string{"code": "challenge_not_found", "error": "configured workspace is not the same physical mount"})
		return
	}
	var input struct {
		SchemaVersion int    `json:"schemaVersion"`
		Nonce         string `json:"nonce"`
	}
	if json.Unmarshal(request, &input) != nil || input.SchemaVersion != 1 || input.Nonce != body.Nonce {
		f.t.Error("challenge was replaced")
		writeJSON(w, 400, nil)
		return
	}
	responsePath := strings.TrimSuffix(body.Challenge.Path, "request.json") + "response.json"
	response, _ := json.Marshal(map[string]any{"schemaVersion": 1, "nonce": body.Nonce, "requestSHA256": hashBytes(request)})
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(responsePath)), response, 0600); err != nil {
		f.t.Error(err)
		writeJSON(w, 500, nil)
		return
	}
	f.challenges = append(f.challenges, preset)
	out := runDeskDiagnosticResponse{SchemaVersion: 1, AppID: "rundesk-video-app", PresetID: preset, InstanceID: b.InstanceID, WorkspaceID: b.WorkspaceID, ExecutionMode: "docker", Probed: true, AccountReady: true, Servers: map[string][]string{"video_project": videoTools}, Runtime: runDeskDiagnosticRuntime{Status: "ready", ProfileID: "video-build", ProfileVersion: "1.4.0", AdapterContractVersion: 2, VideoUseCommit: exchangeVideoUseCommit, PythonVersion: "3.11", FFmpegVersion: "6", FFprobeVersion: "6", Checks: map[string]string{"profile": "ready", "ffmpeg": "ready", "ffprobe": "ready", "pythonDependencies": "ready", "adapter": "ready", "sourcePin": "ready"}}}
	out.Challenge.Status, out.Challenge.Path, out.Challenge.ResponsePath = "verified", body.Challenge.Path, responsePath
	out.Challenge.Nonce, out.Challenge.RequestSHA256 = body.Nonce, hashBytes(request)
	png, _ := diagnosticImage(body.Nonce)
	out.ImageTransport.State, out.ImageTransport.Nonce, out.ImageTransport.ImageSHA256 = "passed", body.Nonce, hashBytes(png)
	out.ImageTransport.MIMEType, out.ImageTransport.Width, out.ImageTransport.Height = "image/png", 32, 32
	if f.afterChallenge != nil {
		f.afterChallenge()
	}
	writeJSON(w, 200, out)
}
func cloneExchangePresetProject(t *testing.T, a *App, p Project, preset, workspace string) Project {
	t.Helper()
	q := clone(&p)
	q.ID = uid()
	q.Revision = 1
	q.ProductionRuns = nil
	q.Renders = nil
	q = exchangePresetProject(q, preset, workspace)
	for _, asset := range q.Assets {
		src := filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File)
		dir := filepath.Join(a.dir(q.ID), "workspace", "assets")
		os.MkdirAll(dir, 0700)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, asset.File), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, job := range q.Speech {
		os.MkdirAll(a.speechDir(q.ID, job.ID), 0700)
		for name := range job.Files {
			data, err := os.ReadFile(filepath.Join(a.speechDir(p.ID, job.ID), name))
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(a.speechDir(q.ID, job.ID), name), data, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := a.save(&q); err != nil {
		t.Fatal(err)
	}
	a.projects[q.ID] = &q
	return q
}
func TestPresetExchangeFreshProofAndRemapIsolation(t *testing.T) {
	a, p, _, legacy := exchangeFixture(t)
	rootA, rootB := t.TempDir(), t.TempDir()
	setExchangeRootMap(t, map[string]string{"workspace-a": rootA, "workspace-b": rootB})
	p = exchangePresetProject(p, "a", "workspace-a")
	a.save(&p)
	a.projects[p.ID] = &p
	q := cloneExchangePresetProject(t, a, p, "b", "workspace-b")
	fixture := &exchangePresetHTTPFixture{t: t, projects: map[string]Project{p.SessionID: p, q.SessionID: q}, roots: map[string]string{"workspace-a": rootA, "workspace-b": rootA}}
	server := httptest.NewServer(fixture)
	defer server.Close()
	a.rd = server.URL
	// A missing/mismatched physical mount must fail the MCP operation itself,
	// even though its local environment mapping and project binding exist.
	w := call(a, "POST", "/api/projects/"+q.ID+"/production-runs/prepare", map[string]any{"revision": q.Revision, "requestId": uid()})
	if w.Code != 409 {
		t.Fatal(w.Code, w.Body.String())
	}
	current, _ := a.get(q.ID)
	if len(current.ProductionRuns) != 0 {
		t.Fatal("failed challenge exported a production snapshot")
	}
	fixture.roots["workspace-b"] = rootB
	runA, p := prepareExchange(t, a, p)
	runB, q := prepareExchange(t, a, q)
	for _, tc := range []struct {
		run                     ProductionRun
		root, preset, workspace string
	}{{runA, rootA, "a", "workspace-a"}, {runB, rootB, "b", "workspace-b"}} {
		if tc.run.PresetID != tc.preset || tc.run.WorkspaceID != tc.workspace || len(tc.run.ExchangeRootFingerprint) != 64 {
			t.Fatal(tc.run)
		}
		if _, err := os.Stat(filepath.Join(tc.root, tc.run.ManifestPath)); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(legacy, tc.run.ManifestPath)); !os.IsNotExist(err) {
			t.Fatal("preset exported into legacy root", err)
		}
	}
	diagnostic, err := a.diagnoseRuntime(context.Background(), q.ID)
	if err != nil || !diagnostic.Ready {
		t.Fatal(diagnostic, err)
	}
	if strings.Join(fixture.challenges, ",") != "a,b,b" {
		t.Fatal("prepare/diagnose did not challenge each selected workspace", fixture.challenges)
	}
	// An administrator remapping A must not make an old receipt read B/new files.
	newRoot := t.TempDir()
	setExchangeRootMap(t, map[string]string{"workspace-a": newRoot, "workspace-b": rootB})
	prepareURL := "/api/projects/" + p.ID + "/production-runs/prepare"
	w = call(a, "POST", prepareURL, map[string]any{"revision": runA.Revision, "requestId": runA.RequestID})
	if w.Code != 409 {
		t.Fatal("prepare receipt ignored root remapping", w.Code, w.Body.String())
	}
	w = call(a, "POST", "/api/projects/"+p.ID+"/production-runs/"+runA.ID+"/ingest", map[string]any{"revision": p.Revision})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "映射已改变") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call(a, "GET", "/api/projects/"+p.ID+"/production-runs/"+runA.ID, nil)
	if w.Code != 200 {
		t.Fatal("owned run metadata became unreadable", w.Code, w.Body.String())
	}
	entries, _ := os.ReadDir(newRoot)
	if len(entries) != 0 {
		t.Fatal("old receipt accessed/wrote new workspace", entries)
	}
	// Once archived, both metadata and idempotent ingest remain local even when
	// current mount configuration cannot be parsed or opened at all.
	a.mu.Lock()
	saved := clone(a.projects[p.ID])
	saved.ProductionRuns[0].RenderID = uid()
	saved.ProductionRuns[0].State = "completed"
	a.save(&saved)
	a.projects[p.ID] = &saved
	a.mu.Unlock()
	t.Setenv("VIDEO_EXCHANGE_ROOTS", "invalid-json")
	for _, method := range []string{"GET", "POST"} {
		url := "/api/projects/" + p.ID + "/production-runs/" + runA.ID
		if method == "POST" {
			url += "/ingest"
		}
		w = call(a, method, url, map[string]any{"revision": saved.Revision})
		if w.Code != 200 {
			t.Fatal("private archived receipt tried to read current shared roots", method, w.Code, w.Body.String())
		}
	}
}
func TestExchangeRootFingerprintRejectsDirectoryReplacement(t *testing.T) {
	a, p, _, root := exchangeFixture(t)
	t.Setenv("VIDEO_EXCHANGE_ROOTS", "")
	run, p := prepareExchange(t, a, p)
	old := root + "-previous"
	if err := os.Rename(root, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	w := call(a, "POST", "/api/projects/"+p.ID+"/production-runs/"+run.ID+"/ingest", map[string]any{"revision": p.Revision})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "映射已改变") {
		t.Fatal("same-path replacement reused old root fingerprint", w.Code, w.Body.String())
	}
}
func TestPresetExchangeRejectsBindingChangesDuringFreshProbe(t *testing.T) {
	a, p, _, _ := exchangeFixture(t)
	root := t.TempDir()
	setExchangeRootMap(t, map[string]string{"workspace-a": root})
	p = exchangePresetProject(p, "a", "workspace-a")
	a.save(&p)
	a.projects[p.ID] = &p
	fixture := &exchangePresetHTTPFixture{t: t, projects: map[string]Project{p.SessionID: p}, roots: map[string]string{"workspace-a": root}}
	fixture.afterChallenge = func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		changed := clone(a.projects[p.ID])
		changed.Revision++
		a.save(&changed)
		a.projects[p.ID] = &changed
	}
	server := httptest.NewServer(fixture)
	defer server.Close()
	a.rd = server.URL
	w := call(a, "POST", "/api/projects/"+p.ID+"/production-runs/prepare", map[string]any{"revision": p.Revision, "requestId": uid()})
	if w.Code != 409 || !strings.Contains(w.Body.String(), "验证期间") {
		t.Fatal(w.Code, w.Body.String())
	}
	q, _ := a.get(p.ID)
	if len(q.ProductionRuns) != 0 {
		t.Fatal("changed input exported after probe")
	}
}
