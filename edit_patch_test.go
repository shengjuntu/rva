package main

import (
	"encoding/json"
	"testing"
)

func TestEditPatchPreviewApplyUndo(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("patch")
	d := simpleEdit()
	d.Clips = append(d.Clips, EditClip{ID: "next", Track: "titles", After: "title", Duration: 1, Text: "next"})
	p.EditDSL = &d
	p.EditActive = true
	a.save(&p)
	a.projects[p.ID] = &p
	url := "/api/projects/" + p.ID + "/edit"
	req := EditPatchRequest{Revision: p.Revision, Changes: []EditChange{{ID: "title", Op: "update", Fields: json.RawMessage(`{"duration":2}`)}}}
	w := call(a, "POST", url+"/patch/preview", req)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var result struct {
		Revision int
		Affected []string `json:"affectedClipIds"`
	}
	json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Affected) != 2 {
		t.Fatal(w.Body.String())
	}
	q, _ := a.get(p.ID)
	if q.Revision != p.Revision || q.EditDSL.Clips[0].Duration != 1 {
		t.Fatal("preview mutated")
	}
	w = call(a, "POST", url+"/patch/apply", req)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	q, _ = a.get(p.ID)
	if q.EditDSL.Clips[0].Duration != 2 || len(q.EditHistory) != 1 {
		t.Fatal(q)
	}
	w = call(a, "POST", url+"/patch/apply", req)
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	w = call(a, "POST", url+"/undo", map[string]any{"revision": q.Revision})
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	q, _ = a.get(p.ID)
	if q.EditDSL.Clips[0].Duration != 1 || len(q.EditHistory) != 0 {
		t.Fatal(q)
	}
	req.Revision = q.Revision
	w = call(a, "POST", url+"/patch/apply", req)
	q, _ = a.get(p.ID)
	q.EditDSL.Clips[0].Text = "later change"
	q.Revision++
	a.save(&q)
	a.projects[q.ID] = &q
	w = call(a, "POST", url+"/undo", map[string]any{"revision": q.Revision})
	if w.Code != 409 {
		t.Fatal("undo overwrote later editing")
	}
}
func TestEditPatchRejectsUnsupportedFields(t *testing.T) {
	for _, raw := range []string{`{"id":"new"}`, `{"invented":true}`, `{}`} {
		if _, e := patchEdit(simpleEdit(), []EditChange{{ID: "title", Op: "update", Fields: json.RawMessage(raw)}}); e == nil {
			t.Fatal(raw)
		}
	}
}
