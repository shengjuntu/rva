package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http"
)

// A deterministic small PNG checks the MCP image payload transport. It never
// invokes a model or implies that a model has interpreted the returned pixels.
func diagnosticImage(nonce string) ([]byte, error) {
	if !identifier.MatchString(nonce) {
		return nil, errors.New("nonce必须为24位小写十六进制")
	}
	seed := sha256.Sum256([]byte("rundesk.diagnostic-image.v1:" + nonce))
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			i := (x + y*3) % len(seed)
			img.SetRGBA(x, y, color.RGBA{R: seed[i], G: seed[(i+11)%32], B: byte(x*7 + y*5), A: 255})
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func (a *App) runtimeDiagnosticImage(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Nonce string `json:"nonce"`
	}
	if !decodeStrict(w, r, &in) {
		return
	}
	pngBytes, err := diagnosticImage(in.Nonce)
	if err != nil {
		fail(w, 422, err)
		return
	}
	metadata, _ := json.Marshal(map[string]any{"schemaVersion": 1, "nonce": in.Nonce, "imageSHA256": hashBytes(pngBytes), "modelVisionVerified": false})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"content": []any{
		map[string]string{"type": "text", "text": string(metadata)},
		map[string]string{"type": "image", "mimeType": "image/png", "data": base64.StdEncoding.EncodeToString(pngBytes)},
	}})
}
