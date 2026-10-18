package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"
)

type EditChange struct {
	Target string          `json:"target,omitempty"`
	ID     string          `json:"id"`
	Op     string          `json:"op"`
	Fields json.RawMessage `json:"fields,omitempty"`
}
type EditPatchRequest struct {
	Revision       int          `json:"revision"`
	Changes        []EditChange `json:"changes"`
	TextSourceHash *string      `json:"textSourceHash,omitempty"`
}
type EditHistory struct {
	After     *EditDocument `json:"after,omitempty"`
	ID        string        `json:"id"`
	Created   string        `json:"created"`
	Before    EditDocument  `json:"before"`
	AfterHash string        `json:"afterHash"`
	Active    bool          `json:"active"`
}

func editHash(d *EditDocument) string { b, _ := json.Marshal(d); return hashBytes(b) }
func patchEdit(d EditDocument, changes []EditChange) (EditDocument, error) {
	raw, _ := json.Marshal(d)
	var doc map[string]json.RawMessage
	json.Unmarshal(raw, &doc)
	if len(changes) == 0 || len(changes) > 100 {
		return d, fmt.Errorf("需要1–100项修改")
	}
	seen := map[string]bool{}
	for _, c := range changes {
		target := c.Target
		if target == "" {
			target = "clip"
		}
		key := map[string]string{"clip": "clips", "track": "tracks", "anchor": "anchors", "canvas": "canvas"}[target]
		if key == "" {
			return d, fmt.Errorf("target必须为clip/track/anchor/canvas")
		}
		id := c.ID
		if target == "canvas" && id != "canvas" {
			return d, fmt.Errorf("画布ID必须为canvas")
		}
		if !editID.MatchString(id) || seen[target+":"+id] {
			return d, fmt.Errorf("对象ID无效或重复")
		}
		seen[target+":"+id] = true
		var objects []map[string]json.RawMessage
		index := -1
		if target == "canvas" {
			var v map[string]json.RawMessage
			json.Unmarshal(doc[key], &v)
			objects = []map[string]json.RawMessage{v}
			index = 0
		} else {
			json.Unmarshal(doc[key], &objects)
			for i, v := range objects {
				var existing string
				json.Unmarshal(v["id"], &existing)
				if existing == id {
					index = i
					break
				}
			}
		}
		switch c.Op {
		case "delete":
			if target == "canvas" || index < 0 || len(c.Fields) > 0 {
				return d, fmt.Errorf("删除需要现存非画布对象且不能提供fields")
			}
			objects = append(objects[:index], objects[index+1:]...)
		case "add", "update":
			if (target == "canvas" && c.Op != "update") || (c.Op == "update" && index < 0) || (c.Op == "add" && index >= 0) {
				return d, fmt.Errorf("对象不存在或重复")
			}
			var fields map[string]json.RawMessage
			if e := json.Unmarshal(c.Fields, &fields); e != nil || len(fields) == 0 {
				return d, fmt.Errorf("fields需要非空对象")
			}
			if _, ok := fields["id"]; ok {
				return d, fmt.Errorf("fields不能修改ID")
			}
			v := map[string]json.RawMessage{}
			if index >= 0 {
				v = objects[index]
			}
			for k, x := range fields {
				v[k] = x
			}
			if target != "canvas" {
				v["id"], _ = json.Marshal(id)
			}
			if index >= 0 {
				objects[index] = v
			} else {
				objects = append(objects, v)
			}
		default:
			return d, fmt.Errorf("op必须为add/update/delete")
		}
		if target == "canvas" {
			doc[key], _ = json.Marshal(objects[0])
		} else {
			doc[key], _ = json.Marshal(objects)
		}
	}
	raw, _ = json.Marshal(doc)
	var q EditDocument
	if e := json.Unmarshal(raw, &q); e != nil {
		return d, e
	}
	return q, nil
}
func (a *App) editPatch(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("action") != "preview" && r.PathValue("action") != "apply" {
		fail(w, 404, fmt.Errorf("未知操作"))
		return
	}
	var in EditPatchRequest
	if !decodeStrict(w, r, &in) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	if p.Revision != in.Revision || p.EditDSL == nil {
		fail(w, 409, fmt.Errorf("版本冲突或尚无剪辑DSL"))
		return
	}
	d, e := patchEdit(*p.EditDSL, in.Changes)
	if e != nil {
		fail(w, 422, e)
		return
	}
	if in.TextSourceHash != nil {
		d.TextSourceHash = *in.TextSourceHash
	}
	ir, e := compileProjectEdit(*p, d)
	if e != nil {
		editErrorResponse(w, e)
		return
	}
	before, _ := compileProjectEdit(*p, *p.EditDSL)
	affected := []string{}
	for _, x := range ir.Clips {
		found := false
		for _, old := range before.Clips {
			if old.ID == x.ID {
				found = true
				if !reflect.DeepEqual(old, x) {
					affected = append(affected, x.ID)
				}
				break
			}
		}
		if !found {
			affected = append(affected, x.ID)
		}
	}
	for _, old := range before.Clips {
		found := false
		for _, x := range ir.Clips {
			if old.ID == x.ID {
				found = true
				break
			}
		}
		if !found {
			affected = append(affected, old.ID)
		}
	}
	// Editorial references do not change rendered pixels, so they are absent
	// from IR equality. Still report their stable clip IDs in a patch preview.
	fieldDiffs := editFieldDiffs(*p.EditDSL, d)
	for _, diff := range fieldDiffs {
		if diff.Target != "clip" {
			continue
		}
		found := false
		for _, id := range affected {
			if id == diff.ID {
				found = true
				break
			}
		}
		if !found {
			affected = append(affected, diff.ID)
		}
	}
	revision := p.Revision
	if r.PathValue("action") == "apply" {
		q := clone(p)
		h := EditHistory{ID: uid(), Created: time.Now().UTC().Format(time.RFC3339Nano), Before: *q.EditDSL, AfterHash: editHash(&d), Active: q.EditActive}
		q.EditHistory = append(q.EditHistory, h)
		if len(q.EditHistory) > 20 {
			q.EditHistory = q.EditHistory[len(q.EditHistory)-20:]
		}
		q.EditDSL = &d
		q.EditRedo = nil
		q.Revision++
		if e = a.save(&q); e != nil {
			fail(w, 500, e)
			return
		}
		a.projects[q.ID] = &q
		revision = q.Revision
	}
	writeJSON(w, 200, map[string]any{"revision": revision, "fieldDiffs": fieldDiffs, "changes": in.Changes, "before": p.EditDSL, "after": d, "ir": ir, "affectedClipIds": affected, "saved": r.PathValue("action") == "apply"})
}
func (a *App) editUndo(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int `json:"revision"`
	}
	if !decodeStrict(w, r, &in) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	if p.Revision != in.Revision || len(p.EditHistory) == 0 {
		fail(w, 409, fmt.Errorf("版本冲突或没有可撤销记录"))
		return
	}
	h := p.EditHistory[len(p.EditHistory)-1]
	if editHash(p.EditDSL) != h.AfterHash || p.EditActive != h.Active {
		fail(w, 409, fmt.Errorf("剪辑已通过其他操作改变，不能覆盖撤销"))
		return
	}
	ir, e := compileProjectEdit(*p, h.Before)
	if e != nil {
		editErrorResponse(w, e)
		return
	}
	q := clone(p)
	h.After = q.EditDSL
	q.EditRedo = append(q.EditRedo, h)
	q.EditDSL = &h.Before
	q.EditHistory = q.EditHistory[:len(q.EditHistory)-1]
	q.Revision++
	if e = a.save(&q); e != nil {
		fail(w, 500, e)
		return
	}
	a.projects[q.ID] = &q
	writeJSON(w, 200, map[string]any{"revision": q.Revision, "dsl": q.EditDSL, "ir": ir, "undone": h.ID})
}

