package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func hyperFramesLocateFixture(t *testing.T) (*App, Project) {
	t.Helper()
	a, p, asset, d := hyperFramesFixture(t)
	p.NarrationText = "甲乙甲乙🙂发布。\n计划量产。"
	d.TextSourceHash = narrationSourceHash(p)
	d.Timeline = []HyperFramesReviewClip{
		{ID: "opening", Track: "main", Kind: "image", Start: 0, Duration: 1, AssetID: asset.ID, TextRefs: []TextReference{{Quote: "甲乙", Occurrence: 1}}},
		{ID: "second", Track: "main", Kind: "animation", Start: 1, Duration: 2, TextRefs: []TextReference{{Quote: "甲乙", Occurrence: 2}, {Quote: "🙂发布。"}}},
		{ID: "background", Track: "backdrop", Kind: "animation", Start: 0, Duration: 5},
	}
	if err := a.save(&p); err != nil {
		t.Fatal(err)
	}
	a.projects[p.ID] = &p
	if w := hyperFramesSave(a, p, d); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	p, _ = a.get(p.ID)
	return a, p
}

func TestHyperFramesLocateCurrentExactUnicodeAndRepeatedQuotes(t *testing.T) {
	a, p := hyperFramesLocateFixture(t)
	for _, tc := range []struct {
		quote                  string
		occurrence, start, end int
		clip                   string
	}{
		{"🙂发布。", 0, 4, 8, "second"},
		{"甲乙", 1, 0, 2, "opening"},
		{"甲乙", 2, 2, 4, "second"},
	} {
		query := map[string]any{"quote": tc.quote}
		if tc.occurrence > 0 {
			query["occurrence"] = tc.occurrence
		}
		w := locateCall(a, p.ID, query)
		var got EditLocateResult
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		if got.Backend != "hyperframes" || !got.Current || got.SourceHash != narrationSourceHash(p) || got.Query.StartRune != tc.start || got.Query.EndRune != tc.end || len(got.Matches) != 1 || got.Matches[0].ClipID != tc.clip || got.Matches[0].TrackID != "main" || len(got.Matches[0].MatchedRefs) != 1 || len(got.UnmappedClipIDs) != 1 || got.UnmappedClipIDs[0] != "background" || !strings.Contains(got.Warning, "作者") || !strings.Contains(got.Warning, "未逐帧") {
			t.Fatal(w.Body.String())
		}
	}
	for _, query := range []map[string]any{{"quote": "甲乙"}, {"quote": "不存在"}} {
		if w := locateCall(a, p.ID, query); w.Code != 422 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	// A valid span without an editorial association returns no invented matches.
	w := locateCall(a, p.ID, map[string]any{"quote": "计划量产。"})
	var got EditLocateResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Matches) != 0 || len(got.UnmappedClipIDs) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestHyperFramesLocateRejectsChangedNarrationAndBoundAsset(t *testing.T) {
	for _, change := range []string{"narration", "asset", "source-reference", "missing-document"} {
		t.Run(change, func(t *testing.T) {
			a, p := hyperFramesLocateFixture(t)
			switch change {
			case "narration":
				p.NarrationText = "新的文字里仍然有🙂发布。"
				p.Revision++
			case "asset":
				if err := os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", p.Assets[0].File), []byte("modified"), 0600); err != nil {
					t.Fatal(err)
				}
			case "source-reference":
				p.HyperFrames.Timeline[1].TextRefs[0].Quote = "编造关联"
			case "missing-document":
				p.HyperFrames = nil
			}
			a.projects[p.ID] = &p
			if w := locateCall(a, p.ID, map[string]any{"quote": "🙂发布。"}); w.Code != 409 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

// This fixture tests archive identity and byte integrity without claiming that
// its synthetic output bytes are a browser-rendered video.
func archiveHyperFramesLocateFixture(t *testing.T, a *App, p Project) (Project, Render) {
	t.Helper()
	id := uid()
	dir := filepath.Join(a.dir(p.ID), "renders", id)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := hyperFramesManifest{SchemaVersion: 1, Engine: authoredHyperFramesEngine, EngineVersion: authoredHyperFramesVersion, ProjectID: p.ID, Revision: p.Revision, DocumentHash: hyperFramesDocumentHash(*p.HyperFrames), TextSourceHash: narrationSourceHash(p)}
	put := func(name string, raw []byte, kind, aid string) {
		target := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, raw, 0600); err != nil {
			t.Fatal(err)
		}
		manifest.Files = append(manifest.Files, hyperFramesFrozenFile{Path: name, SHA256: hashBytes(raw), Size: int64(len(raw)), Kind: kind, AssetID: aid})
	}
	for _, file := range p.HyperFrames.Files {
		put(file.Path, []byte(file.Content), "source", "")
	}
	assets, err := a.hyperFramesAssetFiles(p, *p.HyperFrames)
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range assets {
		raw, err := os.ReadFile(asset.SourceFile)
		if err != nil {
			t.Fatal(err)
		}
		put(asset.Path, raw, "asset", asset.AssetID)
	}
	raw, _ := json.Marshal(manifest)
	manifest.SnapshotHash = hashBytes(raw)
	for name, value := range map[string]any{"snapshot.json": p, "hyperframes-manifest.json": manifest} {
		if err := atomicJSON(filepath.Join(dir, name), value); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "final.mp4"), []byte("synthetic archival output bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	render := Render{ID: id, State: "completed", Engine: authoredHyperFramesEngine, Revision: p.Revision, SourceHash: narrationSourceHash(p), ContentHash: manifest.DocumentHash, CacheKey: manifest.SnapshotHash, Duration: p.HyperFrames.Canvas.Duration, HasAudio: p.HyperFrames.HasAudio}
	if err := a.saveRenderProof(p.ID, id, dir, manifest.SnapshotHash, ""); err != nil {
		t.Fatal(err)
	}
	p.Renders = append(p.Renders, render)
	a.projects[p.ID] = &p
	return p, render
}

func TestHyperFramesLocateHistoricalUsesFrozenOriginal(t *testing.T) {
	a, p := hyperFramesLocateFixture(t)
	p, render := archiveHyperFramesLocateFixture(t, a, p)
	query := map[string]any{"quote": "🙂发布。", "renderId": render.ID}
	w := locateCall(a, p.ID, query)
	var got EditLocateResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || !got.Current || got.Backend != "hyperframes" || len(got.Matches) != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	originalHash := got.SourceHash
	p.NarrationText = "完全不同的新稿。"
	p.Revision++
	p.HyperFrames.Files[0].Content = "changed live composition"
	a.projects[p.ID] = &p
	w = locateCall(a, p.ID, query)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Current || got.RenderID != render.ID || got.SourceHash != originalHash || got.Query.StartRune != 4 || len(got.Matches) != 1 || got.Matches[0].ClipID != "second" || !strings.Contains(got.Warning, "冻结时的原文") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := locateCall(a, p.ID, map[string]any{"quote": "不同的新稿", "renderId": render.ID}); w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
	// Switching the current backend never changes the meaning of the old source.
	wf := defaultWorkflow()
	p.Workflow = &wf
	a.projects[p.ID] = &p
	if w := locateCall(a, p.ID, query); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestHyperFramesLocateHistoricalRejectsArchiveTampering(t *testing.T) {
	for _, artifact := range []string{"snapshot.json", "index.html", "media/news.png", "final.mp4", "render-proof.json", "hyperframes-manifest.json"} {
		t.Run(artifact, func(t *testing.T) {
			a, p := hyperFramesLocateFixture(t)
			p, render := archiveHyperFramesLocateFixture(t, a, p)
			target := filepath.Join(a.dir(p.ID), "renders", render.ID, filepath.FromSlash(artifact))
			if err := os.WriteFile(target, []byte("tampered"), 0600); err != nil {
				t.Fatal(err)
			}
			if w := locateCall(a, p.ID, map[string]any{"quote": "🙂发布。", "renderId": render.ID}); w.Code != 409 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestHyperFramesSelectionDoesNotMarkHistoricalNativeEditCurrent(t *testing.T) {
	a, p := textRefProject(t)
	p, render := archiveLocateFixture(t, a, p, "ffmpeg_edit")
	wf := newProjectWorkflow()
	p.Workflow = &wf
	a.projects[p.ID] = &p
	w := locateCall(a, p.ID, map[string]any{"quote": "甲乙", "occurrence": 2, "renderId": render.ID})
	var got EditLocateResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Current || got.Backend != "native" || len(got.Matches) != 1 || got.Warning == "" {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestHyperFramesLocateArchiveRemainsReadableAfterLiveAssetChanges(t *testing.T) {
	a, p := hyperFramesLocateFixture(t)
	p, render := archiveHyperFramesLocateFixture(t, a, p)
	if err := os.WriteFile(filepath.Join(a.dir(p.ID), "workspace", "assets", p.Assets[0].File), []byte("changed live media"), 0600); err != nil {
		t.Fatal(err)
	}
	w := locateCall(a, p.ID, map[string]any{"quote": "🙂发布。", "renderId": render.ID})
	var got EditLocateResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Current || got.SourceHash != render.SourceHash || len(got.Matches) != 1 || got.Matches[0].ClipID != "second" || !strings.Contains(got.Warning, "不代表当前剪辑") {
		t.Fatal(w.Code, w.Body.String())
	}
}
