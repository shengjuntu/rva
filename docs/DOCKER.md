# Video App v0.25.0：Docker 部署与服务接入

当前配套为 Video App 0.25.0、RunDesk 0.50.0 / video-build 1.4.0。已有适配环境继续沿用；本次合并不要求清空项目、重建 RunDesk 安装身份或重新部署已有 IndexTTS/ComfyUI 模型。

命名制作环境通过 `VIDEO_PRODUCTION_PROFILE` 选择，项目创建时固定绑定。管理员在 RunDesk 配置每个预设的镜像、专用实例与工作区；App 不接收任意镜像或 Docker socket。多环境与 `VIDEO_EXCHANGE_ROOTS` 精确共享映射见 [PRODUCTION-PROFILES](PRODUCTION-PROFILES.md)。

## 各部件分别部署

| 部件 | 职责 | 持久数据 |
| --- | --- | --- |
| RunDesk | Agent 生命周期、模型认证、Skills/MCP、会话、审批和轨迹 | 原 RunDesk data、账户与工作区 |
| RunDesk 托管的 `video-build` 工具容器 | Codex 与其制作工具环境 | RunDesk 管理的工作区和 Codex home |
| `rundesk-video-app:0.25.0` | 极简 WebUI、制作 HTTP/MCP、HyperFrames/FFmpeg 渲染、素材和成片归档 | 原宿主目录挂载到 `/data` |
| 已部署的 IndexTTS / ComfyUI | 各自的 GPU 推理服务 | 各服务自己的模型、音色、节点和输出目录 |

App 镜像包含 HyperFrames 0.8.87、GSAP 3.15.0、Node.js、Chromium、FFmpeg/FFprobe、Noto CJK 和 eSpeak NG。App 与 Codex 工具容器分别更新；模型服务不放进 App 镜像，不向 App 挂载 Docker socket。

新项目默认 HyperFrames。Codex 通过 App MCP 保存源文件与真实素材引用，App 冻结后渲染。无需向 Codex 暴露 App 私人 data，也不需要共享生产目录。旧 native DSL/FFmpeg、video_use 和镜头项目保留原后端；video_use 的专用共享目录要求见后文。

## 构建、启动和升级

以下脚本面向 Linux Docker Engine 与 Compose v2。在源码根目录执行：

```bash
bash scripts/docker.sh init
# 编辑 docker/.env 后继续：
bash scripts/docker.sh build
bash scripts/docker.sh up
bash scripts/docker.sh token
bash scripts/docker.sh doctor
```

1. 新安装的 `init` 生成配置并写入当前 UID/GID 与绝对数据路径；已有 `docker/.env` 不会被覆盖。填入实际 `RUNDESK_URL`、`RUNDESK_TOKEN`，核对浏览器入口、MCP 回调与数据目录。
2. 升级先等待活动任务结束、停止旧 App 并备份完整数据，继续将原目录挂到 `/data`。保留 access-token、config.json、speech-config.json、RunDesk 请求回执、projects、素材和历史输出。旧配置显式更新 `VIDEO_IMAGE=rundesk-video-app:0.25.0`，不要用示例文件覆盖已有配置。
3. `build` 显式构建 App 镜像；`up` 使用现有镜像启动，不自动安装 npm/apt 或下载浏览器。仅替换源码或镜像标签不会更新已经运行的容器，需执行相应重建与启动流程。
4. 用 `token` 输出登录令牌，打开浏览器入口。主页面首次可创建空项目；日常操作是导入正文/素材、开始制作、查看视频版本、对话修改和下载。
5. 首次配置模型认证、Agent 环境与 Skills/MCP 时，在 RunDesk 的视频应用和运行环境页核对已选镜像、账户与工作区。App 开始制作会按现有流程建立连接、配置制作工具并检查运行环境；遇到认证或环境错误，按页面提示转到 RunDesk 处理，再确认原提交。不要为排错删除旧会话或清空配置。

主页面没有“连接 RunDesk”“重新配置工具”“新建当前环境对话”“环境准备”等旧手动按钮。App 的独立“设置”页展示环境状态并提供 RunDesk 链接；详细运行过程、权限和工具配置在 RunDesk。重连与升级保留管理员配置，不能据此假定更换服务后旧会话或认证会自动迁移。

也可在已有 RunDesk 部署目录使用其 `examples/video-build/run-app.sh` 包装脚本；先核对该脚本对应的 App 路径和版本。

## 网络与持久目录

App Compose 默认 `network_mode: host`，适用于 Linux 宿主上的服务：

| 变量 | 示例与方向 |
| --- | --- |
| `VIDEO_APP_PUBLIC_URL` | `http://127.0.0.1:3220`，浏览器到 App；远程使用改成可达域名或宿主 IP |
| `VIDEO_APP_MCP_URL` | `http://host.docker.internal:3220/api/mcp`，RunDesk 的 bridge 工具容器回调 App |
| `RUNDESK_URL` | `http://127.0.0.1:3210`，App 到 RunDesk |
| `VIDEO_HOST_DATA` | 原 App 持久目录，挂到 `/data`；不是 RunDesk home 或模型目录 |

同机 IndexTTS/ComfyUI 可使用宿主 `127.0.0.1`。若改为 bridge 网络，127.0.0.1 指 App 容器自身，需改成服务 DNS 或实际宿主网关。Windows/macOS Docker Desktop 需使用其支持的 host networking 或另行调整网络；本环境未做 Docker Desktop 实机验收。

