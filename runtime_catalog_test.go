package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func runtimeCatalogMeta(presets bool) map[string]any {
	caps := []string{"idempotency", "configuration-summary", "application-connect", "application-runtime"}
	if presets {
		caps = append(caps, "application-runtime-presets")
	}
	return map[string]any{"apiVersions": []string{"v1"}, "capabilities": caps}
}

func runtimeCatalogPreset(id string, enabled bool) map[string]any {
	return map[string]any{"id": id, "appId": "rundesk-video-app", "name": "制作环境 " + id, "description": "视频工具", "instanceId": "instance-" + id, "workspaceId": "workspace-" + id, "enabled": enabled}
}

func runtimeCatalogView(id, state string) map[string]any {
	out := map[string]any{"appId": "rundesk-video-app", "presetId": id, "instanceId": "instance-" + id, "workspaceId": "workspace-" + id, "executionMode": "docker", "image": "rundesk-tools:local", "probed": false, "modelTested": false}
	if state != "" {
		out["environment"] = map[string]any{"state": state, "pendingUpdate": false}
	}
	return out
}

func catalogHTTP(t *testing.T, a *App) (RuntimeProfileCatalog, *httptest.ResponseRecorder) {
	t.Helper()
	w := call(a, "GET", "/api/runtime-profiles", nil)
	var got RuntimeProfileCatalog
	if w.Code == 200 && json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatal("invalid catalog JSON", w.Body.String())
	}
	return got, w
}

func TestRuntimeProfileCatalogQueriesOnlyAuthorizedApplicationBindings(t *testing.T) {
	for _, token := range []string{"fixture-admin-credential", "fixture-app-credential"} {
		t.Run(token, func(t *testing.T) {
			a := testApp(t)
			a.productionProfile = "mine"
			var mu sync.Mutex
			calls := []string{}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				calls = append(calls, r.Method+" "+r.URL.RequestURI())
				mu.Unlock()
				if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer "+token {
					t.Error("catalog changed method or credentials")
				}
				switch r.URL.Path {
				case "/api/v1/meta":
					writeJSON(w, 200, runtimeCatalogMeta(true))
				case "/api/v1/applications/rundesk-video-app/runtime-presets":
					items := []any{runtimeCatalogPreset("mine", true), runtimeCatalogPreset("disabled", false)}
					if token == "fixture-admin-credential" {
						items = append(items, runtimeCatalogPreset("other", true))
					}
					writeJSON(w, 200, items)
				case "/api/v1/applications/rundesk-video-app/runtime":
					id := r.URL.Query().Get("presetId")
					if id == "disabled" || token == "fixture-app-credential" && id == "other" {
						t.Error("queried disabled or unauthorized binding")
					}
					if token == "fixture-app-credential" && id == "" {
						writeJSON(w, 403, map[string]any{"error": "secret HOME=/private/raw", "code": "application_forbidden"})
						return
					}
					writeJSON(w, 200, runtimeCatalogView(id, "running"))
				default:
					t.Error("queried unrelated API", r.URL.RequestURI())
					writeJSON(w, 404, nil)
				}
			}))
			defer upstream.Close()
			a.rd, a.rdToken = upstream.URL, token
			got, w := catalogHTTP(t, a)
			if w.Code != 200 || got.DefaultProfile != "mine" || got.RunDeskURL != upstream.URL {
				t.Fatal(w.Code, w.Body.String())
			}
			byID := map[string]RuntimeProfileChoice{}
			for _, item := range got.Items {
				byID[item.ID] = item
			}
			if !byID["mine"].Selectable || !byID["mine"].IsDefault || byID["mine"].State != "running" || byID[""].IsDefault {
				t.Fatal("selected environment lost identity", got)
			}
			if byID["disabled"].Selectable || byID["disabled"].State != "disabled" {
				t.Fatal("disabled environment admitted", got)
			}
			if token == "fixture-app-credential" {
				if _, exists := byID["other"]; exists || byID[""].Selectable || byID[""].State != "unavailable" {
					t.Fatal("application grant was widened", got)
				}
			}
			if strings.Contains(w.Body.String(), "/private/raw") {
				t.Fatal("upstream error leaked")
			}
			if len(a.projects) != 0 || a.cfg.InstanceID != "" {
				t.Fatal("listing mutated application or project state")
			}
			mu.Lock()
			defer mu.Unlock()
			for _, request := range calls {
				if strings.Contains(request, "prepare") || strings.Contains(request, "connect") || strings.Contains(request, "/instances") || strings.Contains(request, "/images") || !strings.HasPrefix(request, "GET ") {
					t.Fatal("listing executed or read global resources", calls)
				}
			}
		})
	}
}

