package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func reviewTimelineRead(t *testing.T, a *App, p Project, renderID string) ReviewTimeline {
	t.Helper()
	path := "/api/projects/" + p.ID + "/review-timeline"
	if renderID != "" {
		path += "?renderId=" + renderID
	}
	w := call(a, http.MethodGet, path, nil)
	var result ReviewTimeline
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil {
		t.Fatalf("review timeline: %d %s", w.Code, w.Body.String())
	}
	return result
}

func reviewTrack(t *testing.T, timeline ReviewTimeline, kind string) ReviewTrack {
	t.Helper()
	for _, track := range timeline.Tracks {
		if track.Kind == kind {
			return track
		}
	}
	t.Fatalf("missing %s track: %+v", kind, timeline.Tracks)
	return ReviewTrack{}
}

func TestReviewTimelineHyperFramesAnnotationsAndCurrentness(t *testing.T) {
	for _, change := range []string{"unchanged", "narration", "asset bytes"} {
		t.Run(change, func(t *testing.T) {
			a, p, asset, d := hyperFramesFixture(t)
			d.Timeline = append(d.Timeline,
				HyperFramesReviewClip{ID: "voice", Track: "voice", Kind: "audio", Start: 0, Duration: 5},
				HyperFramesReviewClip{ID: "music", Track: "background-music", Kind: "audio", Start: 1, Duration: 4},
				HyperFramesReviewClip{ID: "caption", Track: "captions", Kind: "subtitle", Start: 2, Duration: 2, TextRefs: []TextReference{{Quote: "计划明年量产。"}}},
				HyperFramesReviewClip{ID: "motion", Track: "motion", Kind: "animation", Start: 0, Duration: 5})
			if w := hyperFramesSave(a, p, d); w.Code != 200 {
				t.Fatal(w.Code, w.Body.String())
			}
			p, _ = a.get(p.ID)
			if change == "narration" {
				p.NarrationText = "新的稿件。"
				p.Revision++
				a.projects[p.ID] = &p
			} else if change == "asset bytes" {
				if err := os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			got := reviewTimelineRead(t, a, p, "")
			if got.Source != "authored" || got.Backend != "hyperframes" || got.Current != (change == "unchanged") || got.Text != p.NarrationText || got.Duration != 5 {
				t.Fatalf("incorrect authored/current source: %+v", got)
			}
			if !strings.Contains(strings.Join(got.Warnings, " "), "未逐帧核验") {
				t.Fatal("authored annotations presented without their evidence limit", got.Warnings)
			}
			for _, kind := range []string{"visual", "narration", "music", "subtitle", "animation"} {
				if len(reviewTrack(t, got, kind).Clips) != 1 {
					t.Fatal("lost authored track", kind, got.Tracks)
				}
			}
			visual := reviewTrack(t, got, "visual").Clips[0]
			if visual.ID != "opening" || visual.AssetID != asset.ID || visual.Start != 0 || visual.End != 5 {
				t.Fatal("changed saved authored identity/timing", visual)
			}
			if change == "unchanged" {
				caption := reviewTrack(t, got, "subtitle").Clips[0]
				if len(caption.TextRefs) != 1 || caption.TextRefs[0].StartRune != 9 || caption.TextRefs[0].EndRune != 16 {
					t.Fatal("exact original-text range lost", caption)
				}
			}
		})
	}
}

func TestReviewTimelineDoesNotInventHyperFramesTracks(t *testing.T) {
	a, p, _, d := hyperFramesFixture(t)
	d.Timeline = nil
	if w := hyperFramesSave(a, p, d); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	got := reviewTimelineRead(t, a, p, "")
	for _, track := range got.Tracks {
		if len(track.Clips) != 0 {
			t.Fatal("inferred clips from arbitrary JavaScript", got.Tracks)
		}
	}
	if !got.Current || !strings.Contains(strings.Join(got.Warnings, " "), "无法从任意 JavaScript 推断") {
		t.Fatal(got)
	}
}

// Construct the same admitted archive contract without invoking a browser. The
// opaque output bytes test provenance only; renderer media checks have separate tests.
func reviewHyperFramesArchive(t *testing.T) (*App, Project, Render, string) {
	t.Helper()
	a, p, _, d := hyperFramesFixture(t)
	if w := hyperFramesSave(a, p, d); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	p, _ = a.get(p.ID)
	render := Render{ID: uid(), Revision: p.Revision, Engine: authoredHyperFramesEngine, State: "completed", Duration: 5, SourceHash: narrationSourceHash(p), ContentHash: productionHash(p)}
	dir := filepath.Join(a.dir(p.ID), "renders", render.ID)
	manifest := hyperFramesManifest{SchemaVersion: 1, Engine: authoredHyperFramesEngine, EngineVersion: authoredHyperFramesVersion, ProjectID: p.ID, Revision: p.Revision, DocumentHash: hyperFramesDocumentHash(*p.HyperFrames), TextSourceHash: narrationSourceHash(p), Files: []hyperFramesFrozenFile{}}
	write := func(path, kind string, data []byte) {
		t.Helper()
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, data, 0600); err != nil {
			t.Fatal(err)
		}
		manifest.Files = append(manifest.Files, hyperFramesFrozenFile{Path: path, Kind: kind, SHA256: hashBytes(data), Size: int64(len(data))})
	}
	for _, file := range p.HyperFrames.Files {
		write(file.Path, "source", []byte(file.Content))
	}
	for _, asset := range p.HyperFrames.Assets {
		write(asset.Path, "asset", []byte("picture"))
		manifest.Files[len(manifest.Files)-1].AssetID = asset.AssetID
	}
	snapshot, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	write("snapshot.json", "project", snapshot)
	canonical, _ := json.Marshal(manifest)
	manifest.SnapshotHash = hashBytes(canonical)
	render.CacheKey = manifest.SnapshotHash
	video := []byte("opaque previously verified output")
	if err := os.WriteFile(filepath.Join(dir, "final.mp4"), video, 0600); err != nil {
		t.Fatal(err)
	}
	proof := RenderProof{Schema: "video.render-proof.v1", Key: manifest.SnapshotHash, VideoHash: hashBytes(video), VideoBytes: int64(len(video)), SnapshotHash: hashBytes(snapshot)}
	for name, value := range map[string]any{"hyperframes-manifest.json": manifest, "render-proof.json": proof} {
		if err := atomicJSON(filepath.Join(dir, name), value); err != nil {
			t.Fatal(err)
		}
	}
	p.Renders = append(p.Renders, render)
	a.projects[p.ID] = &p
	return a, p, render, dir
}

