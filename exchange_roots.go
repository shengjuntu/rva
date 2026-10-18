package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Paths stay private; only a digest of the canonical path and opened directory
// identity is saved in production receipts. Different workspace mappings cannot
// alias a directory or overlap through parents, symlinks, or bind mounts.
type exchangeRootSelection struct {
	Path        string
	Fingerprint string
	Info        os.FileInfo
	Ancestors   []os.FileInfo
}

func normalizedExchangeRoot(raw string, absolute bool) (exchangeRootSelection, error) {
	var selected exchangeRootSelection
	if raw == "" || strings.TrimSpace(raw) != raw || absolute && !filepath.IsAbs(raw) {
		return selected, errors.New("交换目录必须为已挂载的绝对目录")
	}
	path, err := filepath.Abs(raw)
	if err != nil {
		return selected, errors.New("交换目录路径无效")
	}
	path, err = filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return selected, errors.New("无法解析已配置交换目录，请核对挂载")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return selected, errors.New("无法打开已配置交换目录")
	}
	defer root.Close()
	f, err := root.Open(".")
	if err != nil {
		return selected, errors.New("无法核对交换目录身份")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.IsDir() {
		return selected, errors.New("交换目录不是有效目录")
	}
	identity, err := exchangeDirectoryIdentity(f)
	if err != nil {
		return selected, errors.New("无法确定交换目录的文件系统身份")
	}
	var ancestors []os.FileInfo
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		st, err := os.Stat(parent)
		if err != nil || !st.IsDir() {
			return selected, errors.New("无法核对交换目录祖先身份，不能保证工作区挂载相互独立")
		}
		ancestors = append(ancestors, st)
		if filepath.Dir(parent) == parent {
			break
		}
	}
	return exchangeRootSelection{Path: path, Fingerprint: hashBytes([]byte(path + "\x00" + identity)), Info: info, Ancestors: ancestors}, nil
}
func exchangeRootsOverlap(a, b exchangeRootSelection) bool {
	if os.SameFile(a.Info, b.Info) {
		return true
	}
	// A nested bind alias may have no lexical relationship to the other root.
	// Compare physical ancestor identities in both directions as well.
	for _, ancestor := range a.Ancestors {
		if os.SameFile(ancestor, b.Info) {
			return true
		}
	}
	for _, ancestor := range b.Ancestors {
		if os.SameFile(ancestor, a.Info) {
			return true
		}
	}
	for _, pair := range [][2]string{{a.Path, b.Path}, {b.Path, a.Path}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && (rel == "." || rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)) {
			return true
		}
	}
	return false
}
func configuredExchangeRoots() (*exchangeRootSelection, map[string]exchangeRootSelection, error) {
	var legacy *exchangeRootSelection
	if raw := strings.TrimSpace(os.Getenv("VIDEO_EXCHANGE_ROOT")); raw != "" {
		selected, err := normalizedExchangeRoot(raw, false)
		if err != nil {
			return nil, nil, err
		}
		legacy = &selected
	}
	roots := map[string]exchangeRootSelection{}
	raw := strings.TrimSpace(os.Getenv("VIDEO_EXCHANGE_ROOTS"))
	if raw != "" {
		if len(raw) > 64<<10 {
			return nil, nil, errors.New("VIDEO_EXCHANGE_ROOTS配置过大")
		}
		decoder := json.NewDecoder(strings.NewReader(raw))
		token, err := decoder.Token()
		if err != nil || token != json.Delim('{') {
			return nil, nil, errors.New("VIDEO_EXCHANGE_ROOTS必须是workspaceId到绝对挂载目录的JSON对象")
		}
		for decoder.More() {
			token, err = decoder.Token()
			key, ok := token.(string)
			if err != nil || !ok || key == "" || len(key) > 128 || strings.TrimSpace(key) != key {
				return nil, nil, errors.New("VIDEO_EXCHANGE_ROOTS的workspaceId无效")
			}
			if _, exists := roots[key]; exists {
				return nil, nil, errors.New("VIDEO_EXCHANGE_ROOTS包含重复workspaceId")
			}
			var value string
			if err = decoder.Decode(&value); err != nil {
				return nil, nil, errors.New("VIDEO_EXCHANGE_ROOTS目录必须为字符串")
			}
			selected, err := normalizedExchangeRoot(value, true)
			if err != nil {
				return nil, nil, err
			}
			roots[key] = selected
			if len(roots) > 64 {
				return nil, nil, errors.New("VIDEO_EXCHANGE_ROOTS最多支持64个工作区")
			}
		}
		if _, err = decoder.Token(); err != nil {
			return nil, nil, errors.New("VIDEO_EXCHANGE_ROOTS不是完整JSON对象")
		}
		var extra any
		if err = decoder.Decode(&extra); err != io.EOF {
			return nil, nil, errors.New("VIDEO_EXCHANGE_ROOTS包含多余内容")
		}
	}
	keys := make([]string, 0, len(roots))
	for key := range roots {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for i, key := range keys {
		selected := roots[key]
		if legacy != nil && exchangeRootsOverlap(*legacy, selected) {
			return nil, nil, errors.New("VIDEO_EXCHANGE_ROOTS不得与legacy VIDEO_EXCHANGE_ROOT相同或存在父子/别名重叠")
		}
		for _, other := range keys[:i] {
			if exchangeRootsOverlap(roots[other], selected) {
				return nil, nil, errors.New("不同workspaceId的交换根目录必须相互独立，不得相同、父子或别名重叠")
			}
		}
	}
	return legacy, roots, nil
}
func exchangeSelectionForProject(p Project) (exchangeRootSelection, error) {
	legacy, roots, err := configuredExchangeRoots()
	if err != nil {
		return exchangeRootSelection{}, err
	}
	if p.RuntimeBinding != nil && p.RuntimeBinding.PresetID != "" {
		if p.RuntimeBinding.WorkspaceID == "" || p.WorkspaceID != p.RuntimeBinding.WorkspaceID {
			return exchangeRootSelection{}, errors.New("项目预设与工作区绑定不一致，未选择共享目录")
		}
		if selected, ok := roots[p.RuntimeBinding.WorkspaceID]; ok {
			return selected, nil
		}
		return exchangeRootSelection{}, errors.New("该制作环境未配置精确workspaceId对应的VIDEO_EXCHANGE_ROOTS挂载；不会回退到legacy目录")
	}
	if p.ProductionProfile != nil && *p.ProductionProfile != "" && p.RuntimeBinding == nil {
		return exchangeRootSelection{}, errors.New("预设项目尚未建立可信工作区绑定，不能选择legacy交换目录")
	}
	if legacy == nil {
		return exchangeRootSelection{}, errors.New("未配置legacy VIDEO_EXCHANGE_ROOT；请挂载原RunDesk应用工作目录")
	}
	return *legacy, nil
}
func openSelectedExchangeRoot(selected exchangeRootSelection) (*os.Root, error) {
	root, err := os.OpenRoot(selected.Path)
	if err != nil {
		return nil, errors.New("无法打开已配置交换目录")
	}
	info, err := root.Stat(".")
	if err != nil || !os.SameFile(info, selected.Info) {
		root.Close()
		return nil, errors.New("交换目录身份已改变，请重新核对挂载")
	}
	return root, nil
}
func exchangeSelectionMatchesRun(p Project, run ProductionRun, selected exchangeRootSelection) error {
	preset := ""
	workspace := p.WorkspaceID
	if p.RuntimeBinding != nil {
		preset = p.RuntimeBinding.PresetID
		workspace = p.RuntimeBinding.WorkspaceID
	}
	if run.ExchangeRootFingerprint == "" {
		if preset != "" || run.PresetID != "" {
			return errors.New("该预设制作运行缺少冻结的交换目录证明，不能读取共享产物")
		}
		return nil // Old legacy receipts predate directory identity recording.
	}
	if run.PresetID != preset || run.WorkspaceID != workspace || run.ExchangeRootFingerprint != selected.Fingerprint {
		return errors.New("制作运行的预设、工作区或共享根目录映射已改变，未读取另一共享目录；历史私有归档仍可查看")
	}
	return nil
}
func exchangeRunBinding(p Project, selected exchangeRootSelection) (string, string) {
	if p.RuntimeBinding != nil {
		return p.RuntimeBinding.PresetID, p.RuntimeBinding.WorkspaceID
	}
	return "", p.WorkspaceID
}

// A production MCP tool independently verifies the chosen physical workspace.
// It does not assume an earlier chat preflight authorized/exported this project.
func (a *App) verifyPresetExchange(ctx context.Context, p Project, root *os.Root) error {
	binding := a.projectRuntimeBinding(&p)
	if binding.PresetID == "" {
		return nil
	}
	v, err := a.runtimeForProject(ctx, &p)
	if err != nil {
		return err
	}
	if v.ExecutionMode != "docker" || v.Environment == nil || v.Environment.State != "running" || v.Environment.PendingUpdate {
		return errors.New("预设共享目录验证需要已准备的当前Docker环境")
	}
	var session struct {
		InstanceID  string `json:"instanceId"`
		WorkspaceID string `json:"workspaceId"`
		Source      struct {
			AppID    string `json:"appId"`
			TaskID   string `json:"taskId"`
			PresetID string `json:"presetId"`
		} `json:"source"`
	}
	if err = a.rdCall(ctx, "GET", "/sessions/"+p.SessionID, nil, &session); err != nil {
		return err
	}
	if session.InstanceID != binding.InstanceID || session.WorkspaceID != binding.WorkspaceID || session.Source.PresetID != binding.PresetID || session.Source.AppID != "rundesk-video-app" || session.Source.TaskID != p.ID {
		return errors.New("共享目录导出前未确认该会话属于当前项目及批准的预设工作区")
	}
	duplicate, err := root.OpenRoot(".")
	if err != nil {
		return errors.New("无法固定共享目录挑战根")
	}
	nonce := uid()
	probe, err := newRuntimeProbeRoot(duplicate, p.SessionID, nonce)
	if err != nil {
		return err
	}
	defer probe.close()
	request := map[string]any{"presetId": binding.PresetID, "instanceId": binding.InstanceID, "workspaceId": binding.WorkspaceID, "nonce": nonce, "mcpImageServer": "video_project", "challenge": map[string]string{"path": probe.requestPath, "nonce": nonce}}
	var remote runDeskDiagnosticResponse
	step, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	if err = a.rdCall(step, "POST", "/applications/rundesk-video-app/runtime/diagnostics", request, &remote); err != nil {
		return err
	}
	if remote.SchemaVersion != 1 || remote.AppID != "rundesk-video-app" || remote.PresetID != binding.PresetID || remote.InstanceID != binding.InstanceID || remote.WorkspaceID != binding.WorkspaceID || remote.ExecutionMode != "docker" {
		return errors.New("共享目录挑战返回的预设或工作区不匹配")
	}
	return probe.verify(remote)
}