func TestRuntimeProfileCatalogProjectsSavedStateWithoutClaimingReadiness(t *testing.T) {
	for _, tc := range []struct {
		name, state string
		mutate      func(map[string]any)
		selectable  bool
	}{
		{"running", "running", nil, true},
		{"not-created", "not_started", func(v map[string]any) { delete(v, "environment") }, true},
		{"stopped", "stopped", func(v map[string]any) { v["environment"] = map[string]any{"state": "exited"} }, true},
		{"pending-update", "pending_update", func(v map[string]any) { v["environment"] = map[string]any{"state": "running", "pendingUpdate": true} }, false},
		{"unknown-state", "unknown", func(v map[string]any) { v["environment"] = map[string]any{"state": "unrecognized"} }, false},
		{"no-image", "unavailable", func(v map[string]any) { v["image"] = "" }, false},
		{"runtime-disabled", "disabled", func(v map[string]any) { v["presetEnabled"] = false }, false},
		{"wrong-instance", "unavailable", func(v map[string]any) { v["instanceId"] = "foreign" }, false},
		{"wrong-preset", "unavailable", func(v map[string]any) { v["presetId"] = "foreign" }, false},
		{"wrong-app", "unavailable", func(v map[string]any) { v["appId"] = "foreign" }, false},
		{"host-path-binding", "unavailable", func(v map[string]any) { v["instanceId"] = "/private/HOME" }, false},
		{"credential-binding", "unavailable", func(v map[string]any) { v["workspaceId"] = "rd_app_" + strings.Repeat("a", 24) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/meta":
					writeJSON(w, 200, runtimeCatalogMeta(true))
				case "/api/v1/applications/rundesk-video-app/runtime-presets":
					writeJSON(w, 200, []any{runtimeCatalogPreset("selected", true)})
				default:
					v := runtimeCatalogView(r.URL.Query().Get("presetId"), "running")
					if tc.mutate != nil && r.URL.Query().Get("presetId") == "selected" {
						tc.mutate(v)
					}
					writeJSON(w, 200, v)
				}
			}))
			defer upstream.Close()
			a.rd = upstream.URL
			got, w := catalogHTTP(t, a)
			if w.Code != 200 || len(got.Items) != 2 || got.Items[1].State != tc.state || got.Items[1].Selectable != tc.selectable {
				t.Fatal(w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), `"ready":true`) || strings.Contains(w.Body.String(), "accountReady") || strings.Contains(w.Body.String(), "probed") {
				t.Fatal("catalog overstated or exposed runtime probes")
			}
		})
	}
}

