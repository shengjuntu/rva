================================================================================
rundesk-video-app (RVA)  —  Video Production Workbench for RunDesk/Codex
视频制作工作台（RunDesk/Codex 自动化视频生产）
================================================================================

【中文版】  [English below]

一、这是什么
----------------------------------------------------------------------
rundesk-video-app（简称 RVA / video-app）是一个用 Go 编写的视频制作工作台。
它负责：视频项目的创建与管理、文字稿（narration）的配音（TTS）、
图片/视频素材的生成与组织、编辑指令（Edit DSL）的执行、以及最终成片的
渲染、校验与归档。它把"制作"这件事交给 RunDesk / Codex（AI Agent），
自己则负责请求、任务、产物校验和保存——分工清晰、可审计。

典型用法：你在 Web 工作台（http://<host>:3220）里写一段口播稿，
App 调用 TTS 生成配音 → 生成/挑选配图 → Agent 按编辑 DSL 排布 →
渲染成片并校验存档。整个过程可以由 RunDesk 里的 Agent 自动驱动。

二、核心能力
----------------------------------------------------------------------
1) 项目与素材管理：项目快照、素材（图片/视频）入库、校验与私有归档。
2) 配音（TTS）：IndexTTS 等后端；支持字幕（SRT）、语速/时长测量。
3) 图片/视频生成适配器：通过 http_binary_v1 协议对接任意外部生成服务
   （图片/视频生成 URL、模型、密钥均可配置），App 负责幂等与产物校验。
4) 编辑 DSL：声明式编辑指令（文本/图片排版、时间线、替换），
   支持文本引用（textrefs）与增量补丁（edit patches）。
5) 三阶段生产交换（v0.21+）：prepare → Agent 渲染 → ingest，
   输入快照冻结、产物哈希校验、私有归档，适合生产流水线。
6) 运行态诊断与文本引用（v0.22+）：诊断图片输出、文本引用解析等。
7) MCP 接口：通过 MCP 暴露给 RunDesk/Codex，Agent 可调用
   video_job_start / video_job_get / video_production_run_* 等工具。

三、架构速览
----------------------------------------------------------------------
                        +---------------------------+
                        |   RunDesk / Codex (Agent)  |
                        |   - 制作视频（渲染/编排）   |
                        +-------------+--------------+
                                      | MCP / HTTP
                                      v
   +-----------------------------+  http_binary_v1   +----------------------+
   |      video-app (Go)          |<---------------->|  外部生成适配器       |
   |  请求/任务/校验/保存/归档    |  POST /image|/video |  (可插拔：图片/视频)  |
   |  TTS、EDL、生产交换、MCP     |   200 = 原始二进制  |  如 ComfyUI H3 适配器  |
   +-----------------------------+                    +----------------------+
              |
              | ffmpeg / chromium / node(渲染器)
              v
         成片（H.264/AAC MP4）+ 字幕 + 校验报告

- video-app 是"裁判与仓库"：它不直接跑模型，只负责编排、校验与存档。
- 生成能力通过 VIDEO_IMAGE_GEN_URL / VIDEO_VIDEO_GEN_URL 指向外部适配器。
- 适配器协议：POST JSON {"schema":"video.generation-request.v1","requestId":...,
  "type":"image_generation|video_generation","model":...,"prompt":...}
  成功响应 = HTTP 200 + 原始 PNG/JPEG/MP4 字节（非 URL、非 Base64、非异步）。
- 限制：图片 ≤32MiB/3200万像素，视频 ≤128MiB 且 ≤100s（ffprobe 校验）。

四、快速开始（Docker）
----------------------------------------------------------------------
1) 准备源码与配置文件（docker/.env 由 docker/.env.example 复制而来）：
     cd rundesk-video-app
     cp docker/.env.example docker/.env
     编辑 docker/.env：设置 RUNDESK_URL / RUNDESK_TOKEN 等。

