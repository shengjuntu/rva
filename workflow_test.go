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
		if yaml, ok := files["agents/openai.yaml"]; ok && !strings.Contains(yaml, "$"+id+" ") {
			t.Fatal("Skill prompt refers to a different installed identity", id)
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
	for _, want := range []string{"剪辑后端：video_use", "素材准备和对齐", "（2）剪辑", "（3）精修", "剪辑声明只记录时间、素材和声音，不限制内容表达", "video_media_alignment_update", "video_runtime_diagnose", "video_edit_locate", "modelVisionVerified=false", "textRefs"} {
		if !strings.Contains(instruction, want) {
			t.Fatal("missing actionable workflow instruction", want)
		}
	}
	if strings.Contains(instruction, "video_production_run_export") {
		t.Fatal("instruction refers to nonexistent export tool")
	}
	// Detailed compatibility contracts are installed as referenced files rather
	// than repeated in every submission prompt.
	files := installedProductionSkills()[runtimeSkillName("video-project")].(map[string]string)
	if !strings.Contains(instruction, runtimeSkillName("video-project")) || !strings.Contains(files["SKILL.md"], "references/backend-compatibility.md") {
		t.Fatal("submission lost its installed compatibility guidance")
	}
	for _, want := range []string{"video_production_run_prepare", "video_production_run_ingest", "frozen manifest textPath", "An EDL needs no top-level textSourceHash"} {
		if !strings.Contains(files["references/backend-compatibility.md"], want) {
			t.Fatal("missing referenced workflow contract", want)
		}
	}
}

func TestInstalledSkillsKeepDiagnosticAndReferenceBoundaries(t *testing.T) {
	// The base Skill is always attached with selected stage Skills. Validate that
	// complete installed bundle, including its reachable compatibility references,
	// instead of requiring every detailed contract to be repeated in SKILL.md.
	bundles := installedProductionSkills()
	base := bundles[runtimeSkillName("video-project")].(map[string]string)
	for _, reference := range []string{"references/backend-compatibility.md", "references/hyperframes-authoring.md"} {
		if base[reference] == "" || !strings.Contains(base["SKILL.md"], reference) {
			t.Fatal("installed reference is missing or unreachable", reference)
		}
	}
	bundleText := func(files map[string]string) string {
		var text strings.Builder
		for _, body := range files {
			text.WriteString(body)
			text.WriteByte('\n')
		}
		return text.String()
	}
	baseText := bundleText(base)
	checks := map[string][]string{
		"video-project": {
			"video_runtime_diagnose", "passed/failed/blocked/not_checked", "modelVisionVerified=false alone",
			"does not prove the model saw the picture", "textSourceHash", "frozen manifest textPath",
			"Empty matches/unmapped clips indicate gaps, not inferred associations",
			"video_edit_locate(projectId,quote,occurrence?,renderId?)", "An EDL needs no top-level textSourceHash",
			"not word alignment or semantic proof",
		},
		"video-narration": {
			"video_runtime_diagnose", "configured capabilities", "Unknown remote state is not permission to duplicate paid work",
			"Diagnostics do not authorize rebuilding environments or switching images", "modelVisionVerified=false alone",
			"preserve facts", "not forced word alignment, ASR or listening",
		},
		"video-editing": {
			"document textSourceHash", "textRefs=[{quote,occurrence?}]", "Allow unreferenced background and legacy clips",
			"frozen manifest textPath", "Source implements animation, captions and audio",
			"not an automatic animation/audio generator or measured timing", "do not silently claim parity",
		},
		"video-review": {
			"video_edit_locate(projectId,quote,occurrence?,renderId?)", "Empty matches and unmapped clips are gaps, not guessed matches",
			"not extracted/verified playback", "Preserve unrelated source, IDs, references and accepted work",
			"frame samples prove neither unseen motion nor audio", "Agent review is not human acceptance",
		},
	}
	for logical, required := range checks {
		t.Run(logical, func(t *testing.T) {
			files := bundles[runtimeSkillName(logical)].(map[string]string)
			body := baseText + "\n" + bundleText(files)
			for _, want := range required {
				if !strings.Contains(strings.ToLower(body), strings.ToLower(want)) {
					t.Errorf("installed Skill bundle missing workflow contract %q", want)
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