RunDesk 工具容器需能访问 MCP 地址，且配置 `Authorization: Bearer <App Token>`。App 和模型服务分别使用各自凭据。公开部署按实际网络配置 HTTPS 和认证；令牌、`.env` 与数据不写入镜像。

## IndexTTS 配音

打开 App“设置”→“配音服务”，保存已有服务的完整 `/v1/audio/speech` 地址、模型名、已注册音色、语言和可选 Key。配置保存在 `/data/speech-config.json`，容器重建后沿用。模型继续运行在原 GPU 服务，不需要安装到 App 或 Codex 容器。

也可启用 `docker/.env.example` 中的 `VIDEO_TTS_*` 环境变量，Compose 已透传。任一非空变量会使配音配置表单显示由环境管理；其余字段沿用保存值或默认值。超时变量为 `VIDEO_TTS_TIMEOUT_SECONDS`。完整协议、原生 Python 适配器和只读自检见 [INDEXTTS](INDEXTTS.md)。

“检查连接”与模型/音色列表发现不进行合成，不能证明音质已验收。实际制作由 Codex 发起配音、检查产物并安排到视频中，主页面不提供旧手动配音/采用按钮。

## ComfyUI 与通用生成服务

内置 MiniMax-H3 直接访问现有 ComfyUI，不需要再部署 http_binary_v1 中转服务。在现有 `docker/.env` 增加并重启 App：

```dotenv
VIDEO_VIDEO_GEN_URL=comfy://127.0.0.1:19897
VIDEO_VIDEO_GEN_MODEL=minimax_h3_fl2va
VIDEO_VIDEO_GEN_KEY=
VIDEO_COMFY_H3_AUDIO_GAIN_DB=18
```

ComfyUI 的节点与模型需预先安装，不向 App 挂载模型或输出目录。当前仅支持文生视频，拒绝参考图；默认音轨增益、124..362 帧限制、实际生效参数和取消边界见 [COMFYUI-H3](COMFYUI-H3.md)。H3 素材原声不自动替代主播口播。

通用图像与视频生成仍可通过 `VIDEO_IMAGE_GEN_*` / `VIDEO_VIDEO_GEN_*` 连接 [http_binary_v1](GENERATION-0.15.md) 服务。同一组视频变量当前选择一个提供方。设置页的生成配置只读，部署修改后重启；发现可达或节点存在不等于完成 GPU 推理或产物验收。

## 检查范围与验收

- `docker.sh doctor`：检查 App、渲染依赖、RunDesk、已绑定运行环境及现有配音连接；不执行模型推理，不是一次 H3 视频生成测试。`--health` 仅检查 App 存活，不因外部模型服务暂不可用而重启 App。
- 设置页服务发现，或 `GET /api/capabilities?probe=1` / MCP `video_capabilities_get(probe=true)`：请求受支持的只读服务探测；IndexTTS 模型列表、ComfyUI 系统/节点信息不证明实际生成成功。
- `bin/video-app speech-doctor --data /data`：在 App 容器内读取实际配音配置，检查基础依赖、模型和音色列表，不合成音频。
- `docker.sh smoke`：创建标记为验收的短测试项目，检查本地渲染和媒体结果，不运行 Codex、IndexTTS 或 H3 模型。它不是自主制作、口播音质或 GPU 生成验收。
- 在实际项目输入短稿、导入素材，点“开始制作”；到 RunDesk 核对真实工具调用和任务回执，回 App 查看已完成版本、下载并观看/试听。再发一条修改要求，检查新版本和原有结果均保留。
- `stop` → `up` 后核对项目、历史输出、访问令牌和服务设置仍在。未知提交确认原回执，不换 requestId 重复生成；停止本地等待不保证远端 GPU 已停止。

HyperFrames 的尺寸、帧率和时长来自工程，并非固定 1080×1920/30fps。旧版本固定竖屏、手动分镜和配音操作属于历史行为；当前用户界面以 [README](../README.md) 为准。实际发布检查范围见 `validation-0.25.0.json`，没有用模拟服务或本地媒体测试替代真实 GPU、Docker 与用户环境验收。

## 可选 video_use 共享目录

仅旧 video_use 流程需要把同一个 RunDesk 视频应用工作目录挂给 App，并设置 `VIDEO_EXCHANGE_ROOT`。可叠加 `docker/compose.video-use.yaml`，其 `VIDEO_EXCHANGE_HOST_WORKSPACE` 必须指向已有应用工作目录，不是 RunDesk home 或整个 data。工具镜像需显式包含 video-use。详见 [共享交换契约](EXCHANGE-0.21.md)。HyperFrames 主路径无需此挂载。

## 镜像分发与复现

示例镜像标签不代表已发布到远程仓库；需要自行构建，或使用维护者明确提供的镜像与 digest。构建上下文白名单包含 Go、comfyh3、嵌入网页与制作 Skill、渲染依赖清单和启动脚本，不包含 `.env`、项目数据、令牌或旧二进制。

Node/Debian 标签和 apt 仓库会变化；严格复现可传入带 digest 的 `GO_IMAGE` / `NODE_IMAGE` 并使用受控 apt 快照。npm 使用 package-lock.json 与 npm ci，镜像记录 runtime-versions.json 和 debian-packages.txt。App 与工具镜像的第三方许可分别保留；本版不声称用户 Docker 镜像已在本环境运行验收。
