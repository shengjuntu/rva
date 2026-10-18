# 升级到 v0.8.1

从 v0.8.0 升级无需重新部署 IndexTTS，也无需重填已保存的服务配置。本次主要增加音色列表、提交恢复与只读自检；可先运行 `bin/video-app speech-doctor --data ./data`，再用两句短稿试听。

1. 停止 Video App，备份整个 data（或 `/data` 挂载源）。保留 access-token、config.json、项目、RunDesk 回执和素材。
2. 替换本包程序/页面；Docker 用户保留原 `VIDEO_HOST_DATA` 等设置，将 `docker/.env` 的 `VIDEO_IMAGE` 改为 `rundesk-video-app:0.8.1`，使用新版 compose 执行 `bash scripts/docker.sh build`，再执行 `bash scripts/docker.sh up`。
3. 浏览器刷新，版本应为 0.8.1。“自动配音 → 配音服务设置”连接已有 IndexTTS，见 [INDEXTTS](INDEXTTS.md)。旧配置默认使用 eSpeak，不会自动切换音色或改写旧配音。
4. 新配置保存在 `data/speech-config.json`，其中可能包含 TTS API Key，备份时一并保护。配置在容器重建后保留；使用非空 VIDEO_TTS_* 环境变量时，页面显示由环境管理。
5. 若通过 Codex 生成配音，在当前 RunDesk 实例更新项目工具/业务 Skill（保持自定义设置；手动安装的容器 Skill 可按差异更新配音段落）。HTTP MCP 仍为同一个入口、10 个工具，只增加可选参数。
6. 创建两句测试稿 → 检查环境 → 生成 → 试听 → 采用 → 渲染。此版不包含或重装你的 IndexTTS 模型；RunDesk 0.48.0+ 与 news2douyin 的现有兼容版本可继续使用。

旧项目、eSpeak 任务、音轨和成片继续读取；新任务额外记录后端/模型/语言/语速。若降级，恢复升级前完整备份。

---
以下为历史升级说明。

# 升级到 v0.7.0

配套 RunDesk 0.48.0+，推荐 0.48.1。停止旧 App，备份完整 data，替换二进制或构建新镜像，沿用原数据目录、RunDesk URL、Token 与安装身份。Docker 配置和步骤见 [DOCKER](DOCKER.md)。

项目新增 previousSessionIds，config.json 新增 workspaceId；原请求回执、成片、素材和历史会话保留。新版采用系统文件锁，server.lock 文件存在不代表服务仍运行，**不要删除运行中实例的锁文件**。

首次更换为 Docker 后，在 RunDesk 的当前容器配置视频 MCP 和 Skill，再回 App 重新连接：空会话会更换为 Docker 会话；已有原生对话须点“新建当前环境对话”，不会搬迁或删除旧历史。与旧版本一致，默认配置不会覆盖管理员编辑。

news2douyin 制作包可新增 storyboard.json、evidence_snapshot.json 和 images/；单文件 4 MiB、整个脚本包 32 MiB、最多 4 张候选图片。保存或渲染前应确认素材适用性。旧平面脚本包继续可导入。

降级前恢复原版完整数据备份，避免旧版本忽略新字段。

---
以下为旧版升级历史。

# 从 v0.5.0 升级到 v0.5.1

1. 先将 RunDesk 升级到 0.6.0 或更新版本；本次联调版本为 0.6.1。等待活动 Agent 任务结束，停止 App，备份完整数据目录。
2. 替换程序，保留原 `VIDEO_DATA`、RunDesk 地址、实例与认证。`bin/video-app --version` 应为 0.5.1。Linux 与 Windows amd64 二进制均在 bin；Windows 仅完成交叉编译，未实机运行。
3. 在使用中的项目点击“升级项目工具至 v0.5.1”。保留工作区和会话，通用 Skill 写入实例；不删除旧项目 Skill，后续提交显式选择实例文件。实例有活动任务时暂缓更新。
4. “环境准备”可查看模型、Skill 与 MCP。配置写入成功不代表模型认证或工具调用已经成功；首次使用需在自己的实例中验证。
5. 旧会话不重写来源标签，新会话记录 `source.appId=rundesk-video-app`、`taskId=项目编号`。旧版运行只有事件记录证明属于该项目时才允许停止；无法确认时到 RunDesk 核对。

`data/rundesk-requests/` 保存提交前的请求编号、不可变请求体与回执。项目新增 `agentSubmission`、`agentRunId`，config.json 新增 RunDesk 地址与实例 Skill 路径。备份必须包含这些文件；有待确认请求时不要换数据目录、清理请求日志或切换 RunDesk 后端。浏览器使用同一标签页的 sessionStorage 保留尚未到达服务器的消息；服务器已收到的待确认消息保存在项目里。主动关闭标签页前，先确认未到达服务器的消息状态。

HTTP 失败、processing/unconfirmed 均不能当成未执行。重试沿用原编号与内容；只有原回执明确不存在才允许重新提交同一操作。已请求停止但仍待确认的任务不会因轮询而被重新启动。消息内容要修改时，先确认原提交，再发送新指令。降级须恢复升级前备份，以免旧版本丢弃新增字段。

