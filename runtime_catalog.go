package main

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// RuntimeProfileChoice is an application-scoped projection. It never contains
// a host path, account response, raw instance configuration or service token.
// Selectable means an existing binding may be selected for a new project; it
// does not establish model authentication, MCP readiness or production quality.
type RuntimeProfileChoice struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Enabled     bool   `json:"enabled"`
	IsDefault   bool   `json:"isDefault"`
	InstanceID  string `json:"instanceId,omitempty"`
	WorkspaceID string `json:"workspaceId,omitempty"`
	State       string `json:"state"`
	Selectable  bool   `json:"selectable"`
	Message     string `json:"message,omitempty"`
}

type RuntimeProfileCatalog struct {
	Items          []RuntimeProfileChoice `json:"items"`
	DefaultProfile string                 `json:"defaultProfile"`
	RunDeskURL     string                 `json:"rundeskURL,omitempty"`
	Warnings       []string               `json:"warnings"`
}

type runtimeProfileMetadata struct {
	ID          string `json:"id"`
	AppID       string `json:"appId"`
	Name        string `json:"name"`
	Description string `json:"description"`
	InstanceID  string `json:"instanceId"`
	WorkspaceID string `json:"workspaceId"`
	Enabled     bool   `json:"enabled"`
}

type catalogRuntimeView struct {
	runtimeView
	PresetEnabled *bool `json:"presetEnabled"`
}

var catalogBearer = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/-]+=*`)
var catalogURLCredentials = regexp.MustCompile(`(https?://)[^\s/@]+:[^\s/@]+@`)
var catalogCredentialID = regexp.MustCompile(`^(?:rd_(?:app|user|browser)_[A-Za-z0-9._-]{16,}|github_pat_[A-Za-z0-9_]{20,}|gh[pousr]_[A-Za-z0-9]{20,}|sk-(?:proj-|svcacct-)?[A-Za-z0-9_-]{16,})$`)

func (a *App) catalogOpaqueID(id string) bool {
	if id == "" || !validProductionProfile(id) || catalogCredentialID.MatchString(id) {
		return false
	}
	for _, secret := range []string{a.rdToken, a.token} {
		if secret != "" && strings.Contains(id, secret) {
			return false
		}
	}
	return true
}

func (a *App) catalogText(value string, limit int) string {
	// Metadata is deliberately selected, then bounded and scrubbed again. Even
	// an upstream error or name accidentally echoing this connection's token
	// must not expose it to the browser.
	for _, secret := range []string{a.rdToken, a.token} {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[redacted]")
		}
	}
	value = catalogBearer.ReplaceAllString(value, "Bearer [redacted]")
	value = catalogURLCredentials.ReplaceAllString(value, "${1}[redacted]@")
	value = strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' && r != '\t' || r == 127 {
			return -1
		}
		return r
	}, strings.TrimSpace(value))
	runes := []rune(value)
	if len(runes) > limit {
		value = string(runes[:limit])
	}
	return value
}

func catalogRunDeskURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	// The UI needs the workbench origin, never authentication or query data.
	return (&url.URL{Scheme: u.Scheme, Host: u.Host}).String()
}

func catalogFailure(err error, subject string) error {
	out := &RunDeskError{Status: 502, Code: "runtime_profiles_unavailable", Message: "暂时无法查询 RunDesk 制作环境，请刷新重试或打开 RunDesk 检查连接。"}
	var remote *RunDeskError
	if errors.As(err, &remote) {
		switch {
		case remote.Code == "backend_changed":
			out.Status, out.Code, out.Message = 409, "backend_changed", "应用已绑定原 RunDesk 地址，请恢复原连接后查询制作环境。"
		case remote.Status == http.StatusUnauthorized:
			out.Code, out.Message = "runtime_profiles_authentication_failed", "RunDesk 凭据无效或已过期，请更新服务连接配置。"
		case remote.Status == http.StatusForbidden:
			out.Status, out.Code, out.Message = 403, "runtime_profiles_forbidden", "当前 RunDesk 凭据没有查询此视频应用制作环境的权限，请在 RunDesk 核对授权。"
		case remote.Status == http.StatusNotFound:
			out.Status, out.Code, out.Message = 409, "runtime_profiles_unregistered", "RunDesk 中尚未登记此视频应用的制作环境，请先在 RunDesk 配置。"
		case remote.Code == "invalid_response":
			out.Code, out.Message = "runtime_profiles_invalid_response", "RunDesk 返回的"+subject+"格式无效，请核对服务版本。"
		}
	}
	return out
}

