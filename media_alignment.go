package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// This document records editorial choices. It is neither a scene template nor
// an assertion that a model has visually inspected the selected media.
type MediaAlignmentDocument struct {
	SourceHash string               `json:"sourceHash"`
	BasisHash  string               `json:"basisHash"`
	Revision   int                  `json:"revision"`
	Updated    string               `json:"updated"`
	Notes      string               `json:"notes,omitempty"`
	Units      []MediaAlignmentUnit `json:"units"`
}
type MediaAlignmentUnit struct {
	ID           string                `json:"id"`
	Quote        string                `json:"quote"`
	Occurrence   int                   `json:"occurrence"`
	VisualIntent string                `json:"visualIntent"`
	AssetIDs     []string              `json:"assetIds,omitempty"`
	Selections   []MediaAlignmentRange `json:"selections,omitempty"`
	Role         string                `json:"role"`
	Reason       string                `json:"reason"`
	Status       string                `json:"status"`
}
type MediaAlignmentRange struct {
	AssetID   string  `json:"assetId"`
	SourceIn  float64 `json:"sourceIn"`
	SourceOut float64 `json:"sourceOut"`
}
type MediaAlignmentRequest struct {
	Revision   int                  `json:"revision"`
	SourceHash string               `json:"sourceHash"`
	Notes      string               `json:"notes,omitempty"`
	Units      []MediaAlignmentUnit `json:"units"`
}
type MediaAlignmentCoverage struct {
	TotalUnits        int                     `json:"totalUnits"`
	SelectedUnits     int                     `json:"selectedUnits"`
	PlannedUnits      int                     `json:"plannedUnits"`
	MissingUnits      int                     `json:"missingUnits"`
	TotalCharacters   int                     `json:"totalCharacters"`
	CoveredCharacters int                     `json:"coveredCharacters"`
	Complete          bool                    `json:"complete"`
	Uncovered         []MediaAlignmentTextGap `json:"uncovered"`
}
type MediaAlignmentTextGap struct {
	Quote     string `json:"quote"`
	StartRune int    `json:"startRune"`
	EndRune   int    `json:"endRune"`
}
type MediaAlignmentAsset struct {
	AssetID    string           `json:"assetId"`
	Name       string           `json:"name"`
	MIME       string           `json:"mime"`
	SHA256     string           `json:"sha256,omitempty"`
	Origin     string           `json:"origin"`
	Source     *NewsImageSource `json:"source,omitempty"`
	Generation *GenerationJob   `json:"generation,omitempty"`
}
type MediaAlignmentView struct {
	ProjectID    string                  `json:"projectId"`
	Revision     int                     `json:"revision"`
	SourceHash   string                  `json:"sourceHash"`
	Alignment    *MediaAlignmentDocument `json:"alignment"`
	State        string                  `json:"state"`
	Coverage     MediaAlignmentCoverage  `json:"coverage"`
	StaleReasons []string                `json:"staleReasons"`
	Assets       []MediaAlignmentAsset   `json:"assets"`
}

func validAlignmentText(s string, limit int, required bool) bool {
	return len(s) <= limit && utf8.ValidString(s) && !strings.ContainsRune(s, 0) && (!required || strings.TrimSpace(s) != "")
}

// Occurrences count overlapping matches and use one-based numbering. Omitting
// the number is safe only when the exact quote appears once in the source.
func alignmentQuoteRange(text, quote string, occurrence int) (int, int, int) {
	if quote == "" {
		return -1, -1, 0
	}
	starts := []int{}
	for offset := 0; offset <= len(text)-len(quote); {
		at := strings.Index(text[offset:], quote)
		if at < 0 {
			break
		}
		at += offset
		starts = append(starts, at)
		_, size := utf8.DecodeRuneInString(text[at:])
		offset = at + size
	}
	if occurrence == 0 && len(starts) == 1 {
		occurrence = 1
	}
	if occurrence < 1 || occurrence > len(starts) {
		return -1, -1, len(starts)
	}
	return starts[occurrence-1], starts[occurrence-1] + len(quote), len(starts)
}

