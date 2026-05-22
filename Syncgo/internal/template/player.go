package template

import (
	"bytes"
	"fmt"
	"html/template"

	"syncgo/internal/util"
)

type PlayerData struct {
	Title     string
	FileSize  int64
	MimeType  string
	StreamURL string
}

const playerHTML = `<!DOCTYPE html>
<html lang="pt-BR">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} — Syncgo</title>
<style>
:root { color-scheme: dark; }
body { margin: 0; background: #0b0d10; color: #e6e8ec; font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; }
.wrap { max-width: 1100px; margin: 0 auto; padding: 24px; }
h1 { font-size: 18px; font-weight: 600; margin: 0 0 12px; word-break: break-all; }
.meta { color: #9aa0a6; font-size: 13px; margin-bottom: 16px; }
.player { background: #000; border-radius: 8px; overflow: hidden; aspect-ratio: 16 / 9; }
video, audio { width: 100%; height: 100%; display: block; background: #000; }
.actions { margin-top: 16px; display: flex; gap: 8px; flex-wrap: wrap; }
a.btn { background: #1a73e8; color: #fff; padding: 10px 16px; border-radius: 6px; text-decoration: none; font-size: 14px; }
a.btn.secondary { background: #2a2d31; }
</style>
</head>
<body>
<div class="wrap">
  <h1>{{.Title}}</h1>
  <div class="meta">{{.MimeType}} · {{.HumanSize}}</div>
  <div class="player">
    {{if .IsVideo}}
    <video controls preload="metadata" playsinline>
      <source src="{{.StreamURL}}" type="{{.MimeType}}">
      Seu navegador não suporta este vídeo.
    </video>
    {{else if .IsAudio}}
    <audio controls preload="metadata">
      <source src="{{.StreamURL}}" type="{{.MimeType}}">
    </audio>
    {{else}}
    <p style="padding:24px;text-align:center;">Pré-visualização indisponível para este tipo de arquivo.</p>
    {{end}}
  </div>
  <div class="actions">
    <a class="btn" href="{{.StreamURL}}?dl=1" download>Download</a>
    <a class="btn secondary" href="{{.StreamURL}}" target="_blank">Abrir direto</a>
  </div>
</div>
</body>
</html>`

var tmpl = template.Must(template.New("player").Parse(playerHTML))

func RenderPlayer(d PlayerData) string {
	type viewModel struct {
		PlayerData
		HumanSize string
		IsVideo   bool
		IsAudio   bool
	}
	vm := viewModel{
		PlayerData: d,
		HumanSize:  util.HumanBytes(d.FileSize),
		IsVideo:    isMimePrefix(d.MimeType, "video/"),
		IsAudio:    isMimePrefix(d.MimeType, "audio/"),
	}
	if d.Title == "" {
		vm.Title = "Stream"
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, vm); err != nil {
		return fmt.Sprintf("template error: %v", err)
	}
	return buf.String()
}

func isMimePrefix(mt, prefix string) bool {
	return len(mt) >= len(prefix) && mt[:len(prefix)] == prefix
}
