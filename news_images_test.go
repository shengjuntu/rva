package main

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func imageNewsFixture() map[string][]byte {
	files := scriptFixture("English source rewritten as Chinese: 中文口播。")
	var snapshot map[string]any
	json.Unmarshal(files["revision.json"], &snapshot)
	snapshot["document"].(map[string]any)["output_language"] = "zh-CN"
	files["revision.json"] = jsonBytes(snapshot)
	files["storyboard.json"] = []byte(`[]`)
	files["evidence_snapshot.json"] = []byte(`{"origin":"short_script_research"}`)
	var imageBytes bytes.Buffer
	png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 120, 90)))
	hash := hashBytes(imageBytes.Bytes())
	name := "images/" + hash + ".png"
	files[name] = imageBytes.Bytes()
	files["assets_manifest.json"] = jsonBytes(map[string]any{"images": []NewsImageSource{{SourceID: "s1", PageURL: "https://example.com/news", URL: "https://example.com/img.png", SHA256: hash, ArchivePath: name, Caption: "来自原文的候选图片", Status: "downloaded", Rights: "unknown"}}})
	var m newsManifest
	json.Unmarshal(files["manifest.json"], &m)
	m.OutputLanguage = "zh-CN"
	m.EventKey = "" // Short-script research is not attached to a clustered event.
	m.ContentHash = hashBytes(files["revision.json"])
	files["manifest.json"] = jsonBytes(m)
	rehashFixture(files)
	return files
}

func TestNewsImagesAndLanguageHandoff(t *testing.T) {
	a := testApp(t)
	p := importFixture(t, a, imageNewsFixture())
	if p.News.OutputLanguage != "zh-CN" || len(p.Assets) != 1 || p.Assets[0].Source.Usage != "candidate_only" || p.Assets[0].Source.Rights != "unknown" {
		t.Fatal(p)
	}
	for _, s := range p.Shots {
		if s.AssetID != "" {
			t.Fatal("candidate assigned without selection")
		}
	}
	asset := p.Assets[0]
	b, e := os.ReadFile(filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File))
	if e != nil || hashBytes(b) != asset.Source.SHA256 {
		t.Fatal("image bytes missing", e)
	}
	q, e := newApp(a.data)
	if e != nil || q.projects[p.ID].Assets[0].Source.PageURL != "https://example.com/news" {
		t.Fatal("provenance lost", e)
	}
}

func TestNewsImageArchiveRejectsInvalidEntries(t *testing.T) {
	for _, kind := range []string{"traversal", "undeclared", "wrong_image_hash", "wrong_language", "fake_png"} {
		t.Run(kind, func(t *testing.T) {
			a := testApp(t)
			files := imageNewsFixture()
			switch kind {
			case "traversal":
				files["images/../../escape.png"] = []byte("bad")
			case "undeclared":
				files["assets_manifest.json"] = []byte(`{"images":[]}`)
			case "wrong_image_hash":
				var v map[string][]NewsImageSource
				json.Unmarshal(files["assets_manifest.json"], &v)
				v["images"][0].SHA256 = "bad"
				files["assets_manifest.json"] = jsonBytes(v)
			case "wrong_language":
				var m newsManifest
				json.Unmarshal(files["manifest.json"], &m)
				m.OutputLanguage = "en"
				files["manifest.json"] = jsonBytes(m)
			case "fake_png":
				var v map[string][]NewsImageSource
				json.Unmarshal(files["assets_manifest.json"], &v)
				delete(files, v["images"][0].ArchivePath)
				hash := hashBytes([]byte("fake image"))
				name := "images/" + hash + ".png"
				files[name] = []byte("fake image")
				v["images"][0].SHA256 = hash
				v["images"][0].ArchivePath = name
				files["assets_manifest.json"] = jsonBytes(v)
			}
			rehashFixture(files)
			w := uploadNews(a, "/api/news/import", zipFixture(files, ""), 0)
			if w.Code < 400 || len(a.projects) != 0 {
				t.Fatal("unsafe bundle accepted", w.Code, w.Body.String())
			}
		})
	}
}
