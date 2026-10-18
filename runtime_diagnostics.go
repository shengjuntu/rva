package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"strings"
	"time"
)

// A passed diagnostic proves the named boundary, never a model's understanding.
type RuntimeDiagnosticRow struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	State   string `json:"state"`
	Summary string `json:"summary"`
	Code    string `json:"code,omitempty"`
	Remedy  string `json:"remedy,omitempty"`
}
type RuntimeDiagnosticBinding struct {
	InstanceID    string `json:"instanceId"`
	WorkspaceID   string `json:"workspaceId"`
	SessionID     string `json:"sessionId"`
	ExecutionMode string `json:"executionMode,omitempty"`
}
type RuntimeDiagnostics struct {
	Schema              string                    `json:"schema"`
	ProjectID           string                    `json:"projectId"`
	CheckedAt           string                    `json:"checkedAt"`
	EditingBackend      string                    `json:"editingBackend"`
	Ready               bool                      `json:"ready"`
	ModelVisionVerified bool                      `json:"modelVisionVerified"`
	Binding             RuntimeDiagnosticBinding  `json:"binding"`
	Rows                []RuntimeDiagnosticRow    `json:"rows"`
	Runtime             *runDeskDiagnosticRuntime `json:"runtime,omitempty"`
}
type runDeskDiagnosticRuntime struct {
	Status                 string            `json:"status"`
	ProfileID              string            `json:"profileId"`
	ProfileVersion         string            `json:"profileVersion"`
	AdapterContractVersion int               `json:"adapterContractVersion"`
	VideoUseCommit         string            `json:"videoUseCommit"`
	PythonVersion          string            `json:"pythonVersion"`
	FFmpegVersion          string            `json:"ffmpegVersion"`
	FFprobeVersion         string            `json:"ffprobeVersion"`
	Checks                 map[string]string `json:"checks,omitempty"`
}
type runDeskDiagnosticResponse struct {
	SchemaVersion    int                      `json:"schemaVersion"`
	AppID            string                   `json:"appId"`
	InstanceID       string                   `json:"instanceId"`
	WorkspaceID      string                   `json:"workspaceId"`
	InstanceRevision int                      `json:"instanceRevision"`
	ExecutionMode    string                   `json:"executionMode"`
	Probed           bool                     `json:"probed"`
	AccountReady     bool                     `json:"accountReady"`
	Servers          map[string][]string      `json:"servers,omitempty"`
	ModelTested      bool                     `json:"modelTested"`
	CheckedAt        string                   `json:"checkedAt"`
	Runtime          runDeskDiagnosticRuntime `json:"runtime"`
	Challenge        struct {
		Status        string `json:"status"`
		Path          string `json:"path"`
		ResponsePath  string `json:"responsePath"`
		RequestSHA256 string `json:"requestSHA256"`
		Nonce         string `json:"nonce"`
	} `json:"challenge"`
	Errors         map[string]string `json:"errors,omitempty"`
	ImageTransport struct {
		State       string `json:"state"`
		Nonce       string `json:"nonce"`
		ImageSHA256 string `json:"imageSHA256"`
		MIMEType    string `json:"mimeType"`
		Width       int    `json:"width"`
		Height      int    `json:"height"`
		Code        string `json:"code,omitempty"`
	} `json:"imageTransport"`
}

// runtimeProbe owns one random directory. It deliberately never removes an
// entire session or probe parent, even when another process has changed it.
type runtimeProbe struct {
	root          *os.Root
	owned         *os.Root
	ownedInfo     os.FileInfo
	dir           string
	requestPath   string
	responsePath  string
	nonce         string
	requestSHA256 string
}

