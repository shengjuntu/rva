package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectWorkflowAndDraft(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("draft")
	url := "/api/projects/" + p.ID
	w := call(a, "POST", url+"/draft/instruction", map[string]any{"revision": p.Revision})
	if w.Code != 422 {
		t.Fatal(w.Code)
	}
	w = call(a, "PUT", url+"/narration", map[string]any{"revision": p.Revision, "text": "原始事实。"})
	json.Unmarshal(w.Body.Bytes(), &p)
	v := ProductionWorkflow{Skills: []string{"video-editing", "video-review"}, Instructions: "保持原意"}
	w = call(a, "PUT", url+"/workflow", map[string]any{"revision": p.Revision, "workflow": v})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &p)
	w = call(a, "PUT", url+"/workflow", map[string]any{"revision": p.Revision - 1, "workflow": v})
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	w = call(a, "POST", url+"/draft/instruction", map[string]any{"revision": p.Revision})
	var instruction struct{ Text string }
	json.Unmarshal(w.Body.Bytes(), &instruction)
	if !strings.Contains(instruction.Text, "保持原意") || strings.Contains(instruction.Text, "Skill video-narration:") {
		t.Fatal(instruction.Text)
	}
	p.WorkspaceID = "w1"
	p.SessionID = "s1"
	p.ToolsVersion = version
	a.save(&p)
	a.projects[p.ID] = &p
	a.cfg.ManagedByRunDesk = true
	a.cfg.InstanceID = "i1"
	a.cfg.WorkspaceID = p.WorkspaceID
	posts := 0
	rd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/requests/"):
			writeJSON(w, 404, map[string]string{"code": "request_not_found"})
		case strings.HasSuffix(r.URL.Path, "/runtime"):
			writeJSON(w, 200, map[string]any{"appId": "rundesk-video-app", "instanceId": "i1", "workspaceId": p.WorkspaceID, "executionMode": "local"})
		case strings.HasSuffix(r.URL.Path, "/configuration"):
			skills := []map[string]any{}
			for _, id := range []string{"video-project", "video-editing", "video-review"} {
				skills = append(skills, map[string]any{"name": id, "path": "/skills/" + id + "/SKILL.md", "enabled": true})
			}
			writeJSON(w, 200, map[string]any{"skills": skills})
		case strings.HasSuffix(r.URL.Path, "/turns"):
			posts++
			var body struct {
				Text   string
				Skills []map[string]string
			}
			json.NewDecoder(r.Body).Decode(&body)
			if len(body.Skills) != 3 || !strings.Contains(body.Text, "保持原意") {
				t.Error(body)
			}
			writeJSON(w, 200, map[string]string{"id": "s1", "runId": "run1"})
		default:
			writeJSON(w, 200, map[string]string{"instanceId": "i1", "workspaceId": p.WorkspaceID, "executionMode": "local"})
		}
	}))
	defer rd.Close()
	a.rd = rd.URL
	msg := map[string]any{"text": instruction.Text, "requestId": "draft-request-1", "skills": v.Skills}
	w = call(a, "POST", url+"/messages", msg)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = call(a, "POST", url+"/messages", msg)
	if w.Code != 200 || posts != 1 {
		t.Fatal(w.Code, posts, w.Body.String())
	}
	msg["skills"] = []string{"video-review"}
	w = call(a, "POST", url+"/messages", msg)
	if w.Code != 409 {
		t.Fatal("changed skills accepted")
	}
}
func TestWorkflowValidation(t *testing.T) {
	for _, v := range []ProductionWorkflow{{}, {Skills: []string{"unknown"}}, {Skills: []string{"video-review", "video-review"}}} {
		if validateWorkflow(v) == nil {
			t.Fatal(v)
		}
	}
	if len(installedProductionSkills()) != 4 {
		t.Fatal("missing production skills")
	}
}
