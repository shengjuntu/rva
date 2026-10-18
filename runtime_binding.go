package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
)

// A nil binding is a project written by an older App. A non-nil binding with an
// empty PresetID intentionally retains RunDesk's legacy application environment.
// Startup defaults are used only when a project first connects.
type ProjectRuntimeBinding struct {
	PresetID         string `json:"presetId,omitempty"`
	InstanceID       string `json:"instanceId"`
	WorkspaceID      string `json:"workspaceId"`
	ManagedByRunDesk bool   `json:"managedByRunDesk"`
	SkillPath        string `json:"skillPath,omitempty"`
}

// Session creation receipts may outlive a restart or project rename. Reuse the
// original title/body, but never replay its receipt into another runtime pair.
func (a *App) validateSessionOperationBinding(scope string, binding ProjectRuntimeBinding) error {
	raw, err := os.ReadFile(a.operationFile(scope))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var op agentOperation
	var body struct {
		InstanceID  string `json:"instanceId"`
		WorkspaceID string `json:"workspaceId"`
		Source      struct {
			PresetID string `json:"presetId"`
		} `json:"source"`
	}
	if json.Unmarshal(raw, &op) != nil || json.Unmarshal(op.Body, &body) != nil {
		return errors.New("原会话创建回执无效，请核对已保存请求")
	}
	if body.InstanceID != binding.InstanceID || body.WorkspaceID != binding.WorkspaceID || body.Source.PresetID != binding.PresetID {
		return uncertain("原会话创建请求属于另一制作环境，未重放到当前项目绑定")
	}
	return nil
}

var productionProfileID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

func validProductionProfile(id string) bool { return id == "" || productionProfileID.MatchString(id) }

func cloneRuntimeBinding(binding *ProjectRuntimeBinding) *ProjectRuntimeBinding {
	if binding == nil {
		return nil
	}
	copy := *binding
	return &copy
}

func (a *App) checkLegacyMigrationBusy(ctx context.Context, instanceID string) error {
	var sessions []struct {
		InstanceID string `json:"instanceId"`
		Status     string `json:"status"`
	}
	if err := a.rdCall(ctx, "GET", "/sessions", nil, &sessions); err != nil {
		return err
	}
	for _, session := range sessions {
		if session.InstanceID == instanceID && (session.Status == "running" || session.Status == "waiting" || session.Status == "starting" || session.Status == "stopping") {
			return errors.New("请等待旧应用当前任务结束后迁移连接")
		}
	}
	return nil
}

func projectRuntimeBinding(p *Project, cfg Config) ProjectRuntimeBinding {
	if p != nil && p.RuntimeBinding != nil {
		return *p.RuntimeBinding
	}
	b := ProjectRuntimeBinding{InstanceID: cfg.InstanceID, WorkspaceID: cfg.WorkspaceID, ManagedByRunDesk: cfg.ManagedByRunDesk, SkillPath: cfg.SkillPath}
	if p != nil && p.WorkspaceID != "" {
		b.WorkspaceID = p.WorkspaceID
	}
	return b
}

func (a *App) projectRuntimeBinding(p *Project) ProjectRuntimeBinding {
	a.mu.Lock()
	cfg := a.cfg
	a.mu.Unlock()
	return projectRuntimeBinding(p, cfg)
}

// Recover a legacy project's actual session before considering startup defaults.
// A changed App default must never move a conversation to another environment.
func (a *App) connectionBinding(ctx context.Context, p Project, cfg Config) (ProjectRuntimeBinding, error) {
	if p.RuntimeBinding != nil {
		b := *p.RuntimeBinding
		if b.InstanceID == "" || b.WorkspaceID == "" || b.WorkspaceID != p.WorkspaceID || !validProductionProfile(b.PresetID) {
			return b, errors.New("项目保存的运行环境绑定无效，请核对原 RunDesk 会话")
		}
		return b, nil
	}
	if p.SessionID != "" {
		var old struct {
			InstanceID  string `json:"instanceId"`
			WorkspaceID string `json:"workspaceId"`
			Source      struct {
				PresetID string `json:"presetId"`
			} `json:"source"`
		}
		if err := a.rdCall(ctx, "GET", "/sessions/"+p.SessionID, nil, &old); err != nil {
			return ProjectRuntimeBinding{}, err
		}
		if old.InstanceID == "" || old.WorkspaceID == "" || p.WorkspaceID != "" && p.WorkspaceID != old.WorkspaceID || !validProductionProfile(old.Source.PresetID) {
			return ProjectRuntimeBinding{}, errors.New("旧项目与原会话绑定不一致，未应用新的默认制作环境")
		}
		return ProjectRuntimeBinding{PresetID: old.Source.PresetID, InstanceID: old.InstanceID, WorkspaceID: old.WorkspaceID, ManagedByRunDesk: cfg.ManagedByRunDesk, SkillPath: cfg.SkillPath}, nil
	}
	if p.WorkspaceID != "" {
		return projectRuntimeBinding(&p, cfg), nil
	}
	profile := a.productionProfile
	if p.ProductionProfile != nil {
		profile = *p.ProductionProfile
	}
	if profile != "" {
		return ProjectRuntimeBinding{PresetID: profile, ManagedByRunDesk: true}, nil
	}
	return projectRuntimeBinding(nil, cfg), nil
}
