package main

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionExchangeValidationRejectsUnboundAndFailedEvidence(t *testing.T) {
	a, p, asset, shared := exchangeFixture(t)
	run, p := prepareExchange(t, a, p)
	inputs, err := os.OpenRoot(filepath.Join(shared, run.RelativeRunRoot))
	if err != nil {
		t.Fatal(err)
	}
	defer inputs.Close()
	archiveDir := t.TempDir()
	archive, err := os.OpenRoot(archiveDir)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	completion := ExchangeCompletion{SchemaVersion: 1, Engine: "video_use", UpstreamCommit: exchangeVideoUseCommit, ProjectID: p.ID, ProductionRunID: run.ID, SourceHash: run.SourceHash, InputHash: run.InputHash}
	var manifest ExchangeManifest
	if err := exchangeJSON(inputs, "input/manifest.json", 4<<20, &manifest); err != nil {
		t.Fatal(err)
	}
	edl := map[string]any{"schemaVersion": 1, "canvas": map[string]any{"width": 320, "height": 180, "fps": 30}, "clips": []any{map[string]any{"id": "image", "assetId": asset.ID, "path": manifest.Media[0].Path, "type": "image", "start": 0, "duration": 1}}}
	writeEDL := func(v any) string {
		t.Helper()
		if err := atomicJSON(filepath.Join(archiveDir, "edit-ir.json"), v); err != nil {
			t.Fatal(err)
		}
		h, _, err := hashLocal(filepath.Join(archiveDir, "edit-ir.json"))
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	edlHash := writeEDL(edl)
	baseReport := map[string]any{"schemaVersion": 1, "engine": "video_use", "upstreamCommit": exchangeVideoUseCommit, "projectId": p.ID, "productionRunId": run.ID, "sourceHash": run.SourceHash, "inputHash": run.InputHash, "edlHash": edlHash, "narrationSHA256": manifest.Narration.SHA256, "passed": true, "checks": []any{map[string]any{"name": "dimensions", "ok": true}, map[string]any{"name": "videoCodec", "ok": true}, map[string]any{"name": "audioCodec", "ok": true}, map[string]any{"name": "duration", "ok": true}, map[string]any{"name": "fileNonempty", "ok": true}, map[string]any{"name": "frameRate", "ok": true}}}
	writeReport := func(v any) {
		t.Helper()
		if err := atomicJSON(filepath.Join(archiveDir, "validation.json"), v); err != nil {
			t.Fatal(err)
		}
	}
	writeReport(baseReport)
	if err := validateProductionExchange(context.Background(), archive, inputs, run, completion, edlHash); err != nil {
		t.Fatal("valid frozen evidence rejected:", err)
	}
	for _, field := range []string{"engine", "upstreamCommit", "projectId", "productionRunId", "sourceHash", "inputHash", "edlHash", "narrationSHA256", "passed", "checks"} {
		t.Run("unbound-"+field, func(t *testing.T) {
			b, _ := json.Marshal(baseReport)
			var report map[string]any
			json.Unmarshal(b, &report)
			delete(report, field)
			writeReport(report)
			if err := validateProductionExchange(context.Background(), archive, inputs, run, completion, edlHash); err == nil {
				t.Fatal("accepted missing", field)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"failed", func(r map[string]any) { r["passed"] = false }},
		{"wrong-edl", func(r map[string]any) { r["edlHash"] = strings.Repeat("f", 64) }},
		{"wrong-audio", func(r map[string]any) { r["narrationSHA256"] = strings.Repeat("e", 64) }},
		{"wrong-input", func(r map[string]any) { r["inputHash"] = strings.Repeat("d", 64) }},
		{"invalid-field-type", func(r map[string]any) { r["projectId"] = map[string]any{"unexpected": "object"} }},
		{"failed-check", func(r map[string]any) { r["checks"].([]any)[0].(map[string]any)["ok"] = false }},
		{"missing-required-check", func(r map[string]any) { r["checks"] = r["checks"].([]any)[:5] }},
		{"duplicate-check", func(r map[string]any) { checks := r["checks"].([]any); r["checks"] = append(checks, checks[0]) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b, _ := json.Marshal(baseReport)
			var report map[string]any
			json.Unmarshal(b, &report)
			tc.mutate(report)
			writeReport(report)
			if err := validateProductionExchange(context.Background(), archive, inputs, run, completion, edlHash); err == nil {
				t.Fatal("accepted invalid evidence")
			}
		})
	}
	writeReport(baseReport)
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"minimal", func(v map[string]any) { delete(v, "canvas"); delete(v, "clips") }},
		{"unknown-field", func(v map[string]any) { v["extraAuthority"] = true }},
		{"undeclared-path", func(v map[string]any) { v["clips"].([]any)[0].(map[string]any)["path"] = "input/media/not-frozen.png" }},
		{"wrong-asset", func(v map[string]any) { v["clips"].([]any)[0].(map[string]any)["assetId"] = "another-asset" }},
		{"wrong-type", func(v map[string]any) { v["clips"].([]any)[0].(map[string]any)["type"] = "video" }},
		{"timeline-gap", func(v map[string]any) { v["clips"].([]any)[0].(map[string]any)["start"] = .1 }},
		{"partial-coverage", func(v map[string]any) { v["clips"].([]any)[0].(map[string]any)["duration"] = .5 }},
		{"image-source-in", func(v map[string]any) { v["clips"].([]any)[0].(map[string]any)["sourceIn"] = 1 }},
	} {
		t.Run("edl-"+tc.name, func(t *testing.T) {
			b, _ := json.Marshal(edl)
			var changed map[string]any
			json.Unmarshal(b, &changed)
			tc.mutate(changed)
			h := writeEDL(changed)
			boundReport := map[string]any{}
			for k, v := range baseReport {
				boundReport[k] = v
			}
			boundReport["edlHash"] = h
			writeReport(boundReport)
			if err := validateProductionExchange(context.Background(), archive, inputs, run, completion, h); err == nil {
				t.Fatal("accepted invalid EDL")
			}
		})
	}
}

func TestExchangeSourceBoundsUseVisualStreamRatherThanLongerAudio(t *testing.T) {
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skip(err)
		}
	}
	dir := t.TempDir()
	cmd := exec.Command("ffmpeg", "-v", "error", "-threads", "1", "-filter_threads", "1", "-f", "lavfi", "-i", "color=red:s=64x64:r=30:d=1", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo:d=3", "-map", "0:v:0", "-map", "1:a:0", "-c:v", "libx264", "-threads", "1", "-c:a", "aac", filepath.Join(dir, "long-audio.mp4"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	hash, size, err := hashLocal(filepath.Join(dir, "long-audio.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	duration, err := probeExchangeSourceDuration(context.Background(), root, ExchangeMedia{Path: "long-audio.mp4", MIMEType: "video/mp4", SHA256: hash, Size: size})
	if err != nil || math.Abs(duration-1) > .01 {
		t.Fatalf("visual duration = %v, %v; expected 1 second, never container's longer audio duration", duration, err)
	}
	wrongDigest := ExchangeMedia{Path: "long-audio.mp4", MIMEType: "video/mp4", SHA256: strings.Repeat("a", 64), Size: size}
	if _, err := probeExchangeSourceDuration(context.Background(), root, wrongDigest); err == nil {
		t.Fatal("probed bytes not bound to frozen digest")
	}
	wrapper := []byte("ffconcat version 1.0\nfile 'long-audio.mp4'\nduration 3\n")
	if err := os.WriteFile(filepath.Join(dir, "wrapped.mp4"), wrapper, 0600); err != nil {
		t.Fatal(err)
	}
	wrapped := ExchangeMedia{Path: "wrapped.mp4", MIMEType: "video/mp4", SHA256: hashBytes(wrapper), Size: int64(len(wrapper))}
	if _, err := probeExchangeSourceDuration(context.Background(), root, wrapped); err == nil {
		t.Fatal("playlist wrapper accepted as frozen MP4")
	}

}

