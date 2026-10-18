# 自动配音 · v0.8.0

支持两种后端：连接已有 IndexTTS-2.5 HTTP 服务，或使用本机 eSpeak NG 做机械预览。Go App 仍只依赖标准库；FFmpeg/FFprobe 用于验证、转码和测量音频。IndexTTS 模型不加载到 App 或 Codex 进程。

部署和首次接入见 [INDEXTTS](INDEXTTS.md)。eSpeak 默认在 PATH 查找 `espeak-ng`，也可用 `--espeak` 或启动脚本的 VIDEO_ESPEAK。IndexTTS 模式无需安装 eSpeak。

## 使用流程

保存分镜正文 → 配置服务 → 检查环境 → 选择本次引擎/音色/语速/语言 → 生成 → 试听 → 采用。镜头标题、项目说明不朗读；无正文镜头保留静音。生成只保存独立任务，不改写当前音轨、字幕或镜头。采用后按实际音频调整镜头时长，背景音乐、素材、标题保留。

采用必须使用当前 revision，且镜头 ID、顺序和正文与任务快照一致。旧任务与音频保留，正文修改后拒绝采用旧结果；仅更换画面素材可继续采用。语言是朗读语言，翻译应在正文生成阶段完成。

## 时间如何得到

每个镜头的正文按句末标点拆分，单段最多 80 个字符；较长段优先在空白或逗号处拆开。空白格式会规范化，但不会删去正文字符。每段**先独立合成**，再由 FFmpeg 转成 24 kHz、单声道、16-bit PCM；根据实际样本数记录该段起止时间。不是对既有音频按字数分配时间。

各段音频原样拼接。镜头末尾补少量静音，保证最少 1 秒并向上对齐到 30 fps 帧边界；无正文镜头按原时长补静音并对齐帧边界。一个视频帧对应 800 个 PCM 样本，避免逐镜头舍入累积造成音画漂移。字幕不延长到末尾补入的静音中。单镜头超过 60 秒或总长超过 600 秒会失败，需拆分镜头/项目或调整语速。

`alignment` 与采用后的字幕绑定方法为 `synthesized_sentence_durations`，表示**实测合成分句边界**，不表示每个字已对齐，也不保证发音、数字和专有名词正确。仍需试听。若更换音频或修改字幕，沿用既有字幕核对流程。

新绑定额外记录严格的镜头正文/结构哈希，保留词间空格差异；例如 `now here` 改为 `nowhere` 会使绑定失效。旧版字幕绑定继续兼容原规则，再次采用/核对时会写入新规则。

## 任务、取消与恢复

- 全 App 一次运行一个配音任务，可同时有一个视频渲染。每项目最多 100 个配音任务，一次 1–300 段。
- eSpeak/FFmpeg 子进程单步限时 45 秒；IndexTTS 请求默认每段 180 秒，可配置 10–900 秒；整个任务最多 20 分钟。
- 取消停止当前 HTTP 请求和后续段落，原生 IndexTTS 已进入 CUDA 的当前段不保证立即停止。App 不采用该结果；不能将取消等同于 GPU 已空闲。
- 超时、失败、重启会保留记录但不暴露未完成音频。不会自动重试、续跑或回退到另一配音引擎。
- 创建请求持久记录原始参数哈希。同一 requestId 原样重试返回既有任务，即使配置已改变或服务已重启；同一 ID 配不同参数返回 409。失败后要重新生成需换新 ID。
- 任务生成、进度和完成不增加编辑 revision；采用结果才增加 revision。生成期间的新编辑不被后台覆盖。
- 完成产物保留 SHA-256/大小；下载和采用都会校验。manifest 记录后端、模型、音色、语言、语速、逐镜头长度和字幕句段时间；不保存 API Key。
- 暂无跨任务片段缓存。已有完整结果可反复试听、下载，不能把新建任务当作免费缓存命中。

目录为 `projects/<id>/speech/<speechId>/`，保存 snapshot.json、manifest.json、narration.wav、subtitles.srt、speech.log。采用后生成独立素材副本。备份整个 data，包括 speech-config.json。

## HTTP 与 MCP

所有接口沿用 App 登录认证。配置 API 供管理员页面使用，不作为 MCP 工具暴露。环境变量配置优先且使设置页只读。

| 接口 | 约定 |
| --- | --- |
| GET /api/speech/settings | 当前默认配置、hasApiKey、managedByEnvironment；不返回密钥 |
| PUT /api/speech/settings | backend/url/model/voice/language/timeoutSeconds/apiKey?；空 key 保留原值，clearApiKey=true 清除。更换地址需明确重新填 key 或清除 |
| GET /api/speech/capabilities?backend=indextts | 模型列表连通性及 FFmpeg/FFprobe 检查；不等于音质验证。不支持 /v1/models 时提示未知，仍可短句合成 |
| POST /api/projects/{id}/speech | revision、24 位十六进制 requestId；可选 backend/voice/language/speed/rate |
| POST /api/projects/{id}/speech/{sid}/stop | 取消任务 |
| POST /api/projects/{id}/speech/{sid}/adopt | 当前 revision，校验快照后采用 |
| GET /api/projects/{id}/speech/{sid}/files/{file} | WAV/SRT/清单/快照/日志；下载加 ?download |
| GET /api/projects/{id} | speech 数组包含状态和进度 |

IndexTTS 请求：`{revision,requestId,backend:"indextts",voice:"news_zh",language:"zh",speed:1}`。省略 backend/voice/language 使用管理员默认；speed 默认 1。不要传 eSpeak 的 rate。

eSpeak 请求：`{revision,requestId,backend:"espeak-ng",voice:"cmn",rate:170}`。不要传 IndexTTS 的 speed/language。

MCP `video_speech_start` 同样接收这些字段，另需 projectId；停止/采用工具不变，总工具数仍为 10。已有部署只要更新 App 二进制，就能通过省略可选参数使用已配置的 IndexTTS；建议同步更新项目业务 Skill。Agent 不得传任意服务地址、参考录音路径或命令。

正文会发送到管理员指定的 TTS 服务；没有内置云配音回退。API Key 不返回浏览器、任务清单或渲染子进程；返回错误不直接回显上游响应正文。