func TestRuntimeProfileCatalogErrorsAreExplicitAndDoNotExposeCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, path, code string
		status, want     int
	}{
		{"forbidden", "/api/v1/applications/rundesk-video-app/runtime-presets", "runtime_profiles_forbidden", 403, 403},
		{"bad-key", "/api/v1/meta", "runtime_profiles_authentication_failed", 401, 502},
		{"not-registered", "/api/v1/applications/rundesk-video-app/runtime-presets", "runtime_profiles_unregistered", 404, 409},
		{"upstream-failure", "/api/v1/applications/rundesk-video-app/runtime-presets", "runtime_profiles_unavailable", 500, 502},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			a.rdToken = "private-runtime-key-should-never-be-returned"
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == tc.path {
					writeJSON(w, tc.status, map[string]any{"error": a.rdToken + " HOME=/raw/account Authorization: Bearer raw-credential", "code": "arbitrary_upstream_error"})
					return
				}
				writeJSON(w, 200, runtimeCatalogMeta(true))
			}))
			defer upstream.Close()
			a.rd = upstream.URL
			_, w := catalogHTTP(t, a)
			if w.Code != tc.want || !strings.Contains(w.Body.String(), tc.code) || strings.Contains(w.Body.String(), a.rdToken) || strings.Contains(w.Body.String(), "/raw/account") || strings.Contains(w.Body.String(), "raw-credential") {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	a := testApp(t)
	a.rd = "http://127.0.0.1:1"
	_, w := catalogHTTP(t, a)
	if w.Code != 502 || !strings.Contains(w.Body.String(), "runtime_profiles_unavailable") {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestRuntimeProfileCatalogOldRunDeskCompatibilityAndMissingDefault(t *testing.T) {
	for _, tc := range []struct {
		name, configured string
		presets          bool
		want             int
	}{
		{"old-default-only", "", false, 200},
		{"old-cannot-use-named-default", "wanted", false, 409},
		{"missing-configured-default", "wanted", true, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := testApp(t)
			a.productionProfile = tc.configured
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/v1/meta":
					writeJSON(w, 200, runtimeCatalogMeta(tc.presets))
				case "/api/v1/applications/rundesk-video-app/runtime-presets":
					if !tc.presets {
						t.Error("unsupported preset endpoint queried")
					}
					writeJSON(w, 200, []any{})
				case "/api/v1/applications/rundesk-video-app/runtime":
					if r.URL.Query().Get("presetId") != "" {
						t.Error("queried missing preset or switched binding")
					}
					v := runtimeCatalogView("", "")
					v["executionMode"] = "local"
					writeJSON(w, 200, v)
				default:
					t.Error("unexpected endpoint", r.URL.RequestURI())
				}
			}))
			defer upstream.Close()
			a.rd = upstream.URL
			got, w := catalogHTTP(t, a)
			if w.Code != tc.want {
				t.Fatal(w.Code, w.Body.String())
			}
			if tc.want != 200 {
				if !strings.Contains(w.Body.String(), "production_profile_unsupported") {
					t.Fatal(w.Body.String())
				}
				return
			}
			if len(got.Warnings) == 0 || got.DefaultProfile != tc.configured || !got.Items[0].Selectable || got.Items[0].State != "local" {
				t.Fatal(got)
			}
			if tc.configured != "" {
				last := got.Items[len(got.Items)-1]
				if last.ID != tc.configured || !last.IsDefault || last.State != "missing" || last.Selectable || got.Items[0].IsDefault {
					t.Fatal("configured default was silently replaced", got)
				}
			}
		})
	}
}

func TestRuntimeProfileCatalogRejectsMalformedBindingsAndSanitizesMetadata(t *testing.T) {
	for _, bad := range []any{nil, []any{runtimeCatalogPreset("duplicate", true), runtimeCatalogPreset("duplicate", true)}, []any{map[string]any{"id": "../bad", "appId": "rundesk-video-app"}}, []any{map[string]any{"id": "foreign", "appId": "another-app"}}, []any{map[string]any{"id": "selected", "appId": "rundesk-video-app", "instanceId": "/private/HOME", "workspaceId": "w"}}, []any{map[string]any{"id": "selected", "appId": "rundesk-video-app", "instanceId": "i", "workspaceId": "rd_app_" + strings.Repeat("a", 24)}}} {
		a := testApp(t)
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/meta" {
				writeJSON(w, 200, runtimeCatalogMeta(true))
			} else {
				writeJSON(w, 200, bad)
			}
		}))
		a.rd = upstream.URL
		_, w := catalogHTTP(t, a)
		upstream.Close()
		if w.Code != 502 || !strings.Contains(w.Body.String(), "runtime_profiles_invalid_response") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	a := testApp(t)
	a.rdToken = "exact-private-runtime-credential"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/meta":
			writeJSON(w, 200, runtimeCatalogMeta(true))
		case "/api/v1/applications/rundesk-video-app/runtime-presets":
			p := runtimeCatalogPreset("selected", true)
			p["name"] = "Visible " + a.rdToken
			p["description"] = "Bearer opaque-credential https://login:password@example.test/private"
			p["rawConfig"], p["token"], p["codexHome"] = "private-model-settings", "foreign-api-secret", "/private/HOME"
			writeJSON(w, 200, []any{p})
		default:
			v := runtimeCatalogView(r.URL.Query().Get("presetId"), "running")
			v["errors"], v["codexHome"], v["token"] = map[string]string{"configuration": "raw-secret"}, "/private/HOME", "foreign-api-secret"
			writeJSON(w, 200, v)
		}
	}))
	defer upstream.Close()
	a.rd = upstream.URL
	got, w := catalogHTTP(t, a)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, secret := range []string{a.rdToken, "opaque-credential", "login:password", "/private/HOME", "raw-secret", "foreign-api-secret", "private-model-settings"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatal("secret was returned", secret)
		}
	}
	if !strings.Contains(got.Items[1].Name, "[redacted]") || catalogRunDeskURL("https://user:password@example.test/path?token=secret#secret") != "https://example.test" {
		t.Fatal("metadata or URL was not scrubbed", got)
	}
}

