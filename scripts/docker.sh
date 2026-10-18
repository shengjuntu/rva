#!/usr/bin/env bash
set -euo pipefail
app_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
env_file="${VIDEO_ENV_FILE:-$app_dir/docker/.env}"
command_name="${1:-help}"
if [[ "$command_name" == init ]]; then
  [[ ! -e "$env_file" ]] || { echo "已有 $env_file，保留原配置。"; exit 0; }
  umask 077
  data_dir="$app_dir/.state/video"
  mkdir -p "$data_dir" "$(dirname -- "$env_file")"
  cp "$app_dir/docker/.env.example" "$env_file"
  # Paths are Compose env values, never shell-sourced. Escape $ for Compose.
  data_dir="${data_dir//\$/\$\$}"
  printf '\nVIDEO_UID=%s\nVIDEO_GID=%s\nVIDEO_HOST_DATA="%s"\n' "$(id -u)" "$(id -g)" "$data_dir" >> "$env_file"
  echo "已生成 $env_file。填写 RunDesk Token，确认对外入口后运行 build、up。"
  exit 0
fi
if [[ "$command_name" == help ]]; then
  echo '用法: scripts/docker.sh init|build|up|stop|logs|status|doctor|smoke|token'
  echo 'Linux Docker Engine + Compose v2；build 显式构建，up 仅启动已有镜像。'
  exit 0
fi
[[ -f "$env_file" ]] || { echo '请先运行 scripts/docker.sh init'; exit 1; }
command -v docker >/dev/null || { echo '请安装 Docker Engine 和 Compose v2'; exit 1; }
docker compose version >/dev/null
dc=(docker compose --env-file "$env_file" -f "$app_dir/docker/compose.yaml")
case "$command_name" in
  build) "${dc[@]}" build video-app ;;
  up) "${dc[@]}" up -d --no-build --wait --wait-timeout 90 video-app ;;
  stop) "${dc[@]}" stop video-app ;;
  logs) "${dc[@]}" logs --tail 150 -f video-app ;;
  status) "${dc[@]}" ps ;;
  doctor) "${dc[@]}" exec -T video-app node /opt/video-app/docker/doctor.mjs ;;
  smoke) "${dc[@]}" exec -T video-app node /opt/video-app/scripts/smoke-render.mjs ;;
  token) "${dc[@]}" exec -T video-app cat /data/access-token; echo ;;
  *) echo "未知命令: $command_name" >&2; exit 2 ;;
esac
