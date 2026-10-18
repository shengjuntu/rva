# Video App 与 RunDesk：视频 Docker 与联调

配套版本：RunDesk 0.48.1、Video App 0.7.0、news2douyin 0.15.0。RunDesk 0.48.0 已具备所需 API，可继续使用；0.48.1 增加入口、说明和通用错误措辞。

## 两个镜像，各准备一次

| 部件 | 运行位置 | 职责 | 持久数据 |
|---|---|---|---|
| RunDesk | Linux 宿主机 | 应用注册、Codex 生命周期、会话、审批、轨迹 | 原 RunDesk data |
| `rundesk-video-build` | RunDesk 托管 Docker | Codex、FFmpeg/Pillow 工具、视频制作 Skill；通过 `video_project` MCP 操作业务 | RunDesk 工作区、Codex home |
| `rundesk-video-app:0.7.0` | 独立 Compose 容器 | WebUI、项目、HTTP MCP、HyperFrames 0.8.87、GSAP 3.15.0、Chromium、FFmpeg、Noto CJK、eSpeak NG | 宿主目录挂到 `/data` |

Codex 发起 `video_render_start`，实际渲染由 App 容器执行。不是在 Codex 容器里启动第二个 App；不要向 App 挂载 Docker socket。App 数据与 Codex 工作目录不共用，素材由 App 按 assetId 读取。视频不要求 Exa/Agent-Reach；新闻搜索继续在 news2douyin 研究环境完成，已审核制作包携带素材交接。

## 升级、构建、启动

1. 停止旧 RunDesk，备份 data；解压源码、`make build`，沿用原 `--data` 启动。不要清空 data 或创建第二份同名应用安装。
2. 在 RunDesk 初始化/运行环境页选择 `video-build`，填写已安装 Codex 的精确版本，准备镜像一次。可以从已有本地镜像或受信仓库拉取；应用注册和任务开始不触发构建。
3. 解压 Video App 0.7.0。若从 0.6.0 升级，先停旧 App、备份整个 data，在 Docker 配置中令 `VIDEO_HOST_DATA` 指向该目录。保留 access-token、config.json、rundesk-requests、projects，避免安装身份、会话和回执丢失。
4. 执行 App 包的 `bash scripts/docker.sh init`。编辑生成的 `docker/.env`：填 RunDesk Token，核对 UID/GID、数据目录、浏览器入口与 MCP 回调地址。
5. `bash scripts/docker.sh build` 显式构建渲染镜像；`bash scripts/docker.sh up` 只启动已有镜像。镜像内已安装所有渲染依赖，启动不再执行 npm/apt 或浏览器下载。
6. `bash scripts/docker.sh token` 获取 App 登录 Token，打开 `http://127.0.0.1:3220`。远程访问需把 `VIDEO_APP_PUBLIC_URL` 改为浏览器可访问地址。
7. 在 App 创建/导入项目，点“连接 RunDesk”。转到 RunDesk 对应应用，选择已准备的 `video-build` Docker 镜像，完成该容器 Codex 认证；首次切换 Docker 时在当前容器安装 `integrations/video-project/SKILL.md`，添加 HTTP MCP `video_project`，URL 为 `VIDEO_APP_MCP_URL`，HTTP Header 为 `Authorization: Bearer <App Token>`。此配置位于容器独立 Codex home，原本机配置不会自动迁移。模型、权限和审批由 RunDesk 管理。
8. 回 App 点击“重新配置工具”以重连（空会话自动更换到 Docker）；若已有本机对话，点击“新建当前环境对话”，旧历史保留。再打开“环境准备 → 启动并检查 Codex 容器”，确认运行、认证、10 个视频 MCP 工具可发现；发送新任务也自动做该检查。检查通过不代表真实模型调用已验收。

也可在 RunDesk 目录统一操作：设置 `VIDEO_APP_DIR` 后执行 `bash examples/video-build/run-app.sh <init|build|up|doctor|smoke|logs|stop>`。

## Linux 网络约定

App Compose 默认 `network_mode: host`，可访问宿主 `127.0.0.1:3210` 的 RunDesk。App 监听 `0.0.0.0:3220`，让 RunDesk 默认 bridge 容器通过内置 `host.docker.internal:host-gateway` 访问回调。

- `VIDEO_APP_PUBLIC_URL=http://127.0.0.1:3220`：浏览器入口；远程使用改成服务域名或宿主 IP。
- `VIDEO_APP_MCP_URL=http://host.docker.internal:3220/api/mcp`：Codex 容器回调，含完整 `/api/mcp` 路径。
- `RUNDESK_URL=http://127.0.0.1:3210`：App 到 RunDesk。

此 Compose 面向 Linux Docker Engine；Windows/macOS Docker Desktop 需另行启用受支持的 host networking 或调整网络与地址。宿主防火墙应允许 Docker 网桥到 3220；App 使用 Token，公网部署应置于 HTTPS 反向代理后。

已有安装只在首次注册写入默认 MCP/Skill。升级时，在 RunDesk 确认 MCP URL、Authorization Header 和 `video-project` Skill：**重连不会覆盖管理员配置**。App 新的业务任务会说明素材与渲染边界，旧 Skill 中“本地 assets 路径”的描述应更新。不要清空配置来强制重新注册。

## 验收

- `doctor`：检查 App、依赖、RunDesk 与已绑定运行环境，只读，不调用模型。
- `smoke`：创建标记为验收的两秒中文标题项目，合成测试音，真实调用 App → HyperFrames/Chromium → FFmpeg，检查 1080×1920 H.264/AAC 和时长；不调用模型。
- 导入 news2douyin 0.15.0 已审核脚本 ZIP：检查语言、原稿、证据和图片候选；图片进入素材库，由用户选入分镜，不自动判定使用权。
- 发送“读取项目并保存分镜，按当前素材制作视频”，检查 RunDesk 轨迹中真实 MCP 调用、App 项目 revision、渲染进度与成片。播放检查中文、图像、字幕和音频。
- `stop` → `up` 后核对项目、成片、Token 与连接仍在；重试未知结果沿用原请求，不重复建会话。

短脚本的文字语言由 news2douyin 指定并随制作包保留；eSpeak NG 是机械预览配音，不代表专业 TTS。当前渲染固定竖屏 1080×1920/30fps，未引入生成视频模型。

## 升级与镜像分发

镜像可由 CI/维护者提前构建并推送到自己的 GHCR/Docker Hub，部署配置引用版本标签或 digest 后拉取即可。当前包没有已发布镜像地址；不要把示例标签当成远程可下载镜像。App 镜像构建上下文采用白名单，不包含 `.env`、data、Token、日志或旧二进制。发布前运行 smoke、记录 digest；同时保留镜像的第三方许可。

Node/Debian 基础标签和 apt 仓库随时间更新；需要字节级复现时传入带 digest 的 `GO_IMAGE`/`NODE_IMAGE` 并使用自己的 apt 快照。npm 依赖使用 package-lock.json + npm ci；镜像记录 runtime-versions.json 和 debian-packages.txt。
