package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

const version = "0.19.0"

//go:embed web/*
var ui embed.FS
var requestIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{7,127}$`)
var identifier = regexp.MustCompile(`^[a-f0-9]{24}$`)

func uid() string {
	var b [12]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}

type Shot struct {
	VisualReason string  `json:"visualReason,omitempty"`
	SegmentID    string  `json:"segmentId,omitempty"`
	ID           string  `json:"id"`
	Title        string  `json:"title"`
	Text         string  `json:"text"`
	Duration     float64 `json:"duration"`
	AssetID      string  `json:"assetId"`
}
type Asset struct {
	Description     *AssetDescription `json:"description,omitempty"`
	ImportRequestID string            `json:"importRequestId,omitempty"`
	ImportHash      string            `json:"importHash,omitempty"`
	Source          *NewsImageSource  `json:"source,omitempty"`
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	File            string            `json:"file"`
	Size            int64             `json:"size"`
	MIME            string            `json:"mime"`
}
type Render struct {
	CacheKey     string       `json:"cacheKey,omitempty"`
	ReusedFrom   string       `json:"reusedFrom,omitempty"`
	Range        *RenderRange `json:"range,omitempty"`
	Engine       string       `json:"engine,omitempty"`
	RequestID    string       `json:"requestId,omitempty"`
	RequestHash  string       `json:"requestHash,omitempty"`
	ContentHash  string       `json:"contentHash,omitempty"`
	Kind         string       `json:"kind,omitempty"`
	HasSubtitles bool         `json:"hasSubtitles,omitempty"`
	ID           string       `json:"id"`
	Revision     int          `json:"revision"`
	State        string       `json:"state"`
	Created      string       `json:"created"`
	Error        string       `json:"error,omitempty"`
	Duration     float64      `json:"duration"`
	HasAudio     bool         `json:"hasAudio"`
}
type Project struct {
	Final               *FinalSnapshot      `json:"final,omitempty"`
	EditRedo            []EditHistory       `json:"editRedo,omitempty"`
	EditHistory         []EditHistory       `json:"editHistory,omitempty"`
	Generation          []GenerationJob     `json:"generation,omitempty"`
	Workflow            *ProductionWorkflow `json:"workflow,omitempty"`
	NarrationText       string              `json:"narrationText,omitempty"`
	EditActive          bool                `json:"editActive,omitempty"`
	EditDSL             *EditDocument       `json:"editDsl,omitempty"`
	CreationRequestID   string              `json:"creationRequestId,omitempty"`
	CreationRequestHash string              `json:"creationRequestHash,omitempty"`
	Production          *Production         `json:"production,omitempty"`
	PreviousSessionIDs  []string            `json:"previousSessionIds,omitempty"`
	AgentSubmission     *AgentSubmission    `json:"agentSubmission,omitempty"`
	AgentRunID          string              `json:"agentRunId,omitempty"`
	Speech              []SpeechJob         `json:"speech,omitempty"`
	Subtitles           *SubtitleTrack      `json:"subtitles,omitempty"`
	News                *NewsOrigin         `json:"news,omitempty"`
	ID                  string              `json:"id"`
	Name                string              `json:"name"`
	Brief               string              `json:"brief"`
	Revision            int                 `json:"revision"`
	Shots               []Shot              `json:"shots"`
	Audio               *AudioMix           `json:"audio,omitempty"`
	Assets              []Asset             `json:"assets"`
	Renders             []Render            `json:"renders"`
	WorkspaceID         string              `json:"workspaceId"`
	SessionID           string              `json:"sessionId"`
	Updated             string              `json:"updated"`
	ToolsVersion        string              `json:"toolsVersion,omitempty"`
}
type Config struct {
	WorkspaceID      string `json:"workspaceId,omitempty"`
	InstallationID   string `json:"installationId,omitempty"`
	ManagedByRunDesk bool   `json:"managedByRunDesk,omitempty"`
	RunDeskURL       string `json:"rundeskURL,omitempty"`
	SkillPath        string `json:"skillPath,omitempty"`
	InstanceID       string `json:"instanceId"`
}
type App struct {
	tts                                                      SpeechConfig
	ttsManaged                                               bool
	espeak                                                   string
	generationJobs                                           map[string]context.CancelFunc
	speechJobs                                               map[string]context.CancelFunc
	mu                                                       sync.Mutex
	data, rd, token, rdToken, hf, font, gsap, public, mcpURL string
	cfg                                                      Config
	projects                                                 map[string]*Project
	jobs                                                     map[string]context.CancelFunc
	client                                                   *http.Client
	closing                                                  bool
	wg                                                       sync.WaitGroup
	bridgeMu                                                 sync.Mutex
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, e error) {
	var remote *RunDeskError
	if errors.As(e, &remote) {
		if remote.Status >= 400 && remote.Status < 600 {
			status = remote.Status
		}
		writeJSON(w, status, remote)
		return
	}
	writeJSON(w, status, map[string]string{"error": e.Error()})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	if e := d.Decode(v); e != nil {
		fail(w, 400, e)
		return false
	}
	return true
}
func atomicJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	tmp := path + "." + uid() + ".tmp"
	f, e := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer os.Remove(tmp)
	_, e = f.Write(b)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return e
	}
	if e = os.Rename(tmp, path); e != nil {
		os.Remove(tmp)
	}
	return e
}
func clone(p *Project) Project {
	b, _ := json.Marshal(p)
	var q Project
	_ = json.Unmarshal(b, &q)
	refreshSubtitleState(&q)
	refreshProduction(&q)
	return q
}
func (a *App) dir(id string) string { return filepath.Join(a.data, "projects", id) }
func (a *App) save(p *Project) error {
	refreshSubtitleState(p)
	refreshProduction(p)
	p.Updated = time.Now().UTC().Format(time.RFC3339Nano)
	return atomicJSON(filepath.Join(a.dir(p.ID), "project.json"), p)
}
func newApp(data string) (*App, error) {
	d, e := filepath.Abs(data)
	if e != nil {
		return nil, e
	}
	if e = os.MkdirAll(filepath.Join(d, "projects"), 0700); e != nil {
		return nil, e
	}
	a := &App{generationJobs: map[string]context.CancelFunc{}, data: d, projects: map[string]*Project{}, jobs: map[string]context.CancelFunc{}, speechJobs: map[string]context.CancelFunc{}, client: &http.Client{Timeout: 25 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}}
	if e = a.loadSpeechConfig(); e != nil {
		return nil, e
	}
	b, _ := os.ReadFile(filepath.Join(d, "config.json"))
	_ = json.Unmarshal(b, &a.cfg)
	entries, _ := os.ReadDir(filepath.Join(d, "projects"))
	for _, entry := range entries {
		if !identifier.MatchString(entry.Name()) {
			continue
		}
		b, e := os.ReadFile(filepath.Join(d, "projects", entry.Name(), "project.json"))
		if e != nil {
			return nil, e
		}
		var p Project
		if e = json.Unmarshal(b, &p); e != nil {
			return nil, e
		}
		if p.ID != entry.Name() {
			return nil, errors.New("project ID mismatch")
		}
		for i := range p.Generation {
			if p.Generation[i].State == "running" || p.Generation[i].State == "stopping" {
				p.Generation[i].State = "interrupted"
				p.Generation[i].Error = "服务重启，远端执行状态未知；不会自动重试"
			}
		}
		for i := range p.Speech {
			if p.Speech[i].State == "running" || p.Speech[i].State == "stopping" {
				p.Speech[i].State = "interrupted"
				p.Speech[i].Error = "服务重启，配音未完成；可重新生成"
			}
		}
		for i := range p.Renders {
			if p.Renders[i].State == "running" || p.Renders[i].State == "stopping" {
				p.Renders[i].State = "interrupted"
				p.Renders[i].Error = "服务重启，渲染未完成；可重新渲染"
			}
		}
		a.projects[p.ID] = &p
		if e = a.save(&p); e != nil {
			return nil, e
		}
	}
	return a, nil
}
func (a *App) get(id string) (Project, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[id]
	if p == nil {
		return Project{}, errors.New("项目不存在")
	}
	return clone(p), nil
}

var errCreateRequestConflict = errors.New("同一requestId不能用于不同新建项目请求")

func (a *App) create(name string) (Project, error) { return a.createRequested(name, "") }
func (a *App) createRequested(name, requestID string) (Project, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 {
		return Project{}, errors.New("项目名不能为空，最多 80 字")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if requestID != "" && !identifier.MatchString(requestID) {
		return Project{}, errors.New("新建项目requestId无效")
	}
	requestHash := hashBytes([]byte(name))
	if requestID != "" {
		for _, old := range a.projects {
			if old.CreationRequestID == requestID {
				if old.CreationRequestHash != requestHash {
					return Project{}, errCreateRequestConflict
				}
				return clone(old), nil
			}
		}
	}
	p := Project{CreationRequestID: requestID, CreationRequestHash: requestHash, ID: uid(), Name: name, Revision: 1, Shots: []Shot{}, Assets: []Asset{}, Renders: []Render{}}
	if e := os.MkdirAll(filepath.Join(a.dir(p.ID), "workspace", "assets"), 0700); e != nil {
		return p, e
	}
	if e := os.MkdirAll(filepath.Join(a.dir(p.ID), "workspace"), 0700); e != nil {
		return p, e
	}
	if e := a.save(&p); e != nil {
		return p, e
	}
	a.projects[p.ID] = &p
	return clone(&p), nil
}
func (a *App) edit(id string, in Project) (Project, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[id]
	if p == nil {
		return Project{}, errors.New("项目不存在")
	}
	if in.Revision != p.Revision {
		return clone(p), errors.New("项目已被其他操作修改，请重新载入后合并")
	}
	if len(in.Shots) > 80 || len(in.Brief) > 128*1024 || len([]rune(in.Name)) > 80 || strings.TrimSpace(in.Name) == "" {
		return clone(p), errors.New("项目内容超出范围")
	}
	if e := validateTextProjection(p.Production, in.Shots); e != nil {
		return clone(p), e
	}
	seen := map[string]bool{}
	assets := map[string]bool{}
	for _, asset := range p.Assets {
		assets[asset.ID] = true
	}
	total := 0.0
	for i := range in.Shots {
		s := &in.Shots[i]
		if s.ID == "" {
			s.ID = uid()
		}
		if !identifier.MatchString(s.ID) || seen[s.ID] || !finite(s.Duration) || s.Duration < 1 || s.Duration > 60 || len([]rune(s.Title)) > 80 || len([]rune(s.Text)) > 220 || len([]rune(s.VisualReason)) > 300 {
			return clone(p), errors.New("镜头 ID、时长或文字长度不合法（标题80字，正文220字，1–60秒）")
		}
		if s.AssetID != "" && !assets[s.AssetID] {
			return clone(p), errors.New("镜头引用了不存在的素材")
		}
		seen[s.ID] = true
		total += s.Duration
	}
	if total > 600 {
		return clone(p), errors.New("原型版最长 10 分钟")
	}
	// Omitting audio preserves it for older clients; an empty mix clears it.
	mix := in.Audio
	if mix == nil {
		mix = p.Audio
	}
	if e := validateAudio(mix, p.Assets, total); e != nil {
		return clone(p), e
	}
	q := clone(p)
	q.Name = in.Name
	q.Brief = in.Brief
	q.Shots = in.Shots
	q.Audio = cloneAudio(mix)
	q.Revision++
	if e := a.save(&q); e != nil {
		return clone(p), e
	}
	a.projects[id] = &q
	return clone(&q), nil
}
func (a *App) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' blob:; media-src 'self'; object-src 'none'; frame-ancestors 'none'")
		host, _, _ := net.SplitHostPort(r.Host)
		if host == "" {
			host = r.Host
		}
		publicURL, _ := url.Parse(a.public)
		if !net.ParseIP(host).IsLoopback() && host != "localhost" && (publicURL == nil || r.Host != publicURL.Host) {
			fail(w, 403, errors.New("请求 Host 与配置的应用地址不一致"))
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, e := url.Parse(origin)
			if e != nil || u.Host != r.Host {
				fail(w, 403, errors.New("跨来源请求被拒绝"))
				return
			}
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			sup := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if c, e := r.Cookie("video_session"); e == nil && sup == "" {
				sup = c.Value
			}
			if subtle.ConstantTimeCompare([]byte(sup), []byte(a.token)) != 1 {
				fail(w, 401, errors.New("请登录视频工作台"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (a *App) routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("POST /api/projects/{id}/edit/active", a.editActivate)
	m.HandleFunc("GET /api/projects/{id}/edit", a.editGet)
	m.HandleFunc("PUT /api/projects/{id}/edit", a.editSave)
	m.HandleFunc("POST /api/projects/{id}/edit/validate", a.editValidate)
	m.HandleFunc("GET /api/capabilities", a.capabilities)
	m.HandleFunc("GET /api/projects/{id}/jobs", a.jobsList)
	m.HandleFunc("POST /api/projects/{id}/jobs", a.jobStart)
	m.HandleFunc("GET /api/projects/{id}/jobs/{jid}", a.jobGet)
	m.HandleFunc("POST /api/projects/{id}/jobs/{jid}/cancel", a.jobCancel)
	m.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"ready": true, "version": version})
	})
	m.HandleFunc("POST /api/mcp", a.mcpHTTP)
	m.HandleFunc("GET /api/runtime", a.runtimeStatus)
	m.HandleFunc("POST /api/runtime/prepare", a.runtimePrepare)
	m.HandleFunc("GET /api/speech/capabilities", a.speechCapabilities)
	m.HandleFunc("GET /api/speech/settings", a.speechSettings)
	m.HandleFunc("PUT /api/speech/settings", a.saveSpeechSettings)
	m.HandleFunc("GET /api/speech/voices", a.speechVoices)
	m.HandleFunc("POST /api/speech/voices", a.speechVoices)
	m.HandleFunc("POST /api/projects/{id}/speech", a.startSpeech)
	m.HandleFunc("POST /api/projects/{id}/speech/{sid}/stop", a.stopSpeech)
	m.HandleFunc("POST /api/projects/{id}/speech/{sid}/adopt", a.adoptSpeech)
	m.HandleFunc("PUT /api/projects/{id}/narration", a.saveNarration)
	m.HandleFunc("POST /api/projects/{id}/speech/{sid}/materialize", a.materializeSpeech)
	m.HandleFunc("GET /api/projects/{id}/speech/{sid}/files/{file}", a.speechFile)
	m.HandleFunc("POST /api/news/import", a.importNews)
	m.HandleFunc("GET /api/projects/{id}/news", a.newsDetail)
	m.HandleFunc("POST /api/projects/{id}/news/video", a.importNewsVideo)
	m.HandleFunc("POST /api/projects/{id}/news/audio", a.adoptNewsAudio)
	m.HandleFunc("PUT /api/projects/{id}/subtitles", a.updateSubtitles)
	m.HandleFunc("GET /api/projects/{id}/subtitles.srt", a.subtitleFile)
	m.HandleFunc("GET /api/projects/{id}/news/{group}/files/{file}", a.newsFile)
	m.HandleFunc("POST /login", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Token string `json:"token"`
		}
		if !decode(w, r, &in) {
			return
		}
		if subtle.ConstantTimeCompare([]byte(in.Token), []byte(a.token)) != 1 {
			fail(w, 401, errors.New("令牌错误"))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "video_session", Value: a.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode, MaxAge: 86400})
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("POST /logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "video_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("GET /api/projects", func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		list := []Project{}
		for _, p := range a.projects {
			list = append(list, clone(p))
		}
		a.mu.Unlock()
		sort.Slice(list, func(i, j int) bool { return list[i].Updated > list[j].Updated })
		writeJSON(w, 200, list)
	})
	m.HandleFunc("POST /api/projects", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name      string `json:"name"`
			RequestID string `json:"requestId"`
		}
		if !decode(w, r, &in) {
			return
		}
		p, e := a.createRequested(in.Name, in.RequestID)
		if e != nil {
			if errors.Is(e, errCreateRequestConflict) {
				fail(w, 409, e)
				return
			}
			fail(w, 400, e)
			return
		}
		writeJSON(w, 201, p)
	})
	m.HandleFunc("GET /api/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		p, e := a.get(r.PathValue("id"))
		if e != nil {
			fail(w, 404, e)
			return
		}
		writeJSON(w, 200, p)
	})
	m.HandleFunc("PUT /api/projects/{id}", func(w http.ResponseWriter, r *http.Request) {
		var p Project
		if !decode(w, r, &p) {
			return
		}
		q, e := a.edit(r.PathValue("id"), p)
		if e != nil {
			fail(w, 409, e)
			return
		}
		writeJSON(w, 200, q)
	})
	m.HandleFunc("GET /api/projects/{id}/assets", a.assetsList)
	m.HandleFunc("POST /api/projects/{id}/assets/import", a.assetImport)
	m.HandleFunc("POST /api/projects/{id}/assets", a.upload)
	m.HandleFunc("GET /api/projects/{id}/assets/{aid}", a.asset)
	m.HandleFunc("GET /api/projects/{id}/assets/{aid}/preview", a.assetPreview)
	m.HandleFunc("PUT /api/projects/{id}/production/text", a.production)
	m.HandleFunc("POST /api/projects/{id}/production/stage", a.production)
	m.HandleFunc("POST /api/projects/{id}/render", a.startRender)
	m.HandleFunc("POST /api/projects/{id}/renders/{rid}/stop", a.stopRender)
	m.HandleFunc("GET /api/projects/{id}/renders/{rid}/{file}", a.renderFile)
	m.HandleFunc("GET /api/environment", a.environment)
	m.HandleFunc("POST /api/projects/{id}/connect", a.connect)
	m.HandleFunc("POST /api/projects/{id}/messages", a.message)
	m.HandleFunc("PUT /api/projects/{id}/assets/{aid}/description", a.assetDescribe)
	m.HandleFunc("POST /api/projects/{id}/media/align", a.mediaAlign)
	m.HandleFunc("POST /api/projects/{id}/edit/patch/{action}", a.editPatch)
	m.HandleFunc("POST /api/projects/{id}/edit/undo", a.editUndo)
	m.HandleFunc("POST /api/projects/{id}/edit/redo", a.editRedo)
	m.HandleFunc("GET /api/projects/{id}/final/verify", a.verifyFinal)
	m.HandleFunc("GET /api/projects/{id}/final", a.finalGet)
	m.HandleFunc("POST /api/projects/{id}/final", a.finalSave)
	m.HandleFunc("GET /api/projects/{id}/workflow", a.workflowGet)
	m.HandleFunc("PUT /api/projects/{id}/workflow", a.workflowSave)
	m.HandleFunc("POST /api/projects/{id}/draft/instruction", a.draftInstruction)
	m.HandleFunc("GET /api/projects/{id}/agent", a.agentState)
	m.HandleFunc("GET /api/projects/{id}/agent/configuration", a.agentConfiguration)
	m.HandleFunc("POST /api/projects/{id}/agent/stop", a.agentStop)
	m.HandleFunc("GET /api/projects/{id}/events", a.events)
	m.HandleFunc("GET /api/projects/{id}/approvals", a.approvals)
	m.HandleFunc("POST /api/projects/{id}/approvals/{aid}", a.approve)
	sub, _ := fs.Sub(ui, "web")
	m.Handle("/", http.FileServer(http.FS(sub)))
	return a.auth(m)
}
func (a *App) upload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, e := a.get(id); e != nil {
		fail(w, 404, e)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 257<<20)
	if e := r.ParseMultipartForm(1 << 20); e != nil {
		fail(w, 400, e)
		return
	}
	defer r.MultipartForm.RemoveAll()
	f, h, e := r.FormFile("file")
	if e != nil {
		fail(w, 400, e)
		return
	}
	defer f.Close()
	if h.Size > 256<<20 {
		fail(w, 400, errors.New("单文件最大256MiB"))
		return
	}
	ext := strings.ToLower(filepath.Ext(h.Filename))
	allowed := map[string]string{".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".webp": "image/webp", ".mp4": "video/mp4", ".webm": "video/webm", ".wav": "audio/wav", ".mp3": "audio/mpeg", ".m4a": "audio/mp4", ".flac": "audio/flac", ".ogg": "audio/ogg", ".txt": "text/plain", ".md": "text/plain", ".pdf": "application/pdf"}
	mime, ok := allowed[ext]
	if !ok {
		fail(w, 400, errors.New("不支持的素材类型"))
		return
	}
	asset := Asset{ID: uid(), Name: filepath.Base(strings.ReplaceAll(h.Filename, "\\", "/")), Size: h.Size, MIME: mime}
	asset.File = asset.ID + ext
	if requestID := r.FormValue("requestId"); requestID != "" {
		if !identifier.MatchString(requestID) {
			fail(w, 400, errors.New("素材requestId无效"))
			return
		}
		digest, _, err := hashReader(f)
		if err != nil {
			fail(w, 500, err)
			return
		}
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			fail(w, 500, err)
			return
		}
		asset.ImportRequestID = requestID
		asset.ImportHash = hashBytes([]byte(asset.Name + "\x00" + digest))
	}
	root, e := os.OpenRoot(filepath.Join(a.dir(id), "workspace", "assets"))
	if e != nil {
		fail(w, 500, e)
		return
	}
	defer root.Close()
	out, e := root.OpenFile(asset.File, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		fail(w, 500, e)
		return
	}
	_, e = io.Copy(out, f)
	out.Close()
	if e != nil {
		root.Remove(asset.File)
		fail(w, 500, e)
		return
	}
	a.mu.Lock()
	p := clone(a.projects[id])
	if asset.ImportRequestID != "" {
		for _, old := range p.Assets {
			if old.ImportRequestID == asset.ImportRequestID {
				a.mu.Unlock()
				root.Remove(asset.File)
				if old.ImportHash != asset.ImportHash {
					fail(w, 409, errors.New("同一requestId不能用于不同素材"))
					return
				}
				writeJSON(w, 200, p)
				return
			}
		}
	}
	p.Assets = append(p.Assets, asset)
	p.Revision++
	e = a.save(&p)
	if e == nil {
		a.projects[id] = &p
	}
	a.mu.Unlock()
	if e != nil {
		root.Remove(asset.File)
		fail(w, 500, e)
		return
	}
	writeJSON(w, 201, p)
}
func (a *App) asset(w http.ResponseWriter, r *http.Request) {
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	for _, x := range p.Assets {
		if x.ID == r.PathValue("aid") {
			root, e := os.OpenRoot(filepath.Join(a.dir(p.ID), "workspace", "assets"))
			if e != nil {
				fail(w, 404, e)
				return
			}
			defer root.Close()
			f, e := root.Open(x.File)
			if e != nil {
				fail(w, 404, e)
				return
			}
			defer f.Close()
			info, e := f.Stat()
			if e != nil {
				fail(w, 500, e)
				return
			}
			w.Header().Set("Content-Type", x.MIME)
			if x.MIME == "application/pdf" || r.URL.Query().Has("download") {
				w.Header().Set("Content-Disposition", "attachment; filename=\""+x.File+"\"")
			}
			http.ServeContent(w, r, x.File, info.ModTime(), f)
			return
		}
	}
	fail(w, 404, errors.New("素材不存在"))
}
func (a *App) environment(w http.ResponseWriter, r *http.Request) {
	checks := map[string]any{}
	for _, n := range []string{"node", "ffmpeg", "ffprobe"} {
		p, e := exec.LookPath(n)
		checks[n] = map[string]any{"ready": e == nil, "path": p}
	}
	for n, p := range map[string]string{"hyperframes": a.hf, "gsap": a.gsap, "font": a.font} {
		_, e := os.Stat(p)
		checks[n] = map[string]any{"ready": e == nil && p != "", "path": p}
	}
	var meta any
	e := a.rdCall(r.Context(), "GET", "/meta", nil, &meta)
	a.mu.Lock()
	instanceID := a.cfg.InstanceID
	a.mu.Unlock()
	writeJSON(w, 200, map[string]any{"version": version, "checks": checks, "rundeskReady": e == nil, "rundeskError": errText(e), "rundesk": meta, "rundeskURL": a.rd, "instanceId": instanceID, "data": a.data})
}
func errText(e error) string {
	if e == nil {
		return ""
	}
	return e.Error()
}
func main() {
	if len(os.Args) > 1 && os.Args[1] == "speech-doctor" {
		os.Exit(runSpeechDoctor(os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		mcpMain(os.Args[2:])
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "--version" {
		fmt.Println("RunDesk Video App " + version)
		return
	}
	data := flag.String("data", "./data", "data directory")
	listen := flag.String("listen", "127.0.0.1:3220", "loopback address")
	rd := flag.String("rundesk", "http://127.0.0.1:3210", "RunDesk base URL")
	hf := flag.String("hyperframes", "", "absolute HyperFrames CLI path")
	font := flag.String("font", "", "local Chinese font file")
	gsap := flag.String("gsap", "", "local gsap.min.js file")
	espeak := flag.String("espeak", "", "local espeak-ng executable (defaults to PATH)")
	flag.Parse()
	if e := serve(serverOptions{Data: *data, Listen: *listen, RunDesk: *rd, HyperFrames: *hf, Font: *font, GSAP: *gsap, ESpeak: *espeak}); e != nil {
		log.Fatal(e)
	}
}

type serverOptions struct{ Data, Listen, RunDesk, HyperFrames, Font, GSAP, ESpeak string }

func serve(opts serverOptions) error {
	host, _, e := net.SplitHostPort(opts.Listen)
	if e != nil || net.ParseIP(host) == nil {
		return errors.New("--listen must use an IP address and port")
	}
	if e = os.MkdirAll(opts.Data, 0700); e != nil {
		return e
	}
	unlock, e := lockData(opts.Data)
	if e != nil {
		return fmt.Errorf("无法取得数据目录锁，请检查是否已有实例使用此目录: %w", e)
	}
	defer unlock()
	a, e := newApp(opts.Data)
	if e != nil {
		return e
	}
	a.rd = strings.TrimSuffix(strings.TrimSuffix(strings.TrimRight(opts.RunDesk, "/"), "/api/v1"), "/api")
	u, e := url.Parse(a.rd)
	if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("RunDesk 需要有效的 HTTP(S) 地址")
	}
	a.rdToken = os.Getenv("RUNDESK_TOKEN")
	a.token = os.Getenv("VIDEO_APP_TOKEN")
	if a.token == "" {
		b, _ := os.ReadFile(filepath.Join(a.data, "access-token"))
		a.token = strings.TrimSpace(string(b))
		if a.token == "" {
			a.token = uid() + uid()
			if e = os.WriteFile(filepath.Join(a.data, "access-token"), []byte(a.token), 0600); e != nil {
				return e
			}
		}
	}
	if len(a.token) < 24 {
		return errors.New("VIDEO_APP_TOKEN must be at least 24 characters")
	}
	if a.hf, e = absoluteOrEmpty(opts.HyperFrames); e != nil {
		return e
	}
	if a.font, e = absoluteOrEmpty(opts.Font); e != nil {
		return e
	}
	if a.gsap, e = absoluteOrEmpty(opts.GSAP); e != nil {
		return e
	}
	if a.espeak, e = absoluteOrEmpty(opts.ESpeak); e != nil {
		return e
	}
	if e = a.speechEnvironment(); e != nil {
		return e
	}
	a.public = "http://" + opts.Listen
	if override := os.Getenv("VIDEO_APP_PUBLIC_URL"); override != "" {
		u, err := url.Parse(override)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("VIDEO_APP_PUBLIC_URL 无效")
		}
		a.public = strings.TrimRight(override, "/")
	}
	a.mcpURL = a.public + "/api/mcp"
	if override := os.Getenv("VIDEO_APP_MCP_URL"); override != "" {
		u, err := url.Parse(override)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasSuffix(u.Path, "/api/mcp") {
			return errors.New("VIDEO_APP_MCP_URL 需要有效 HTTP(S) 地址并以 /api/mcp 结尾")
		}
		a.mcpURL = strings.TrimRight(override, "/")
	}
	srv := &http.Server{Addr: opts.Listen, Handler: a.routes(), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		a.mu.Lock()
		a.closing = true
		for _, cancel := range a.jobs {
			cancel()
		}
		for _, cancel := range a.generationJobs {
			cancel()
		}
		for _, cancel := range a.speechJobs {
			cancel()
		}
		a.mu.Unlock()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(shutdown)
	}()
	tokenSource := "VIDEO_APP_TOKEN"
	if os.Getenv("VIDEO_APP_TOKEN") == "" {
		tokenSource = filepath.Join(a.data, "access-token")
	}
	log.Printf("Video App %s: %s; token source: %s", version, a.public, tokenSource)
	if e = srv.ListenAndServe(); e != http.ErrServerClosed {
		return e
	}
	a.wg.Wait()
	return nil
}

func absoluteOrEmpty(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	return filepath.Abs(p)
}
