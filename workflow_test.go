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
				name := runtimeSkillName(id)
				skills = append(skills, map[string]any{"name": name, "path": "/skills/" + name + "/SKILL.md", "enabled": true})
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
	for _, v := range []ProductionWorkflow{{}, {Skills: []string{"unknown"}}, {Skills: []string{"video-review", "video-review"}}, {Skills: []string{"video-editing"}, EditingBackend: "arbitrary-shell"}} {
		if validateWorkflow(v) == nil {
			t.Fatal(v)
		}
	}
	if len(installedProductionSkills()) != 4 {
		t.Fatal("missing production skills")
	}
}

func TestVersionedSkillsInstallWithoutOverwritingLegacy(t *testing.T) {
	bundles := installedProductionSkills()
	if len(bundles) != 4 {
		t.Fatal(len(bundles))
	}
	if _, ok := bundles["video-project"]; ok {
		t.Fatal("unversioned customized skill would collide")
	}
	for id, entry := range bundles {
		files := entry.(map[string]string)
		if !strings.Contains(id, "-v"+strings.ReplaceAll(version, ".", "-")) || !strings.Contains(files["SKILL.md"], "name: "+id+"\n") {
			t.Fatal(id)
		}
	}
}

func TestWorkflowBackendCompatibilityAndInstruction(t *testing.T) {
	legacy := ProductionWorkflow{Skills: []string{"video-narration", "video-editing", "video-review"}, Instructions: "保留素材来源"}
	if got := workflowFor(Project{Workflow: &legacy}); got.EditingBackend != "native" {
		t.Fatal("legacy backend was not normalized", got)
	}
	native := legacy
	native.EditingBackend = "native"
	if workflowHash(legacy) != workflowHash(native) {
		t.Fatal("empty/native backend changed historical workflow hash")
	}
	external := native
	external.EditingBackend = "video_use"
	if workflowHash(external) == workflowHash(native) {
		t.Fatal("different editing authorities share a workflow hash")
	}
	p := Project{ID: "example", Revision: 3, NarrationText: "原文完整保留。", Workflow: &external}
	instruction, err := draftInstruction(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"剪辑后端：video_use", "素材准备和对齐", "（2）剪辑", "（3）精修", "DSL/EDL 只约束剪辑", "video_media_alignment_update", "video_production_run_prepare", "video_production_run_ingest", "video_runtime_diagnose", "video_edit_locate", "modelVisionVerified=false", "textRefs"} {
		if !strings.Contains(instruction, want) {
			t.Fatal("missing actionable workflow instruction", want)
		}
	}
	if strings.Contains(instruction, "video_production_run_export") {
		t.Fatal("instruction refers to nonexistent export tool")
	}
}

func TestInstalledSkillsKeepDiagnosticAndReferenceBoundaries(t *testing.T) {
	// The installed bundles are the Agent's executable workflow contract. Ensure
	// upgrades carry the real tools without teaching semantic or vision proof.
	bundles := installedProductionSkills()
	checks := map[string][]string{
		"video-project": {
			"video_runtime_diagnose(projectId)", "passed/failed/blocked/not_checked",
			"modelVisionVerified", "not prove the model saw an image",
			"textSourceHash", "frozen manifest textPath", "unmappedClipIds",
			"video_edit_locate(projectId,quote,occurrence?,renderId?)",
			"An EDL needs no top-level textSourceHash", "do not prove semantic correctness or word-level timing",
		},
		"video-narration": {
			"video_runtime_diagnose(projectId)", "before paid generation",
			"Do not rebuild environments", "not that all production is blocked",
		},
		"video-editing": {
			"document textSourceHash", "textRefs=[{quote,occurrence?}]",
			"unreferenced background and legacy clips", "exact frozen-original textRefs",
		},
		"video-review": {
			"video_edit_locate(projectId,quote,occurrence?,renderId?)",
			"unmappedClipIds and empty matches", "not word timing or verified semantics",
			"Preserve unrelated IDs, references, assets and edits",
		},
	}
	for logical, required := range checks {
		t.Run(logical, func(t *testing.T) {
			files := bundles[runtimeSkillName(logical)].(map[string]string)
			body := files["SKILL.md"]
			for _, want := range required {
				if !strings.Contains(body, want) {
					t.Errorf("installed Skill missing workflow contract %q", want)
				}
			}
			if strings.Contains(body, "rundesk_diagnostic_image(") {
				t.Error("internal transport probe must not be presented as an Agent-facing tool")
			}
		})
	}
}

func TestWorkflowBackendSaveRoundTrip(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("backend")
	u := "/api/projects/" + p.ID + "/workflow"
	for _, backend := range []string{"", "video_use", "native"} {
		v := ProductionWorkflow{Skills: []string{"video-editing"}, EditingBackend: backend}
		w := call(a, "PUT", u, map[string]any{"revision": p.Revision, "workflow": v})
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		expect := backend
		if expect == "" {
			expect = "native"
		}
		if p.Workflow == nil || p.Workflow.EditingBackend != expect {
			t.Fatal(p.Workflow)
		}
		w = call(a, "GET", u, nil)
		var result struct{ Workflow ProductionWorkflow }
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Workflow.EditingBackend != expect {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}
