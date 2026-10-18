package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type RenderProof struct {
	Schema       string `json:"schema"`
	Key          string `json:"key"`
	VideoHash    string `json:"videoHash"`
	VideoBytes   int64  `json:"videoBytes"`
	SnapshotHash string `json:"snapshotHash"`
	ReusedFrom   string `json:"reusedFrom,omitempty"`
}

func hashLocal(path string) (string, int64, error) {
	root, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return "", 0, e
	}
	defer root.Close()
	f, e := root.Open(filepath.Base(path))
	if e != nil {
		return "", 0, e
	}
	defer f.Close()
	return hashReader(f)
}
func renderInputKey(ctx context.Context, dir string, ir EditIR, assets []Asset, font string, span *RenderRange) (string, error) {
	hashes := map[string]string{}
	names := map[string]string{}
	for _, a := range assets {
		names[a.ID] = a.File
	}
	for _, c := range ir.Clips {
		if c.Asset == "" || hashes[c.Asset] != "" {
			continue
		}
		name := names[c.Asset]
		if name == "" || filepath.Base(name) != name {
			return "", fmt.Errorf("素材路径无效")
		}
		h, _, e := hashLocal(filepath.Join(dir, name))
		if e != nil {
			return "", e
		}
		hashes[c.Asset] = h
	}
	fontHash := ""
	if ir.HasText {
		h, _, e := hashLocal(font)
		if e != nil {
			return "", e
		}
		fontHash = h
	}
	step, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(step, "ffmpeg", "-version")
	cmd.Env = rendererEnvironment()
	b, e := cmd.Output()
	if e != nil {
		return "", fmt.Errorf("无法识别FFmpeg版本")
	}
	raw, _ := json.Marshal(map[string]any{"schema": "video.render-input.v1", "appVersion": version, "ir": ir, "range": span, "assets": hashes, "font": fontHash, "ffmpeg": hashBytes(b), "encoding": "libx264/veryfast/crf23/threads2/aac48k"})
	return hashBytes(raw), nil
}
func readRenderProof(dir string) (RenderProof, error) {
	var v RenderProof
	root, e := os.OpenRoot(dir)
	if e != nil {
		return v, e
	}
	defer root.Close()
	f, e := root.Open("render-proof.json")
	if e != nil {
		return v, e
	}
	defer f.Close()
	e = json.NewDecoder(io.LimitReader(f, 16384)).Decode(&v)
	if e == nil && (v.Schema != "video.render-proof.v1" || v.Key == "" || v.VideoHash == "" || v.VideoBytes <= 0 || v.SnapshotHash == "") {
		e = fmt.Errorf("渲染校验记录无效")
	}
	return v, e
}
func (a *App) reuseRender(ctx context.Context, p Project, job Render, key, dir string) (string, error) {
	for i := len(p.Renders) - 1; i >= 0; i-- {
		old := p.Renders[i]
		if old.ID == job.ID || old.State != "completed" || old.Engine != "ffmpeg_edit" || old.CacheKey != key {
			continue
		}
		oldDir := filepath.Join(a.dir(p.ID), "renders", old.ID)
		proof, e := readRenderProof(oldDir)
		if e != nil || proof.Key != key {
			continue
		}
		root, e := os.OpenRoot(oldDir)
		if e != nil {
			continue
		}
		f, e := root.Open("final.mp4")
		root.Close()
		if e != nil {
			continue
		}
		h, n, e := hashReader(f)
		if e != nil || h != proof.VideoHash || n != proof.VideoBytes {
			f.Close()
			continue
		}
		f.Seek(0, 0)
		out, e := os.OpenFile(filepath.Join(dir, "final.mp4"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			f.Close()
			return "", e
		}
		_, e = io.Copy(out, f)
		f.Close()
		ce := out.Close()
		if e == nil {
			e = ce
		}
		return old.ID, e
	}
	return "", nil
}
func (a *App) saveRenderProof(pid, rid, dir, key, reused string) error {
	h, n, e := hashLocal(filepath.Join(dir, "final.mp4"))
	if e != nil {
		return e
	}
	s, _, e := hashLocal(filepath.Join(dir, "snapshot.json"))
	if e != nil {
		return e
	}
	proof := RenderProof{Schema: "video.render-proof.v1", Key: key, VideoHash: h, VideoBytes: n, SnapshotHash: s, ReusedFrom: reused}
	if e = atomicJSON(filepath.Join(dir, "render-proof.json"), proof); e != nil {
		return e
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[pid]
	if p == nil {
		return fmt.Errorf("项目不存在")
	}
	q := clone(p)
	for i := range q.Renders {
		if q.Renders[i].ID == rid {
			q.Renders[i].CacheKey = key
			q.Renders[i].ReusedFrom = reused
		}
	}
	if e = a.save(&q); e == nil {
		a.projects[pid] = &q
	}
	return e
}
func (a *App) verifyFinal(w http.ResponseWriter, r *http.Request) {
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	if p.Final == nil {
		fail(w, 404, fmt.Errorf("尚无终稿记录"))
		return
	}
	f := p.Final
	for _, render := range p.Renders {
		if render.ID != f.RenderID || render.Engine != authoredHyperFramesEngine {
			continue
		}
		dir := filepath.Join(a.dir(p.ID), "renders", render.ID)
		proof, pe := readRenderProof(dir)
		vh, n, ve := hashLocal(filepath.Join(dir, "final.mp4"))
		sh, _, se := hashLocal(filepath.Join(dir, "snapshot.json"))
		inputsCurrent := finalCurrent(p) && a.hyperFramesRenderInputsIntact(p, render)
		outputIntact := pe == nil && ve == nil && se == nil && vh == f.VideoHash && n == f.VideoBytes && sh == f.SnapshotHash && a.hyperFramesArchiveIntact(p, render, proof)
		writeJSON(w, 200, map[string]any{"revision": p.Revision, "final": f, "inputsCurrent": inputsCurrent, "outputIntact": outputIntact, "verified": inputsCurrent && outputIntact})
		return
	}
	for _, render := range p.Renders {
		if render.ID != f.RenderID || render.Engine != "video_use" {
			continue
		}
		dir := filepath.Join(a.dir(p.ID), "renders", render.ID)
		vh, n, ve := hashLocal(filepath.Join(dir, "final.mp4"))
		sh, _, se := hashLocal(filepath.Join(dir, "snapshot.json"))
		proof, pe := readRenderProof(dir)
		inputsCurrent := finalCurrent(p) && a.externalRenderInputsIntact(p, render)
		outputIntact := ve == nil && se == nil && pe == nil && vh == f.VideoHash && n == f.VideoBytes && sh == f.SnapshotHash && a.externalArchiveIntact(p, render, proof)
		writeJSON(w, 200, map[string]any{"revision": p.Revision, "final": f, "inputsCurrent": inputsCurrent, "outputIntact": outputIntact, "verified": inputsCurrent && outputIntact})
		return
	}
	dir := filepath.Join(a.dir(p.ID), "renders", f.RenderID)
	vh, n, ve := hashLocal(filepath.Join(dir, "final.mp4"))
	sh, _, se := hashLocal(filepath.Join(dir, "snapshot.json"))
	ir := EditIR{}
	ce := fmt.Errorf("没有DSL")
	if p.EditDSL != nil {
		ir, ce = compileProjectEdit(p, *p.EditDSL)
	}
	key := ""
	if ce == nil {
		key, ce = renderInputKey(r.Context(), filepath.Join(a.dir(p.ID), "workspace", "assets"), ir, p.Assets, a.font, nil)
	}
	proof, pe := readRenderProof(dir)
	inputCurrent := ce == nil && pe == nil && key == proof.Key && usesEditDSL(p) && productionHash(p) == f.ContentHash && hashBytes([]byte(p.NarrationText)) == f.NarrationHash
	outputValid := ve == nil && se == nil && vh == f.VideoHash && n == f.VideoBytes && sh == f.SnapshotHash
	writeJSON(w, 200, map[string]any{"revision": p.Revision, "final": f, "inputsCurrent": inputCurrent, "outputIntact": outputValid, "verified": inputCurrent && outputValid})
}