func TestExchangeWebMSourceUsesVisualDurationTag(t *testing.T) {
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skip(err)
		}
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "source.webm")
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=red:s=64x64:r=30:d=1", "-c:v", "libvpx-vp9", "-threads", "1", file)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	hash, size, err := hashLocal(file)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	duration, err := probeExchangeSourceDuration(context.Background(), root, ExchangeMedia{Path: "source.webm", MIMEType: "video/webm", SHA256: hash, Size: size})
	if err != nil || math.Abs(duration-1) > .01 {
		t.Fatalf("WebM visual duration = %v, %v", duration, err)
	}
}

func TestExchangeIngestRejectsActualOutputContradictingPassedReport(t *testing.T) {
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skip(err)
		}
	}
	for _, tc := range []struct {
		name, visualLength, size, fps string
		accepted                      bool
	}{
		{"matching-output", "3", "64x64", "30", true},
		{"truncated-visual-with-full-audio", "1", "64x64", "30", false},
		{"wrong-canvas", "3", "128x64", "30", false},
		{"wrong-frame-rate", "3", "64x64", "24", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, p, asset, shared := exchangeFixture(t)
			j := p.Speech[0]
			j.Duration = 3
			speechDir := a.speechDir(p.ID, j.ID)
			wave, err := os.Create(filepath.Join(speechDir, "narration.wav"))
			if err != nil {
				t.Fatal(err)
			}
			if err := writeSpeechWAVHeader(wave, 72000); err != nil {
				t.Fatal(err)
			}
			if _, err := wave.Write(make([]byte, 144000)); err != nil {
				t.Fatal(err)
			}
			wave.Close()
			os.WriteFile(filepath.Join(speechDir, "subtitles.srt"), []byte("1\n00:00:00,000 --> 00:00:03,000\n芯片发布。\n"), 0600)
			atomicJSON(filepath.Join(speechDir, "manifest.json"), SpeechManifest{Schema: 1, JobID: j.ID, ScriptHash: j.ScriptHash, Duration: 3, Cues: []SubtitleCue{{Start: 0, End: 3, Text: p.NarrationText}}, Shots: []SpeechShot{{ID: "n1", Duration: 3}}})
			for _, name := range []string{"narration.wav", "subtitles.srt", "manifest.json"} {
				h, n, err := hashLocal(filepath.Join(speechDir, name))
				if err != nil {
					t.Fatal(err)
				}
				j.Files[name] = NewsFile{SHA256: h, Size: n}
			}
			p.Speech[0] = j
			if err := a.save(&p); err != nil {
				t.Fatal(err)
			}
			a.projects[p.ID] = &p
			run, p := prepareExchange(t, a, p)
			runDir := filepath.Join(shared, run.RelativeRunRoot)
			artifactDir := filepath.Join(runDir, "artifacts", "claimed-passed")
			if err := os.MkdirAll(artifactDir, 0700); err != nil {
				t.Fatal(err)
			}
			videoFile := filepath.Join(artifactDir, "final.mp4")
			cmd := exec.Command("ffmpeg", "-v", "error", "-threads", "1", "-filter_threads", "1", "-f", "lavfi", "-i", "color=red:s="+tc.size+":r="+tc.fps+":d="+tc.visualLength, "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo:d=3", "-map", "0:v:0", "-map", "1:a:0", "-c:v", "libx264", "-threads", "1", "-c:a", "aac", videoFile)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatal(err, string(out))
			}
			var manifest ExchangeManifest
			inputs, err := os.OpenRoot(runDir)
			if err != nil {
				t.Fatal(err)
			}
			defer inputs.Close()
			if err := exchangeJSON(inputs, "input/manifest.json", 4<<20, &manifest); err != nil {
				t.Fatal(err)
			}
			edl := map[string]any{"schemaVersion": 1, "canvas": map[string]any{"width": 64, "height": 64, "fps": 30}, "clips": []any{map[string]any{"id": "image", "assetId": asset.ID, "path": manifest.Media[0].Path, "type": "image", "start": 0, "duration": 3}}}
			if err := atomicJSON(filepath.Join(artifactDir, "edit-ir.json"), edl); err != nil {
				t.Fatal(err)
			}
			edlHash, _, err := hashLocal(filepath.Join(artifactDir, "edit-ir.json"))
			if err != nil {
				t.Fatal(err)
			}
			checks := []any{}
			for _, name := range []string{"dimensions", "videoCodec", "audioCodec", "duration", "fileNonempty", "frameRate"} {
				checks = append(checks, map[string]any{"name": name, "ok": true})
			}
			report := map[string]any{"schemaVersion": 1, "engine": "video_use", "upstreamCommit": exchangeVideoUseCommit, "projectId": p.ID, "productionRunId": run.ID, "sourceHash": run.SourceHash, "inputHash": run.InputHash, "edlHash": edlHash, "narrationSHA256": manifest.Narration.SHA256, "passed": true, "checks": checks}
			if err := atomicJSON(filepath.Join(artifactDir, "validation.json"), report); err != nil {
				t.Fatal(err)
			}
			completion := ExchangeCompletion{SchemaVersion: 1, Engine: "video_use", UpstreamCommit: exchangeVideoUseCommit, ProjectID: p.ID, ProductionRunID: run.ID, SourceHash: run.SourceHash, InputHash: run.InputHash}
			for _, item := range []struct{ kind, name, mime string }{{"video", "final.mp4", "video/mp4"}, {"edit", "edit-ir.json", "application/json"}, {"validation", "validation.json", "application/json"}} {
				h, n, err := hashLocal(filepath.Join(artifactDir, item.name))
				if err != nil {
					t.Fatal(err)
				}
				completion.Artifacts = append(completion.Artifacts, ExchangeArtifact{ID: item.kind, Kind: item.kind, Path: "artifacts/claimed-passed/" + item.name, SHA256: h, Size: n, MIMEType: item.mime})
			}
			archive, err := os.OpenRoot(artifactDir)
			if err != nil {
				t.Fatal(err)
			}
			defer archive.Close()
			if err := validateProductionExchange(context.Background(), archive, inputs, run, completion, edlHash); err != nil {
				t.Fatal("fixture must have otherwise valid EDL and passed report:", err)
			}
			if err := atomicJSON(filepath.Join(runDir, "completion.json"), completion); err != nil {
				t.Fatal(err)
			}
			response := call(a, "POST", "/api/projects/"+p.ID+"/production-runs/"+run.ID+"/ingest", map[string]any{"revision": p.Revision})
			if tc.accepted && response.Code != 200 || !tc.accepted && response.Code != 409 {
				t.Fatal(response.Code, response.Body.String())
			}
			if !tc.accepted && !strings.Contains(response.Body.String(), "画面轨") {
				t.Fatal("output mismatch rejected for unexpected reason:", response.Body.String())
			}
			latest, _ := a.get(p.ID)
			if !tc.accepted && len(latest.Renders) != 0 {
				t.Fatal("rejected output was archived as render")
			}
			if tc.accepted && (len(latest.Renders) != 1 || math.Abs(latest.Renders[0].Duration-3) > .01) {
				t.Fatal("render duration must be actual visual duration", latest.Renders)
			}
		})
	}
}

func TestExchangeOutputAcceptsOneFrameQuantizationAtLowFPS(t *testing.T) {
	for _, name := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skip(err)
		}
	}
	file := filepath.Join(t.TempDir(), "low-fps.mp4")
	cmd := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "color=red:s=64x64:r=1:d=2", "-f", "lavfi", "-i", "anullsrc=r=48000:cl=stereo:d=1.5", "-map", "0:v:0", "-map", "1:a:0", "-c:v", "libx264", "-threads", "1", "-c:a", "aac", file)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatal(err, string(out))
	}
	duration, err := probeExchangeVideo(context.Background(), file, 1.5, exchangeCanvas{Width: 64, Height: 64, FPS: 1})
	if err != nil || math.Abs(duration-2) > .01 {
		t.Fatalf("low-FPS visual duration = %v, %v", duration, err)
	}
}
