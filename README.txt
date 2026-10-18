rundesk-video-app (RVA) · v0.24.0
视频制作工作台 / Video Production Workbench for RunDesk/Codex

【中文版】 English follows below.

一、日常使用

提供正文和已有素材，点击“开始制作”，查看成片，再通过对话修改。
RunDesk/Codex 决定分镜、选材、生成、配音、动画和重做；Video App 提供
制作 MCP、版本检查、素材与成片保存。正文是内容依据，画面服务文字。
不要求一句话一张图，也不把正文段落固定投影为镜头模板。

1. 首次打开可自动建立空项目；也可从项目列表新建、打开或重命名项目。
2. 粘贴或导入 TXT/Markdown 正文，导入本地素材目录或文件，补充制作要求。
   目录恰好有一份 TXT/MD 且正文为空时，会将其载入正文草稿；已有正文
   不会被自动覆盖。多份文本全部保留，由用户显式选择“作为正文”。
3. 点击“开始制作”。程序先保存最新正文与要求，再交给 RunDesk。
   未发送的对话草稿仍保留，不会自动变成这次初稿的制作指令。
4. 在“视频与版本”查看已完成成片或历史版本，下载选定的 MP4；
   不满意时通过对话提出修改。完整输出须在实际观看后再确认为终稿。

未发送的对话、正文和要求草稿按项目保存在本机浏览器；正式对话历史由
RunDesk 事件重放。接收情况不确定时确认原提交，沿用原 requestId 和内容，
不要换编号重复启动。停止操作不保证远端模型已经停止或释放显存。

主页面只有项目、输入、简要状态、成片和对话。手动 TTS、渲染、DSL、对齐、
多轨时间线、源码和详细工具日志不在本版 UI 中；详细执行与调试在 RunDesk。
旧后台接口保留供 Agent 和既有集成使用，不需要恢复旧手动控制台。

二、后台能力与边界

- 新项目默认 HyperFrames：Agent 编写 HTML/CSS/JavaScript/GSAP，绑定真实
  素材，App 冻结工程、检查并保存实际视频。声明的时间线不是逐帧验证证据。
- IndexTTS：连接已部署的 HTTP 语音服务；eSpeak 用于机械预览。实际合成
  片段的测量时序不等于逐字强制对齐，也不代表完成听审。
- 图片/视频生成：支持 http_binary_v1 服务；另内置 ComfyUI MiniMax-H3
  文生视频客户端，无需为 H3 再启动一层生成适配器。
- 素材观察与文字关联：真实图片/抽帧、精确原文引用、素材来源和选择理由。
  检索与记录不等于模型已经理解全部画面或核实新闻事实。
- 旧项目的 native DSL/FFmpeg、video_use EDL 和旧镜头工程继续保留原后端。
  video_use 的 prepare/Agent-render/ingest 是可选兼容流程，使用专用共享
  工作目录；HyperFrames 主流程不需要它，也不读取 RunDesk 私人数据目录。

素材准备与对齐、剪辑、精修是后台产物分类，可以往返处理，不是用户必须
逐项点击的流程。工具成功、检查通过和 Agent 自检都不代替具体输出的人审。

三、启动与 Docker 部署

已有渲染依赖时，本机启动示例：
  VIDEO_DATA=./data VIDEO_FONT=/absolute/path/to/NotoSansCJK-Regular.ttc bash scripts/start.sh

默认地址 http://127.0.0.1:3220。令牌保存在 data/access-token，或设置至少
24 字符的 VIDEO_APP_TOKEN。源码构建使用 bash scripts/build.sh。
HyperFrames 需要 Node.js 22+、Chromium、HyperFrames 0.8.87、GSAP 3.15.0、
FFmpeg/FFprobe 和字体；scripts/start.sh 显式传入已安装的 HyperFrames/GSAP
路径。新本机环境可使用 scripts/setup-renderer.sh 准备渲染依赖，已有环境
不必重装；随包 App Dockerfile 在构建时准备这些依赖。
IndexTTS 和 ComfyUI 模型在独立服务运行，App 镜像不加载 GPU 模型权重。

