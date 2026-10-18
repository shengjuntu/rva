package main

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testApp(t *testing.T) *App {
	t.Helper()
	a, e := newApp(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	a.token = "test-access-token-not-secret"
	return a
}
func call(a *App, method, path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "http://127.0.0.1:3220"+path, bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+a.token)
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	return w
}
func TestRevisionConflictPreservesAgentEdit(t *testing.T) {
	a := testApp(t)
	p, e := a.create("项目")
	if e != nil {
		t.Fatal(e)
	}
	draft := p
	p.Brief = "Agent 修改"
	p.Shots = []Shot{{Title: "标题", Duration: 2}}
	updated, e := a.edit(p.ID, p)
	if e != nil {
		t.Fatal(e)
	}
	draft.Brief = "旧草稿"
	if _, e = a.edit(p.ID, draft); e == nil {
		t.Fatal("stale write accepted")
	}
	got, _ := a.get(p.ID)
	if got.Brief != "Agent 修改" || got.Revision != updated.Revision {
		t.Fatal(got)
	}
	if got.Shots[0].ID == "" {
		t.Fatal("missing shot id")
	}
	got.Shots[0].Duration = 0
	if _, e = a.edit(p.ID, got); e == nil {
		t.Fatal("invalid duration accepted")
	}
}
func TestAuthAndOrigin(t *testing.T) {
	a := testApp(t)
	for _, test := range []struct {
		host, origin, token string
		want                int
	}{{"127.0.0.1:3220", "", "", 401}, {"attacker.example", "", a.token, 403}, {"127.0.0.1:3220", "http://elsewhere", a.token, 403}, {"127.0.0.1:3220", "http://127.0.0.1:3220", a.token, 200}} {
		r := httptest.NewRequest("GET", "http://"+test.host+"/api/projects", nil)
		r.Header.Set("Authorization", "Bearer "+test.token)
		r.Header.Set("Origin", test.origin)
		w := httptest.NewRecorder()
		a.routes().ServeHTTP(w, r)
		if w.Code != test.want {
			t.Fatalf("%s: %d want %d", test.host, w.Code, test.want)
		}
	}
}
func TestAssetCannotEscapeWorkspace(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("项目")
	secret := filepath.Join(a.data, "secret.txt")
	os.WriteFile(secret, []byte("secret"), 0600)
	file := filepath.Join(a.dir(p.ID), "workspace", "assets", "escape.txt")
	if e := os.Symlink(secret, file); e != nil {
		t.Skip(e)
	}
	aid := uid()
	a.projects[p.ID].Assets = []Asset{{ID: aid, File: "escape.txt", MIME: "text/plain"}}
	w := call(a, "GET", "/api/projects/"+p.ID+"/assets/"+aid, nil)
	if w.Code != 404 || strings.Contains(w.Body.String(), "secret\"") {
		t.Fatal(w.Code, w.Body.String())
	}
}
func TestRenderRestartAndSnapshots(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("项目")
	p.Shots = []Shot{{ID: uid(), Title: "<script>alert(1)</script>", Text: "正文", Duration: 2}}
	p.Renders = []Render{{ID: uid(), State: "running"}}
	a.save(&p)
	b, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	got, _ := b.get(p.ID)
	if got.Renders[0].State != "interrupted" {
		t.Fatal(got)
	}
	a.font = filepath.Join(a.data, "font.otf")
	a.gsap = filepath.Join(a.data, "gsap.js")
	os.WriteFile(a.font, []byte("fixture"), 0600)
	os.WriteFile(a.gsap, []byte("fixture"), 0600)
	dir := filepath.Join(a.data, "composition")
	if e = a.compose(p, dir); e != nil {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "index.html"))
	if strings.Contains(string(raw), "<script>alert(1)") || !strings.Contains(string(raw), "&lt;script&gt;") {
		t.Fatal("unsafe HTML")
	}
	p.Shots[0].Title = "new"
	raw, _ = os.ReadFile(filepath.Join(dir, "snapshot.json"))
	if strings.Contains(string(raw), `"title": "new"`) {
		t.Fatal("mutable snapshot")
	}
}
func TestRunDeskConfigurationAndSteer(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("项目")
	iid, wid, sid := uid(), uid(), uid()
	seen := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer rd-test-token" {
			t.Error("missing bearer")
		}
		seen = append(seen, r.Method+" "+r.URL.Path)
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if strings.HasPrefix(r.URL.Path, "/api/v1/requests/") {
			writeJSON(w, 404, map[string]string{"code": "request_not_found"})
			return
		}
		if r.Method == "POST" && (r.URL.Path == "/api/v1/instances" || r.URL.Path == "/api/v1/workspaces" || r.URL.Path == "/api/v1/sessions") && r.Header.Get("Idempotency-Key") == "" {
			t.Error("missing operation key")
		}
		switch r.URL.Path {
		case "/api/v1/meta":
			writeJSON(w, 200, v1Meta())
		case "/api/v1/instances/" + iid + "/configuration":
			writeJSON(w, 200, map[string]any{"skills": []any{map[string]any{"name": "video-project", "path": "/instance/video-project/SKILL.md", "sourceScope": "instance", "enabled": true}}})
		case "/api/v1/sessions/" + sid:
			writeJSON(w, 200, map[string]string{"runId": "run-1", "turnId": "turn-1"})
		case "/api/v1/sessions":
			if r.Method == "GET" {
				writeJSON(w, 200, []any{})
				return
			}
			if body["source"].(map[string]any)["taskId"] != p.ID {
				t.Error("missing application source")
			}
			writeJSON(w, 200, map[string]string{"id": sid})
		case "/api/v1/applications/rundesk-video-app/connect":
			defaults := body["defaults"].(map[string]any)
			mcp := defaults["mcp"].(map[string]any)["video_project"].(map[string]any)
			if mcp["url"] != "http://127.0.0.1:3220/api/mcp" {
				t.Fatal(mcp)
			}
			writeJSON(w, 200, map[string]any{"application": map[string]string{"instanceId": iid, "workspaceId": wid}, "status": "connected"})
		case "/api/v1/instances":
			writeJSON(w, 200, map[string]string{"id": iid})
		case "/api/v1/workspaces":
			if body["path"] != filepath.Join(a.dir(p.ID), "workspace") {
				t.Error(body)
			}
			writeJSON(w, 200, map[string]string{"id": wid})
		case "/api/v1/workspaces/" + wid + "/skills/video-project":
			if r.URL.Query().Get("scope") != "instance" {
				t.Error("skill not instance scoped")
			}
			if !strings.Contains(body["content"].(string), "name: video-project") {
				t.Error(body)
			}
			writeJSON(w, 200, map[string]bool{"ok": true})
		case "/api/v1/workspaces/" + wid + "/mcp":
			writeJSON(w, 200, map[string]string{"version": "v1"})
		case "/api/v1/workspaces/" + wid + "/mcp/video_project":
			if body["version"] != "v1" {
				t.Error(body)
			}
			cfg := body["config"].(map[string]any)
			if cfg["env"].(map[string]any)["VIDEO_APP_TOKEN"] != a.token {
				t.Error("missing MCP token")
			}
			writeJSON(w, 200, map[string]bool{"ok": true})

		case "/api/v1/sessions/" + sid + "/steer":
			if body["expectedTurnId"] != "turn-1" || body["requestId"] != "request-1" || !strings.Contains(body["text"].(string), p.ID) {
				t.Error(body)
			}
			writeJSON(w, 200, map[string]string{"status": "accepted"})
		default:
			t.Error("unexpected " + r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	a.rd = server.URL
	a.rdToken = "rd-test-token"
	a.public = "http://127.0.0.1:3220"
	w := call(a, "POST", "/api/projects/"+p.ID+"/connect", map[string]any{})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), a.token) {
		t.Fatal("token exposed")
	}
	before := len(seen)
	call(a, "POST", "/api/projects/"+p.ID+"/connect", map[string]any{})
	if len(seen) != before+2 || seen[len(seen)-1] != "POST /api/v1/applications/rundesk-video-app/connect" {
		t.Fatal("connect not idempotent")
	}
	a.saveAgent(p.ID, func(q *Project) { q.AgentRunID = "run-1" })
	w = call(a, "POST", "/api/projects/"+p.ID+"/messages", map[string]string{"text": "继续", "expectedTurnId": "turn-1", "requestId": "request-1"})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestUploadAndMediaRange(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("素材项目")
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	f, _ := form.CreateFormFile("file", "example.txt")
	f.Write([]byte("hello video app"))
	form.Close()
	r := httptest.NewRequest("POST", "http://127.0.0.1:3220/api/projects/"+p.ID+"/assets", &buf)
	r.Header.Set("Authorization", "Bearer "+a.token)
	r.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 201 {
		t.Fatal(w.Code, w.Body.String())
	}
	got, _ := a.get(p.ID)
	if len(got.Assets) != 1 || got.Revision != 2 {
		t.Fatal(got)
	}
	asset := got.Assets[0]
	if _, e := os.Stat(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File)); e != nil {
		t.Fatal(e)
	}
	r = httptest.NewRequest("GET", "http://127.0.0.1:3220/api/projects/"+p.ID+"/assets/"+asset.ID, nil)
	r.Header.Set("Authorization", "Bearer "+a.token)
	r.Header.Set("Range", "bytes=0-4")
	w = httptest.NewRecorder()
	a.routes().ServeHTTP(w, r)
	if w.Code != 206 || w.Body.String() != "hello" {
		t.Fatal(w.Code, w.Body.String())
	}
}