func TestRuntimeProfileCatalogBoundsStatusConcurrencyAndHonorsCancellation(t *testing.T) {
	a := testApp(t)
	var inFlight, peak atomic.Int32
	statusStarted := make(chan struct{}, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/meta":
			writeJSON(w, 200, runtimeCatalogMeta(true))
		case "/api/v1/applications/rundesk-video-app/runtime-presets":
			items := []any{}
			for _, id := range []string{"one", "two", "three", "four", "five", "six"} {
				items = append(items, runtimeCatalogPreset(id, true))
			}
			writeJSON(w, 200, items)
		default:
			current := inFlight.Add(1)
			defer inFlight.Add(-1)
			statusStarted <- struct{}{}
			for previous := peak.Load(); current > previous && !peak.CompareAndSwap(previous, current); previous = peak.Load() {
			}
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
				writeJSON(w, 200, runtimeCatalogView(r.URL.Query().Get("presetId"), "running"))
			}
		}
	}))
	defer upstream.Close()
	a.rd = upstream.URL
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type response struct {
		catalog RuntimeProfileCatalog
		err     error
	}
	result := make(chan response, 1)
	go func() { got, err := a.runtimeProfileCatalog(ctx); result <- response{got, err} }()
	for n := 0; n < 4; n++ {
		select {
		case <-statusStarted:
		case <-time.After(2 * time.Second):
			t.Fatal("status workers did not start")
		}
	}
	start := time.Now()
	cancel()
	var answer response
	select {
	case answer = <-result:
	case <-time.After(time.Second):
		t.Fatal("cancellation did not bound status reads")
	}
	got, err := answer.catalog, answer.err
	if err != nil || len(got.Items) != 7 || peak.Load() > 4 || peak.Load() < 2 || time.Since(start) > time.Second {
		t.Fatal("status reads were not bounded", peak.Load(), time.Since(start), got, err)
	}
	for _, item := range got.Items {
		if item.Selectable || item.State != "unavailable" {
			t.Fatal("canceled status read was presented as usable", item)
		}
	}
}

func TestRuntimeProfileCatalogRejectsInvalidDefaultBeforeAnyNetworkRead(t *testing.T) {
	for _, invalid := range []string{"../private/default", "https://remote/default", "rd_app_" + strings.Repeat("b", 24)} {
		a := testApp(t)
		a.productionProfile = invalid
		a.rd = "http://127.0.0.1:1"
		_, w := catalogHTTP(t, a)
		if w.Code != 409 || !strings.Contains(w.Body.String(), "runtime_profiles_invalid_default") || strings.Contains(w.Body.String(), invalid) {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	a := testApp(t)
	a.rdToken = "private-opaque-token"
	a.productionProfile = "binding-" + a.rdToken
	a.rd = "http://127.0.0.1:1"
	_, w := catalogHTTP(t, a)
	if w.Code != 409 || strings.Contains(w.Body.String(), a.rdToken) {
		t.Fatal(w.Code, w.Body.String())
	}
}