func mediaAlignmentCoverage(text string, units []MediaAlignmentUnit) MediaAlignmentCoverage {
	c := MediaAlignmentCoverage{TotalUnits: len(units), Uncovered: []MediaAlignmentTextGap{}}
	covered := make([]bool, len(text))
	for _, unit := range units {
		switch unit.Status {
		case "selected":
			c.SelectedUnits++
			start, end, _ := alignmentQuoteRange(text, unit.Quote, unit.Occurrence)
			if start >= 0 {
				for i := start; i < end; i++ {
					covered[i] = true
				}
			}
		case "planned":
			c.PlannedUnits++
		case "missing":
			c.MissingUnits++
		}
	}
	gapStart, gapEnd, gapStartRune, gapEndRune, runeIndex := -1, 0, 0, 0, 0
	flushGap := func() {
		if gapStart >= 0 {
			c.Uncovered = append(c.Uncovered, MediaAlignmentTextGap{Quote: text[gapStart:gapEnd], StartRune: gapStartRune, EndRune: gapEndRune})
			gapStart = -1
		}
	}
	for i, r := range text {
		currentRune := runeIndex
		runeIndex++
		// Punctuation/spacing alone should not create a fake missing shot.
		if unicode.IsSpace(r) || unicode.IsPunct(r) {
			continue
		}
		c.TotalCharacters++
		if covered[i] {
			c.CoveredCharacters++
			flushGap()
		} else {
			if gapStart < 0 {
				gapStart, gapStartRune = i, currentRune
			}
			gapEnd, gapEndRune = i+utf8.RuneLen(r), currentRune+1
		}
	}
	flushGap()
	c.Complete = c.TotalUnits > 0 && c.TotalCharacters > 0 && c.CoveredCharacters == c.TotalCharacters && c.PlannedUnits == 0 && c.MissingUnits == 0
	return c
}