func diagnosticMkdir(root *os.Root, name string) error {
	if !exchangeRelative(name) {
		return errors.New("诊断目录路径无效")
	}
	parts := strings.Split(name, "/")
	for i := range parts {
		p := strings.Join(parts[:i+1], "/")
		st, err := root.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			if err = root.Mkdir(p, 0700); err != nil && !errors.Is(err, os.ErrExist) {
				return err
			}
			st, err = root.Lstat(p)
		}
		if err != nil {
			return err
		}
		if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
			return errors.New("共享诊断目录不允许符号链接或非目录")
		}
	}
	return nil
}
func newRuntimeProbe(sessionID, nonce string) (*runtimeProbe, error) {
	if !exchangeSessionID.MatchString(sessionID) || !identifier.MatchString(nonce) {
		return nil, errors.New("需要有效的 RunDesk 会话")
	}
	root, err := exchangeRoot()
	if err != nil {
		return nil, err
	}
	p := &runtimeProbe{root: root, nonce: nonce}
	p.dir = "outputs/" + sessionID + "/.rundesk-probes/" + uid()
	p.requestPath, p.responsePath = p.dir+"/request.json", p.dir+"/response.json"
	if err = diagnosticMkdir(root, path.Dir(p.dir)); err == nil {
		err = root.Mkdir(p.dir, 0700)
	}
	if err != nil {
		root.Close()
		return nil, err
	}
	p.owned, err = root.OpenRoot(p.dir)
	if err == nil {
		p.ownedInfo, err = p.owned.Stat(".")
	}
	if err != nil {
		p.close()
		return nil, err
	}
	request, _ := json.Marshal(struct {
		SchemaVersion int    `json:"schemaVersion"`
		Nonce         string `json:"nonce"`
	}{1, p.nonce})
	p.requestSHA256 = hashBytes(request)
	f, err := p.owned.OpenFile("request.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		_, err = f.Write(request)
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	}
	if err != nil {
		p.close()
		return nil, err
	}
	return p, nil
}
func (p *runtimeProbe) close() {
	if p == nil || p.root == nil {
		return
	}
	// The pinned directory handle avoids removing another directory's files if
	// a peer renames/replaces an ancestor while the network probe is running.
	if p.owned != nil {
		_ = p.owned.Remove("request.json")
		_ = p.owned.Remove("response.json")
		_ = p.owned.Close()
	}
	if st, err := p.root.Lstat(p.dir); err == nil && p.ownedInfo != nil && os.SameFile(st, p.ownedInfo) {
		_ = p.root.Remove(p.dir)
	}
	_ = p.root.Close()
}
func (p *runtimeProbe) verify(remote runDeskDiagnosticResponse) error {
	c := remote.Challenge
	if c.Status != "verified" || c.Path != p.requestPath || c.ResponsePath != p.responsePath || c.Nonce != p.nonce || c.RequestSHA256 != p.requestSHA256 {
		return errors.New("容器返回的共享目录挑战与本次请求不一致")
	}
	f, err := exchangeOpen(p.root, p.responsePath, 16<<10)
	if err != nil {
		return errors.New("App 未读到容器写回的普通响应文件；请核对两个容器是否挂载同一物理目录及读写权限")
	}
	defer f.Close()
	var response struct {
		SchemaVersion int    `json:"schemaVersion"`
		Nonce         string `json:"nonce"`
		RequestSHA256 string `json:"requestSHA256"`
	}
	d := json.NewDecoder(io.LimitReader(f, (16<<10)+1))
	if err = d.Decode(&response); err != nil {
		return errors.New("共享目录写回文件不是有效 JSON")
	}
	var extra any
	if err = d.Decode(&extra); err != io.EOF {
		return errors.New("共享目录写回文件包含多余内容")
	}
	if response.SchemaVersion != 1 || response.Nonce != p.nonce || response.RequestSHA256 != p.requestSHA256 {
		return errors.New("共享目录写回凭证与本次挑战不一致")
	}
	// The request must also remain intact; a response to a replaced request does
	// not prove the original App input crossed the mount boundary.
	f, err = exchangeOpen(p.root, p.requestPath, 4<<10)
	if err != nil {
		return errors.New("诊断请求文件已改变或不可读")
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, (4<<10)+1))
	if err != nil || hashBytes(b) != p.requestSHA256 {
		return errors.New("诊断请求文件指纹已改变")
	}
	return nil
}

