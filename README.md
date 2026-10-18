# RunDesk Video App · v0.10.0

第一阶段：制作服务基础。RunDesk/Codex负责创作与编排，video-app提供能力发现、素材服务与统一制作任务。新增8个MCP工具，共21个；已实现TTS与渲染任务，图片生成、视频生成和语义对齐明确报告未实现。

本版不新增模型依赖，继续复用IndexTTS、HyperFrames、GSAP与FFmpeg。剪辑DSL将在第二阶段实现；当前仍兼容旧版镜头/段落编辑，不把它当作未来的内容模板。

- 能力注册表区分未实现、未就绪、已配置与检查通过；默认不联系远端服务，显式探测只查询IndexTTS模型列表。
- 统一任务启动、查询、列表与取消；状态直接来自原有持久记录，取消配音、渲染和Agent各自独立。
- 项目创建、素材导入和渲染支持请求幂等；浏览器保留不确定渲染提交的原请求。
- 完成的任务返回需认证的产物URL，未知渲染进度不会伪造成百分比。

见[服务契约](docs/PRODUCTION-SERVICE.md)、[分阶段计划](docs/ROADMAP.md)、[验证](docs/VALIDATION-0.10.md)。

独立的视频制作工作台：管理项目、素材、分镜、音轨、字幕、预览配音和成片。通过 RunDesk 调用 Codex，通过 HTTP MCP 让 Agent 读取和更新项目、启动渲染；RunDesk 保留会话、审批和执行轨迹。

推荐配套 **RunDesk 0.48.1**（0.48.0 也有所需运行环境 API）与 **news2douyin 0.15.0**。Go 主程序只使用标准库，WebUI 嵌入二进制；视频渲染使用锁定的 HyperFrames 0.8.87 / GSAP 3.15.0，以及 Chromium、FFmpeg、Noto CJK。配音可连接已有 IndexTTS-2.5 服务，eSpeak NG 保留为机械预览。App 无需 Python 或 GPU，IndexTTS 在你的现有服务器运行。

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

`build` 构建 `rundesk-video-app:0.10.0`；`up` 只启动已有镜像。`smoke` 创建两秒中文标题与测试音视频，检查实际 MP4。`logs / status / stop` 提供日常运维入口。数据默认保存到 `.state/video`，升级须保留原完整数据目录。

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
- `examples/short_script_bundle.zip`：news2douyin 0.15.0 实际导出器生成的协议样例，新闻和图片均为测试数据。

旧版本验证文档仅说明当时版本的检查结果，不代表本版镜像已实机构建或完成视频验收。
