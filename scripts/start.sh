#!/usr/bin/env bash
set -euo pipefail
app_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
font_file="${VIDEO_FONT:-/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc}"
[[ -f "$font_file" ]] || { echo '请安装 fonts-noto-cjk，或设置 VIDEO_FONT 为中文字体的绝对路径'; exit 1; }
export HYPERFRAMES_NO_TELEMETRY=1
export DO_NOT_TRACK=1
exec "$app_dir/bin/video-app" \
  --data "${VIDEO_DATA:-$app_dir/data}" \
  --listen "${VIDEO_LISTEN:-127.0.0.1:3220}" \
  --rundesk "${RUNDESK_URL:-http://127.0.0.1:3210}" \
  --hyperframes "$app_dir/runtime/node_modules/hyperframes/bin/hyperframes.mjs" \
  --gsap "$app_dir/runtime/node_modules/gsap/dist/gsap.min.js" \
  --font "$font_file"
