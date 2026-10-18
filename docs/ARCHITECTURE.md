# v0.24.0 统一生成服务与简洁制作入口

生成任务可以调用通用 http_binary_v1 服务，或内置 ComfyUI MiniMax-H3 Go 客户端。H3模型仍在独立 ComfyUI 服务中运行；App负责提交、查询、实际媒体校验、生成来源与素材归档。两种视频提供方共用现有环境配置槽位，不新增 Agent 编排循环。IndexTTS 和简洁工作台沿用既有接口，RunDesk负责执行与详细轨迹。

ComfyUI能力发现区分配置检查、节点/模型列表探测和真实推理；实际生效帧数与目标时长分开记录。设置页初次只读配置，用户刷新才探测；此探测不运行GPU模型。具体限制见 [ComfyUI H3](COMFYUI-H3.md)。

## 简洁制作入口与配置分离

日常页面只承担项目列表、正文/本地素材导入、一键初稿、实际成片/历史版本、对话修改和导出。过程仅显示简要状态，完整轨迹和详细诊断在 RunDesk。手动 TTS、render、DSL、素材对齐、多轨时间线、可视原文/片段引用选择、源码检查和详细阶段控制台不进入本版 UI。后台 MCP、素材/作品版本、修订检查和历史渲染继续保留；页面不维护第二份私有剪辑状态，不重置项目、原稿、工作区或服务设置。

Codex 负责导演与执行步骤：依据实际材料和工具自行分镜、选材、调用生成/TTS、编写动画、检查结果并修改重做。App 提供组合工具与领域产物，RunDesk 管会话、环境、Skills/MCP 和轨迹。素材准备和对齐、剪辑、精修只是可检查产物的分类；没有固定状态机强制 Agent 走同一条制作路线。

独立设置页保留必要的 IndexTTS/配音配置，使用现有写 API。其他环境状态按真实检查结果展示，渲染与生成服务沿用部署配置；模型、Skills/MCP 和运行环境管理进入 RunDesk。页面不伪造其他服务的写能力，也不把配置存在等同于模型已可用。

检查与重做由 Codex 根据真实工具结果安排。Skill 中通常三轮的建议是指导，不是后台硬限制；媒体结构校验、抽帧、文字引用和任务状态均不等于自动新闻语义审核或完整观看/听审。历史成片与版本继续保留，人的“第二节重做”等对话要求由 Codex 结合原文和实际工程定位，不需要可视选片控件；不能静默覆盖其他满意部分。后台 checkpoint、locate 和 review-timeline 数据接口继续供 Codex 使用，未来可以在同一版本数据上增加轻量审阅界面。

以下保留 v0.23.0 及更早的工程与协议记录；其中旧界面手动入口不再代表本版日常操作。

---

# v0.23 自主导演与工具平台

RunDesk 驱动 Codex 决策与执行，Video App 提供制作 MCP、素材与工程状态、异步任务和多轨审阅。App 不实现第二个 Agent 调度循环，也不把导演决策硬编码成固定段落镜头流程。通用配置、轨迹与深入调试归 RunDesk，领域产物与调整入口留在 App。

HyperFrames 为新制作的主要动画/合成后端。工程将自由 HTML/CSS/JavaScript 表达与可检查的剪辑/声音声明放在同一版本，Codex 可以自行编写动画。渲染任务冻结完整工程与依赖素材；后续保存不修改已经开始的渲染或历史输出。多轨界面消费工程/输出的时间线，不另建编辑状态。

工具负责来源、素材引用、参数、修订、幂等、校验和归档；Codex 负责理解文字、分镜、选材、生成、动画与检查。正文、导演简报、文字素材关联和剪辑/动画工程各有职责，不用一个强制 scene 模型混合所有信息。

native DSL、video_use 和旧镜头工程保持兼容，旧项目不静默迁移。每次渲染选择一个工程依据。HyperFrames 主流程使用 App MCP 提交工程，无需共享 App 私人数据；只有 video_use 兼容路径继续使用专用共享生产目录。

