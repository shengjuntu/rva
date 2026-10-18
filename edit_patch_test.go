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
func TestPatchTargetsAndFieldDiff(t *testing.T) {
	d := simpleEdit()
	changes := []EditChange{{Target: "track", ID: "overlay", Op: "add", Fields: json.RawMessage(`{"kind":"text","name":"新增"}`)}, {Target: "anchor", ID: "cue", Op: "add", Fields: json.RawMessage(`{"time":2}`)}, {Target: "clip", ID: "title", Op: "update", Fields: json.RawMessage(`{"track":"overlay","start":null,"anchor":"cue"}`)}, {Target: "canvas", ID: "canvas", Op: "update", Fields: json.RawMessage(`{"width":640,"height":360}`)}, {Target: "track", ID: "titles", Op: "delete"}}
	q, e := patchEdit(d, changes)
	if e != nil {
		t.Fatal(e)
	}
	ir, e := compileEdit(q, nil)
	if e != nil || ir.Clips[0].Start != 2 || ir.Canvas.Width != 640 {
		t.Fatal(ir, e)
	}
	diff := editFieldDiffs(d, q)
	if len(diff) < 5 {
		t.Fatal(diff)
	}
	for _, c := range []EditChange{{Target: "canvas", ID: "canvas", Op: "delete"}, {Target: "anchor", ID: "cue", Op: "add", Fields: json.RawMessage(`{"invented":3}`)}} {
		if _, e := patchEdit(d, []EditChange{c}); e == nil {
			t.Fatal(c)
		}
	}
}
func TestUndoRedoAndBranch(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("redo")
	d := simpleEdit()
	p.EditDSL = &d
	p.EditActive = true
	a.save(&p)
	a.projects[p.ID] = &p
	url := "/api/projects/" + p.ID + "/edit"
	apply := func(text string) {
		q, _ := a.get(p.ID)
		w := call(a, "POST", url+"/patch/apply", EditPatchRequest{Revision: q.Revision, Changes: []EditChange{{ID: "title", Op: "update", Fields: json.RawMessage(`{"text":"` + text + `"}`)}}})
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
	}
	step := func(action string) {
		q, _ := a.get(p.ID)
		w := call(a, "POST", url+"/"+action, map[string]any{"revision": q.Revision})
		if w.Code != 200 {
			t.Fatal(action, w.Body.String())
		}
	}
	apply("first")
	apply("second")
	step("undo")
	step("undo")
	step("redo")
	step("redo")
	q, _ := a.get(p.ID)
	if q.EditDSL.Clips[0].Text != "second" || len(q.EditRedo) != 0 {
		t.Fatal(q)
	}
	step("undo")
	apply("branch")
	q, _ = a.get(p.ID)
	w := call(a, "POST", url+"/redo", map[string]any{"revision": q.Revision})
	if w.Code != 409 {
		t.Fatal("branch retained redo")
	}
	step("undo")
	b, e := newApp(a.data)
	if e != nil {
		t.Fatal(e)
	}
	q, _ = b.get(p.ID)
	w = call(b, "POST", url+"/redo", map[string]any{"revision": q.Revision})
	if w.Code != 200 {
		t.Fatal("restart redo", w.Body.String())
	}
}