func (a *App) diagnoseRuntime(ctx context.Context, projectID string) (RuntimeDiagnostics, error) {
	p, err := a.get(projectID)
	if err != nil {
		return RuntimeDiagnostics{}, err
	}
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	backend := workflowFor(p).EditingBackend
	out := RuntimeDiagnostics{Schema: "video.runtime-diagnostics.v1", ProjectID: projectID, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), EditingBackend: backend, Ready: true, Binding: RuntimeDiagnosticBinding{InstanceID: cfg.InstanceID, WorkspaceID: p.WorkspaceID, SessionID: p.SessionID}, Rows: []RuntimeDiagnosticRow{}}
	add := func(id, title, state, summary, code, remedy string, required bool) {
		out.Rows = append(out.Rows, RuntimeDiagnosticRow{id, title, state, summary, code, remedy})
		if required && state != "passed" {
			out.Ready = false
		}
	}
	missing := missingCommands("ffmpeg", "ffprobe")
	if len(missing) == 0 {
		add("app_media_tools", "App 媒体工具", "passed", "已找到 FFmpeg 和 ffprobe；未运行媒体制作", "", "", true)
	} else {
		add("app_media_tools", "App 媒体工具", "blocked", strings.Join(missing, "；"), "app_dependencies_missing", "在 Video App 运行环境安装 FFmpeg 和 ffprobe", true)
	}
	add("model_vision", "模型视觉理解", "not_checked", "本诊断不调用模型，未验证模型是否理解图片；工具返回图片与模型看懂图片分别验收", "model_not_tested", "实际制作时让 Codex 描述测试图片，并核对描述与原图", false)
	add("generation_services", "图片与视频生成", "not_checked", "生成服务按需调用；本诊断不发起生成或计费请求", "optional_services_not_called", "需要生成素材时再检查对应服务配置与真实任务", false)
	if cfg.InstanceID == "" || p.SessionID == "" || p.WorkspaceID == "" || !exchangeSessionID.MatchString(p.SessionID) {
		add("binding", "RunDesk 项目绑定", "blocked", "项目尚未建立有效的 RunDesk 会话", "connect_required", "先连接 RunDesk 并为当前项目建立会话，再运行诊断", true)
		return out, nil
	}
	if cfg.WorkspaceID != "" && cfg.WorkspaceID != p.WorkspaceID {
		add("binding", "RunDesk 项目绑定", "failed", "项目工作区与当前应用绑定不一致", "binding_mismatch", "恢复原 RunDesk 连接，或核对后为项目建立新会话", true)
		return out, nil
	}
	v, err := a.runtime(ctx)
	if err != nil {
		add("binding", "RunDesk 项目绑定", "failed", err.Error(), "runtime_unavailable", "核对 RunDesk 地址、应用 Token 和当前实例绑定", true)
		return out, nil
	}
	out.Binding.ExecutionMode = v.ExecutionMode
	var session struct {
		InstanceID    string `json:"instanceId"`
		WorkspaceID   string `json:"workspaceId"`
		ExecutionMode string `json:"executionMode"`
	}
	if err = a.rdCall(ctx, "GET", "/sessions/"+p.SessionID, nil, &session); err != nil || session.InstanceID != cfg.InstanceID || session.WorkspaceID != p.WorkspaceID || session.ExecutionMode != v.ExecutionMode {
		add("binding", "RunDesk 项目绑定", "failed", "原会话与当前实例、工作区或运行方式不一致，或无法读取", "session_binding_mismatch", "在 RunDesk 核对原会话；切换环境后为项目建立新会话", true)
		return out, nil
	}
	add("binding", "RunDesk 项目绑定", "passed", "当前项目会话、实例与工作区一致", "", "", true)
	if v.ExecutionMode == "docker" && (v.Environment == nil || v.Environment.State != "running" || v.Environment.PendingUpdate) {
		add("agent_runtime", "Agent 运行环境", "blocked", "Docker 环境未运行或有待应用的镜像更新", "environment_not_ready", "在 RunDesk 准备运行环境；重建镜像后应用环境更新，再运行诊断", true)
	} else if v.ExecutionMode == "local" || v.ExecutionMode == "docker" {
		add("agent_runtime", "Agent 运行环境", "passed", "已确认当前运行方式为 "+v.ExecutionMode, "", "", true)
	} else {
		add("agent_runtime", "Agent 运行环境", "failed", "不支持的运行方式", "execution_mode_unsupported", "选择 RunDesk 支持的运行环境", true)
	}
	// GET /runtime is cached binding/environment metadata, not an account or
	// MCP probe. Only the new read-only diagnostic POST supplies this evidence.
	var observedProbe *runDeskDiagnosticResponse
	if backend != "video_use" {
		add("video_use_runtime", "video-use 运行时", "not_checked", "当前后端通过 App MCP 制作，无需 video-use 运行时", "backend_not_selected", "", false)
		add("shared_workspace", "共享目录双向读写", "not_checked", "当前后端通过 App MCP 操作素材，无需共享目录", "backend_not_selected", "", false)
	}
	if v.ExecutionMode != "docker" || v.Environment == nil || v.Environment.State != "running" || v.Environment.PendingUpdate {
		add("mcp_image_transport", "MCP 图片传输", "not_checked", "尚未进入可诊断的 Docker 环境，未检查容器接收 PNG", "docker_environment_required", "准备 Docker 运行环境后重新诊断；本机模式需另行验收图片传递", v.ExecutionMode == "docker" || backend == "video_use")
		if backend == "video_use" {
			add("video_use_runtime", "video-use 运行时", "blocked", "video-use 需要已准备的 Docker video-build 环境", "docker_environment_required", "选择 video-build 1.3.0，或带 --with-video-use 的 1.4.0 镜像", true)
			add("shared_workspace", "共享目录双向读写", "blocked", "运行环境尚未就绪，未执行跨容器读写", "docker_environment_required", "准备运行环境后重新诊断", true)
		}
	} else {
		nonce := uid()
		request := map[string]any{"instanceId": cfg.InstanceID, "workspaceId": p.WorkspaceID, "nonce": nonce, "mcpImageServer": "video_project"}
		var probe *runtimeProbe
		if backend == "video_use" {
			probe, err = newRuntimeProbe(p.SessionID, nonce)
			if err != nil {
				add("shared_workspace", "共享目录双向读写", "blocked", err.Error(), "exchange_unavailable", "设置 VIDEO_EXCHANGE_ROOT 并挂载与 RunDesk 应用工作区相同的物理目录，核对读写权限", true)
			} else {
				defer probe.close()
				request["challenge"] = map[string]string{"path": probe.requestPath, "nonce": probe.nonce}
			}
		}
		var remote runDeskDiagnosticResponse
		step, cancel := context.WithTimeout(ctx, 35*time.Second)
		err = a.rdCall(step, "POST", "/applications/rundesk-video-app/runtime/diagnostics", request, &remote)
		cancel()
		if err != nil {
			code, summary, remedy := "runtime_diagnostic_failed", err.Error(), "核对 RunDesk 运行环境和共享目录挂载"
			var rdErr *RunDeskError
			if errors.As(err, &rdErr) && rdErr.Status == 404 {
				code, summary, remedy = "rundesk_upgrade_required", "RunDesk 尚未提供运行环境诊断接口", "升级 RunDesk，并准备带 video-use 的 video-build 1.4.0 镜像"
			}
			add("mcp_image_transport", "MCP 图片传输", "blocked", summary, code, remedy, true)
			if backend == "video_use" {
				add("video_use_runtime", "video-use 运行时", "blocked", summary, code, remedy, true)
				if probe != nil {
					add("shared_workspace", "共享目录双向读写", "blocked", "未取得容器挑战响应，未验证共享挂载", code, remedy, true)
				}
			}
		} else if remote.SchemaVersion != 1 || remote.AppID != "rundesk-video-app" || remote.InstanceID != cfg.InstanceID || remote.WorkspaceID != p.WorkspaceID || remote.ExecutionMode != "docker" {
			add("mcp_image_transport", "MCP 图片传输", "failed", "诊断返回的版本、实例或工作区不匹配", "diagnostic_binding_mismatch", "恢复当前绑定后重新诊断", true)
			if backend == "video_use" {
				add("video_use_runtime", "video-use 运行时", "failed", "未接受其他绑定的运行时结果", "diagnostic_binding_mismatch", "核对 RunDesk 实例和工作区", true)
				if probe != nil {
					add("shared_workspace", "共享目录双向读写", "failed", "未接受其他绑定的挑战响应", "diagnostic_binding_mismatch", "核对 RunDesk 实例和工作区", true)
				}
			}
		} else {
			observedProbe = &remote
			out.Runtime = &remote.Runtime
			imageBytes, imageErr := diagnosticImage(nonce)
			imageResult := remote.ImageTransport
			if imageErr == nil && imageResult.State == "passed" && imageResult.Nonce == nonce && imageResult.ImageSHA256 == hashBytes(imageBytes) && imageResult.MIMEType == "image/png" && imageResult.Width == 32 && imageResult.Height == 32 {
				add("mcp_image_transport", "MCP 图片传输", "passed", "容器通过当前 MCP 配置收到本次随机挑战的真实 PNG，像素载荷指纹已核对；未调用模型", "", "", true)
			} else {
				state, code := "failed", "mcp_image_payload_mismatch"
				if imageResult.State == "not_checked" {
					state, code = "not_checked", "mcp_image_transport_unavailable"
				}
				add("mcp_image_transport", "MCP 图片传输", state, "未验证容器通过 MCP 收到本次正确 PNG；工具名称存在不代表图片载荷可用", code, "核对 App/RunDesk 版本、容器 MCP 地址与 Token，并重新准备运行环境", true)
			}
			if backend == "video_use" {
				rt := remote.Runtime
				checksReady := true
				incomplete := []string{}
				for _, name := range []string{"profile", "ffmpeg", "ffprobe", "pythonDependencies", "adapter", "sourcePin"} {
					checksReady = checksReady && rt.Checks[name] == "ready"
					if rt.Checks[name] != "ready" {
						incomplete = append(incomplete, name)
					}
				}
				profileSupported := rt.ProfileVersion == "1.3.0" || rt.ProfileVersion == "1.4.0"
				if rt.Status == "ready" && checksReady && rt.ProfileID == "video-build" && profileSupported && rt.AdapterContractVersion == 2 && rt.VideoUseCommit == exchangeVideoUseCommit && rt.PythonVersion != "" && rt.FFmpegVersion != "" && rt.FFprobeVersion != "" {
					add("video_use_runtime", "video-use 运行时", "passed", "已核对 video-build "+rt.ProfileVersion+"、适配器 v2、固定上游版本、Python 依赖及 FFmpeg/ffprobe", "", "", true)
				} else {
					summary := fmt.Sprintf("实际 profile=%s %s，适配器=%d；运行时与所需版本或依赖不一致", rt.ProfileID, rt.ProfileVersion, rt.AdapterContractVersion)
					if len(incomplete) > 0 {
						summary += "；未通过：" + strings.Join(incomplete, "、")
					}
					add("video_use_runtime", "video-use 运行时", "blocked", summary, "video_use_runtime_mismatch", "以 --with-video-use 重建 video-build 1.4.0，并在 RunDesk 应用镜像更新；仅修改镜像标签无效", true)
				}
				if probe != nil {
					if err = probe.verify(remote); err != nil {
						add("shared_workspace", "共享目录双向读写", "failed", err.Error(), "exchange_challenge_failed", "两个容器必须挂载同一物理工作目录，并允许当前运行用户读写", true)
					} else {
						add("shared_workspace", "共享目录双向读写", "passed", "App 写入的随机挑战被容器读取，容器写回的凭证已由 App 校验", "", "", true)
					}
				}
			}
		}
	}
	if observedProbe == nil || !observedProbe.Probed {
		add("model_auth", "模型认证配置", "not_checked", "本次未完成认证配置只读探测；环境元数据不能证明认证就绪", "model_auth_not_probed", "在 RunDesk 准备运行环境以建立配置客户端后重新诊断", true)
		add("mcp_tools", "MCP 工具发现", "not_checked", "本次未完成 MCP 状态只读探测；未使用缓存元数据代替探测", "mcp_not_probed", "准备运行环境后重新诊断，并核对 MCP 回调地址与 Token", true)
	} else {
		if observedProbe.AccountReady && observedProbe.Errors["account"] == "" {
			add("model_auth", "模型认证配置", "passed", "RunDesk 通过 account/read 只读检查认证配置；未发送模型请求", "", "", true)
		} else {
			add("model_auth", "模型认证配置", "blocked", "本次认证配置只读探测未通过", "model_auth_not_ready", "在 RunDesk 完成认证并重新诊断", true)
		}
		available := map[string]bool{}
		for _, name := range observedProbe.Servers["video_project"] {
			available[name] = true
		}
		missingTools := []string{}
		for _, name := range videoTools {
			if !available[name] {
				missingTools = append(missingTools, name)
			}
		}
		if len(missingTools) == 0 && observedProbe.Errors["mcp"] == "" {
			add("mcp_tools", "MCP 工具发现", "passed", "RunDesk 通过 mcpServerStatus/list 读取到所需 Video App 工具；模型理解另行验收", "", "", true)
		} else {
			add("mcp_tools", "MCP 工具发现", "blocked", fmt.Sprintf("本次 MCP 状态探测未完整通过，缺少 %d 项工具或存在 MCP 错误", len(missingTools)), "mcp_probe_incomplete", "核对回调地址、Token 与 App 版本，然后重新准备运行环境", true)
		}
	}
	// No global lock covers the network. Reject observations made while the user
	// reconnected or switched the selected backend/session.
	a.mu.Lock()
	current, exists := a.projects[projectID]
	unchanged := exists && current.SessionID == p.SessionID && current.WorkspaceID == p.WorkspaceID && workflowFor(*current).EditingBackend == backend && a.cfg.InstanceID == cfg.InstanceID && a.cfg.WorkspaceID == cfg.WorkspaceID && a.cfg.RunDeskURL == cfg.RunDeskURL
	a.mu.Unlock()
	if !unchanged {
		add("binding_stability", "诊断期间绑定", "failed", "诊断期间项目会话、后端或应用绑定已改变；本次结果不可用于制作预检", "binding_changed", "等待连接操作结束后重新诊断", true)
	}
	return out, nil
}

func (a *App) runtimeDiagnose(w http.ResponseWriter, r *http.Request) {
	var in struct{}
	if !decodeStrict(w, r, &in) {
		return
	}
	v, err := a.diagnoseRuntime(r.Context(), r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 200, v)
}
