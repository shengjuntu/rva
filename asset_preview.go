package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Give the RunDesk agent visual evidence rather than inferring contents from names.
func (a *App) assetPreview(w http.ResponseWriter, r *http.Request) {
	p, e := a.get(r.PathValue("id"))
	if e != nil {
		fail(w, 404, e)
		return
	}
	var asset *Asset
	for i := range p.Assets {
		if p.Assets[i].ID == r.PathValue("aid") {
			asset = &p.Assets[i]
			break
		}
	}
	if asset == nil {
		fail(w, 404, errors.New("素材不存在"))
		return
	}
	if !strings.HasPrefix(asset.MIME, "image/") && !strings.HasPrefix(asset.MIME, "video/") {
		fail(w, 400, errors.New("仅图片与视频支持视觉预览"))
		return
	}
	if filepath.Base(asset.File) != asset.File {
		fail(w, 400, errors.New("素材路径不合法"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	args := []string{"-nostdin", "-v", "error", "-i", filepath.Join(a.dir(p.ID), "workspace", "assets", asset.File), "-frames:v", "1", "-vf", "scale=640:640:force_original_aspect_ratio=decrease", "-f", "image2pipe", "-c:v", "png", "pipe:1"}
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	if e = cmd.Run(); e != nil {
		fail(w, 409, errors.New("无法提取素材预览，请检查FFmpeg和文件格式"))
		return
	}
	if out.Len() == 0 || out.Len() > 4<<20 {
		fail(w, 409, errors.New("预览图片大小异常"))
		return
	}
	writeJSON(w, 200, map[string]any{"content": []any{map[string]string{"type": "text", "text": "素材ID: " + asset.ID + "；名称: " + asset.Name + "。视频仅展示第一帧，不代表完整视频内容。"}, map[string]string{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(out.Bytes())}}})
}
