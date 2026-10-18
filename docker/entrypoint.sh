#!/usr/bin/env sh
set -eu
umask 077
mkdir -p "$VIDEO_DATA/home" "$VIDEO_DATA/cache"
if [ "$#" -gt 0 ]; then exec "$@"; fi
exec /opt/video-app/scripts/start.sh
