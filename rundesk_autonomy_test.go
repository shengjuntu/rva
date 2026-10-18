package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestRunDeskDispatchUsesAutonomousBackendSpecificInstructions(t *testing.T) {
	for _, backend := range []string{"hyperframes", "native", "video_use"} {
		t.Run(backend, func(t *testing.T) {
			a := testApp(t)
			p, err := a.create("自主制作")
			if err != nil {
				t.Fatal(err)
			}
			workflow := defaultWorkflow()
			workflow.EditingBackend = backend
			p.Workflow = &workflow
			p.SessionID = "session-autonomy"
			p.AgentSubmission = &AgentSubmission{ID: "autonomy-request", Text: "第二节重做，其余部分保留。", State: "pending"}
			if err := a.save(&p); err != nil {
				t.Fatal(err)
			}
			a.projects[p.ID] = &p
			var mu sync.Mutex
			var captured []byte
			rd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && strings.Contains(r.URL.Path, "/requests/") {
					writeJSON(w, 404, map[string]string{"code": "request_not_found"})
					return
				}
				if r.Method != "POST" || r.URL.Path != "/api/v1/sessions/"+p.SessionID+"/turns" {
					t.Errorf("unexpected dispatch %s %s", r.Method, r.URL.Path)
					writeJSON(w, 404, nil)
					return
				}
				raw, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				mu.Lock()
				captured = raw
				mu.Unlock()
				writeJSON(w, 200, map[string]string{"id": p.SessionID, "runId": "run-autonomy"})
			}))
			defer rd.Close()
			a.rd = rd.URL
			if _, err := a.recoverAgent(context.Background(), p, true); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			raw := append([]byte{}, captured...)
			mu.Unlock()
			var body struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{"projectId=" + p.ID, "自主选择必要工具与执行顺序", "不是固定步骤或审批", "执行后检查实际结果", "修订并重做", "保留无关且已满意的内容", "不要求重走全部流程", "第二节重做，其余部分保留。", "当前后端 " + backend} {
				if !strings.Contains(body.Text, want) {
					t.Error("missing execution contract", want, body.Text)
				}
			}
			if strings.Contains(body.Text, "按素材准备和对齐、剪辑、精修三个阶段执行") {
				t.Fatal("fixed sequence survived in actual dispatch", body.Text)
			}
			switch backend {
			case "hyperframes":
				for _, want := range []string{"video_hyperframes_get/update/check", "HTML/CSS/JavaScript/GSAP", "video_job_start(type=render)", "该 HyperFrames 工程"} {
					if !strings.Contains(body.Text, want) {
						t.Error("missing HyperFrames route", want)
					}
				}
				if strings.Contains(body.Text, "渲染当前原生剪辑") {
					t.Fatal("HyperFrames mislabeled native")
				}
			case "native":
				if !strings.Contains(body.Text, "当前原生工程") || !strings.Contains(body.Text, "video_render_start") {
					t.Fatal(body.Text)
				}
			case "video_use":
				for _, want := range []string{"video_production_run_prepare", "work/edl.json", "video_production_run_ingest", "不要另写原生 DSL"} {
					if !strings.Contains(body.Text, want) {
						t.Error("missing video_use route", want)
					}
				}
			}
			stored, _ := a.get(p.ID)
			if !bytes.Equal(stored.AgentSubmission.Body, raw) {
				t.Fatal("persisted dispatch differs from actual HTTP body")
			}
		})
	}
}

func TestRunDeskAutonomyUpgradeDoesNotRewriteFrozenPendingBody(t *testing.T) {
	a := testApp(t)
	p, err := a.create("重试旧请求")
	if err != nil {
		t.Fatal(err)
	}
	// This request was frozen before autonomous routing was introduced. Even a
	// configuration change must not rewrite its bytes or reuse its key for new work.
	frozen := []byte(`{"text":"old frozen instruction: render native; keep this request unchanged","files":[],"skills":[]}`)
	p.SessionID = "session-frozen"
	p.AgentSubmission = &AgentSubmission{ID: "frozen-request", Text: "old user text", State: "pending", Body: json.RawMessage(frozen)}
	if err := a.save(&p); err != nil {
		t.Fatal(err)
	}
	a.projects[p.ID] = &p
	a.cfg.ManagedByRunDesk = true // A rewrite would incorrectly try to resolve new Skills.
	var mu sync.Mutex
	var requests [][]byte
	var keys []string
	rd := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.Contains(r.URL.Path, "/requests/") {
			writeJSON(w, 404, map[string]string{"code": "request_not_found"})
			return
		}
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/turns") {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			writeJSON(w, 404, nil)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		requests = append(requests, raw)
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		count := len(requests)
		mu.Unlock()
		if count == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		}
		writeJSON(w, 200, map[string]string{"id": p.SessionID, "runId": "run-original"})
	}))
	defer rd.Close()
	a.rd = rd.URL
	if _, err := a.recoverAgent(context.Background(), p, true); err == nil {
		t.Fatal("lost ack reported success")
	}
	restarted, err := newApp(a.data)
	if err != nil {
		t.Fatal(err)
	}
	restarted.rd = rd.URL
	restarted.cfg.ManagedByRunDesk = true
	current, _ := restarted.get(p.ID)
	workflow := defaultWorkflow()
	workflow.EditingBackend = "video_use"
	current.Workflow = &workflow
	current.AgentSubmission.Text = "changed local description must not rewrite frozen request"
	restarted.projects[current.ID] = &current
	if _, err := restarted.recoverAgent(context.Background(), current, true); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 || !bytes.Equal(requests[0], frozen) || !bytes.Equal(requests[1], frozen) || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("frozen request changed: requests=%q keys=%q", requests, keys)
	}
	stored, _ := restarted.get(p.ID)
	if !bytes.Equal(stored.AgentSubmission.Body, frozen) || stored.AgentRunID != "run-original" {
		t.Fatal("frozen request or receipt identity changed", stored.AgentSubmission)
	}
}
