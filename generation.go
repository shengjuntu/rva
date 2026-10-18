package main

import (
	"bytes"
	"context"
	"encoding/base64"
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
	ID          string                `json:"id"`
	Type        string                `json:"type"`
	RequestID   string                `json:"requestId"`
	RequestHash string                `json:"requestHash"`
	Revision    int                   `json:"revision"`
	State       string                `json:"state"`
	Created     string                `json:"created"`
	Prompt      string                `json:"prompt"`
	Model       string                `json:"model"`
	AssetID     string                `json:"assetId,omitempty"`
	SHA256      string                `json:"sha256,omitempty"`
	Size        int64                 `json:"size,omitempty"`
	Error       string                `json:"error,omitempty"`
	Duration    *float64              `json:"duration,omitempty"`
	AspectRatio string                `json:"aspectRatio,omitempty"`
	References  []GenerationReference `json:"references,omitempty"`
}

// References retain evidence of exactly which bytes were sent to the adapter.
// Image payloads and provider credentials are never persisted in project JSON.
type GenerationReference struct {
	AssetID string `json:"assetId"`
	MIME    string `json:"mimeType"`
	SHA256  string `json:"sha256"`
	Size    int64  `json:"size"`
}
type GeneratedAssetInfo struct {
	JobID       string                `json:"jobId"`
	RequestID   string                `json:"requestId"`
	Type        string                `json:"type"`
	Provider    string                `json:"provider"`
	Model       string                `json:"model"`
	Prompt      string                `json:"prompt"`
	Duration    *float64              `json:"duration,omitempty"`
	AspectRatio string                `json:"aspectRatio,omitempty"`
	References  []GenerationReference `json:"references,omitempty"`
	Created     string                `json:"created"`
}

