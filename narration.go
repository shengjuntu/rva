package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// These units are a private synthesis projection, never an editorial storyboard.
func narrationShots(text string) []Shot {
	var out []Shot
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		for _, chunk := range speechChunks(line) {
			out = append(out, Shot{ID: fmt.Sprintf("n%d", len(out)+1), Text: chunk, Duration: 1})
		}
	}
	return out
}
func (a *App) saveNarration(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int    `json:"revision"`
		Text     string `json:"text"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !utf8.ValidString(in.Text) || strings.ContainsRune(in.Text, 0) || len(in.Text) > 60000 || len(narrationShots(in.Text)) > 300 {
		fail(w, 422, errors.New("口播正文无效或超过 300 个合成片段"))
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, errors.New("项目不存在"))
		return
	}
	if p.Revision != in.Revision {
		fail(w, 409, errors.New("项目版本冲突"))
		return
	}
	q := clone(p)
	q.NarrationText = in.Text
	q.Revision++
	if e := a.save(&q); e != nil {
		fail(w, 500, e)
		return
	}
	a.projects[q.ID] = &q
	writeJSON(w, 200, q)
}
func (a *App) materializeSpeech(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision int `json:"revision"`
	}
	if !decode(w, r, &in) {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, errors.New("项目不存在"))
		return
	}
	if p.Revision != in.Revision {
		fail(w, 409, errors.New("项目版本冲突"))
		return
	}
	j, e := findSpeech(*p, r.PathValue("sid"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	projection := clone(p)
	projection.Shots = narrationShots(p.NarrationText)
	if j.Source != "narration" || j.State != "completed" || j.ScriptHash != speechScriptHash(projection) {
		fail(w, 409, errors.New("独立口播未完成或正文已经改变"))
		return
	}
	f, e := a.readSpeechArtifact(*p, j, "manifest.json")
	if e != nil {
		fail(w, 409, e)
		return
	}
	var m SpeechManifest
	e = json.NewDecoder(io.LimitReader(f, 1<<20)).Decode(&m)
	f.Close()
	if e != nil || m.JobID != j.ID || m.ScriptHash != j.ScriptHash || len(m.Shots) != len(projection.Shots) || validateCues(m.Cues, m.Duration) != nil {
		fail(w, 409, errors.New("配音清单无效"))
		return
	}
	anchors := []EditAnchor{}
	t := 0.0
	for i, s := range m.Shots {
		if s.ID != projection.Shots[i].ID || !finite(s.Duration) || s.Duration < 1 || s.Duration > 60 {
			fail(w, 409, errors.New("合成单元时序无效"))
			return
		}
		start, end := t, t+s.Duration
		anchors = append(anchors, EditAnchor{ID: s.ID + ".start", Time: &start}, EditAnchor{ID: s.ID + ".end", Time: &end})
		t = end
	}
	if !finite(m.Duration) || t < 0 || t > 600 || t-m.Duration > 0.001 || m.Duration-t > 0.001 {
		fail(w, 409, errors.New("时长不一致"))
		return
	}
	q := clone(p)
	asset := Asset{ID: j.ID, File: j.ID + ".wav", Name: "独立口播.wav", MIME: "audio/wav", Size: j.Files["narration.wav"].Size}
	found := false
	for _, x := range q.Assets {
		if x.ID == asset.ID {
			found = true
			asset = x
		}
	}
	if !found {
		input, err := a.readSpeechArtifact(*p, j, "narration.wav")
		if err != nil {
			fail(w, 409, err)
			return
		}
		defer input.Close()
		dest := filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File)
		out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			fail(w, 409, err)
			return
		}
		_, err = io.Copy(out, input)
		ce := out.Close()
		if err == nil {
			err = ce
		}
		if err != nil {
			os.Remove(dest)
			fail(w, 500, err)
			return
		}
		q.Assets = append(q.Assets, asset)
		q.Revision++
		if err = a.save(&q); err != nil {
			os.Remove(dest)
			fail(w, 500, err)
			return
		}
		a.projects[q.ID] = &q
	}
	writeJSON(w, 200, map[string]any{"revision": q.Revision, "asset": asset, "scriptHash": j.ScriptHash, "duration": m.Duration, "cues": m.Cues, "units": m.Shots, "anchors": anchors, "alignment": "synthesis_chunk_boundaries", "reviewed": false})
}
