#!/usr/bin/env bash
set -euo pipefail
app_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
command -v node >/dev/null || { echo '请先安装 Node.js 22 或以上版本'; exit 1; }
node -e 'if(Number(process.versions.node.split(".")[0])<22)process.exit(1)' || { echo '需要 Node.js 22+'; exit 1; }
command -v npm >/dev/null
command -v ffmpeg >/dev/null || { echo '请先安装 ffmpeg'; exit 1; }
command -v ffprobe >/dev/null
export HYPERFRAMES_NO_TELEMETRY=1
export DO_NOT_TRACK=1
npm ci --prefix "$app_dir/runtime" --no-audit --no-fund
node "$app_dir/runtime/node_modules/hyperframes/bin/hyperframes.mjs" browser ensure
printf '%s\n' '渲染工具已准备。请继续配置中文字体并运行 scripts/start.sh。'
