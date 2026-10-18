package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func diagnosticRow(t *testing.T, v RuntimeDiagnostics, id string) RuntimeDiagnosticRow {
	t.Helper()
	for _, row := range v.Rows {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("missing diagnostic row %s: %+v", id, v.Rows)
	return RuntimeDiagnosticRow{}
}

func diagnosticFixture(t *testing.T, backend string) (*App, Project, string) {
	t.Helper()
	a := testApp(t)
	p, err := a.create("diagnostics")
	if err != nil {
		t.Fatal(err)
	}
	p.SessionID, p.WorkspaceID = "session-diagnostics", "workspace-diagnostics"
	wf := defaultWorkflow()
	wf.EditingBackend = backend
	p.Workflow = &wf
	a.projects[p.ID] = &p
	a.cfg = Config{ManagedByRunDesk: true, InstanceID: "instance-diagnostics", WorkspaceID: p.WorkspaceID}
	root := t.TempDir()
	t.Setenv("VIDEO_EXCHANGE_ROOT", root)
	// This test checks executable discovery, not media synthesis.
	bin := t.TempDir()
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if err = os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin)
	return a, p, root
}

func TestRuntimeDiagnosticsBoundaries(t *testing.T) {
	for _, kind := range []string{"ready", "profile_1_4_compat", "profile_1_4_no_video_use", "unknown_profile", "native", "hyperframes", "missing_exchange", "older_rundesk", "different_mount", "stale_nonce", "wrong_digest", "symlink_response", "oversized_response", "trailing_json", "changed_request", "wrong_binding", "old_adapter", "wrong_image", "image_not_checked", "binding_changed", "unprobed", "auth_missing", "mcp_missing", "mcp_error", "missing_probe_fields", "missing_ffprobe", "python_dependencies_missing"} {
		t.Run(kind, func(t *testing.T) {
			backend := "video_use"
			if kind == "native" {
				backend = "native"
			}
			if kind == "hyperframes" {
				backend = "hyperframes"
			}
			a, p, exchange := diagnosticFixture(t, backend)
			otherMount := t.TempDir()
			probeDir := ""
			posts := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/applications/rundesk-video-app/runtime":
					// Match real GET /runtime: it reports cached binding/env
					// metadata, always probed:false, without account/tools.
					writeJSON(w, 200, map[string]any{"appId": "rundesk-video-app", "instanceId": a.cfg.InstanceID, "workspaceId": p.WorkspaceID, "executionMode": "docker", "environment": map[string]any{"state": "running", "pendingUpdate": false}, "probed": false})
				case "/api/v1/sessions/" + p.SessionID:
					writeJSON(w, 200, map[string]string{"instanceId": a.cfg.InstanceID, "workspaceId": p.WorkspaceID, "executionMode": "docker"})
				case "/api/v1/applications/rundesk-video-app/runtime/diagnostics":
					posts++
					if kind == "older_rundesk" {
						writeJSON(w, 404, map[string]string{"error": "not found"})
						return
					}
					var in struct {
						InstanceID     string `json:"instanceId"`
						WorkspaceID    string `json:"workspaceId"`
						Nonce          string `json:"nonce"`
						MCPImageServer string `json:"mcpImageServer"`
						Challenge      *struct {
							Path  string `json:"path"`
							Nonce string `json:"nonce"`
						} `json:"challenge"`
					}
					if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.InstanceID != a.cfg.InstanceID || in.WorkspaceID != p.WorkspaceID || !identifier.MatchString(in.Nonce) || in.MCPImageServer != "video_project" {
						t.Errorf("invalid request: %+v %v", in, err)
					}
					out := runDeskDiagnosticResponse{SchemaVersion: 1, AppID: "rundesk-video-app", InstanceID: in.InstanceID, WorkspaceID: in.WorkspaceID, ExecutionMode: "docker", Runtime: runDeskDiagnosticRuntime{Status: "ready", ProfileID: "video-build", ProfileVersion: "1.3.0", AdapterContractVersion: 2, VideoUseCommit: exchangeVideoUseCommit, PythonVersion: "3.11.8", FFmpegVersion: "ffmpeg 6", FFprobeVersion: "ffprobe 6", Checks: map[string]string{"profile": "ready", "ffmpeg": "ready", "ffprobe": "ready", "pythonDependencies": "ready", "adapter": "ready", "sourcePin": "ready"}}}
					out.Probed, out.AccountReady, out.Servers = true, true, map[string][]string{"video_project": videoTools}
					im, _ := diagnosticImage(in.Nonce)
					out.ImageTransport.State, out.ImageTransport.Nonce, out.ImageTransport.ImageSHA256 = "passed", in.Nonce, hashBytes(im)
					out.ImageTransport.MIMEType, out.ImageTransport.Width, out.ImageTransport.Height = "image/png", 32, 32
					if kind == "native" || kind == "hyperframes" {
						if in.Challenge != nil {
							t.Error("native backend requested exchange challenge")
						}
						out.Runtime = runDeskDiagnosticRuntime{Status: "incomplete", ProfileID: "other"}
					}
					if in.Challenge != nil {
						c := in.Challenge
						if c.Nonce != in.Nonce || !strings.HasPrefix(c.Path, "outputs/"+p.SessionID+"/.rundesk-probes/") {
							t.Errorf("invalid challenge: %+v", c)
						}
						request, err := os.ReadFile(filepath.Join(exchange, filepath.FromSlash(c.Path)))
						if err != nil {
							t.Error(err)
						}
						probeDir = filepath.Dir(c.Path)
						out.Challenge.Status, out.Challenge.Path, out.Challenge.ResponsePath = "verified", c.Path, filepath.ToSlash(filepath.Join(probeDir, "response.json"))
						out.Challenge.RequestSHA256, out.Challenge.Nonce = hashBytes(request), c.Nonce
						response := map[string]any{"schemaVersion": 1, "nonce": c.Nonce, "requestSHA256": hashBytes(request)}
						switch kind {
						case "stale_nonce":
							response["nonce"] = uid()
						case "wrong_digest":
							response["requestSHA256"] = strings.Repeat("0", 64)
						case "changed_request":
							_ = os.WriteFile(filepath.Join(exchange, filepath.FromSlash(c.Path)), []byte("changed"), 0600)
						}
						root := exchange
						if kind == "different_mount" {
							root = otherMount
						}
						responsePath := filepath.Join(root, filepath.FromSlash(out.Challenge.ResponsePath))
						_ = os.MkdirAll(filepath.Dir(responsePath), 0700)
						raw, _ := json.Marshal(response)
						if kind == "oversized_response" {
							raw = []byte(strings.Repeat("x", 20<<10))
						}
						if kind == "trailing_json" {
							raw = append(raw, []byte("\n{}")...)
						}
						if kind == "symlink_response" {
							target := filepath.Join(otherMount, "response.json")
							_ = os.WriteFile(target, raw, 0600)
							if err = os.Symlink(target, responsePath); err != nil {
								t.Fatal(err)
							}
						} else if err = os.WriteFile(responsePath, raw, 0600); err != nil {
							t.Error(err)
						}
					}
					switch kind {
					case "profile_1_4_compat":
						out.Runtime.ProfileVersion = "1.4.0"
					case "profile_1_4_no_video_use":
						out.Runtime.ProfileVersion = "1.4.0"
						out.Runtime.VideoUseCommit = ""
						out.Runtime.Checks["sourcePin"] = "not_required"
						out.Runtime.Checks["pythonDependencies"] = "not_required"
					case "unknown_profile":
						out.Runtime.ProfileVersion = "9.0.0"
					case "wrong_binding":
						out.WorkspaceID = "other-workspace"
					case "old_adapter":
						out.Runtime.AdapterContractVersion = 1
					case "wrong_image":
						out.ImageTransport.ImageSHA256 = strings.Repeat("0", 64)
					case "image_not_checked":
						out.ImageTransport.State = "not_checked"
					case "unprobed":
						out.Probed = false
						out.Errors = map[string]string{"account": "configuration client is not initialized", "mcp": "configuration client is not initialized"}
					case "auth_missing":
						out.AccountReady = false
					case "mcp_missing":
						out.Servers = map[string][]string{"video_project": videoTools[:1]}
					case "mcp_error":
						out.Errors = map[string]string{"mcp": "status read failed"}
					case "missing_probe_fields":
						out.Probed, out.AccountReady, out.Servers = false, false, nil
					case "missing_ffprobe":
						out.Runtime.Checks["ffprobe"] = "missing"
						out.Runtime.FFprobeVersion = ""
					case "python_dependencies_missing":
						out.Runtime.Checks["pythonDependencies"] = "missing"
					case "binding_changed":
						// Acquiring this lock while the request is in flight also
						// catches a diagnostic holding the global data mutex.
						a.mu.Lock()
						a.projects[p.ID].SessionID = "replacement-session"
						a.mu.Unlock()
					}
					writeJSON(w, 200, out)
				default:
					t.Errorf("unexpected network operation %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			a.rd = server.URL
			if kind == "native" || kind == "hyperframes" || kind == "missing_exchange" {
				t.Setenv("VIDEO_EXCHANGE_ROOT", "")
			}
			v, err := a.diagnoseRuntime(context.Background(), p.ID)
			if err != nil {
				t.Fatal(err)
			}
			if posts != 1 || v.ModelVisionVerified || v.Ready != (kind == "ready" || kind == "profile_1_4_compat" || kind == "native" || kind == "hyperframes") {
				t.Fatalf("posts=%d result=%+v", posts, v)
			}
			if diagnosticRow(t, v, "model_vision").State != "not_checked" {
				t.Fatal("mistook payload transfer for model visual understanding")
			}
			if kind == "native" && diagnosticRow(t, v, "video_use_runtime").State != "not_checked" {
				t.Fatal("native backend required video-use dependencies")
			}
			if kind == "older_rundesk" && diagnosticRow(t, v, "video_use_runtime").Code != "rundesk_upgrade_required" {
				t.Fatal("missing actionable old server upgrade diagnostic")
			}
			if probeDir != "" {
				if _, err := os.Stat(filepath.Join(exchange, probeDir)); !os.IsNotExist(err) {
					t.Fatalf("own challenge not cleaned up: %v", err)
				}
			}
		})
	}
}

func TestRuntimeDiagnosticsMissingSessionAndExchangeRoot(t *testing.T) {
	a, p, _ := diagnosticFixture(t, "video_use")
	a.projects[p.ID].SessionID = ""
	v, err := a.diagnoseRuntime(context.Background(), p.ID)
	if err != nil || v.Ready || diagnosticRow(t, v, "binding").Code != "connect_required" {
		t.Fatalf("%+v %v", v, err)
	}
	t.Setenv("VIDEO_EXCHANGE_ROOT", "")
	if _, err = newRuntimeProbe("session", uid()); err == nil {
		t.Fatal("missing exchange root accepted")
	}
	file := filepath.Join(t.TempDir(), "not-a-directory")
	_ = os.WriteFile(file, []byte("file"), 0600)
	t.Setenv("VIDEO_EXCHANGE_ROOT", file)
	if _, err = newRuntimeProbe("session", uid()); err == nil {
		t.Fatal("file exchange root accepted")
	}
}

func TestRuntimeProbeRejectsSymlinkParentAndCleansOnlyOwnedFiles(t *testing.T) {
	root, other := t.TempDir(), t.TempDir()
	t.Setenv("VIDEO_EXCHANGE_ROOT", root)
	if err := os.Symlink(other, filepath.Join(root, "outputs")); err != nil {
		t.Skip(err)
	}
	if _, err := newRuntimeProbe("session", uid()); err == nil {
		t.Fatal("symlink parent accepted")
	}
	_ = os.Remove(filepath.Join(root, "outputs"))
	p, err := newRuntimeProbe("session", uid())
	if err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(root, filepath.FromSlash(p.dir))
	moved := original + "-moved"
	if err = os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	_ = os.Mkdir(original, 0700)
	foreign := filepath.Join(original, "request.json")
	_ = os.WriteFile(foreign, []byte("foreign"), 0600)
	p.close()
	if b, err := os.ReadFile(foreign); err != nil || string(b) != "foreign" {
		t.Fatal("cleanup removed a replacement directory's file", err)
	}
	if _, err = os.Stat(filepath.Join(moved, "request.json")); !os.IsNotExist(err) {
		t.Fatal("cleanup did not use pinned owned directory", err)
	}
}