func TestReviewTimelineHistoricalHyperFramesKeepsFrozenText(t *testing.T) {
	a, p, render, _ := reviewHyperFramesArchive(t)
	originalText := p.NarrationText
	if got := reviewTimelineRead(t, a, p, render.ID); !got.Current || got.Text != originalText {
		t.Fatal(got)
	}
	p.NarrationText = "修改后的完全不同稿件。"
	p.Revision++
	a.projects[p.ID] = &p
	got := reviewTimelineRead(t, a, p, render.ID)
	visual := reviewTrack(t, got, "visual").Clips[0]
	if got.Current || got.Text != originalText || got.SourceHash != render.SourceHash || got.RenderID != render.ID || len(visual.TextRefs) != 1 || visual.TextRefs[0].Quote != "发布了一款新芯片。" || visual.TextRefs[0].EndRune != 9 {
		t.Fatalf("history substituted live narration: %+v, %+v", got, visual)
	}
}

func TestReviewTimelineHistoricalHyperFramesLiveAssetChangeIsStaleButReadable(t *testing.T) {
	a, p, render, _ := reviewHyperFramesArchive(t)
	if got := reviewTimelineRead(t, a, p, render.ID); !got.Current {
		t.Fatal("unchanged archive should match live project", got)
	}
	if err := os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", p.Assets[0].File), []byte("changed live picture"), 0600); err != nil {
		t.Fatal(err)
	}
	live := reviewTimelineRead(t, a, p, "")
	archived := reviewTimelineRead(t, a, p, render.ID)
	if live.Current || archived.Current || archived.Text != p.NarrationText || archived.Source != "authored" || len(reviewTrack(t, archived, "visual").Clips) != 1 {
		t.Fatal("live input mutation must make archive stale without losing historical review", live, archived)
	}
}

func TestReviewTimelineRejectsChangedHyperFramesArchive(t *testing.T) {
	for _, file := range []string{"snapshot.json", "index.html", "media/news.png", "final.mp4", "hyperframes-manifest.json"} {
		t.Run(file, func(t *testing.T) {
			a, p, render, dir := reviewHyperFramesArchive(t)
			reviewTimelineRead(t, a, p, render.ID)
			changed := []byte("tampered archive")
			if file == "snapshot.json" {
				frozen := p
				frozen.NarrationText = "被替换但仍有效的快照。"
				changed = mustHyperFramesJSON(t, frozen)
			}
			if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(file)), changed, 0600); err != nil {
				t.Fatal(err)
			}
			w := call(a, http.MethodGet, "/api/projects/"+p.ID+"/review-timeline?renderId="+render.ID, nil)
			if w.Code != 409 {
				t.Fatal("changed archive accepted", w.Code, w.Body.String())
			}
		})
	}
}