Linux Docker Engine + Compose v2：
  bash scripts/docker.sh init
  # 编辑 docker/.env：保留已有数据路径、地址、令牌和服务设置。
  # 旧安装显式更新 VIDEO_IMAGE=rundesk-video-app:0.24.0。
  bash scripts/docker.sh build
  bash scripts/docker.sh up
  bash scripts/docker.sh token
  bash scripts/docker.sh doctor

init 不覆盖已有配置，up 不自动构建。沿用原 VIDEO_HOST_DATA，升级前等待
活动任务结束并备份完整数据。App 渲染容器与 RunDesk 的 Agent 工具容器分别
更新；已配置的 RunDesk 0.49.5 / video-build 1.4.0 可继续使用。认证、模型、
Skills/MCP 和执行环境在 RunDesk 管理，不向 App 挂载 Docker socket。

详见 docs/DOCKER.md、docs/UPGRADING.md。更换 RunDesk 服务或机器不意味着
旧会话、认证和秘密会自动迁移；先核对原有绑定和各自持久目录。

四、IndexTTS 与 ComfyUI 配置

打开主页面“设置”进入 /settings.html，保存 IndexTTS 地址、模型、音色、
语言、超时和可选 API Key，再点“检查连接”。可读取服务提供的音色列表。
已有 data/speech-config.json 继续沿用；非空 VIDEO_TTS_* 环境变量会使配音
配置表单只读。完整变量名包括 VIDEO_TTS_TIMEOUT_SECONDS。

  VIDEO_TTS_BACKEND=indextts
  VIDEO_TTS_URL=http://127.0.0.1:8092/v1/audio/speech
  VIDEO_TTS_MODEL=IndexTeam/IndexTTS-2.5
  VIDEO_TTS_VOICE=news_zh
  VIDEO_TTS_LANGUAGE=zh
  VIDEO_TTS_TIMEOUT_SECONDS=180
  VIDEO_TTS_API_KEY=

服务须直接返回 WAV。Gradio 网页或返回 JSON 文件路径的接口不能直接使用；
可在已有 IndexTTS 环境运行 integrations/indextts/server.py，复用现有模型。
配置、适配器与命令行 speech-doctor 见 docs/INDEXTTS.md。

内置 H3 在 App 启动环境或 docker/.env 配置，修改后重启 App：
  VIDEO_VIDEO_GEN_URL=comfy://127.0.0.1:19897
  VIDEO_VIDEO_GEN_MODEL=minimax_h3_fl2va
  VIDEO_VIDEO_GEN_KEY=
  VIDEO_COMFY_H3_AUDIO_GAIN_DB=18

ComfyUI 需预装原工作流的节点和模型。comfy:// 连接指定主机/端口的 HTTP
服务，可选 KEY 用作 Bearer。默认保留安静音轨的 +18dB 增益修复并限幅；
设为 0 可保留原始媒体，不声称已经完成响度或音质验收。
当前仅文生视频，参考图请求明确拒绝。默认 124 帧、24fps，目标时长换算后
限制在 124..362 帧；目标 60 秒不能生成 60 秒。任务记录目标、生效参数和
调整说明，产物实际时长以媒体探测为准。H3 素材原声不等于 IndexTTS 主播
配音，最终混音由 Agent 明确安排。详见 docs/COMFYUI-H3.md。

通用 VIDEO_IMAGE_GEN_URL/MODEL/KEY 与 VIDEO_VIDEO_GEN_URL/MODEL/KEY
仍支持 http_binary_v1：POST JSON，成功直接返回原始 PNG/JPEG/MP4 字节，
不接受下载 URL、Base64 JSON 或异步任务对象。同一进程的视频生成配置
当前选择内置 H3 或一个通用适配器。协议见 docs/GENERATION-0.15.md。

五、探测、测试与升级边界

