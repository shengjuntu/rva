package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"time"
)

type EditChange struct {
	ID     string          `json:"id"`
	Op     string          `json:"op"`
	Fields json.RawMessage `json:"fields,omitempty"`
}
type EditPatchRequest struct {
	Revision int          `json:"revision"`
	Changes  []EditChange `json:"changes"`
}
type EditHistory struct {
	ID        string       `json:"id"`
	Created   string       `json:"created"`
	Before    EditDocument `json:"before"`
	AfterHash string       `json:"afterHash"`
	Active    bool         `json:"active"`
}

func editHash(d *EditDocument) string { b, _ := json.Marshal(d); return hashBytes(b) }
func patchEdit(d EditDocument, changes []EditChange) (EditDocument, error) {
	raw, _ := json.Marshal(d)
	var q EditDocument
	json.Unmarshal(raw, &q)
	if len(changes) == 0 || len(changes) > 100 {
		return q, fmt.Errorf("需要1–100项修改")
	}
	seen := map[string]bool{}
	for _, c := range changes {
		if !editID.MatchString(c.ID) || seen[c.ID] {
			return q, fmt.Errorf("对象ID无效或重复: %s", c.ID)
		}
		seen[c.ID] = true
		index := -1
		for i, x := range q.Clips {
			if x.ID == c.ID {
				index = i
				break
			}
		}
		switch c.Op {
		case "delete":
			if index < 0 || len(c.Fields) > 0 {
				return q, fmt.Errorf("删除需要现存对象且不能提供fields")
			}
			q.Clips = append(q.Clips[:index], q.Clips[index+1:]...)
		case "update", "add":
			if (c.Op == "update" && index < 0) || (c.Op == "add" && index >= 0) {
				return q, fmt.Errorf("对象不存在或重复: %s", c.ID)
			}
			var fields map[string]json.RawMessage
			if e := json.Unmarshal(c.Fields, &fields); e != nil || len(fields) == 0 {
				return q, fmt.Errorf("fields需要非空对象")
			}
			if _, ok := fields["id"]; ok {
				return q, fmt.Errorf("fields不能修改ID")
			}
			base := map[string]json.RawMessage{}
			if index >= 0 {
				b, _ := json.Marshal(q.Clips[index])
				json.Unmarshal(b, &base)
			}
			for k, v := range fields {
				base[k] = v
			}
			id, _ := json.Marshal(c.ID)
			base["id"] = id
			b, _ := json.Marshal(base)
			var clip EditClip
			dec := json.NewDecoder(bytes.NewReader(b))
			dec.DisallowUnknownFields()
			if e := dec.Decode(&clip); e != nil {
				return q, e
			}
			if index >= 0 {
				q.Clips[index] = clip
			} else {
				q.Clips = append(q.Clips, clip)
			}
		default:
			return q, fmt.Errorf("op必须为add/update/delete")
		}
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
	ir, e := compileEdit(d, p.Assets)
	if e != nil {
		editErrorResponse(w, e)
		return
	}
	before, _ := compileEdit(*p.EditDSL, p.Assets)
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
	revision := p.Revision
	if r.PathValue("action") == "apply" {
		q := clone(p)
		h := EditHistory{ID: uid(), Created: time.Now().UTC().Format(time.RFC3339Nano), Before: *q.EditDSL, AfterHash: editHash(&d), Active: q.EditActive}
		q.EditHistory = append(q.EditHistory, h)
		if len(q.EditHistory) > 20 {
			q.EditHistory = q.EditHistory[len(q.EditHistory)-20:]
		}
		q.EditDSL = &d
		q.Revision++
		if e = a.save(&q); e != nil {
			fail(w, 500, e)
			return
		}
		a.projects[q.ID] = &q
		revision = q.Revision
	}
	writeJSON(w, 200, map[string]any{"revision": revision, "changes": in.Changes, "before": p.EditDSL, "after": d, "ir": ir, "affectedClipIds": affected, "saved": r.PathValue("action") == "apply"})
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
	ir, e := compileEdit(h.Before, p.Assets)
	if e != nil {
		editErrorResponse(w, e)
		return
	}
	q := clone(p)
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
