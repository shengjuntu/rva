package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type AssetDescription struct {
	Text  string   `json:"text"`
	Tags  []string `json:"tags,omitempty"`
	Basis string   `json:"basis"`
}
type AlignUnit struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}
type AlignRequest struct {
	Revision int         `json:"revision"`
	Units    []AlignUnit `json:"units"`
	Limit    int         `json:"limit,omitempty"`
}
type AlignCandidate struct {
	AssetID     string   `json:"assetId"`
	Score       float64  `json:"score"`
	Matches     []string `json:"matches"`
	Basis       string   `json:"basis"`
	Description string   `json:"description"`
}
type AlignResult struct {
	ID         string           `json:"id"`
	Candidates []AlignCandidate `json:"candidates"`
	Unmatched  bool             `json:"unmatched"`
}

func (a *App) assetDescribe(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revision    int              `json:"revision"`
		Description AssetDescription `json:"description"`
	}
	if !decodeStrict(w, r, &in) {
		return
	}
	d := in.Description
	if !utf8.ValidString(d.Text) || strings.TrimSpace(d.Text) == "" || len(d.Text) > 8000 || strings.ContainsRune(d.Text, 0) || len(d.Tags) > 30 || (d.Basis != "user" && d.Basis != "agent_inspection") {
		fail(w, 422, errors.New("描述需要正文和 basis=user 或 agent_inspection；最多30个标签"))
		return
	}
	for _, t := range d.Tags {
		if len(t) > 120 || !utf8.ValidString(t) || strings.ContainsRune(t, 0) {
			fail(w, 422, errors.New("标签无效"))
			return
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	p := a.projects[r.PathValue("id")]
	if p == nil {
		fail(w, 404, errors.New("项目不存在"))
		return
	}
	if p.Revision != in.Revision {
		fail(w, 409, errors.New("项目版本冲突"))
		return
	}
	q := clone(p)
	found := false
	for i := range q.Assets {
		if q.Assets[i].ID == r.PathValue("aid") {
			if !strings.HasPrefix(q.Assets[i].MIME, "image/") && !strings.HasPrefix(q.Assets[i].MIME, "video/") {
				fail(w, 422, errors.New("仅描述图片与视频"))
				return
			}
			q.Assets[i].Description = &d
			found = true
			break
		}
	}
	if !found {
		fail(w, 404, errors.New("素材不存在"))
		return
	}
	q.Revision++
	if e := a.save(&q); e != nil {
		fail(w, 500, e)
		return
	}
	a.projects[q.ID] = &q
	writeJSON(w, 200, q)
}

// Lexical baseline: explicit labels and observed descriptions, never inferred pixels.
func alignmentTerms(text string) map[string]bool {
	out := map[string]bool{}
	parts := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	for _, part := range parts {
		r := []rune(part)
		hasHan := false
		for _, x := range r {
			if unicode.Is(unicode.Han, x) {
				hasHan = true
				break
			}
		}
		if hasHan {
			for i := 0; i+1 < len(r); i++ {
				out[string(r[i:i+2])] = true
			}
		} else if len(r) > 1 {
			out[part] = true
		}
	}
	return out
}
func alignMedia(p Project, in AlignRequest) []AlignResult {
	out := []AlignResult{}
	limit := in.Limit
	if limit == 0 {
		limit = 5
	}
	for _, u := range in.Units {
		terms := alignmentTerms(u.Text)
		result := AlignResult{ID: u.ID, Candidates: []AlignCandidate{}, Unmatched: true}
		for _, asset := range p.Assets {
			if asset.Description == nil {
				continue
			}
			d := asset.Description
			other := alignmentTerms(d.Text + " " + strings.Join(d.Tags, " "))
			matches := []string{}
			for term := range terms {
				if other[term] {
					matches = append(matches, term)
				}
			}
			if len(matches) == 0 {
				continue
			}
			sort.Strings(matches)
			score := 2 * float64(len(matches)) / float64(len(terms)+len(other))
			result.Candidates = append(result.Candidates, AlignCandidate{asset.ID, score, matches, d.Basis, d.Text})
		}
		sort.Slice(result.Candidates, func(i, j int) bool {
			a, b := result.Candidates[i], result.Candidates[j]
			if a.Score == b.Score {
				return a.AssetID < b.AssetID
			}
			return a.Score > b.Score
		})
		if len(result.Candidates) > limit {
			result.Candidates = result.Candidates[:limit]
		}
		result.Unmatched = len(result.Candidates) == 0
		out = append(out, result)
	}
	return out
}
func (a *App) mediaAlign(w http.ResponseWriter, r *http.Request) {
	var in AlignRequest
	if !decodeStrict(w, r, &in) {
		return
	}
	if len(in.Units) == 0 || len(in.Units) > 100 || in.Limit < 0 || in.Limit > 20 {
		fail(w, 422, errors.New("需要1–100个文本单元，候选上限1–20"))
		return
	}
	seen := map[string]bool{}
	for _, u := range in.Units {
		if !editID.MatchString(u.ID) || seen[u.ID] || strings.TrimSpace(u.Text) == "" || len(u.Text) > 8000 || !utf8.ValidString(u.Text) || strings.ContainsRune(u.Text, 0) {
			fail(w, 422, errors.New("文本单元 ID 或正文无效"))
			return
		}
		seen[u.ID] = true
	}
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	if p.Revision != in.Revision {
		fail(w, 409, errors.New("项目版本冲突"))
		return
	}
	writeJSON(w, 200, map[string]any{"revision": p.Revision, "method": "description_lexical_dice_v1", "results": alignMedia(p, in), "notes": []string{"分数是词项重合率，不是置信概率或视觉语义相似度", "中文使用双字片段；候选仅依据用户或Agent提供的描述", "不保存剪辑、不强制一段文本对应一张图；Codex可组合或拒绝候选"}})
}

func (d *AssetDescription) UnmarshalJSON(raw []byte) error {
	type plain AssetDescription
	var v plain
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if e := dec.Decode(&v); e != nil {
		return e
	}
	*d = AssetDescription(v)
	return nil
}
func (u *AlignUnit) UnmarshalJSON(raw []byte) error {
	type plain AlignUnit
	var v plain
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if e := dec.Decode(&v); e != nil {
		return e
	}
	*u = AlignUnit(v)
	return nil
}
