package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
)

// Job views are projections of durable native records, never a second state store.
type JobArtifact struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256,omitempty"`
	Size   int64  `json:"size,omitempty"`
}
type ProductionJob struct {
	ID             string        `json:"jobId"`
	NativeID       string        `json:"nativeId"`
	ProjectID      string        `json:"projectId"`
	Type           string        `json:"type"`
	Provider       string        `json:"provider"`
	State          string        `json:"state"`
	NativeState    string        `json:"nativeState"`
	Revision       int           `json:"revision"`
	RequestID      string        `json:"requestId,omitempty"`
	Created        string        `json:"created"`
	CompletedUnits int           `json:"completedUnits"`
	TotalUnits     int           `json:"totalUnits"`
	ProgressKnown  bool          `json:"progressKnown"`
	Error          string        `json:"error,omitempty"`
	Artifacts      []JobArtifact `json:"artifacts"`
}
type JobOptions struct {
	Source   string  `json:"source,omitempty"`
	Backend  string  `json:"backend,omitempty"`
	Voice    string  `json:"voice,omitempty"`
	Language string  `json:"language,omitempty"`
	Speed    float64 `json:"speed,omitempty"`
	Rate     int     `json:"rate,omitempty"`
	Kind     string  `json:"kind,omitempty"`
}

func (o *JobOptions) UnmarshalJSON(raw []byte) error {
	type plain JobOptions
	var value plain
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if e := decoder.Decode(&value); e != nil {
		return e
	}
	*o = JobOptions(value)
	return nil
}

type JobStartRequest struct {
	Type      string     `json:"type"`
	Revision  int        `json:"revision"`
	RequestID string     `json:"requestId"`
	Options   JobOptions `json:"options,omitempty"`
}