func unsupportedRuntimeCatalog(message string) error {
	return &RunDeskError{Status: 409, Code: "production_profile_unsupported", Message: message}
}

func (a *App) runtimeProfileCatalog(ctx context.Context) (RuntimeProfileCatalog, error) {
	a.mu.Lock()
	configuredDefault := a.productionProfile
	a.mu.Unlock()
	out := RuntimeProfileCatalog{Items: []RuntimeProfileChoice{}, RunDeskURL: catalogRunDeskURL(a.rd), Warnings: []string{}}
	if configuredDefault != "" && !a.catalogOpaqueID(configuredDefault) {
		return out, &RunDeskError{Status: 409, Code: "runtime_profiles_invalid_default", Message: "新项目默认环境的标识无效，请在服务配置中核对。"}
	}
	out.DefaultProfile = configuredDefault
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var meta struct {
		APIVersions  []string `json:"apiVersions"`
		Capabilities []string `json:"capabilities"`
	}
	if err := a.rdCall(ctx, "GET", "/meta", nil, &meta); err != nil {
		return out, catalogFailure(err, "能力信息")
	}
	has := func(items []string, value string) bool {
		for _, item := range items {
			if item == value {
				return true
			}
		}
		return false
	}
	if !has(meta.APIVersions, "v1") {
		return out, unsupportedRuntimeCatalog("当前 RunDesk 不支持 API v1，请先升级并配置制作环境。")
	}
	for _, capability := range []string{"idempotency", "configuration-summary", "application-connect", "application-runtime"} {
		if !has(meta.Capabilities, capability) {
			return out, unsupportedRuntimeCatalog("当前 RunDesk 缺少视频应用所需的运行接口，请先升级。")
		}
	}
	presets := []runtimeProfileMetadata{}
	if has(meta.Capabilities, "application-runtime-presets") {
		// RunDesk applies the current credential's exact application/preset
		// grants here. Never replace this with the global instance/image list.
		if err := a.rdCall(ctx, "GET", "/applications/rundesk-video-app/runtime-presets", nil, &presets); err != nil {
			return out, catalogFailure(err, "制作环境列表")
		}
		if presets == nil || len(presets) > 1000 {
			return out, &RunDeskError{Status: 502, Code: "runtime_profiles_invalid_response", Message: "RunDesk 制作环境列表为空值或超出范围，请核对服务版本。"}
		}
	} else if configuredDefault != "" {
		return out, unsupportedRuntimeCatalog("当前 RunDesk 不支持已配置的命名制作环境，请升级；未切换到原默认环境。")
	} else {
		out.Warnings = append(out.Warnings, "当前 RunDesk 版本只支持原默认环境；升级后可查询多个制作环境。")
	}
	seen := map[string]bool{"": true}
	out.Items = append(out.Items, RuntimeProfileChoice{ID: "", Name: "应用默认环境", Description: "此应用在 RunDesk 中的默认绑定；额外预设列表为空时仍可独立选择", Enabled: true, IsDefault: configuredDefault == "", State: "unknown"})
	for _, preset := range presets {
		if !a.catalogOpaqueID(preset.ID) || preset.AppID != "rundesk-video-app" || !a.catalogOpaqueID(preset.InstanceID) || !a.catalogOpaqueID(preset.WorkspaceID) || seen[preset.ID] {
			return out, &RunDeskError{Status: 502, Code: "runtime_profiles_invalid_response", Message: "RunDesk 制作环境列表包含无效或重复绑定，请刷新并核对。"}
		}
		seen[preset.ID] = true
		name := a.catalogText(preset.Name, 160)
		if name == "" {
			name = preset.ID
		}
		out.Items = append(out.Items, RuntimeProfileChoice{ID: preset.ID, Name: name, Description: a.catalogText(preset.Description, 4096), Enabled: preset.Enabled, IsDefault: preset.ID == configuredDefault, InstanceID: preset.InstanceID, WorkspaceID: preset.WorkspaceID, State: "unknown"})
	}
	if !seen[configuredDefault] {
		out.Items = append(out.Items, RuntimeProfileChoice{ID: configuredDefault, Name: "已配置的默认环境（不可用）", IsDefault: true, State: "missing", Message: "当前凭据未返回这个默认环境，可能尚未登记或未获授权；请在 RunDesk 核对。"})
		out.Warnings = append(out.Warnings, "新项目默认环境不在可访问列表中；请选择可用环境或在 RunDesk 修复默认配置。")
	}
	// Read-only runtime views report saved environment state without Docker
	// starts, model calls, configuration probes or default-connect mutations.
	var wg sync.WaitGroup
	jobs := make(chan int)
	workers := 4
	if len(out.Items) < workers {
		workers = len(out.Items)
	}
	for n := 0; n < workers; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				a.resolveRuntimeChoice(ctx, &out.Items[index])
			}
		}()
	}
	for index := range out.Items {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	return out, nil
}

