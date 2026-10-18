# 制作服务契约 · v0.10.0

## 职责

RunDesk管理Codex、会话与执行。video-app管理媒体数据、专业工具、制作任务与产物。Skills指导创作与工具使用。本版建立第一阶段基础；未把新的剪辑DSL放进旧镜头模型。

## 能力注册表

GET /api/capabilities，默认本地检查；GET /api/capabilities?probe=1额外读取已配置IndexTTS /v1/models。

返回video.capabilities.v1，包括能力ID、implemented、ready、提供者、运行方式、支持项、工具、限制和原因。提供者状态：blocked、configured、available。available表示指定检查通过，不是成片/音质实测。

- media：列表、小素材导入、已有上传。图片/视频预览需FFmpeg。
- tts：现有IndexTTS和eSpeak；TTS正文仍从保存的旧镜头快照读取。
- editing：旧版镜头与音轨编辑；多轨剪辑DSL第二阶段实现。
- rendering：HyperFrames旧镜头渲染。检查Node22+、媒体命令、CLI/字体/GSAP文件及浏览器。引擎私有下载缓存未自动探测，未知浏览器请配置HYPERFRAMES_BROWSER_PATH。
- image_generation / video_generation / text_media_alignment：implemented=false，第四阶段接入。不返回假的可执行工具。

响应不含TTS密钥或服务地址。默认不做远端网络探测。模型列表匹配不验证音色或真实合成。

## 统一制作任务

- GET /api/projects/{id}/jobs
- POST /api/projects/{id}/jobs
- GET /api/projects/{id}/jobs/{jobId}
- POST /api/projects/{id}/jobs/{jobId}/cancel

提交例：

```json
{"type":"render","revision":12,"requestId":"0123456789abcdef01234567","options":{"kind":"draft"}}
```

支持tts与render。tts接受backend、voice、language、speed、rate，按原后端约束校验；render只接受kind。未知选项拒绝。未实现type返回501，不分配任务。

jobId为tts:<原配音ID>或render:<原渲染ID>，保留nativeId。统一状态：running、cancel_requested、succeeded、failed、interrupted、unknown。原状态同时保留在nativeState。

配音可报告completedUnits/totalUnits；渲染progressKnown=false，不制造进度。完成任务提供认证产物URL。取消幂等且限于指定项目；已完成任务返回完成状态。interrupted可能是取消或服务重启，不等于远端模型已停止/停止计费。

任务直接投影现有持久Speech/Render记录，没有第二套状态表。重启不自动重新提交。采用配音仍需原有adopt接口，不自动改变当前音轨。

## 幂等与素材

POST /api/projects支持可选requestId。相同请求返回同一项目，即使项目后来已重命名；不同名称复用ID返回409。旧调用不传requestId保持原行为。

GET /api/projects/{id}/assets返回素材元数据和认证URL。

POST /api/projects/{id}/assets/import：name、data（标准Base64）、requestId；最大解码1MiB。相同文件名/字节的并发重试只创建一个素材，改变内容返回409。大文件继续用原multipart上传（最大256MiB）或页面目录导入。multipart也可提交requestId获得同样幂等语义。

原POST /render支持可选revision/requestId，统一任务渲染入口要求两者。相同请求即使内容后来改变也返回原回执；修改请求需新requestId。保留旧客户端不带这两个字段的行为。

## MCP

新增video_capabilities_get、video_project_create、video_asset_list、video_asset_import、video_job_start、video_job_get、video_job_list、video_job_cancel。原13个工具保留。

统一接口采用严格JSON选项；不能通过未知参数调用尚未接入的DSL或生成模型。既有stdio与Streamable HTTP共用调度器。
