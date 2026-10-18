package main

import (
	"encoding/json"
	"fmt"
)

// Validate new tool contracts before the typed dispatcher can drop unknown fields.
func validateFoundationArguments(name string, params json.RawMessage) error {
	var allowed, required []string
	switch name {
	case "video_hyperframes_get":
		allowed = []string{"projectId"}
		required = allowed
	case "video_hyperframes_update":
		allowed = []string{"projectId", "revision", "document"}
		required = allowed
	case "video_hyperframes_check":
		allowed = []string{"projectId", "revision"}
		required = allowed
	case "video_review_timeline_get":
		allowed = []string{"projectId", "renderId"}
		required = []string{"projectId"}
	case "rundesk_diagnostic_image":
		allowed = []string{"nonce"}
		required = allowed
	case "video_runtime_diagnose":
		allowed = []string{"projectId"}
		required = allowed
	case "video_edit_locate":
		allowed = []string{"projectId", "quote", "occurrence", "renderId"}
		required = []string{"projectId", "quote"}
	case "video_media_alignment_get":
		allowed = []string{"projectId"}
		required = allowed
	case "video_media_alignment_update":
		allowed = []string{"projectId", "revision", "sourceHash", "notes", "units"}
		required = []string{"projectId", "revision", "sourceHash", "units"}
	case "video_asset_video_inspect":
		allowed = []string{"projectId", "assetId", "times"}
		required = allowed
	case "video_production_run_prepare":
		allowed = []string{"projectId", "revision", "requestId"}
		required = allowed
	case "video_production_run_get":
		allowed = []string{"projectId", "productionRunId"}
		required = allowed
	case "video_production_run_ingest":
		allowed = []string{"projectId", "productionRunId", "revision"}
		required = allowed
	case "video_production_board_get":
		allowed = []string{"projectId"}
		required = allowed
	case "video_production_checkpoint":
		allowed = []string{"projectId", "revision", "sourceHash", "stage", "status", "summary", "document", "renderId"}
		required = []string{"projectId", "revision", "sourceHash", "stage", "status", "summary", "document"}
	case "video_render_inspect":
		allowed = []string{"projectId", "renderId", "time"}
		required = allowed
	case "video_final_get", "video_final_verify":
		allowed = []string{"projectId"}
		required = allowed
	case "video_final_save":
		allowed = []string{"projectId", "revision", "renderId", "reviewed"}
		required = allowed
	case "video_edit_patch_preview", "video_edit_patch_apply":
		allowed = []string{"projectId", "revision", "changes", "textSourceHash"}
		required = []string{"projectId", "revision", "changes"}
	case "video_edit_undo", "video_edit_redo":
		allowed = []string{"projectId", "revision"}
		required = allowed
	case "video_asset_describe":
		allowed = []string{"projectId", "revision", "assetId", "description"}
		required = allowed
	case "video_media_align":
		allowed = []string{"projectId", "revision", "units", "limit"}
		required = []string{"projectId", "revision", "units"}
	case "video_narration_update":
		allowed = []string{"projectId", "revision", "text"}
		required = allowed
	case "video_speech_materialize":
		allowed = []string{"projectId", "revision", "speechId"}
		required = allowed
	case "video_edit_get":
		allowed = []string{"projectId"}
		required = allowed
	case "video_edit_update":
		allowed = []string{"projectId", "revision", "dsl"}
		required = allowed
	case "video_edit_validate":
		allowed = []string{"projectId", "dsl"}
		required = allowed
	case "video_edit_activate":
		allowed = []string{"projectId", "revision", "active"}
		required = allowed
	case "video_capabilities_get":
		allowed = []string{"probe"}
	case "video_project_create":
		allowed = []string{"name", "requestId", "productionProfile"}
		required = []string{"name", "requestId"}
	case "video_asset_list", "video_job_list":
		allowed = []string{"projectId"}
		required = allowed
	case "video_asset_import":
		allowed = []string{"projectId", "name", "data", "requestId"}
		required = allowed
	case "video_job_start":
		allowed = []string{"projectId", "type", "revision", "requestId", "options"}
		required = []string{"projectId", "type", "revision", "requestId"}
	case "video_job_get", "video_job_cancel":
		allowed = []string{"projectId", "jobId"}
		required = allowed
	default:
		return nil
	}
	var envelope struct {
		Arguments map[string]json.RawMessage `json:"arguments"`
	}
	if e := json.Unmarshal(params, &envelope); e != nil {
		return e
	}
	for key := range envelope.Arguments {
		found := false
		for _, a := range allowed {
			if a == key {
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("unknown argument %q for %s", key, name)
		}
	}
	if name == "video_edit_locate" {
		if raw, exists := envelope.Arguments["occurrence"]; exists {
			var occurrence int
			if err := json.Unmarshal(raw, &occurrence); err != nil || occurrence < 1 {
				return fmt.Errorf("occurrence must be a positive integer when supplied")
			}
		}
	}
	for _, key := range required {
		if _, ok := envelope.Arguments[key]; !ok {
			return fmt.Errorf("missing argument %q for %s", key, name)
		}
	}
	return nil
}
