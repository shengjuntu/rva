package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
)

type AssetImportRequest struct {
	Name      string `json:"name"`
	Data      string `json:"data"`
	RequestID string `json:"requestId"`
}

func (a *App) assetsList(w http.ResponseWriter, r *http.Request) {
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	views := []map[string]any{}
	for _, asset := range p.Assets {
		v := map[string]any{"id": asset.ID, "name": asset.Name, "mime": asset.MIME, "size": asset.Size, "url": "/api/projects/" + p.ID + "/assets/" + asset.ID}
		if asset.Source != nil {
			v["source"] = asset.Source
		}
		v["inspectable"] = strings.HasPrefix(asset.MIME, "image/") || strings.HasPrefix(asset.MIME, "video/")
		views = append(views, v)
	}
	writeJSON(w, 200, map[string]any{"revision": p.Revision, "assets": views})
}
func (a *App) assetImport(w http.ResponseWriter, r *http.Request) {
	var in AssetImportRequest
	if !decodeStrict(w, r, &in) {
		return
	}
	if !identifier.MatchString(in.RequestID) || in.Name == "" || len(in.Name) > 255 || filepath.Base(in.Name) != in.Name || strings.ContainsAny(in.Name, "\\/\r\n\x00") {
		fail(w, 400, errors.New("需要合法文件名和24位requestId"))
		return
	}
	if len(in.Data) > base64.StdEncoding.EncodedLen(1<<20) {
		fail(w, 413, errors.New("MCP导入最大1MiB；大文件使用multipart上传"))
		return
	}
	data, e := base64.StdEncoding.Strict().DecodeString(in.Data)
	if e != nil || len(data) == 0 || len(data) > 1<<20 {
		fail(w, 400, errors.New("data必须是非空标准Base64，解码后不超过1MiB"))
		return
	}
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, e := form.CreateFormFile("file", in.Name)
	if e != nil {
		fail(w, 400, e)
		return
	}
	part.Write(data)
	form.WriteField("requestId", in.RequestID)
	form.Close()
	req := r.Clone(r.Context())
	req.Body = io.NopCloser(&body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	response := &mcpResponse{header: http.Header{}}
	a.upload(response, req)
	if response.status >= 300 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(response.status)
		w.Write(response.body.Bytes())
		return
	}
	var p Project
	if e = json.Unmarshal(response.body.Bytes(), &p); e != nil {
		fail(w, 500, e)
		return
	}
	for _, asset := range p.Assets {
		if asset.ImportRequestID == in.RequestID {
			writeJSON(w, response.status, map[string]any{"revision": p.Revision, "asset": asset})
			return
		}
	}
	fail(w, 500, errors.New("素材导入回执缺失"))
}
