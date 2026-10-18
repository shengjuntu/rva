package main

import (
	"encoding/json"
	"fmt"
)

// Validate new tool contracts before the typed dispatcher can drop unknown fields.
func validateFoundationArguments(name string, params json.RawMessage) error {
	var allowed, required []string
	switch name {
	case "video_capabilities_get":
		allowed = []string{"probe"}
	case "video_project_create":
		allowed = []string{"name", "requestId"}
		required = allowed
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
	for _, key := range required {
		if _, ok := envelope.Arguments[key]; !ok {
			return fmt.Errorf("missing argument %q for %s", key, name)
		}
	}
	return nil
}