func (a *App) resolveRuntimeChoice(ctx context.Context, choice *RuntimeProfileChoice) {
	if choice.State == "missing" {
		return
	}
	if !choice.Enabled {
		choice.State, choice.Selectable, choice.Message = "disabled", false, "此制作环境已停用，不能用于新项目；原项目历史保留。"
		return
	}
	path := "/applications/rundesk-video-app/runtime"
	if choice.ID != "" {
		path += "?presetId=" + url.QueryEscape(choice.ID)
	}
	var runtime catalogRuntimeView
	if err := a.rdCall(ctx, "GET", path, nil, &runtime); err != nil {
		choice.State, choice.Selectable = "unavailable", false
		var remote *RunDeskError
		if errors.As(err, &remote) && remote.Status == http.StatusForbidden {
			choice.Message = "当前凭据没有访问此制作环境的权限。"
		} else if errors.As(err, &remote) && remote.Status == http.StatusNotFound {
			choice.Message = "此制作环境尚未登记，请在 RunDesk 配置。"
		} else {
			choice.Message = "暂时无法读取此制作环境的状态，请刷新或在 RunDesk 检查。"
		}
		return
	}
	if runtime.AppID != "rundesk-video-app" || runtime.PresetID != choice.ID || !a.catalogOpaqueID(runtime.InstanceID) || !a.catalogOpaqueID(runtime.WorkspaceID) || choice.InstanceID != "" && choice.InstanceID != runtime.InstanceID || choice.WorkspaceID != "" && choice.WorkspaceID != runtime.WorkspaceID {
		choice.State, choice.Selectable, choice.Message = "unavailable", false, "环境状态与登记绑定不一致，未选择其他环境；请在 RunDesk 核对。"
		return
	}
	choice.InstanceID, choice.WorkspaceID = runtime.InstanceID, runtime.WorkspaceID
	if runtime.PresetEnabled != nil && !*runtime.PresetEnabled {
		choice.Enabled, choice.State, choice.Selectable, choice.Message = false, "disabled", false, "此制作环境已停用，请刷新列表；原项目历史保留。"
		return
	}
	choice.Selectable = true
	switch runtime.ExecutionMode {
	case "local":
		if choice.ID != "" {
			choice.State, choice.Selectable, choice.Message = "unavailable", false, "命名制作环境已不符合专用 Docker 绑定，请在 RunDesk 核对。"
		} else {
			choice.State, choice.Message = "local", "本机执行环境；开始制作时仍会检查工具与连接。"
		}
	case "docker":
		switch {
		case runtime.Image == "":
			choice.State, choice.Selectable, choice.Message = "unavailable", false, "此环境尚未配置工具镜像，请在 RunDesk 配置。"
		case runtime.Environment == nil:
			choice.State, choice.Message = "not_started", "工具容器尚未启动，开始制作时按已配置环境准备。"
		case runtime.Environment.PendingUpdate:
			choice.State, choice.Selectable, choice.Message = "pending_update", false, "环境配置有待更新，请先在 RunDesk 更新环境。"
		default:
			switch runtime.Environment.State {
			case "running":
				choice.State, choice.Message = "running", "已记录容器运行；模型认证和工具将在开始制作时检查。"
			case "created", "not_created":
				choice.State, choice.Message = "not_started", "工具容器尚未启动，开始制作时按已配置环境准备。"
			case "exited", "stopped":
				choice.State, choice.Message = "stopped", "工具容器已停止，开始制作时按已配置环境准备。"
			default:
				choice.State, choice.Selectable, choice.Message = "unknown", false, "容器状态尚不能确认，请刷新或在 RunDesk 检查。"
			}
		}
	default:
		choice.State, choice.Selectable, choice.Message = "unavailable", false, "此环境的执行方式不可用，请在 RunDesk 核对。"
	}
}

func (a *App) runtimeProfiles(w http.ResponseWriter, r *http.Request) {
	catalog, err := a.runtimeProfileCatalog(r.Context())
	if err != nil {
		fail(w, 502, err)
		return
	}
	writeJSON(w, 200, catalog)
}