type generationReferenceImage struct {
	GenerationReference
	Data string `json:"data"`
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
	if in.Options.Range != nil || in.Options.Source != "" || in.Options.Backend != "" || in.Options.Voice != "" || in.Options.Language != "" || in.Options.Speed != 0 || in.Options.Rate != 0 || in.Options.Kind != "" || strings.TrimSpace(in.Options.Prompt) == "" || len(in.Options.Prompt) > 16000 || strings.ContainsRune(in.Options.Prompt, 0) {
		fail(w, 400, errors.New("生成任务接受prompt、duration、aspectRatio、referenceAssetIds；prompt最多16000字节"))
		return
	}
	if in.Options.Duration != nil && (in.Type != "video_generation" || !finite(*in.Options.Duration) || *in.Options.Duration <= 0 || *in.Options.Duration > 60) {
		fail(w, 400, errors.New("duration仅用于视频生成，必须大于0且不超过60秒；这是请求目标，实际时长以产物为准"))
		return
	}
	if in.Options.AspectRatio != "" && in.Options.AspectRatio != "16:9" && in.Options.AspectRatio != "9:16" && in.Options.AspectRatio != "1:1" && in.Options.AspectRatio != "4:3" && in.Options.AspectRatio != "3:4" {
		fail(w, 400, errors.New("aspectRatio支持16:9、9:16、1:1、4:3、3:4"))
		return
	}
	refs, e := a.generationReferences(*p, in.Options.ReferenceAssetIDs)
	if e != nil {
		fail(w, 400, e)
		return
	}
	j := GenerationJob{ID: uid(), Type: in.Type, RequestID: in.RequestID, RequestHash: hash, Revision: p.Revision, State: "running", Created: time.Now().UTC().Format(time.RFC3339Nano), Prompt: in.Options.Prompt, Model: target.Model, Duration: in.Options.Duration, AspectRatio: in.Options.AspectRatio}
	for _, ref := range refs {
		j.References = append(j.References, ref.GenerationReference)
	}
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
	go func() { defer a.wg.Done(); defer cancel(); a.runGeneration(ctx, q.ID, j, target, refs) }()
	writeJSON(w, 202, generationView(q.ID, j))
}
func (a *App) runGeneration(ctx context.Context, pid string, j GenerationJob, target GenerationTarget, references []generationReferenceImage) {
	data, e := fetchGenerated(ctx, j, target, references)
	var asset Asset
	dest := ""
	if e == nil {
		ext, mime, err := validateGenerated(ctx, a, pid, j, data)
		e = err
		if e == nil {
			asset = Asset{ID: j.ID, File: j.ID + ext, Name: "生成素材" + ext, MIME: mime, Size: int64(len(data)), Generated: &GeneratedAssetInfo{JobID: j.ID, RequestID: j.RequestID, Type: j.Type, Provider: "http_binary_v1", Model: j.Model, Prompt: j.Prompt, Duration: j.Duration, AspectRatio: j.AspectRatio, References: j.References, Created: j.Created}}
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
func fetchGenerated(ctx context.Context, j GenerationJob, target GenerationTarget, references []generationReferenceImage) ([]byte, error) {
	payload := map[string]any{"schema": "video.generation-request.v1", "requestId": j.RequestID, "type": j.Type, "model": target.Model, "prompt": j.Prompt}
	if j.Duration != nil {
		payload["duration"] = *j.Duration
	}
	if j.AspectRatio != "" {
		payload["aspectRatio"] = j.AspectRatio
	}
	if len(references) > 0 {
		payload["referenceImages"] = references
	}
	raw, _ := json.Marshal(payload)
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

func (a *App) generationReferences(p Project, ids []string) ([]generationReferenceImage, error) {
	if len(ids) > 4 {
		return nil, errors.New("最多允许4张参考图片")
	}
	if len(ids) == 0 {
		return nil, nil
	}
	root, err := os.OpenRoot(filepath.Join(a.dir(p.ID), "workspace", "assets"))
	if err != nil {
		return nil, errors.New("无法读取参考素材目录")
	}
	defer root.Close()
	var refs []generationReferenceImage
	seen := map[string]bool{}
	var total int64
	for _, id := range ids {
		if !identifier.MatchString(id) || seen[id] {
			return nil, errors.New("参考素材ID无效或重复")
		}
		seen[id] = true
		var found *Asset
		for i := range p.Assets {
			if p.Assets[i].ID == id {
				found = &p.Assets[i]
				break
			}
		}
		if found == nil || (found.MIME != "image/png" && found.MIME != "image/jpeg") || found.File == "." || filepath.Base(found.File) != found.File {
			return nil, errors.New("参考素材必须是当前项目中的PNG/JPEG图片")
		}
		f, e := root.Open(found.File)
		if e != nil {
			return nil, errors.New("参考图片不可读取")
		}
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 8<<20 {
			f.Close()
			return nil, errors.New("参考图片必须为普通文件且不超过8MiB")
		}
		data, e := io.ReadAll(io.LimitReader(f, (8<<20)+1))
		f.Close()
		if e != nil || len(data) == 0 || len(data) > 8<<20 {
			return nil, errors.New("参考图片读取失败或超过8MiB")
		}
		total += int64(len(data))
		if total > 16<<20 {
			return nil, errors.New("参考图片总大小不得超过16MiB")
		}
		config, format, e := image.DecodeConfig(bytes.NewReader(data))
		if e != nil || (format != "png" && format != "jpeg") || config.Width < 1 || config.Height < 1 || config.Width > 8192 || config.Height > 8192 || int64(config.Width)*int64(config.Height) > 32000000 {
			return nil, errors.New("参考图片无效或像素过大")
		}
		mime := "image/png"
		if format == "jpeg" {
			mime = "image/jpeg"
		}
		if found.MIME != mime {
			return nil, errors.New("参考图片实际格式与素材记录不一致")
		}
		if _, _, e := image.Decode(bytes.NewReader(data)); e != nil {
			return nil, errors.New("参考图片解码失败")
		}
		refs = append(refs, generationReferenceImage{GenerationReference: GenerationReference{AssetID: id, MIME: mime, SHA256: hashBytes(data), Size: int64(len(data))}, Data: base64.StdEncoding.EncodeToString(data)})
	}
	return refs, nil
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
	duration, e := inspectionMediaDuration(ctx, name, "mov")
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