当前接口与边界见 [HYPERFRAMES-0.23](HYPERFRAMES-0.23.md)。以下章节记录历史实现，不代表新项目默认选择。

---

# v0.11 剪辑DSL

项目保存editDsl与editActive，旧Shots/Audio/Subtitles另行保留。edit_dsl.go负责纯结构、跨引用与时间编译；edit_render.go负责资产快照、真实媒体源范围校验、FFmpeg滤镜图与MP4校验。渲染引擎由提交时项目模式确定，任务复用原渲染幂等/取消/持久状态。

IR的轨道层级、解析时间、源范围与内容分别表达。源视频音默认静音，音频以独立轨道混合；音频速度/采样率变换后重建样本时间基，保证延迟、淡入淡出与AAC时间戳一致。输出保存DSL快照、IR、滤镜图与验证报告。

内容没有强制段落模板，未来生成内容作为媒体资产接入。高级组件/关键帧与语义编辑操作仍待后续阶段。详见EDIT-DSL.md。

# v0.10 制作服务基础

能力发现(capabilities.go)、统一任务投影(jobs.go)、小素材导入(media_service.go)均在Go服务内。任务使用原Speech/Render记录作为唯一状态来源；不增加重复队列或Agent编排器。render增加请求哈希和revision校验，创建项目及素材导入保留请求身份。

Capabilities将工具存在、配置检查、远端模型列表与真实输出验收分开。未接入生成工具明确不可用。MCP、页面与现有HTTP使用同一业务服务，统一任务响应不带部署密钥或本地文件路径。

未来内容产物与剪辑DSL分开，旧镜头渲染只作为兼容执行器。详见PRODUCTION-SERVICE.md与ROADMAP.md。

# v0.8 配音服务补充

Video App 直接调用独立 IndexTTS HTTP 服务；RunDesk 负责 Agent 编排和轨迹。服务在任务创建时形成配置快照，进行中的任务不会被设置更改影响；设置页在任务运行时拒绝变更。普通 Agent MCP 不能设置服务 URL、API Key 或任意服务端路径。Go App 保持标准库依赖，Python/GPU 仅属于独立推理服务。

新增 `GET/PUT /api/speech/settings`；`GET /api/speech/capabilities` 可按 backend 检查服务模型列表和媒体工具。任务保留原始请求哈希，配置改变或重启后同一请求仍返回原回执，避免重复生成。详细 HTTP 约定见 [SPEECH](SPEECH.md)，部署见 [INDEXTTS](INDEXTTS.md)。

# v0.7 架构补充（当前约定）

Video App/HyperFrames 在独立渲染容器运行；RunDesk 在宿主机管理 Codex 容器。业务 API 与 HTTP MCP 均由 App 提供。两容器不共享 App 数据；Codex 用 assetId 操作分镜，App 自己读取素材与保存成片。RunDesk 注册应用创建一个工作区供该应用项目使用，项目分别关联会话。项目工作区目录不是 Codex 的 cwd。

新增 `GET /api/health`（认证后的进程健康）、`GET /api/runtime`（只读运行状态）、`POST /api/runtime/prepare`（启动并探测当前 Docker 环境）。注册/检查不等于模型可执行，探测结果不等于媒体渲染已通过。

`POST /api/projects/{id}/connect?new=1&expectedSessionId=...` 明确新建当前环境对话，保留历史；只允许非活动且无待确认提交的项目，原会话 ID 校验使重试安全。发送新消息前校验项目、实例、工作区和运行方式；已接受或待确认消息仍用原回执恢复，不受重复准备影响。

持久数据挂载 `/data`；构建上下文排除 Token、业务数据和旧二进制。部署拓扑及地址见 [DOCKER](DOCKER.md)。以下原有业务状态、媒体和 revision 规则继续有效。

# 架构与接口