设置页展示部署配置并可请求服务发现；GET /api/capabilities?probe=1 或 MCP
video_capabilities_get(probe=true) 用于受支持的只读探测。IndexTTS 模型列表、
ComfyUI 系统/节点信息可达，都不等于已实际合成、生成或验收模型质量。
Docker doctor 检查 App、渲染依赖、RunDesk 和现有配音连接；不执行 GPU
推理，也不是一次 ComfyUI 视频生成测试。详细运行诊断在 RunDesk 查看。

测试入口：
  go vet ./...
  go test ./...
  node --test tests/*.test.cjs          # 需独立安装 jsdom 开发依赖
  bin/video-app speech-doctor --data ./data
  bin/video-app --version

本轮合并保留 shengjuntu/rva 的 065e4c7 及之前的内置 H3、音轨修复和双语
说明，加入 HyperFrames 与极简工作台。历史：v0.8 IndexTTS，v0.11 native
DSL，v0.21 video_use 交换，v0.23 HyperFrames，v0.23.1 极简界面，v0.24.0
合并与生成服务完善。旧版本页面操作说明仅用于理解历史，不是当前入口。

核对实际验证范围见 docs/validation-0.24.0.json。HTTP 模拟与本地媒体检查
不代表用户 GPU、真实 IndexTTS 音质、H3 生成质量、Codex 创作或 Docker
部署已经实机验收；Windows 二进制仅交叉编译。升级继续保留原项目、素材、
会话和服务配置，降级恢复完整升级前备份。

English

1. Daily workflow

Provide narration and local material, click Start production, review completed
videos, then request changes in conversation. RunDesk/Codex decides the scenes,
material, generation, voiceover, animation and revisions. Video App provides
production MCP tools, revision checks, media storage and output history.
Pictures follow the words; there is no mandatory sentence-to-shot template.

The first launch can create an empty project. Import text and files, add optional
requirements, and start. A folder containing exactly one TXT/MD file adopts it
as a narration draft only when narration is empty. Existing text is preserved;
multiple scripts require an explicit choice. Start saves current narration and
requirements without automatically sending an unsent conversation draft.

Watch completed versions, download the selected MP4, and describe changes in
chat. Confirm a final only after reviewing that exact complete output. Local
unsent drafts are stored per project; formal conversation is replayed from
RunDesk events. An uncertain submission reuses its original request ID and
content. Cancellation does not guarantee remote GPU work has stopped.

This release has no manual TTS/render/DSL/alignment controls, multitrack editor,
source viewer or detailed tool log in the main UI. Execution details belong in
RunDesk. Existing backend interfaces remain available to agents and integrations.

2. Production backends

New projects default to HyperFrames. The agent authors HTML/CSS/JavaScript/GSAP
with real asset bindings; App freezes, checks and renders each version. Declared
timeline annotations do not prove that every rendered frame was verified.

IndexTTS remains an independent HTTP voice service; eSpeak is a mechanical
preview. Measured synthesis chunks are not forced word alignment or listening
verification. Generated H3 audio is asset sound, not the narration voiceover.

Generation supports generic http_binary_v1 services and a built-in ComfyUI
MiniMax-H3 text-to-video client. Native DSL/FFmpeg, video_use EDL and legacy shot
projects retain their original backend. The optional video_use exchange uses a
dedicated shared workspace; the normal HyperFrames path does not require it.
Preparation/alignment, editing and refinement are artifact categories, not a
sequence of manual approvals. Successful tools do not establish factual accuracy
or replace human review of the actual result.

3. Startup and deployment

With renderer dependencies installed:
  VIDEO_DATA=./data VIDEO_FONT=/absolute/path/to/NotoSansCJK-Regular.ttc bash scripts/start.sh

Default URL: http://127.0.0.1:3220. The access token is in data/access-token,
or set VIDEO_APP_TOKEN to at least 24 characters. Build with bash scripts/build.sh.
HyperFrames requires Node.js 22+, Chromium, HyperFrames 0.8.87, GSAP 3.15.0,
FFmpeg/FFprobe and a font. scripts/start.sh passes the installed HyperFrames/GSAP
paths. A new native setup can use scripts/setup-renderer.sh; an existing setup
does not need reinstallation. The App Dockerfile installs renderer dependencies;
IndexTTS and ComfyUI model weights run in separate services.

For Linux Docker Engine and Compose v2:
  bash scripts/docker.sh init
  # Edit docker/.env; retain existing data paths, endpoints and credentials.
  # Set VIDEO_IMAGE=rundesk-video-app:0.24.0 for an existing installation.
  bash scripts/docker.sh build
  bash scripts/docker.sh up
  bash scripts/docker.sh token
  bash scripts/docker.sh doctor

init preserves existing configuration; up does not build. Back up the entire
data directory after active tasks finish. Update the App renderer and RunDesk
agent-tool containers separately. Existing RunDesk 0.49.5 / video-build 1.4.0
can continue to be used. RunDesk owns model authentication, Skills/MCP and agent
environments. Moving hosts or RunDesk services does not automatically transfer
old sessions or secrets. See docs/DOCKER.md and docs/UPGRADING.md.

4. IndexTTS and ComfyUI

Open Settings (/settings.html) to save IndexTTS defaults and check connectivity.
Existing speech-config.json remains in use. Nonempty VIDEO_TTS_* overrides make
the speech configuration form readonly. Use VIDEO_TTS_TIMEOUT_SECONDS, not
VIDEO_TTS_TIMEOUT. The full example is in the Chinese section and docs/INDEXTTS.md.
The endpoint must return WAV directly; Gradio pages and JSON file paths are not
supported. The bundled Python adapter can reuse an existing IndexTTS installation.

Built-in ComfyUI H3 configuration, followed by an App restart:
  VIDEO_VIDEO_GEN_URL=comfy://127.0.0.1:19897
  VIDEO_VIDEO_GEN_MODEL=minimax_h3_fl2va
  VIDEO_VIDEO_GEN_KEY=
  VIDEO_COMFY_H3_AUDIO_GAIN_DB=18

Install the workflow's models and nodes in ComfyUI first. The optional key is
sent as Bearer authentication. Default +18dB audio gain with limiting preserves
the quiet-audio fix; 0 retains the original media. This is not a loudness or
listening-quality certification. The built-in path is text-to-video only and
rejects reference images. It defaults to 124 frames at 24fps and clamps requested
length to 124..362 frames; requesting 60 seconds does not produce 60 seconds.
Requested and effective parameters are recorded; measured media duration is
authoritative. See docs/COMFYUI-H3.md for exact models, options and limitations.

Generic image/video services still use VIDEO_IMAGE_GEN_* / VIDEO_VIDEO_GEN_* and
http_binary_v1: POST JSON, return raw PNG/JPEG/MP4 bytes. Download URLs, Base64 JSON
and async job objects are not accepted. One App process selects either H3 or one
generic video provider. See docs/GENERATION-0.15.md.

5. Discovery and validation

GET /api/capabilities?probe=1 or video_capabilities_get(probe=true) requests
supported readonly service discovery. IndexTTS model lists and ComfyUI system/node
information do not prove successful synthesis, video inference or model quality.
Docker doctor checks App, renderer dependencies, RunDesk and existing speech
connectivity; it never runs GPU inference or a ComfyUI generation test.

Run go vet ./..., go test ./..., node --test tests/*.test.cjs (jsdom required),
and bin/video-app speech-doctor --data ./data as appropriate. Check the actual
release evidence in docs/validation-0.24.0.json. HTTP fixtures and local media
tests do not certify the user's GPU, IndexTTS audio, H3 quality, Codex creativity
or Docker deployment. Windows binaries are cross-compiled, not device-tested.

This release preserves the shengjuntu/rva changes through 065e4c7, including H3,
the audio fix and bilingual documentation, alongside HyperFrames and the minimal
workbench. Historical milestones: v0.8 IndexTTS; v0.11 DSL; v0.21 video_use exchange;
v0.23 HyperFrames; v0.23.1 minimal UI; v0.24.0 merged generation support. Historical
UI instructions are not current navigation. Retain projects, assets, sessions
and configuration on upgrade; restore a complete pre-upgrade backup to downgrade.
