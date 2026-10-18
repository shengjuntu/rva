ARG GO_IMAGE=golang:1.25.12-bookworm
ARG NODE_IMAGE=node:22-bookworm-slim
FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
COPY comfyh3 ./comfyh3
COPY web ./web
COPY integrations/video-project ./integrations/video-project
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath -ldflags='-s -w' -o /out/video-app .

FROM ${NODE_IMAGE} AS renderer
ENV DEBIAN_FRONTEND=noninteractive HYPERFRAMES_NO_TELEMETRY=1 DO_NOT_TRACK=1 \
    HYPERFRAMES_BROWSER_PATH=/usr/bin/chromium VIDEO_FONT=/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc \
    VIDEO_DATA=/data HOME=/data/home XDG_CACHE_HOME=/data/cache
# Browser and all its shared libraries come from the same Debian repository.
# There is no browser/npm/apt download on container startup.
RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates chromium ffmpeg fonts-noto-cjk fontconfig espeak-ng \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /opt/video-app
COPY runtime/package*.json ./runtime/
RUN npm ci --prefix runtime --no-audit --no-fund && npm cache clean --force
COPY --from=build /out/video-app ./bin/video-app
COPY scripts/start.sh scripts/smoke-render.mjs ./scripts/
COPY docker/entrypoint.sh docker/doctor.mjs ./docker/
RUN chmod 755 bin/video-app scripts/start.sh docker/entrypoint.sh \
    && mkdir -p /data && chown node:node /data \
    && node -e "require('fs').writeFileSync('/opt/video-app/runtime-versions.json',JSON.stringify({app:require('child_process').execFileSync('./bin/video-app',['--version'],{encoding:'utf8'}).trim().split(' ').pop(),node:process.version,...Object.fromEntries(['hyperframes','gsap'].map(n=>[n,require('./runtime/node_modules/'+n+'/package.json').version]))}))" \
    && dpkg-query -W > /opt/video-app/debian-packages.txt
USER node
EXPOSE 3220
ENTRYPOINT ["/opt/video-app/docker/entrypoint.sh"]