视频 App → RunDesk HTTP API → Codex App Server。视频 App 不直接启动或复用 Codex App Server 连接，也不复用 Sandbox Agent。

| 部分 | 职责 |
| --- | --- |
| 视频 App | 项目/素材/分镜的权威状态、专业界面、版本、渲染任务、成片 |
| RunDesk | 实例配置、会话、事件轨迹、审批、Skills/MCP 管理、Agent 生命周期 |
| Codex | 模型调用、Agent 循环、原生上下文与工具执行 |
| HyperFrames + FFmpeg | HTML/GSAP 工程检查与视频渲染 |

Go 服务与嵌入式页面同源，通过 Cookie 登录。App 后端持有 RunDesk API 令牌；不会把令牌放入页面。供 Codex 调用的 MCP 复用同一 App 二进制的 `mcp` 子命令，使用 stdio JSON-RPC，再通过回环 HTTP 访问 App。

## 状态与目录

- `config.json`：RunDesk 专用实例 ID。
- `access-token`：未用环境变量覆盖时的 App 访问令牌。
- `projects/<id>/project.json`：项目权威状态，原子替换写入。
- `projects/<id>/workspace/assets/`：App 持有的上传素材；Agent 通过业务 MCP 和 assetId 引用。
- `integrations/video-project/SKILL.md`：业务技能安装到 RunDesk 当前实例/运行环境；不在 App 素材目录执行。
- `projects/<id>/renders/<renderId>/`：独立 index.html、GSAP、字体、素材副本、snapshot.json、render.log、validation.json、final.mp4；有音轨时还保留 silent.mp4 和解码后的 audio-N.wav。

项目包含 revision、name、brief、shots、audio、subtitles、speech、news、assets、renders、workspaceId、sessionId。界面与 MCP 修改分镜都必须携带当前 revision，旧版本写入返回 409。Agent 回写后，未编辑的界面自动刷新；存在草稿时显示冲突提醒，等待用户合并，绝不无声覆盖。

镜头结构为 `{id,title,text,duration,assetId}`。新增镜头可不提供 id，由服务器生成。修改使用完整 shots 数组，保留未修改镜头。音轨和分镜共用修订号；省略 audio 保留原音轨，audio:{} 清空音轨。assets/renders/绑定字段由服务维护，PUT 请求不能覆盖它们。

每次渲染使用独立快照，后续分镜编辑不改变进行中的渲染。正文经过 HTML 转义。工作流为 compose → HyperFrames check → render → 可选 FFmpeg 混音 → FFprobe 检查 → completed。只有通过检查的 MP4 才能通过 API 下载。失败日志保留。

## HTTP API

API 使用 `Authorization: Bearer <VIDEO_APP_TOKEN>`，浏览器使用登录 Cookie。

| 方法与路径 | 用途 |
| --- | --- |
| POST /login | `{token}` 获取 Cookie |
| GET /api/environment | 环境存在性检查、RunDesk 连接 |
| GET / POST /api/projects | 列表 / `{name}` 新建 |
| GET / PUT /api/projects/{id} | 读取 / 带 revision 更新项目 |
| POST /api/projects/{id}/assets | multipart `file` 上传 |
| GET /api/projects/{id}/assets/{assetId} | 读取素材，支持 Range |
| POST /api/projects/{id}/connect | 配置专用实例、工作区、Skill、MCP、会话；工具版本相同时幂等返回，?refresh=1 强制刷新应用管理的配置 |
| POST /api/projects/{id}/messages | `{text,requestId,expectedTurnId?}` |
| GET /api/projects/{id}/agent | 会话状态 |
| POST /api/projects/{id}/agent/stop | 请求停止 Agent |
| GET /api/projects/{id}/events?after=N | 增量事件，每次最多 1000 条 |
| GET /api/projects/{id}/approvals | 授权记录 |
| POST /api/projects/{id}/approvals/{approvalId} | 转发 RunDesk 决策对象 |
| POST /api/projects/{id}/render | 新建独立后台渲染任务 |
| POST /api/projects/{id}/renders/{renderId}/stop | 取消对应渲染任务 |
| GET /api/projects/{id}/renders/{renderId}/{file} | final.mp4、snapshot.json、render.log、validation.json、subtitles.srt |

