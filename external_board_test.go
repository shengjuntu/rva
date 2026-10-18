package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExternalBoardUsesArchivedEDLAsEditingAuthority(t *testing.T) {
	a, p, _, _ := exchangeFixture(t)
	run, p := prepareExchange(t, a, p)
	rid := uid()
	p.ProductionRuns[0].State = "completed"
	p.ProductionRuns[0].RenderID = rid
	p.ProductionRuns[0].EditHash = "edit-digest"
	p.ProductionRuns[0].OutputHash = "video-digest"
	p.Renders = append(p.Renders, Render{ID: rid, Engine: "video_use", State: "completed", ContentHash: run.InputBasisHash, SourceHash: run.SourceHash, Duration: run.Duration})
	// An unrelated saved native edit must not become this backend's edit card
	// or invalidate a report about an archived external EDL.
	native := simpleEdit()
	p.EditDSL = &native
	p.EditActive = false
	a.save(&p)
	a.projects[p.ID] = &p
	w := call(a, "GET", "/api/projects/"+p.ID+"/production-board", nil)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var board struct{ Artifacts []ProductionCard }
	if err := json.Unmarshal(w.Body.Bytes(), &board); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, card := range board.Artifacts {
		if card.ID != "edit" {
			continue
		}
		found = true
		if card.State != "ready" || len(card.Items) != 1 || !strings.HasSuffix(card.Items[0].URL, "/"+rid+"/edit-ir.json") {
			t.Fatal(card)
		}
	}
	if !found {
		t.Fatal("missing external edit artifact")
	}
	before := checkpointBasis(p, "review", rid)
	p.EditActive = true
	p.EditDSL.Canvas.Width = 720
	if checkpointBasis(p, "review", rid) != before {
		t.Fatal("inactive backend changed external review basis")
	}
	p.ProductionRuns[0].EditHash = "changed-external-edit"
	if checkpointBasis(p, "review", rid) == before {
		t.Fatal("external EDL change did not invalidate review basis")
	}
}
