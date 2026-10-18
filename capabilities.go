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
	envelope.Capabilities = append(envelope.Capabilities, ProductionCapability{ID: "media", Implemented: true, Ready: true, Providers: []ProviderCapability{}, Tools: []string{"video_asset_list", "video_asset_import", "video_asset_inspect", "video_asset_video_inspect"}, Notes: []string{"图片/视频预览需要FFmpeg；inspect-video支持最多8个带时间点的真实视频帧，不包含听音或完整运动检查", "大文件通过已有multipart上传或页面目录导入，MCP导入限小文件"}, Limits: map[string]any{"uploadBytes": 256 << 20, "mcpImportBytes": 1 << 20}})
	envelope.Capabilities = append(envelope.Capabilities, ProductionCapability{ID: "tts", Implemented: true, Ready: espeak.Ready || index.Ready, Providers: []ProviderCapability{espeak, index}, Tools: []string{"video_job_start", "video_speech_start", "video_speech_adopt"}, Notes: []string{"模型列表探测不代表真实合成、音色和音质已验收", "TTS支持独立口播 source=narration 与兼容旧镜头 source=shots"}})
	for _, id := range []string{"image_generation", "video_generation"} {
		_, err := generationTarget(id)
		provider := ProviderCapability{ID: "http_binary_v1", Runtime: "http_service", State: "blocked", Verified: "configuration_only", Reasons: []string{}, Supports: []string{id}}
		if err == nil {
			provider.Configured = true
			provider.State = "configured"
		} else {
			provider.Reasons = append(provider.Reasons, err.Error())
		}
		envelope.Capabilities = append(envelope.Capabilities, ProductionCapability{ID: id, Implemented: true, Ready: false, Providers: []ProviderCapability{provider}, Tools: []string{"video_job_start", "video_job_get", "video_job_cancel"}, Notes: []string{"自定义HTTP二进制适配协议，非厂商原生API", "配置检查不是实际模型验收；取消不保证远端停止"}})
	}

	envelope.Capabilities = append(envelope.Capabilities, ProductionCapability{ID: "text_media_alignment", Implemented: true, Ready: true, Providers: []ProviderCapability{}, Tools: []string{"video_asset_describe", "video_media_align", "video_media_alignment_get", "video_media_alignment_update"}, Notes: []string{"描述与标签可作词项检索；实际画面语义由具备视觉能力的Agent查看后判断", "关联表保存精确原文引用、真实素材、理由、用途和缺口；不要求一段一镜，未覆盖内容仍显示未完成"}})
	envelope.Capabilities = append(envelope.Capabilities, ProductionCapability{ID: "editing", Implemented: true, Ready: true, Providers: []ProviderCapability{}, Tools: []string{"video_edit_get", "video_edit_validate", "video_edit_update", "video_edit_activate", "video_edit_patch_preview", "video_edit_patch_apply", "video_edit_undo", "video_edit_redo", "video_project_update"}, Notes: []string{"剪辑DSL v1：多轨、片段裁剪/速度、布局、淡入淡出、时间引用；内容通过素材引用自由组合", "旧镜头与DSL分别保存，渲染采用明确激活的模式"}})
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
	editRenderer := ProviderCapability{ID: "ffmpeg_edit", Runtime: "local", State: "blocked", Verified: "dependency_check", Reasons: missingCommands("ffmpeg", "ffprobe"), Supports: []string{"edit_dsl_v1", "multi_track", "source_trim", "source_speed", "layout", "fades", "audio_mix"}}
	editRenderer.Ready = len(editRenderer.Reasons) == 0
	editRenderer.Configured = editRenderer.Ready
	if editRenderer.Ready {
		editRenderer.State = "available"
	}
	envelope.Capabilities = append(envelope.Capabilities, ProductionCapability{ID: "rendering", Implemented: true, Ready: renderer.Ready || editRenderer.Ready, Providers: []ProviderCapability{renderer, editRenderer}, Tools: []string{"video_job_start", "video_render_start"}, Notes: []string{"激活DSL时使用FFmpeg，无需Node/浏览器；文字片段另需配置字体", "就绪表示依赖检查通过；实际合成由任务执行与MP4验证确认"}})
	exchangeReady := false
	exchangeReasons := []string{}
	if dir := strings.TrimSpace(os.Getenv("VIDEO_EXCHANGE_ROOT")); dir == "" {
		exchangeReasons = append(exchangeReasons, "未配置与RunDesk应用工作区对应的VIDEO_EXCHANGE_ROOT")
	} else if root, err := os.OpenRoot(dir); err != nil {
		exchangeReasons = append(exchangeReasons, "共享工作目录不可读")
	} else {
		root.Close()
		exchangeReady = true
	}
	exchangeState := "blocked"
	if exchangeReady {
		exchangeState = "configured"
	}
	exchange := ProviderCapability{ID: "video_use", Runtime: "rundesk_shared_workspace", State: exchangeState, Configured: exchangeReady, Ready: false, Verified: "local_path_only", Reasons: exchangeReasons, Supports: []string{"script_media_edl", "continuous_narration", "images", "generated_video", "external_artifact_ingest"}}
	envelope.Capabilities = append(envelope.Capabilities, ProductionCapability{ID: "video_use_exchange", Implemented: true, Ready: false, Providers: []ProviderCapability{exchange}, Tools: []string{"video_production_run_prepare", "video_production_run_get", "video_production_run_ingest"}, Notes: []string{"需要RunDesk video-build 1.2.0运行环境及同一物理工作区挂载；本地目录存在不证明容器已挂载或运行时已安装", "由Agent验证返回的快照指纹后运行适配器；MP4经实际探测和复制校验后才归档，不复写原生DSL"}})
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
