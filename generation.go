package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type GenerationJob struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	RequestID   string `json:"requestId"`
	RequestHash string `json:"requestHash"`
	Revision    int    `json:"revision"`
	State       string `json:"state"`
	Created     string `json:"created"`
	Prompt      string `json:"prompt"`
	Model       string `json:"model"`
	AssetID     string `json:"assetId,omitempty"`
	SHA256      string `json:"sha256,omitempty"`
	Size        int64  `json:"size,omitempty"`
	Error       string `json:"error,omitempty"`
}
type GenerationTarget struct{ URL, Model, Key string }

func generationTarget(kind string) (GenerationTarget, error) {
	prefix := "VIDEO_IMAGE_GEN_"
	if kind == "video_generation" {
		prefix = "VIDEO_VIDEO_GEN_"
	}
	v := GenerationTarget{os.Getenv(prefix + "URL"), os.Getenv(prefix + "MODEL"), os.Getenv(prefix + "KEY")}
	u, e := url.Parse(v.URL)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || strings.TrimSpace(v.Model) == "" || len(v.Model) > 200 || len(v.Key) > 4096 || strings.ContainsAny(v.Key, "\r\n") {
		return v, errors.New("未配置合法的生成服务 URL/MODEL/KEY")
	}
	return v, nil
}
func generationView(pid string, j GenerationJob) ProductionJob {
	v := ProductionJob{ID: "generation:" + j.ID, NativeID: j.ID, ProjectID: pid, Type: j.Type, Provider: "http_binary_v1", State: unifiedState(j.State), NativeState: j.State, Revision: j.Revision, RequestID: j.RequestID, Created: j.Created, Error: j.Error, Artifacts: []JobArtifact{}}
	if j.State == "completed" {
		v.Artifacts = append(v.Artifacts, JobArtifact{Name: j.AssetID, URL: "/api/projects/" + pid + "/assets/" + j.AssetID, SHA256: j.SHA256, Size: j.Size})
	}
	return v
}
func (a *App) startGeneration(w http.ResponseWriter, r *http.Request, in JobStartRequest) {
	raw, _ := json.Marshal(in)
	hash := hashBytes(raw)
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, errors.New("项目不存在"))
		return
	}
	for _, j := range p.Generation {
		if j.RequestID == in.RequestID {
			if j.RequestHash != hash {
				fail(w, 409, errors.New("同一requestId不能改变生成请求"))
				return
			}
			writeJSON(w, 200, generationView(p.ID, j))
			return
		}
	}
	target, e := generationTarget(in.Type)
	if e != nil {
		fail(w, 409, e)
		return
	}
	if p.Revision != in.Revision {
		fail(w, 409, errors.New("项目版本冲突"))
		return
	}
	if a.closing || len(a.generationJobs) >= 2 || len(p.Generation) >= 100 {
		fail(w, 409, errors.New("生成任务容量已满或服务正在停止"))
		return
	}
	if in.Options.Source != "" || in.Options.Backend != "" || in.Options.Voice != "" || in.Options.Language != "" || in.Options.Speed != 0 || in.Options.Rate != 0 || in.Options.Kind != "" || strings.TrimSpace(in.Options.Prompt) == "" || len(in.Options.Prompt) > 16000 || strings.ContainsRune(in.Options.Prompt, 0) {
		fail(w, 400, errors.New("生成任务仅接受非空 prompt，最多16000字节"))
		return
	}
	j := GenerationJob{ID: uid(), Type: in.Type, RequestID: in.RequestID, RequestHash: hash, Revision: p.Revision, State: "running", Created: time.Now().UTC().Format(time.RFC3339Nano), Prompt: in.Options.Prompt, Model: target.Model}
	q := clone(p)
	q.Generation = append(q.Generation, j)
	if e = a.save(&q); e != nil {
		fail(w, 500, e)
		return
	}
	a.projects[q.ID] = &q
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	a.generationJobs[j.ID] = cancel
	a.wg.Add(1)
	go func() { defer a.wg.Done(); defer cancel(); a.runGeneration(ctx, q.ID, j, target) }()
	writeJSON(w, 202, generationView(q.ID, j))
}
func (a *App) runGeneration(ctx context.Context, pid string, j GenerationJob, target GenerationTarget) {
	data, e := fetchGenerated(ctx, j, target)
	var asset Asset
	dest := ""
	if e == nil {
		ext, mime, err := validateGenerated(ctx, a, pid, j, data)
		e = err
		if e == nil {
			asset = Asset{ID: j.ID, File: j.ID + ext, Name: "生成素材" + ext, MIME: mime, Size: int64(len(data))}
			dest = filepath.Join(a.dir(pid), "workspace", "assets", asset.File)
			e = os.WriteFile(dest, data, 0600)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	defer delete(a.generationJobs, j.ID)
	p := a.projects[pid]
	if p == nil {
		if dest != "" {
			os.Remove(dest)
		}
		return
	}
	q := clone(p)
	committed := false
	defer func() {
		if !committed && dest != "" {
			os.Remove(dest)
		}
	}()
	for i := range q.Generation {
		g := &q.Generation[i]
		if g.ID != j.ID {
			continue
		}
		if ctx.Err() != nil || g.State == "stopping" {
			g.State = "interrupted"
			g.Error = "已取消或超时；远端可能仍在执行，不会自动重试"
		} else if e != nil {
			g.State = "failed"
			g.Error = e.Error()
		} else {
			g.State = "completed"
			g.AssetID = asset.ID
			g.SHA256 = hashBytes(data)
			g.Size = asset.Size
			q.Assets = append(q.Assets, asset)
			q.Revision++
			committed = true
		}
		if err := a.save(&q); err == nil {
			a.projects[pid] = &q
		} else {
			committed = false
		}
		return
	}
}
func fetchGenerated(ctx context.Context, j GenerationJob, target GenerationTarget) ([]byte, error) {
	raw, _ := json.Marshal(map[string]any{"schema": "video.generation-request.v1", "requestId": j.RequestID, "type": j.Type, "model": target.Model, "prompt": j.Prompt})
	r, e := http.NewRequestWithContext(ctx, "POST", target.URL, bytes.NewReader(raw))
	if e != nil {
		return nil, errors.New("生成请求配置无效")
	}
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Idempotency-Key", j.RequestID)
	if target.Key != "" {
		r.Header.Set("Authorization", "Bearer "+target.Key)
	}
	client := &http.Client{Timeout: 15 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, e := client.Do(r)
	if e != nil {
		return nil, errors.New("生成服务调用失败或超时")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, fmt.Errorf("生成服务返回 HTTP %d", response.StatusCode)
	}
	limit := int64(32 << 20)
	if j.Type == "video_generation" {
		limit = 128 << 20
	}
	data, e := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if e != nil || int64(len(data)) > limit || len(data) == 0 {
		return nil, errors.New("生成产物为空、过大或读取失败")
	}
	return data, nil
}
func validateGenerated(ctx context.Context, a *App, pid string, j GenerationJob, data []byte) (string, string, error) {
	if j.Type == "image_generation" {
		c, format, e := image.DecodeConfig(bytes.NewReader(data))
		if e != nil || (format != "png" && format != "jpeg") || c.Width < 1 || c.Height < 1 || c.Width > 8192 || c.Height > 8192 || int64(c.Width)*int64(c.Height) > 32000000 {
			return "", "", errors.New("生成图片必须为有效PNG/JPEG，最多3200万像素")
		}
		if _, _, e = image.Decode(bytes.NewReader(data)); e != nil {
			return "", "", errors.New("生成图片解码失败")
		}
		if format == "jpeg" {
			return ".jpg", "image/jpeg", nil
		}
		return ".png", "image/png", nil
	}
	if len(data) < 12 || string(data[4:8]) != "ftyp" {
		return "", "", errors.New("生成视频必须为MP4")
	}
	f, e := os.CreateTemp(a.dir(pid), "generation-*.mp4")
	if e != nil {
		return "", "", errors.New("无法验证生成视频")
	}
	name := f.Name()
	defer os.Remove(name)
	_, e = f.Write(data)
	f.Close()
	if e != nil {
		return "", "", errors.New("无法写入验证视频")
	}
	duration, e := editMediaDuration(ctx, name, "v:0")
	if e != nil || duration <= 0 || duration > 600 {
		return "", "", errors.New("生成视频无有效视频流或超过600秒")
	}
	return ".mp4", "video/mp4", nil
}
func (a *App) cancelGeneration(w http.ResponseWriter, r *http.Request, id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, errors.New("项目不存在"))
		return
	}
	q := clone(p)
	for i := range q.Generation {
		j := &q.Generation[i]
		if j.ID != id {
			continue
		}
		if j.State == "running" {
			j.State = "stopping"
			if e := a.save(&q); e != nil {
				fail(w, 500, e)
				return
			}
			a.projects[q.ID] = &q
			if c := a.generationJobs[id]; c != nil {
				c()
			}
		}
		writeJSON(w, 200, generationView(q.ID, *j))
		return
	}
	fail(w, 404, errors.New("生成任务不存在"))
}