运行中补充指令使用 `expectedTurnId` 作为前置条件、`requestId` 作为 RunDesk 幂等键。拒绝后不自动转成新一轮对话。事件界面按 turnId/itemId 维护 DOM 节点，刷新不会重建已有 details，因此保留展开状态。

页面使用增量轮询；后台与渲染任务不依赖页面连接。复杂的工具询问、OAuth elicitation、完整轨迹和配置操作仍在 RunDesk 页面完成。

## MCP

- `video_project_get(projectId)`
- `video_news_get(projectId)`
- `video_news_audio_adopt(projectId, revision)`
- `video_subtitles_update(projectId, revision, srt, enabled, reviewed)`
- `video_project_update(projectId, project)`
- `video_speech_start(projectId, revision, requestId, backend?, voice?, language?, speed?, rate?)`
- `video_speech_stop(projectId, speechId)`
- `video_speech_adopt(projectId, speechId, revision)`
- `video_render_start(projectId)`
- `video_render_stop(projectId, renderId)`

这是单用户本机管理服务。所有视频项目共用 App 专用实例，项目各有独立工作区与会话；App MCP 凭据可访问此 App 的所有项目，不是租户隔离边界。Skills 不替代沙箱与权限策略，App 不自动放宽 RunDesk 审批配置。

分镜是结构化数据，渲染时生成 HTML。手动改生成工程不会回写分镜，下次生成会产生另一份工程。音轨也属于 App 的结构化项目状态，后续可新增模板和更丰富的轨道模型，再由 Go 驱动渲染器，不把视频业务塞入 RunDesk。

音轨混音先解码 sourceStart 之后的片段为 48kHz 双声道 PCM，再应用延时/音量，按需要循环背景音乐；补齐/截断至视频时长，限制峰值并编码 AAC，视频流直接复制。每次渲染使用自己的音频素材副本。最终校验包括尺寸、30 fps、总时长和预期音频流是否存在。

项目 toolsVersion 记录已写入的 Skill/MCP 版本。旧版本项目保留会话与工作区，通过更新工具按钮应用新规范。App 的配置写入与发送请求串行处理，更新前检查该专用实例是否存在活动会话；原生权限和并发策略仍由 RunDesk 决定。

## v0.3 新闻交接

news2douyin 通过版本化 ZIP 交接，不共享数据库。项目新增只由导入接口维护的 news 来源记录；分镜仍是制作草稿。来源文件独立保存在 Agent 工作区之外，MCP 新增 video_news_get 读取原稿、审核与证据。

原始交接成片与 APP 渲染列表分开，独立保存哈希和版本。完整接口、存储及限制见 [NEWS-HANDOFF.md](NEWS-HANDOFF.md)。

## v0.4 字幕时间轴

字幕为独立绝对时间轨，与配音/正文绑定。普通项目 PUT 保留服务端字幕；专用接口保存草稿或人工核对。已启用但失效的字幕阻止渲染；快照同时保存 SRT、音频副本和绑定元数据，并复核音频哈希。画面模板留出底部字幕区域。详细模型、接口、版本冲突和限制见 [SUBTITLES.md](SUBTITLES.md)。

## v0.5 本机配音

SpeechJob 独立保存到项目，生成使用固定修订快照，任务进度不改变编辑 revision。eSpeak NG 子进程逐段输出后，FFmpeg 转为标准 PCM，Go 按真实样本拼接音频、生成字幕和帧对齐的逐镜头时长。任务与渲染各有独立取消句柄；启动时恢复为中断态。结果由用户显式采用，并重新检查当前修订、正文/结构和音频哈希。完整接口、限制及恢复行为见 [SPEECH.md](SPEECH.md)。
