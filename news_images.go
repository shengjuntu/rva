package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Provenance is retained as data. Importing a candidate does not approve its use.
type NewsImageSource struct {
	SourceID    string `json:"source_id"`
	PageURL     string `json:"source_page_url"`
	ContentHash string `json:"source_content_hash"`
	URL         string `json:"url"`
	Caption     string `json:"caption"`
	Rights      string `json:"rights_status"`
	Usage       string `json:"usage"`
	ArchivePath string `json:"archive_path"`
	SHA256      string `json:"sha256"`
	Status      string `json:"status"`
}

func newsImages(pkg newsPackage) ([]Asset, error) {
	var manifest struct {
		Images []NewsImageSource `json:"images"`
	}
	b, e := os.ReadFile(filepath.Join(pkg.Dir, "assets_manifest.json"))
	if e != nil {
		return nil, e
	}
	if json.Unmarshal(b, &manifest) != nil || len(manifest.Images) > 32 {
		return nil, errors.New("图片素材清单无效")
	}
	assets := []Asset{}
	seen := map[string]bool{}
	for _, candidate := range manifest.Images {
		if candidate.Status != "downloaded" {
			continue
		}
		f, exists := pkg.Files[candidate.ArchivePath]
		if !newsImagePath.MatchString(candidate.ArchivePath) || !exists || seen[candidate.ArchivePath] || f.SHA256 != candidate.SHA256 || strings.TrimSuffix(filepath.Base(candidate.ArchivePath), filepath.Ext(candidate.ArchivePath)) != f.SHA256 || len(assets) == 4 {
			return nil, errors.New("图片记录、数量或文件哈希不符")
		}
		b, e := os.ReadFile(filepath.Join(pkg.Dir, candidate.ArchivePath))
		if e != nil {
			return nil, e
		}
		mime := http.DetectContentType(b)
		ext := filepath.Ext(candidate.ArchivePath)
		if !(mime == "image/png" && ext == ".png" || mime == "image/jpeg" && (ext == ".jpg" || ext == ".jpeg") || mime == "image/webp" && ext == ".webp") {
			return nil, errors.New("图片格式与扩展名不符")
		}
		candidate.Rights, candidate.Usage = "unknown", "candidate_only"
		assetID := uid()
		name := candidate.Caption
		if name == "" {
			name = filepath.Base(candidate.ArchivePath)
		}
		assets = append(assets, Asset{ID: assetID, Name: shortTitle(name), File: assetID + ext, Size: f.Size, MIME: mime, Source: &candidate})
		seen[candidate.ArchivePath] = true
	}
	for name := range pkg.Files {
		if newsImagePath.MatchString(name) && !seen[name] {
			return nil, errors.New("存在图片清单未声明的文件")
		}
	}
	return assets, nil
}