2) 构建并启动：
     cd docker
     docker compose up -d --build
   （network_mode: host；App 监听 0.0.0.0:3220；数据目录 VIDEO_HOST_DATA）

3) 打开工作台：
     http://<服务器IP>:3220/
   API/MCP： http://<服务器IP>:3220/api/mcp  （Authorization: Bearer <token>）

4) 验证健康：
     docker ps --filter name=rundesk-video-video-app-1
     curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:3220/

五、主要环境变量
----------------------------------------------------------------------
  VIDEO_LISTEN            监听地址（默认 0.0.0.0:3220）
  VIDEO_APP_PUBLIC_URL    对外访问地址（如 http://192.168.0.234:3220）
  VIDEO_APP_MCP_URL       MCP 端点（供 RunDesk 桥接容器使用）
  RUNDESK_URL / RUNDESK_TOKEN   RunDesk 服务地址与令牌
  VIDEO_TTS_BACKEND/URL/MODEL/VOICE/LANGUAGE/TIMEOUT/API_KEY   配音后端
  VIDEO_IMAGE_GEN_URL/MODEL/KEY   图片生成适配器（http_binary_v1）
  VIDEO_VIDEO_GEN_URL/MODEL/KEY   视频生成适配器（http_binary_v1）
  VIDEO_UID/VIDEO_GID     容器运行 UID/GID
  VIDEO_HOST_DATA         持久数据目录（默认 ../.state/video）

六、代码结构与模块
----------------------------------------------------------------------
  main.go / bridge.go        服务入口、HTTP 与 MCP 桥接
  generation.go              生成任务与 http_binary_v1 适配器客户端
  exchange_validation.go     三阶段生产交换（prepare/ingest）校验
  edit_dsl.go / edit_patch.go / edit_text_refs.go  编辑 DSL、补丁、文本引用
  alignment.go / audio.go / speech / subtitles   对齐、TTS、字幕
  asset_preview.go / diagnostic_image.go          素材预览、诊断输出
  jobs.go / capabilities.go  任务队列、能力目录
  web/                       前端工作台（Node/hyperframes 渲染器）
  docker/                    Dockerfile / compose / 健康检查
  docs/                      协议与设计文档（GENERATION-0.15、EXCHANGE-0.21 等）

七、版本历史（本仓库按版本打 tag：v0.2.0 → v0.22.0）
----------------------------------------------------------------------
  v0.2.0   初始版本
  v0.3.0   基础服务
  v0.5.0 / v0.5.1   功能扩展
  v0.6.0   （大版本，含 bin 二进制）
  v0.7.0   Docker 化部署
  v0.8.0 / v0.8.1   IndexTTS 配音接入
  v0.10.0  服务基础（service foundation）
  v0.11.0  编辑 DSL
  v0.12.0  口播工作流（narration workflow）
  v0.13.0  Agent 技能工作流（workflow skills）
  v0.15.0  图片/视频生成适配器（generation adapter，http_binary_v1）
  v0.16.0  编辑补丁（edit patches）
  v0.17.0  编辑重做（edit redo）
  v0.18.0  预览与终稿（preview/final）
  v0.19.0  渲染缓存（render cache）
  v0.20.0  文本驱动工作台（text-driven workbench）
  v0.21.0  三阶段生产交换（three-stage video-use）
  v0.22.0  运行态诊断与文本引用（runtime diagnostics & textrefs）

八、与 RunDesk / Codex 的关系
----------------------------------------------------------------------
RunDesk 是承载 AI Agent（如 Codex）的编排平台。video-app 通过 MCP 暴露
能力，Agent 可以在 RunDesk 会话里：创建项目、提交配音任务、调用
video_job_start 发起图片/视频生成、用编辑 DSL 组装、触发生产交换渲染，
最后 ingest 回成片。简单说：Agent 负责"怎么剪"，video-app 负责
"剪得好不好、存得对不对"。

