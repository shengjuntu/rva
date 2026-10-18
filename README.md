# RunDesk Video App · v0.17.0

第二阶段：剪辑DSL v1。内容通过素材引用自由组合，DSL描述轨道、片段、裁剪、布局、声音和时间关系，不强制一段正文一个镜头。

新增4个MCP工具，共25个；剪辑DSL可校验、保存、编译为IR、显示时间线并通过FFmpeg实际生成MP4。剪辑模式无需Node.js或浏览器，文字片段需要字体；旧HyperFrames镜头模式继续保留，两个模式明确切换。

- 多轨：图片/视频、音频、文字；轨道顺序决定叠放，音轨可混合。
- 片段：素材入出点、0.5–2倍速、cover/contain、位置、尺寸、透明度、淡入淡出。
- 时间：绝对起点、跟随片段末尾、锚点与偏移；循环与悬空引用拒绝。
- 编辑：版本校验、严格字段检查、编译诊断、稳定片段ID与不可变渲染快照。

见[DSL使用说明](docs/EDIT-DSL.md)、[JSON Schema](schemas/edit-dsl-v1.json)、[本版验证](docs/VALIDATION-0.11.md)。包内examples/edit-preview.mp4为实际FFmpeg多轨渲染测试示例。

TTS内容自由输入与RunDesk制作闭环是第三阶段；生成模型和语义对齐仍待第四阶段。当前DSL不支持任意组件代码、关键帧或局部渲染缓存。

## 剪辑模式最小启动

配置FFmpeg/FFprobe及字体后直接运行：

```bash
bin/video-app --data ./data --font /absolute/path/to/NotoSansCJK-Regular.ttc
```

页面上传素材，创建或由Codex编写剪辑，校验后保存并启用，再生成视频。RunDesk连接配置沿用现有方式。

## 旧镜头模式兼容说明

独立的视频制作工作台：管理项目、素材、分镜、音轨、字幕、预览配音和成片。通过 RunDesk 调用 Codex，通过 HTTP MCP 让 Agent 读取和更新项目、启动渲染；RunDesk 保留会话、审批和执行轨迹。

推荐配套 **RunDesk 0.48.1**（0.48.0 也有所需运行环境 API）与 **news2douyin 0.17.0**。Go 主程序只使用标准库，WebUI 嵌入二进制；视频渲染使用锁定的 HyperFrames 0.8.87 / GSAP 3.15.0，以及 Chromium、FFmpeg、Noto CJK。配音可连接已有 IndexTTS-2.5 服务，eSpeak NG 保留为机械预览。App 无需 Python 或 GPU，IndexTTS 在你的现有服务器运行。

## 本版变化

- 新增 IndexTTS-2.5 HTTP 配音：页面配置服务地址、模型、音色、语言、语速；设置持久保存，API Key 不回传浏览器或写入任务日志。
- 对接 `/v1/audio/speech` WAV 协议；附复用现有官方模型/环境的轻量 Python 服务入口。详细步骤见 [IndexTTS 接入](docs/INDEXTTS.md)。
- 保留已有配音任务、进度、取消、试听、采用和真实音频时长字幕；不会静默回退到 eSpeak。新任务记录后端/模型/音色/语言/语速。
- MCP 配音工具支持 IndexTTS 参数；省略参数使用管理员默认配置。RunDesk 与 news2douyin 无需为此次配音接入升级。
- 继承 v0.7 的 Docker、RunDesk 运行环境准备、news2douyin 素材导入与持久数据挂载。

## Docker 启动（Linux）

完整步骤、地址和升级说明见 [Docker 与联调](docs/DOCKER.md)。需要 Docker Engine 和 Compose v2，RunDesk 默认运行在同一 Linux 宿主机。

```bash
bash scripts/docker.sh init
# 编辑 docker/.env，填写 RunDesk Token、入口、数据目录及用户映射
bash scripts/docker.sh build
bash scripts/docker.sh up
bash scripts/docker.sh token
bash scripts/docker.sh doctor
bash scripts/docker.sh smoke
```

`build` 构建 `rundesk-video-app:0.17.0`；`up` 只启动已有镜像。`smoke` 创建两秒中文标题与测试音视频，检查实际 MP4。`logs / status / stop` 提供日常运维入口。数据默认保存到 `.state/video`，升级须保留原完整数据目录。

RunDesk 中的 `video-build` 是另一个 **Codex 工具镜像**，通过初始化向导准备一次并选给视频应用。它不包含本 App 的 HyperFrames 渲染服务。首次切换到 Docker，需要为该容器配置视频 MCP/Skill 和 Codex 认证，详见联调文档。

## 本机运行

Linux/Windows amd64 二进制在 `bin/`；Windows 仅完成交叉编译。本机渲染需要 Node.js 22+、FFmpeg/FFprobe、Chrome/Chromium、中文字体。Linux eSpeak NG 可选。

```bash
# 从源码构建时需要 Go 1.25.12 或兼容版本
bash scripts/build.sh
bash scripts/setup-renderer.sh
# 使用已安装的浏览器可设置 HYPERFRAMES_BROWSER_PATH
bash scripts/start.sh
```

默认打开 `http://127.0.0.1:3220`。App Token 自动写入 `data/access-token`，也可设置不少于 24 字符的 `VIDEO_APP_TOKEN`。`VIDEO_DATA`、`VIDEO_LISTEN`、`RUNDESK_URL`、`RUNDESK_TOKEN`、`VIDEO_FONT` 可配置；部署 Token 不写入镜像。

## 制作流程

1. 创建项目、上传素材；或从 news2douyin 导入已审核制作包。
2. 连接 RunDesk，在应用实例配置 Codex、Skill、MCP 和运行环境；点击环境准备检查。
3. 让 Agent 读取项目并保存分镜。通过素材 ID 选图，业务素材不直接挂载到 Codex 的工作目录。
4. 按需要导入/生成配音、采用音轨、核对字幕；发送制作要求或点渲染。
5. 查看渲染日志与进度，播放成片，检查中文排版、画面、音频和字幕。Agent 完成与视频渲染完成分开记录。

当前固定竖屏 1080×1920、30fps，单镜头 1–60 秒、最多 80 镜头，总长不超过 10 分钟。一次只执行一个渲染。IndexTTS 合成结果需要试听；字幕是实测句段边界。尚未接入生成视频模型、ASR 自动校对或逐字强制对齐。

## 文档与验证

- [升级](docs/UPGRADING.md)、[Docker 联调](docs/DOCKER.md)、[架构](docs/ARCHITECTURE.md)
- [新闻交接](docs/NEWS-HANDOFF.md)、[音轨](docs/AUDIO.md)、[配音](docs/SPEECH.md)、[字幕](docs/SUBTITLES.md)
- [IndexTTS 部署与接入](docs/INDEXTTS.md)、[v0.8 验证结果](docs/VALIDATION-0.8.md)
- `integrations/video-project/SKILL.md`：可安装到 RunDesk 当前容器环境的业务 Skill。
- `scripts/integration_rundesk.py`：真实 App/RunDesk HTTP + Docker/Codex 测试替身；不是实际模型或 Docker 验收。
- `examples/short_script_bundle.zip`：news2douyin 0.17.0 实际导出器生成的协议样例，新闻和图片均为测试数据。

旧版本验证文档仅说明当时版本的检查结果，不代表本版镜像已实机构建或完成视频验收。
