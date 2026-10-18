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
- `projects/<id>/workspace/assets/`：上传素材，同时供 Agent 在其工作目录下读取。
- `projects/<id>/workspace/.agents/skills/video-project/SKILL.md`：通过 RunDesk 写入。
- `projects/<id>/renders/<renderId>/`：独立 index.html、GSAP、字体、素材副本、snapshot.json、render.log、validation.json、final.mp4；有音轨时还保留 silent.mp4 和解码后的 audio-N.wav。

项目包含 revision、name、brief、shots、audio、assets、renders、workspaceId、sessionId。界面与 MCP 修改分镜都必须携带当前 revision，旧版本写入返回 409。Agent 回写后，未编辑的界面自动刷新；存在草稿时显示冲突提醒，等待用户合并，绝不无声覆盖。

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
| GET /api/projects/{id}/renders/{renderId}/{file} | final.mp4、snapshot.json、render.log、validation.json |

运行中补充指令使用 `expectedTurnId` 作为前置条件、`requestId` 作为 RunDesk 幂等键。拒绝后不自动转成新一轮对话。事件界面按 turnId/itemId 维护 DOM 节点，刷新不会重建已有 details，因此保留展开状态。

页面使用增量轮询；后台与渲染任务不依赖页面连接。复杂的工具询问、OAuth elicitation、完整轨迹和配置操作仍在 RunDesk 页面完成。

## MCP

- `video_project_get(projectId)`
- `video_project_update(projectId, project)`
- `video_render_start(projectId)`
- `video_render_stop(projectId, renderId)`

这是单用户本机管理服务。所有视频项目共用 App 专用实例，项目各有独立工作区与会话；App MCP 凭据可访问此 App 的所有项目，不是租户隔离边界。Skills 不替代沙箱与权限策略，App 不自动放宽 RunDesk 审批配置。

分镜是结构化数据，渲染时生成 HTML。手动改生成工程不会回写分镜，下次生成会产生另一份工程。音轨也属于 App 的结构化项目状态，后续可新增模板和更丰富的轨道模型，再由 Go 驱动渲染器，不把视频业务塞入 RunDesk。

音轨混音先解码 sourceStart 之后的片段为 48kHz 双声道 PCM，再应用延时/音量，按需要循环背景音乐；补齐/截断至视频时长，限制峰值并编码 AAC，视频流直接复制。每次渲染使用自己的音频素材副本。最终校验包括尺寸、30 fps、总时长和预期音频流是否存在。

项目 toolsVersion 记录已写入的 Skill/MCP 版本。旧版本项目保留会话与工作区，通过更新工具按钮应用新规范。App 的配置写入与发送请求串行处理，更新前检查该专用实例是否存在活动会话；原生权限和并发策略仍由 RunDesk 决定。

## v0.3 新闻交接

news2douyin 通过版本化 ZIP 交接，不共享数据库。项目新增只由导入接口维护的 news 来源记录；分镜仍是制作草稿。来源文件独立保存在 Agent 工作区之外，MCP 新增 video_news_get 读取原稿、审核与证据。

原始交接成片与 APP 渲染列表分开，独立保存哈希和版本。完整接口、存储及限制见 [NEWS-HANDOFF.md](NEWS-HANDOFF.md)。
