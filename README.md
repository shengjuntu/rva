# RunDesk Video App · v0.7.0

独立的视频制作工作台：管理项目、素材、分镜、音轨、字幕、预览配音和成片。通过 RunDesk 调用 Codex，通过 HTTP MCP 让 Agent 读取和更新项目、启动渲染；RunDesk 保留会话、审批和执行轨迹。

推荐配套 **RunDesk 0.48.1**（0.48.0 也有所需运行环境 API）与 **news2douyin 0.15.0**。Go 主程序只使用标准库，WebUI 嵌入二进制；视频渲染使用锁定的 HyperFrames 0.8.87 / GSAP 3.15.0，以及 Chromium、FFmpeg、Noto CJK。eSpeak NG 提供机械预览配音，不是专业 TTS。

## 本版变化

- 补齐 App/渲染 Dockerfile、Linux Compose、持久数据挂载和统一生命周期脚本；依赖在构建阶段安装，启动不再下载安装。
- 分离浏览器入口 `VIDEO_APP_PUBLIC_URL` 与 Codex 容器回调 `VIDEO_APP_MCP_URL`。
- 新任务提交前检查并启动已选 Docker 环境，核对模型认证与 10 个视频 MCP 工具；不构建镜像、不改管理员设置、不消耗模型请求。
- 更换运行方式后，重连自动更换空会话；已有对话使用“新建当前环境对话”，历史留在 RunDesk。新建操作带原会话校验，重试不重复创建。
- 前后端工具版本号统一为 0.7.0；数据锁在进程异常退出后由系统释放。
- 支持 news2douyin 0.15.0 短脚本 ZIP：保留目标语言、证据和最多 4 张图片候选；图片保留出处，先选入分镜再渲染。支持不属于聚类事件的短脚本。

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

`build` 构建 `rundesk-video-app:0.7.0`；`up` 只启动已有镜像。`smoke` 创建两秒中文标题与测试音视频，检查实际 MP4。`logs / status / stop` 提供日常运维入口。数据默认保存到 `.state/video`，升级须保留原完整数据目录。

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

当前固定竖屏 1080×1920、30fps，单镜头 1–60 秒、最多 80 镜头，总长不超过 10 分钟。一次只执行一个渲染。没有接入生成视频模型、专业配音服务或自动逐字校对。

## 文档与验证

- [升级](docs/UPGRADING.md)、[Docker 联调](docs/DOCKER.md)、[架构](docs/ARCHITECTURE.md)
- [新闻交接](docs/NEWS-HANDOFF.md)、[音轨](docs/AUDIO.md)、[配音](docs/SPEECH.md)、[字幕](docs/SUBTITLES.md)
- [v0.7 验证结果与未验证项](docs/VALIDATION-0.7.md)
- `integrations/video-project/SKILL.md`：可安装到 RunDesk 当前容器环境的业务 Skill。
- `scripts/integration_rundesk.py`：真实 App/RunDesk HTTP + Docker/Codex 测试替身；不是实际模型或 Docker 验收。
- `examples/short_script_bundle.zip`：news2douyin 0.15.0 实际导出器生成的协议样例，新闻和图片均为测试数据。

旧版本验证文档仅说明当时版本的检查结果，不代表本版镜像已实机构建或完成视频验收。