九、二次开发 / 集成新生成后端
----------------------------------------------------------------------
新视频/图片模型（如本地 ComfyUI MiniMax-H3）接入方式：
  1) 实现一个 http_binary_v1 适配器（HTTP 服务，POST /video|/image 返回原始
     MP4/PNG 字节；内部可调用 ComfyUI API：提交 /prompt → 轮询 /history →
     下载 /view 产物）。
  2) 在 docker/.env 设置 VIDEO_VIDEO_GEN_URL=http://<adapter>:<port>/video
     与 VIDEO_VIDEO_GEN_MODEL=...，重启容器。
  3) Agent/工作台即可通过现有 video_job_start(type=video_generation) 使用。
  参考 docs/GENERATION-0.15.md 与 docs/EXCHANGE-0.21.md。

十、验证与测试
----------------------------------------------------------------------
  go vet ./... && go test ./...      单元测试（含 generation、exchange 契约）
  docker compose up -d --build       容器级部署验证
  bin/video-app --version            版本自检
  浏览器打开工作台 → 新建项目 → 写口播稿 → 配音 → 出片

--------------------------------------------------------------------------------

================================================================================
ENGLISH VERSION
================================================================================

WHAT IT IS
----------------------------------------------------------------------
rundesk-video-app (RVA / video-app) is a video production workbench written in
Go. It manages video projects, narration (TTS voiceover), image/video asset
generation, declarative edit instructions (Edit DSL), and final render,
validation and archiving. It deliberately leaves "making the video" to
RunDesk/Codex AI agents, while the App itself owns requests, jobs, artifact
validation and storage — clean separation, fully auditable.

Typical flow: author a script in the web workbench (http://<host>:3220),
the App generates TTS narration, generates/selects images, an agent lays out
the video with Edit DSL, renders and archives the final cut. The whole flow
can be driven automatically by an agent inside RunDesk.

CORE FEATURES
----------------------------------------------------------------------
- Project & asset management: snapshots, media (image/video) ingestion,
  validation, private archiving.
- TTS voiceover: IndexTTS and other backends; subtitles (SRT), duration/timing.
- Image/video generation via http_binary_v1 adapters: pluggable external
  generation endpoints (URL/model/key configurable); App handles idempotency
  and artifact validation.
- Edit DSL: declarative editing (text/image layout, timeline, replacements),
  text references (textrefs) and incremental edit patches.
- Three-stage production exchange (v0.21+): prepare -> agent render -> ingest,
  frozen input snapshot, hash validation, private archive.
- Runtime diagnostics & textrefs (v0.22+).
- MCP interface exposed to RunDesk/Codex: video_job_start, video_job_get,
  video_production_run_* etc.

ARCHITECTURE
----------------------------------------------------------------------
   RunDesk / Codex (Agent: renders/orchestrates)
        |  MCP / HTTP
        v
   video-app (Go): requests / jobs / validation / storage / TTS / EDL / MCP
        |  http_binary_v1  POST /image|/video  -> 200 = raw bytes
        v
   External generation adapters (pluggable; e.g. ComfyUI MiniMax-H3 adapter)
        |
        v
   Final video (H.264/AAC MP4) + subtitles + validation report

- video-app is the "judge & warehouse": it never runs models itself.
- Generation is delegated to adapters configured via
  VIDEO_IMAGE_GEN_URL / VIDEO_VIDEO_GEN_URL.
- Adapter contract: POST JSON {"schema":"video.generation-request.v1",
  "requestId":..., "type":"image_generation|video_generation",
  "model":..., "prompt":...}  -> HTTP 200 + raw PNG/JPEG/MP4 bytes
  (no download URLs, no base64 JSON, no async polling).
- Limits: image <=32MiB / 32MP; video <=128MiB and <=100s (ffprobe checked).

QUICK START (Docker)
----------------------------------------------------------------------
  1) Prepare config:
       cd rundesk-video-app
       cp docker/.env.example docker/.env
       edit docker/.env (set RUNDESK_URL / RUNDESK_TOKEN ...)
  2) Build & run:
       cd docker && docker compose up -d --build
     (network_mode: host; listens on 0.0.0.0:3220; data in VIDEO_HOST_DATA)
  3) Open http://<server-ip>:3220/  (API/MCP: /api/mcp, Bearer token)
  4) Health: docker ps --filter name=rundesk-video-video-app-1