func alignmentAssetIDs(units []MediaAlignmentUnit) []string {
	seen := map[string]bool{}
	for _, unit := range units {
		for _, id := range unit.AssetIDs {
			seen[id] = true
		}
		for _, s := range unit.Selections {
			seen[s.AssetID] = true
		}
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Hash both actual bytes and editorial/provenance metadata. New unrelated
// assets do not invalidate an existing choice, but changing a chosen file or
// its source does. Managed assets never come from the Agent exchange folder.
func (a *App) mediaAlignmentBasis(p Project, units []MediaAlignmentUnit) (string, []MediaAlignmentAsset, error) {
	views := []MediaAlignmentAsset{}
	basis := []any{}
	ids := alignmentAssetIDs(units)
	if len(ids) == 0 {
		return hashBytes([]byte("[]")), views, nil
	}
	root, err := os.OpenRoot(filepath.Join(a.dir(p.ID), "workspace", "assets"))
	if err != nil {
		return "", views, errors.New("无法读取所选素材目录")
	}
	defer root.Close()
	assets := map[string]Asset{}
	for _, asset := range p.Assets {
		assets[asset.ID] = asset
	}
	for _, id := range ids {
		asset, ok := assets[id]
		if !ok || filepath.Base(asset.File) != asset.File || !strings.HasPrefix(asset.MIME, "image/") && !strings.HasPrefix(asset.MIME, "video/") {
			return "", views, fmt.Errorf("所选视觉素材%s不存在或格式无效", id)
		}
		f, err := root.Open(asset.File)
		if err != nil {
			return "", views, fmt.Errorf("无法读取所选素材%s", id)
		}
		stat, err := f.Stat()
		if err != nil || !stat.Mode().IsRegular() {
			f.Close()
			return "", views, fmt.Errorf("所选素材%s不是普通文件", id)
		}
		digest, size, err := hashReader(f)
		f.Close()
		if err != nil {
			return "", views, fmt.Errorf("无法核对所选素材%s", id)
		}
		view := MediaAlignmentAsset{AssetID: id, Name: asset.Name, MIME: asset.MIME, SHA256: digest, Origin: "unrecorded", Source: asset.Source}
		if asset.Source != nil {
			view.Origin = "news_import"
		}
		for i := len(p.Generation) - 1; i >= 0; i-- {
			if p.Generation[i].AssetID == id && p.Generation[i].State == "completed" {
				job := p.Generation[i]
				view.Generation, view.Origin = &job, "generated"
				break
			}
		}
		views = append(views, view)
		basis = append(basis, map[string]any{"asset": asset, "sha256": digest, "size": size, "generation": view.Generation})
	}
	raw, err := json.Marshal(basis)
	if err != nil {
		return "", views, err
	}
	return hashBytes(raw), views, nil
}

func (a *App) mediaAlignmentView(p Project) MediaAlignmentView {
	v := MediaAlignmentView{ProjectID: p.ID, Revision: p.Revision, SourceHash: narrationSourceHash(p), Alignment: p.MediaAlignment, State: "empty", StaleReasons: []string{}, Assets: []MediaAlignmentAsset{}}
	if p.MediaAlignment == nil {
		v.Coverage = mediaAlignmentCoverage(p.NarrationText, nil)
		return v
	}
	doc := p.MediaAlignment
	v.Coverage = mediaAlignmentCoverage(p.NarrationText, doc.Units)
	v.State = "blocked"
	if v.Coverage.Complete {
		v.State = "ready"
	}
	if doc.SourceHash != v.SourceHash {
		v.StaleReasons = append(v.StaleReasons, "主播稿已变化，原文引用需重新核对")
	}
	basis, assets, err := a.mediaAlignmentBasis(p, doc.Units)
	v.Assets = assets
	if err != nil {
		v.StaleReasons = append(v.StaleReasons, err.Error())
	} else if basis != doc.BasisHash {
		v.StaleReasons = append(v.StaleReasons, "已选素材、描述、来源或生成记录已变化")
	}
	if len(v.StaleReasons) > 0 {
		v.State = "stale"
	}
	return v
}

func (a *App) mediaAlignmentGet(w http.ResponseWriter, r *http.Request) {
	p, err := a.get(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 200, a.mediaAlignmentView(p))
}

func (a *App) mediaAlignmentUpdate(w http.ResponseWriter, r *http.Request) {
	var in MediaAlignmentRequest
	if !decodeStrict(w, r, &in) {
		return
	}
	p, err := a.get(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	if p.Revision != in.Revision || strings.TrimSpace(p.NarrationText) == "" || in.SourceHash != narrationSourceHash(p) {
		fail(w, 409, errors.New("项目修订或主播稿指纹已变化，请重新读取"))
		return
	}
	if len(in.Units) > 200 || !validAlignmentText(in.Notes, 16000, false) {
		fail(w, 422, errors.New("最多200个自由文本关联单元，说明最多16000字节"))
		return
	}
	assets := map[string]Asset{}
	for _, asset := range p.Assets {
		assets[asset.ID] = asset
	}
	seen := map[string]bool{}
	durations := map[string]float64{}
	for i := range in.Units {
		u := &in.Units[i]
		if !editID.MatchString(u.ID) || seen[u.ID] || !validAlignmentText(u.Quote, 32000, true) || !validAlignmentText(u.VisualIntent, 8000, true) || !validAlignmentText(u.Reason, 8000, true) || len(u.AssetIDs)+len(u.Selections) > 32 {
			fail(w, 422, fmt.Errorf("关联单元%d需要唯一ID、原文、画面意图及选用/缺失理由", i+1))
			return
		}
		seen[u.ID] = true
		start, _, count := alignmentQuoteRange(p.NarrationText, u.Quote, u.Occurrence)
		if start < 0 {
			fail(w, 422, fmt.Errorf("单元%s必须精确引用主播稿；重复原文需提供1起算的occurrence（共%d处）", u.ID, count))
			return
		}
		if u.Occurrence == 0 {
			u.Occurrence = 1
		}
		if u.Role != "evidence" && u.Role != "illustration" && u.Role != "background" && u.Role != "textcard" || u.Status != "selected" && u.Status != "missing" && u.Status != "planned" {
			fail(w, 422, fmt.Errorf("单元%s的画面用途或状态无效", u.ID))
			return
		}
		if u.Status == "selected" && len(u.AssetIDs)+len(u.Selections) == 0 && u.Role != "textcard" {
			fail(w, 422, fmt.Errorf("单元%s标为selected时须引用素材；文字卡可由剪辑生成", u.ID))
			return
		}
		seenAssets := map[string]bool{}
		for _, id := range u.AssetIDs {
			asset, ok := assets[id]
			if !ok || seenAssets[id] || !strings.HasPrefix(asset.MIME, "image/") && !strings.HasPrefix(asset.MIME, "video/") {
				fail(w, 422, fmt.Errorf("单元%s引用了不存在、重复或非视觉素材", u.ID))
				return
			}
			seenAssets[id] = true
		}
		for _, selection := range u.Selections {
			asset, ok := assets[selection.AssetID]
			if !ok || !strings.HasPrefix(asset.MIME, "video/") || filepath.Base(asset.File) != asset.File || !finite(selection.SourceIn) || !finite(selection.SourceOut) || selection.SourceIn < 0 || selection.SourceOut <= selection.SourceIn || seenAssets[selection.AssetID] {
				fail(w, 422, fmt.Errorf("单元%s的视频选择须引用视频、给出有效起止秒数，且不能与整份素材重复", u.ID))
				return
			}
			duration, ok := durations[asset.ID]
			if !ok {
				root, openErr := os.OpenRoot(filepath.Join(a.dir(p.ID), "workspace", "assets"))
				if openErr != nil {
					fail(w, 422, errors.New("无法读取视频素材目录"))
					return
				}
				stat, statErr := root.Lstat(asset.File)
				root.Close()
				if statErr != nil || !stat.Mode().IsRegular() {
					fail(w, 422, fmt.Errorf("视频素材%s不是普通文件", asset.ID))
					return
				}
				ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
				duration, err = editMediaDuration(ctx, filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File), "video")
				cancel()
				if err != nil {
					fail(w, 422, fmt.Errorf("无法确认视频%s的实际时长", asset.ID))
					return
				}
				durations[asset.ID] = duration
			}
			if selection.SourceOut > duration+0.001 {
				fail(w, 422, fmt.Errorf("单元%s的视频范围超出实际时长%.3f秒", u.ID, duration))
				return
			}
		}
	}
	if len(alignmentAssetIDs(in.Units)) > 120 {
		fail(w, 422, errors.New("一份关联表最多引用120份视觉素材"))
		return
	}
	basis, validatedAssets, err := a.mediaAlignmentBasis(p, in.Units)
	if err != nil {
		fail(w, 422, err)
		return
	}
	doc := MediaAlignmentDocument{SourceHash: in.SourceHash, BasisHash: basis, Revision: p.Revision, Updated: time.Now().UTC().Format(time.RFC3339Nano), Notes: in.Notes, Units: in.Units}
	if doc.Units == nil {
		doc.Units = []MediaAlignmentUnit{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	current := a.projects[p.ID]
	if current == nil || current.Revision != in.Revision || narrationSourceHash(*current) != in.SourceHash {
		fail(w, 409, errors.New("保存期间项目已变化，请重新读取"))
		return
	}
	q := clone(current)
	q.MediaAlignment = &doc
	q.Revision++
	if err = a.save(&q); err != nil {
		fail(w, 500, err)
		return
	}
	a.projects[q.ID] = &q
	// This result uses the just-validated snapshot; avoid hashing large files a
	// second time while holding the project lock.
	v := MediaAlignmentView{ProjectID: q.ID, Revision: q.Revision, SourceHash: in.SourceHash, Alignment: &doc, State: "blocked", Coverage: mediaAlignmentCoverage(q.NarrationText, doc.Units), StaleReasons: []string{}, Assets: validatedAssets}
	if v.Coverage.Complete {
		v.State = "ready"
	}
	writeJSON(w, 200, v)
}

func (s *MediaAlignmentRange) UnmarshalJSON(raw []byte) error {
	type plain MediaAlignmentRange
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for _, name := range []string{"sourceIn", "sourceOut"} {
		if len(fields[name]) == 0 || bytes.Equal(bytes.TrimSpace(fields[name]), []byte("null")) {
			return fmt.Errorf("%s必须是秒数", name)
		}
	}
	var value plain
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&value); err != nil {
		return err
	}
	*s = MediaAlignmentRange(value)
	return nil
}