渲染、配音、字幕依赖及制作能力与 0.5.0 相同。

# 从 v0.4.0 升级到 v0.5.0

1. 停止 APP，备份完整 data，替换程序和页面，保留原 VIDEO_DATA、RunDesk 登录与实例配置。
2. 确认 `bin/video-app --version` 为 0.5.0。旧音轨、字幕、原片、渲染和会话继续保留，无需重新生成。
3. 需要自动配音时另外安装 eSpeak NG 和 FFmpeg；默认通过 PATH 查找，或用 VIDEO_ESPEAK / --espeak 指定。未装 eSpeak 不影响旧有编辑与渲染功能。
4. 等 Agent 空闲，在使用中的项目点击“升级项目工具至 v0.5.0”，更新到 10 个 MCP 工具；保留既有会话和工作区。
5. 先生成一个短配音任务，试听后采用，再生成视频。生成任务不会自动覆盖音轨，采用会替换配音/字幕与镜头时长。

新任务在 projects/<id>/speech 下，项目 JSON 保存 speech 元数据。降级应恢复升级前备份；旧版本保存工程会丢弃任务字段。旧字幕的原有绑定规则继续兼容，再次核对或采用时使用更严格的正文/结构哈希。HyperFrames/GSAP 版本未变。

# 从 v0.3.0 升级到 v0.4.0

1. 停止 APP，备份完整 data；替换程序、源码与页面，保留原 VIDEO_DATA、RunDesk 实例与登录。
2. 确认 `bin/video-app --version` 为 0.4.0。旧工程、音轨、原片、成片记录自动保留；旧项目默认没有字幕轨，不需要强制采用。
3. 等 Agent 任务结束，在使用中的项目点击“升级项目工具至 v0.4.0”，保留会话与工作区，更新到七个 MCP 工具。
4. 原片采用会替换当前配音/字幕并缩放镜头，先检查界面说明。需要字幕但未有原片时，可以上传自己的音频和 SRT。
5. 渲染依赖不变：HyperFrames 0.8.87、GSAP 3.15.0；字幕核对需要 FFprobe，重新渲染需要既有 Node/Chrome/FFmpeg/中文字体。

字幕随 project.json 保存，采用的配音是 workspace/assets 中的独立副本，备份需一起保存。旧版不认识字幕字段；如需降级，恢复升级前备份，避免旧版保存丢弃字幕绑定。

# 从 v0.2.0 升级到 v0.3.0

1. 停止视频 APP，备份完整 data 目录。保留 RunDesk 专用实例和凭据。
2. 替换 APP 源码、页面、脚本与 bin 二进制，继续使用原来的 VIDEO_DATA。不要用发行包覆盖数据目录。
3. 核对 `bin/video-app --version` 输出 0.3.0。旧项目无需迁移，仍可编辑和渲染。
4. 等 Agent 当前任务结束，在继续使用的项目中点击“升级项目工具至 v0.3.0”。保留原会话和工作区，只更新该应用管理的 Skill/MCP。
5. 导入新闻制作包会建立独立项目，原项目不会被覆盖。news 字段由服务维护，项目 PUT 和 MCP 编辑不能改写它。

新增文件在 projects/<id>/news/ 下，备份时必须包含。降级前应恢复备份；旧版本保存项目会忽略并丢弃 news 元数据，不能直接降级后继续编辑新闻项目。

# 从 v0.1.0 升级到 v0.2.0

1. 停止视频 App，备份视频 App 的整个数据目录。保留 RunDesk 数据、专用实例、登录与模型配置。
2. 将 v0.2.0 解压到新的临时目录，核对版本后替换原安装目录里的源码、页面、脚本与二进制。**不要用发行包覆盖你的 data 或环境配置。** 保持 `VIDEO_DATA` 指向原目录。
3. 渲染依赖版本未改变：HyperFrames 0.8.87、GSAP 3.15.0。已有 runtime/node_modules 可继续使用；新安装目录可以重新运行 setup-renderer.sh。
4. 启动 App，刷新浏览器，选择原项目。旧项目默认为没有音轨；旧成片、分镜、素材和会话保留。
5. 等专用实例的 Agent 任务结束，点击“升级项目工具至 v0.2.0”。每个需要继续创作的旧项目分别更新一次。这会更新该项目的应用 Skill 和实例的 video_project MCP，不新建会话，也不复制或清除登录凭据。
6. 上传配音/音乐，保存并渲染一个短项目确认效果。

如果更换了安装路径、端口或 App 令牌，即使版本相同，也可点击“重新配置工具”修复 MCP command/args/env。正在运行或等待授权的任务会阻止配置刷新；先完成或停止任务。

启动参数错误、令牌过短或端口占用不会再留下新锁文件。v0.1 异常退出已经留下的锁仍需在确认旧进程结束后人工删除。强制终止进程同样可能残留锁。

更新后的 project.json 含 audio 和 toolsVersion，旧版 App 不理解这些字段，回退旧版可能丢失新增音轨配置；需要回退时恢复升级前的完整备份。