func (c *EditChange) UnmarshalJSON(raw []byte) error {
	type plain EditChange
	var v plain
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if e := dec.Decode(&v); e != nil {
		return e
	}
	*c = EditChange(v)
	return nil
}

type EditFieldDiff struct {
	Target string `json:"target"`
	ID     string `json:"id"`
	Field  string `json:"field"`
	Before any    `json:"before"`
	After  any    `json:"after"`
}

func editObjects(d EditDocument) map[string]map[string]any {
	out := map[string]map[string]any{}
	raw, _ := json.Marshal(d)
	var doc map[string]any
	json.Unmarshal(raw, &doc)
	out["document:document"] = map[string]any{"textSourceHash": d.TextSourceHash}
	out["canvas:canvas"] = doc["canvas"].(map[string]any)
	for _, pair := range [][2]string{{"clips", "clip"}, {"tracks", "track"}, {"anchors", "anchor"}} {
		if list, ok := doc[pair[0]].([]any); ok {
			for _, v := range list {
				obj := v.(map[string]any)
				out[pair[1]+":"+obj["id"].(string)] = obj
			}
		}
	}
	return out
}
func editFieldDiffs(before, after EditDocument) []EditFieldDiff {
	a, b := editObjects(before), editObjects(after)
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	sorted := []string{}
	for k := range keys {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)
	out := []EditFieldDiff{}
	for _, key := range sorted {
		parts := strings.SplitN(key, ":", 2)
		fields := map[string]bool{}
		for k := range a[key] {
			fields[k] = true
		}
		for k := range b[key] {
			fields[k] = true
		}
		names := []string{}
		for k := range fields {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, field := range names {
			av, aok := a[key][field]
			bv, bok := b[key][field]
			if aok != bok || !reflect.DeepEqual(av, bv) {
				out = append(out, EditFieldDiff{parts[0], parts[1], field, av, bv})
			}
		}
	}
	return out
}
func (a *App) editRedo(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int `json:"revision"`
	}
	if !decodeStrict(w, r, &in) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, fmt.Errorf("项目不存在"))
		return
	}
	if p.Revision != in.Revision || len(p.EditRedo) == 0 {
		fail(w, 409, fmt.Errorf("版本冲突或没有可重做记录"))
		return
	}
	h := p.EditRedo[len(p.EditRedo)-1]
	if h.After == nil || editHash(p.EditDSL) != editHash(&h.Before) || p.EditActive != h.Active {
		fail(w, 409, fmt.Errorf("当前剪辑已改变，不能覆盖重做"))
		return
	}
	ir, e := compileProjectEdit(*p, *h.After)
	if e != nil {
		editErrorResponse(w, e)
		return
	}
	q := clone(p)
	q.EditDSL = h.After
	h.After = nil
	q.EditHistory = append(q.EditHistory, h)
	q.EditRedo = q.EditRedo[:len(q.EditRedo)-1]
	q.Revision++
	if e = a.save(&q); e != nil {
		fail(w, 500, e)
		return
	}
	a.projects[q.ID] = &q
	writeJSON(w, 200, map[string]any{"revision": q.Revision, "dsl": q.EditDSL, "ir": ir, "redone": h.ID})
}