func TestReviewTimelineRejectsResignedReplacementHyperFramesManifest(t *testing.T) {
	a, p, render, dir := reviewHyperFramesArchive(t)
	manifest, err := readHyperFramesManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	frozen := clone(&p)
	frozen.Renders = nil
	frozen.HyperFrames.Files[0].Content = "<!doctype html><p>Replacement source</p>"
	if err = os.WriteFile(filepath.Join(dir, "index.html"), []byte(frozen.HyperFrames.Files[0].Content), 0600); err != nil {
		t.Fatal(err)
	}
	if err = atomicJSON(filepath.Join(dir, "snapshot.json"), frozen); err != nil {
		t.Fatal(err)
	}
	for i := range manifest.Files {
		item := &manifest.Files[i]
		item.SHA256, item.Size, err = hashLocal(filepath.Join(dir, filepath.FromSlash(item.Path)))
		if err != nil {
			t.Fatal(err)
		}
	}
	manifest.DocumentHash = hyperFramesDocumentHash(*frozen.HyperFrames)
	manifest.SnapshotHash = ""
	raw, _ := json.Marshal(manifest)
	manifest.SnapshotHash = hashBytes(raw)
	proof, err := readRenderProof(dir)
	if err != nil {
		t.Fatal(err)
	}
	proof.Key = manifest.SnapshotHash
	proof.SnapshotHash, _, err = hashLocal(filepath.Join(dir, "snapshot.json"))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]any{"hyperframes-manifest.json": manifest, "render-proof.json": proof} {
		if err = atomicJSON(filepath.Join(dir, name), value); err != nil {
			t.Fatal(err)
		}
	}
	// All replacement bytes and their local hashes agree. Only the original
	// admission key saved on the render distinguishes them from accepted inputs.
	if !hyperFramesFrozenInputsIntact(dir, manifest) || manifest.SnapshotHash == render.CacheKey {
		t.Fatal("replacement fixture does not exercise admission-key binding")
	}
	w := call(a, http.MethodGet, "/api/projects/"+p.ID+"/review-timeline?renderId="+render.ID, nil)
	if w.Code != 409 {
		t.Fatal("rewritten manifest/proof replaced admitted archive", w.Code, w.Body.String())
	}
}

func TestReviewTimelineVideoUseRequiresOriginalEDLDigest(t *testing.T) {
	for _, change := range []string{"clip", "missing digest", "wrong run source"} {
		t.Run(change, func(t *testing.T) {
			a, p := textRefProject(t)
			workflow := defaultWorkflow()
			workflow.EditingBackend = "video_use"
			p.Workflow = &workflow
			p, render := archiveLocateFixture(t, a, p, "video_use")
			originalText := p.NarrationText
			p.NarrationText = "完全不同的新稿。"
			a.projects[p.ID] = &p
			got := reviewTimelineRead(t, a, p, render.ID)
			visual := reviewTrack(t, got, "visual")
			if got.Current || got.Text != originalText || got.Source != "archived_edl" || len(visual.Clips) != 2 || len(visual.Clips[0].TextRefs) != 1 || visual.Clips[0].TextRefs[0].StartRune != 2 {
				t.Fatal("not reading exact archived EDL/text", got)
			}
			for _, kind := range []string{"narration", "subtitle", "music"} {
				if len(reviewTrack(t, got, kind).Clips) != 0 {
					t.Fatal("invented missing archived track", kind)
				}
			}
			switch change {
			case "clip":
				path := filepath.Join(a.dir(p.ID), "renders", render.ID, "edit-ir.json")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				// Still valid JSON with a plausible asset, but no longer admitted bytes.
				raw = []byte(strings.Replace(string(raw), "asset-original", "asset-replaced", 1))
				if err = os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "missing digest":
				p.ProductionRuns[0].EditHash = ""
			case "wrong run source":
				p.ProductionRuns[0].SourceHash = strings.Repeat("a", 64)
			}
			a.projects[p.ID] = &p
			w := call(a, http.MethodGet, "/api/projects/"+p.ID+"/review-timeline?renderId="+render.ID, nil)
			if w.Code != 409 {
				t.Fatal("unbound/modified EDL accepted", w.Code, w.Body.String())
			}
		})
	}
}

func TestReviewTimelineRangePreviewKeepsProjectCoordinates(t *testing.T) {
	a, p := textRefProject(t)
	p.EditDSL.Clips[0].Duration = 12
	p, render := archiveLocateFixture(t, a, p, "ffmpeg_edit")
	render.Range = &RenderRange{Start: 10, End: 12}
	render.Duration = 2
	p.Renders[0] = render
	a.projects[p.ID] = &p
	got := reviewTimelineRead(t, a, p, render.ID)
	clips := reviewTrack(t, got, "subtitle").Clips
	if got.Duration != 15 || len(clips) != 3 || clips[1].ID != "second" || clips[1].Start != 12 || clips[1].End != 14 || clips[2].End != 15 || !strings.Contains(strings.Join(got.Warnings, " "), "保持工程时间坐标") {
		t.Fatalf("partial output rebased/truncated project coordinates: %+v", got)
	}
}
