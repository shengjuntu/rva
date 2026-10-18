package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

type ProviderCapability struct {
	ID         string   `json:"id"`
	Runtime    string   `json:"runtime"`
	State      string   `json:"state"`
	Ready      bool     `json:"ready"`
	Configured bool     `json:"configured"`
	Verified   string   `json:"verified"`
	Reasons    []string `json:"reasons"`
	Supports   []string `json:"supports"`
	Model      string   `json:"model,omitempty"`
	Default    bool     `json:"default,omitempty"`
}
type ProductionCapability struct {
	ID          string               `json:"id"`
	Implemented bool                 `json:"implemented"`
	Ready       bool                 `json:"ready"`
	Providers   []ProviderCapability `json:"providers"`
	Tools       []string             `json:"tools"`
	Notes       []string             `json:"notes"`
	Limits      map[string]any       `json:"limits,omitempty"`
}
type CapabilityEnvelope struct {
	Schema       string                 `json:"schema"`
	Version      string                 `json:"appVersion"`
	CheckedAt    string                 `json:"checkedAt"`
	Probed       bool                   `json:"remoteProbed"`
	Capabilities []ProductionCapability `json:"capabilities"`
}

func regularFile(path string) bool { s, e := os.Stat(path); return e == nil && s.Mode().IsRegular() }
func missingCommands(names ...string) []string {
	out := []string{}
	for _, name := range names {
		if _, e := exec.LookPath(name); e != nil {
			out = append(out, "缺少命令 "+name)
		}
	}
	return out
}
func (a *App) capabilityEnvelope(ctx context.Context, probe bool) CapabilityEnvelope {
	a.mu.Lock()
	cfg, hf, font, gsap := a.tts, a.hf, a.font, a.gsap
	a.mu.Unlock()
	envelope := CapabilityEnvelope{Schema: "video.capabilities.v1", Version: version, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), Probed: probe, Capabilities: []ProductionCapability{}}
	espeak := ProviderCapability{ID: "espeak-ng", Runtime: "local", State: "blocked", Verified: "dependency_check", Reasons: missingCommands("ffmpeg", "ffprobe"), Supports: []string{"tts", "mechanical_preview", "sentence_durations"}, Default: cfg.Backend == "espeak-ng"}
	if _, e := a.speechExecutable(); e != nil {
		espeak.Reasons = append(espeak.Reasons, "缺少 eSpeak NG")
	}
	espeak.Configured = len(espeak.Reasons) == 0
	espeak.Ready = espeak.Configured
	if espeak.Ready {
		espeak.State = "available"
	}
	index := ProviderCapability{ID: "indextts", Runtime: "http_service", State: "blocked", Verified: "not_probed", Reasons: missingCommands("ffmpeg", "ffprobe"), Supports: []string{"tts", "registered_voice", "sentence_durations", "speed"}, Model: cfg.Model, Default: cfg.Backend == "indextts"}
	target := cfg
	target.Backend = "indextts"
	if e := validateSpeechConfig(target); e != nil {
		index.Reasons = append(index.Reasons, e.Error())
	} else {
		index.Configured = true
		if len(index.Reasons) == 0 {
			index.State = "configured"
		}
		if probe {
			step, cancel := context.WithTimeout(ctx, 8*time.Second)
			_, e := probeIndexTTS(step, target)
			cancel()
			if e != nil {
				index.State = "blocked"
				index.Verified = "probe_failed"
				index.Reasons = append(index.Reasons, e.Error())
			} else {
				index.Verified = "model_list"
				index.Ready = len(index.Reasons) == 0
				if index.Ready {
					index.State = "available"
				}
			}
		}
	}
	envelope.Capabilities = append(envelope.Capabilities, ProductionCapability{ID: "media", Implemented: true, Ready: true, Providers: []ProviderCapability{}, Tools: []string{"video_asset_list", "video_asset_import", "video_asset_inspect"}, Notes: []string{"图片/视频预览需要FFmpeg；视频当前仅取首帧", "大文件通过已有multipart上传或页面目录导入，MCP导入限小文件"}, Limits: map[string]any{"uploadBytes": 256 << 20, "mcpImportBytes": 1 << 20}})
	envelope.Capabilities = append(envelope.Capabilities, ProductionCapability{ID: "tts", Implemented: true, Ready: espeak.Ready || index.Ready, Providers: []ProviderCapability{espeak, index}, Tools: []string{"video_job_start", "video_speech_start", "video_speech_adopt"}, Notes: []string{"模型列表探测不代表真实合成、音色和音质已验收", "当前TTS输入来自保存的镜头正文；自由内容输入将在后续制作闭环扩展"}})
	for _, id := range []string{"image_generation", "video_generation", "text_media_alignment"} {
		envelope.Capabilities = append(envelope.Capabilities, ProductionCapability{ID: id, Implemented: false, Ready: false, Providers: []ProviderCapability{}, Tools: []string{}, Notes: []string{"第四阶段接入；当前不会分配占位任务或假称已经可用"}})
	}
	envelope.Capabilities = append(envelope.Capabilities, ProductionCapability{ID: "editing", Implemented: true, Ready: true, Providers: []ProviderCapability{}, Tools: []string{"video_project_get", "video_project_update"}, Notes: []string{"当前是旧版镜头/音轨编辑；剪辑DSL、多轨片段与自由内容关系将在第二阶段实现"}})
	renderer := ProviderCapability{ID: "hyperframes", Runtime: "local", State: "blocked", Verified: "dependency_check", Reasons: missingCommands("node", "ffmpeg", "ffprobe"), Supports: []string{"legacy_storyboard", "html_gsap", "vertical_1080x1920_30fps"}}
	for name, path := range map[string]string{"HyperFrames CLI": hf, "字体": font, "GSAP": gsap} {
		if path == "" || !regularFile(path) {
			renderer.Reasons = append(renderer.Reasons, "未配置可读的 "+name+" 文件")
		}
	}
	if _, e := exec.LookPath("node"); e == nil {
		step, cancel := context.WithTimeout(ctx, 2*time.Second)
		b, e := exec.CommandContext(step, "node", "--version").Output()
		cancel()
		major, _ := strconv.Atoi(strings.Split(strings.TrimPrefix(strings.TrimSpace(string(b)), "v"), ".")[0])
		if e != nil || major < 22 {
			renderer.Reasons = append(renderer.Reasons, "需要可执行的 Node.js 22+")
		}
	}
	browser := os.Getenv("HYPERFRAMES_BROWSER_PATH")
	if browser != "" {
		if _, e := exec.LookPath(browser); e != nil {
			renderer.Reasons = append(renderer.Reasons, "配置的浏览器不可执行")
		}
	} else {
		found := false
		for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
			if _, e := exec.LookPath(name); e == nil {
				found = true
				break
			}
		}
		if !found {
			renderer.Reasons = append(renderer.Reasons, "未发现可执行浏览器；请配置 HYPERFRAMES_BROWSER_PATH（未探测引擎私有下载缓存）")
		}
	}
	renderer.Configured = hf != "" && font != "" && gsap != ""
	renderer.Ready = len(renderer.Reasons) == 0
	if renderer.Ready {
		renderer.State = "available"
	}
	envelope.Capabilities = append(envelope.Capabilities, ProductionCapability{ID: "rendering", Implemented: true, Ready: renderer.Ready, Providers: []ProviderCapability{renderer}, Tools: []string{"video_job_start", "video_render_start"}, Notes: []string{"就绪表示依赖检查通过；实际合成由任务执行与MP4验证确认"}})
	return envelope
}
func (a *App) capabilities(w http.ResponseWriter, r *http.Request) {
	probe := r.URL.Query().Get("probe")
	if probe != "" && probe != "0" && probe != "1" {
		fail(w, 400, fmt.Errorf("probe must be 0 or 1"))
		return
	}
	writeJSON(w, 200, a.capabilityEnvelope(r.Context(), probe == "1"))
}
