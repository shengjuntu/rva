package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

type RenderRange struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}
type FinalSnapshot struct {
	ID            string `json:"id"`
	RenderID      string `json:"renderId"`
	Created       string `json:"created"`
	Revision      int    `json:"revision"`
	ContentHash   string `json:"contentHash"`
	NarrationHash string `json:"narrationHash"`
	VideoHash     string `json:"videoHash"`
	SnapshotHash  string `json:"snapshotHash"`
	VideoBytes    int64  `json:"videoBytes"`
	Reviewed      bool   `json:"reviewed"`
}

func (a *App) finalGet(w http.ResponseWriter, r *http.Request) {
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	current := finalCurrent(p)
	writeJSON(w, 200, map[string]any{"revision": p.Revision, "final": p.Final, "current": current})
}
func (a *App) finalSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int    `json:"revision"`
		RenderID string `json:"renderId"`
		Reviewed bool   `json:"reviewed"`
	}
	if !decodeStrict(w, r, &in) {
		return
	}
	if !in.Reviewed || !identifier.MatchString(in.RenderID) {
		fail(w, 422, errors.New("需明确确认已检查成片，并提供renderId"))
		return
	}
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	if p.Revision != in.Revision {
		fail(w, 409, errors.New("项目版本已变化"))
		return
	}
	var found *Render
	for _, j := range p.Renders {
		if j.ID == in.RenderID {
			copy := j
			found = &copy
			break
		}
	}
	if found == nil || found.State != "completed" || (found.Engine != "ffmpeg_edit" && found.Engine != "video_use") || found.Range != nil || !renderCurrent(p, *found) {
		fail(w, 409, errors.New("需要当前完整剪辑的已完成渲染，局部预览不能作为终稿"))
		return
	}
	root, e := os.OpenRoot(filepath.Join(a.dir(p.ID), "renders", found.ID))
	if e != nil {
		fail(w, 409, e)
		return
	}
	defer root.Close()
	video, e := root.Open("final.mp4")
	if e != nil {
		fail(w, 409, e)
		return
	}
	vh, size, e := hashReader(video)
	video.Close()
	if e != nil || size == 0 {
		fail(w, 409, errors.New("无法校验成片"))
		return
	}
	snap, e := root.Open("snapshot.json")
	if e != nil {
		fail(w, 409, e)
		return
	}
	sh, _, e := hashReader(snap)
	snap.Seek(0, 0)
	var original Project
	decodeErr := json.NewDecoder(snap).Decode(&original)
	snap.Close()
	snapshotCurrent := productionHash(original) == productionHash(p)
	if found.Engine == "video_use" {
		snapshotCurrent = externalProductionBasis(original) == externalProductionBasis(p)
	}
	if e != nil || decodeErr != nil || !snapshotCurrent || original.NarrationText != p.NarrationText {
		fail(w, 409, errors.New("渲染快照与当前内容不一致"))
		return
	}
	proof, err := readRenderProof(filepath.Join(a.dir(p.ID), "renders", found.ID))
	if err != nil || proof.VideoHash != vh || proof.VideoBytes != size || proof.SnapshotHash != sh {
		fail(w, 409, errors.New("渲染校验记录缺失或产物已改变，请重新渲染"))
		return
	}
	if found.Engine == "video_use" {
		if !a.externalRenderInputsIntact(p, *found) || !a.externalArchiveIntact(p, *found, proof) {
			fail(w, 409, errors.New("外部剪辑的素材或归档依据已改变，请重新制作"))
			return
		}
	} else {
		if !usesEditDSL(p) {
			fail(w, 409, errors.New("当前原生剪辑未启用"))
			return
		}
		ir, err := compileProjectEdit(p, *p.EditDSL)
		if err != nil {
			fail(w, 409, err)
			return
		}
		key, err := renderInputKey(r.Context(), filepath.Join(a.dir(p.ID), "workspace", "assets"), ir, p.Assets, a.font, nil)
		if err != nil || key != proof.Key {
			fail(w, 409, errors.New("素材、字体或渲染环境已改变，请重新渲染"))
			return
		}
	}
	record := FinalSnapshot{ID: uid(), RenderID: found.ID, Created: time.Now().UTC().Format(time.RFC3339Nano), Revision: p.Revision, ContentHash: found.ContentHash, NarrationHash: hashBytes([]byte(p.NarrationText)), VideoHash: vh, SnapshotHash: sh, VideoBytes: size, Reviewed: true}
	a.mu.Lock()
	defer a.mu.Unlock()
	latest := a.projects[p.ID]
	if latest == nil || latest.Revision != p.Revision {
		fail(w, 409, errors.New("项目已改变"))
		return
	}
	dir := filepath.Join(a.dir(p.ID), "finals")
	if e = os.MkdirAll(dir, 0700); e != nil {
		fail(w, 500, e)
		return
	}
	file := filepath.Join(dir, record.ID+".json")
	if e = atomicJSON(file, record); e != nil {
		fail(w, 500, e)
		return
	}
	q := clone(latest)
	q.Final = &record
	q.Revision++
	if e = a.save(&q); e != nil {
		os.Remove(file)
		fail(w, 500, e)
		return
	}
	a.projects[q.ID] = &q
	writeJSON(w, 200, map[string]any{"revision": q.Revision, "final": record, "current": true})
}

func (v *RenderRange) UnmarshalJSON(raw []byte) error {
	type plain RenderRange
	var p plain
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(&p); e != nil {
		return e
	}
	*v = RenderRange(p)
	return nil
}