KEY ENVIRONMENT VARIABLES
----------------------------------------------------------------------
  VIDEO_LISTEN / VIDEO_APP_PUBLIC_URL / VIDEO_APP_MCP_URL
  RUNDESK_URL / RUNDESK_TOKEN
  VIDEO_TTS_BACKEND|URL|MODEL|VOICE|LANGUAGE|TIMEOUT_SECONDS|API_KEY
  VIDEO_IMAGE_GEN_URL|MODEL|KEY        (image adapter, http_binary_v1)
  VIDEO_VIDEO_GEN_URL|MODEL|KEY        (video adapter, http_binary_v1)
  VIDEO_UID / VIDEO_GID / VIDEO_HOST_DATA

CODE MAP
----------------------------------------------------------------------
  bridge.go / main.go      HTTP & MCP bridge, entrypoint
  generation.go            generation jobs + http_binary_v1 adapter client
  exchange_validation.go   three-stage production exchange validation
  edit_dsl.go / edit_patch.go / edit_text_refs.go   editing
  alignment.go / audio.go / speech / subtitles      TTS & timing
  asset_preview.go / diagnostic_image.go            preview & diagnostics
  jobs.go / capabilities.go                         job queue & capabilities
  web/                     frontend (Node/hyperframes renderer)
  docker/                  Dockerfile / compose / healthcheck
  docs/                    protocol docs (GENERATION-0.15, EXCHANGE-0.21, ...)

VERSION HISTORY (tags v0.2.0 -> v0.22.0)
----------------------------------------------------------------------
  v0.2.0   initial
  v0.3.0   base service
  v0.5.0 / v0.5.1   feature extensions
  v0.6.0   major release (with binaries)
  v0.7.0   dockerized deployment
  v0.8.0 / v0.8.1   IndexTTS voiceover
  v0.10.0  service foundation
  v0.11.0  edit DSL
  v0.12.0  narration workflow
  v0.13.0  workflow skills
  v0.15.0  generation adapter (http_binary_v1)
  v0.16.0  edit patches
  v0.17.0  edit redo
  v0.18.0  preview & final
  v0.19.0  render cache
  v0.20.0  text-driven workbench
  v0.21.0  three-stage video-use production exchange
  v0.22.0  runtime diagnostics & textrefs

RELATION TO RUNDESK / CODEX
----------------------------------------------------------------------
RunDesk is an orchestration platform hosting AI agents (e.g. Codex).
video-app exposes its capabilities over MCP; an agent inside RunDesk can
create projects, submit TTS jobs, call video_job_start to generate images/
videos, assemble with Edit DSL, trigger the production exchange render, and
finally ingest the cut. In short: the agent decides "how to edit"; video-app
decides "whether it is valid and correctly stored".

ADDING A NEW GENERATION BACKEND (e.g. local ComfyUI MiniMax-H3)
----------------------------------------------------------------------
  1) Implement an http_binary_v1 adapter: an HTTP service with
     POST /video|/image returning raw MP4/PNG bytes; internally talk to
     ComfyUI API (submit /prompt -> poll /history -> download /view).
  2) Set VIDEO_VIDEO_GEN_URL=http://<adapter>:<port>/video and
     VIDEO_VIDEO_GEN_MODEL=... in docker/.env; restart the container.
  3) Agents/workbench can then use video_job_start(type=video_generation).
  See docs/GENERATION-0.15.md and docs/EXCHANGE-0.21.md.

VALIDATION / TESTING
----------------------------------------------------------------------
  go vet ./... && go test ./...     unit + contract tests
  docker compose up -d --build       deployment verification
  bin/video-app --version            version self-check
  Workbench: new project -> script -> TTS -> render -> finalize