func unifiedState(s string) string {
	switch s {
	case "running":
		return "running"
	case "stopping":
		return "cancel_requested"
	case "completed":
		return "succeeded"
	case "failed":
		return "failed"
	case "interrupted":
		return "interrupted"
	default:
		return "unknown"
	}
}
func speechJobView(pid string, j SpeechJob) ProductionJob {
	v := ProductionJob{ID: "tts:" + j.ID, NativeID: j.ID, ProjectID: pid, Type: "tts", Provider: speechBackend(j.Backend), State: unifiedState(j.State), NativeState: j.State, Revision: j.Revision, RequestID: j.RequestID, Created: j.Created, CompletedUnits: j.Progress, TotalUnits: j.Total, ProgressKnown: j.Total > 0, Error: j.Error, Artifacts: []JobArtifact{}}
	if j.State == "completed" {
		for name, f := range j.Files {
			v.Artifacts = append(v.Artifacts, JobArtifact{Name: name, URL: "/api/projects/" + pid + "/speech/" + j.ID + "/files/" + name, SHA256: f.SHA256, Size: f.Size})
		}
		sort.Slice(v.Artifacts, func(i, k int) bool { return v.Artifacts[i].Name < v.Artifacts[k].Name })
	}
	return v
}
func renderProvider(j Render) string {
	if j.Engine == "ffmpeg_edit" {
		return "ffmpeg_edit"
	}
	return "hyperframes"
}
func renderJobView(pid string, j Render) ProductionJob {
	v := ProductionJob{ID: "render:" + j.ID, NativeID: j.ID, ProjectID: pid, Type: "render", Provider: renderProvider(j), State: unifiedState(j.State), NativeState: j.State, Revision: j.Revision, RequestID: j.RequestID, Created: j.Created, Error: j.Error, Artifacts: []JobArtifact{}}
	if j.State == "completed" {
		v.Artifacts = append(v.Artifacts, JobArtifact{Name: "final.mp4", URL: "/api/projects/" + pid + "/renders/" + j.ID + "/final.mp4"}, JobArtifact{Name: "snapshot.json", URL: "/api/projects/" + pid + "/renders/" + j.ID + "/snapshot.json"}, JobArtifact{Name: "validation.json", URL: "/api/projects/" + pid + "/renders/" + j.ID + "/validation.json"})
	}
	if j.State == "completed" && j.Engine == "ffmpeg_edit" {
		v.Artifacts = append(v.Artifacts, JobArtifact{Name: "edit-ir.json", URL: "/api/projects/" + pid + "/renders/" + j.ID + "/edit-ir.json"})
	}
	return v
}
func projectJobs(p Project) []ProductionJob {
	jobs := []ProductionJob{}
	for _, j := range p.Speech {
		jobs = append(jobs, speechJobView(p.ID, j))
	}
	for _, j := range p.Renders {
		jobs = append(jobs, renderJobView(p.ID, j))
	}
	sort.Slice(jobs, func(i, k int) bool {
		if jobs[i].Created == jobs[k].Created {
			return jobs[i].ID < jobs[k].ID
		}
		return jobs[i].Created > jobs[k].Created
	})
	return jobs
}
func findJob(p Project, id string) (ProductionJob, error) {
	for _, j := range projectJobs(p) {
		if j.ID == id {
			return j, nil
		}
	}
	return ProductionJob{}, errors.New("制作任务不存在")
}
func validJobID(id string) bool {
	kind, native, ok := strings.Cut(id, ":")
	return ok && (kind == "tts" || kind == "render") && identifier.MatchString(native)
}
func decodeStrict(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		fail(w, 400, e)
		return false
	}
	if e := d.Decode(new(any)); e != io.EOF {
		fail(w, 400, errors.New("请求必须只有一个JSON对象"))
		return false
	}
	return true
}
func (a *App) jobsList(w http.ResponseWriter, r *http.Request) {
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	writeJSON(w, 200, map[string]any{"schema": "video.jobs.v1", "jobs": projectJobs(p)})
}
func (a *App) jobGet(w http.ResponseWriter, r *http.Request) {
	if !validJobID(r.PathValue("jid")) {
		fail(w, 400, errors.New("jobId格式无效"))
		return
	}
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	j, e := findJob(p, r.PathValue("jid"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	writeJSON(w, 200, j)
}
func (a *App) jobStart(w http.ResponseWriter, r *http.Request) {
	var in JobStartRequest
	if !decodeStrict(w, r, &in) {
		return
	}
	if !identifier.MatchString(in.RequestID) || in.Revision < 1 {
		fail(w, 400, errors.New("需要当前revision和24位十六进制requestId"))
		return
	}
	var native any
	switch in.Type {
	case "tts":
		if in.Options.Kind != "" {
			fail(w, 400, errors.New("TTS不接受渲染kind"))
			return
		}
		native = SpeechRequest{Source: in.Options.Source, Revision: in.Revision, RequestID: in.RequestID, Backend: in.Options.Backend, Voice: in.Options.Voice, Language: in.Options.Language, Speed: in.Options.Speed, Rate: in.Options.Rate}
	case "render":
		if in.Options.Source != "" || in.Options.Backend != "" || in.Options.Voice != "" || in.Options.Language != "" || in.Options.Speed != 0 || in.Options.Rate != 0 {
			fail(w, 400, errors.New("渲染仅接受kind"))
			return
		}
		native = RenderRequest{Revision: in.Revision, RequestID: in.RequestID, Kind: in.Options.Kind}
	default:
		fail(w, 501, fmt.Errorf("任务能力 %q 尚未实现；当前支持tts和render", in.Type))
		return
	}
	raw, _ := json.Marshal(native)
	req := r.Clone(r.Context())
	req.Body = io.NopCloser(bytes.NewReader(raw))
	response := &mcpResponse{header: http.Header{}}
	if in.Type == "tts" {
		a.startSpeech(response, req)
	} else {
		a.startRender(response, req)
	}
	if response.status >= 300 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.status)
		w.Write(response.body.Bytes())
		return
	}
	var view ProductionJob
	if in.Type == "tts" {
		var j SpeechJob
		if e := json.Unmarshal(response.body.Bytes(), &j); e != nil {
			fail(w, 500, e)
			return
		}
		view = speechJobView(r.PathValue("id"), j)
	} else {
		var j Render
		if e := json.Unmarshal(response.body.Bytes(), &j); e != nil {
			fail(w, 500, e)
			return
		}
		view = renderJobView(r.PathValue("id"), j)
	}
	writeJSON(w, response.status, view)
}
func jobTerminal(s string) bool { return s == "succeeded" || s == "failed" || s == "interrupted" }
func (a *App) jobCancel(w http.ResponseWriter, r *http.Request) {
	jid := r.PathValue("jid")
	if !validJobID(jid) {
		fail(w, 400, errors.New("jobId格式无效"))
		return
	}
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	j, e := findJob(p, jid)
	if e != nil {
		fail(w, 404, e)
		return
	}
	if jobTerminal(j.State) || j.State == "cancel_requested" {
		writeJSON(w, 200, j)
		return
	}
	req := r.Clone(r.Context())
	response := &mcpResponse{header: http.Header{}}
	if j.Type == "tts" {
		req.SetPathValue("sid", j.NativeID)
		a.stopSpeech(response, req)
	} else {
		req.SetPathValue("rid", j.NativeID)
		a.stopRender(response, req)
	}
	// Cancellation may race with completion. Return the authoritative terminal result.
	p, e = a.get(p.ID)
	if e == nil {
		j, e = findJob(p, jid)
	}
	if e == nil && (response.status < 300 || jobTerminal(j.State) || j.State == "cancel_requested") {
		writeJSON(w, 200, j)
		return
	}
	if response.status >= 300 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.status)
		w.Write(response.body.Bytes())
		return
	}
	fail(w, 500, e)
}
