package main

import (
	"encoding/json"
	"testing"
)

func TestMediaAlignmentEvidence(t *testing.T) {
	p := Project{Assets: []Asset{{ID: "b", MIME: "image/png", Description: &AssetDescription{Text: "上海 港口 航运", Basis: "user"}}, {ID: "a", MIME: "video/mp4", Description: &AssetDescription{Text: "上海 港口 航运", Basis: "agent_inspection"}}, {ID: "c", MIME: "image/png", Description: &AssetDescription{Text: "北京 会议", Basis: "user"}}, {ID: "d", Name: "上海港口.png", MIME: "image/png"}}}
	r := alignMedia(p, AlignRequest{Units: []AlignUnit{{ID: "free", Text: "上海港口航运"}, {ID: "none", Text: "森林"}}, Limit: 2})
	if len(r[0].Candidates) != 2 || r[0].Candidates[0].AssetID != "a" || r[0].Candidates[0].Basis != "agent_inspection" || len(r[0].Candidates[0].Matches) == 0 || !r[1].Unmatched {
		t.Fatal(r)
	}
}
func TestDescriptionAndAlignmentAPI(t *testing.T) {
	a := testApp(t)
	p, _ := a.create("align")
	p.Assets = []Asset{{ID: uid(), MIME: "image/png"}}
	a.save(&p)
	a.projects[p.ID] = &p
	url := "/api/projects/" + p.ID
	aid := p.Assets[0].ID
	w := call(a, "PUT", url+"/assets/"+aid+"/description", map[string]any{"revision": p.Revision, "description": AssetDescription{Text: "上海港口", Basis: "user"}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	json.Unmarshal(w.Body.Bytes(), &p)
	before := p.Revision
	w = call(a, "POST", url+"/media/align", AlignRequest{Revision: p.Revision, Units: []AlignUnit{{ID: "lead", Text: "上海港口新闻"}}})
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var result struct{ Results []AlignResult }
	json.Unmarshal(w.Body.Bytes(), &result)
	if len(result.Results) != 1 || len(result.Results[0].Candidates) != 1 {
		t.Fatal(w.Body.String())
	}
	q, _ := a.get(p.ID)
	if q.Revision != before || q.EditDSL != nil {
		t.Fatal("alignment wrote editing")
	}
	w = call(a, "POST", url+"/media/align", AlignRequest{Revision: p.Revision - 1, Units: []AlignUnit{{ID: "lead", Text: "上海"}}})
	if w.Code != 409 {
		t.Fatal(w.Code)
	}
	w = call(a, "PUT", url+"/assets/"+aid+"/description", map[string]any{"revision": p.Revision, "description": AssetDescription{Text: "x", Basis: "invented"}})
	if w.Code != 422 {
		t.Fatal(w.Code)
	}
}
