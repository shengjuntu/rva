# v0.12 独立口播与剪辑衔接

本版推进第三阶段的服务闭环。自由正文存于 narrationText，独立于旧 Shots 与编辑 DSL。App 可保存正文；Codex 通过项目 Skill 调用 MCP 完成配音、物化音频和时序、编排 DSL、渲染初稿。创意内容仍由 Codex 和人决定。

1. video_narration_update(projectId, revision, text) 保存原文，不强制拆成画面。
2. video_job_start(type=tts, options={source:"narration"})。requestId 为新 24 位十六进制 ID；轮询 video_job_get。
3. 完成后 video_speech_materialize(projectId, revision, speechId=nativeId)。返回音频 asset、duration、cues、units、绝对时间 anchors。重复调用不会重复添加素材。正文变化会拒绝过时配音。
4. Codex 检查图片/视频素材，自由确定画面和字幕，再 video_edit_validate/update。音轨引用刚物化的素材，字幕时间来自 cues。仅复制使用到的 anchors，DSL 最多 300 个。
5. video_job_start(type=render) 生成初稿；人工试听检查后调整。尚不代表终稿验收。

TTS 的 synthesis_chunk_boundaries 是合成片段的实测边界，不是逐字强制对齐。片段为既有 TTS 实现兼容 30fps 和最短 1 秒，可有静音补齐。正文原文完整保存，合成时会规范空白。音频物化不会覆盖旧分镜、旧音轨或 DSL。

新 MCP 共 27 项；更新项目工具以取得新版 Skill。默认生产 Skill 已包含上述流程。按项目配置多个制作 Skills、初稿的一键 RunDesk 派发、完整真实 Codex 验收仍待第三阶段后续；图片/视频生成和语义匹配留第四阶段。

验证：Go race 全套、独立口播通过模拟 IndexTTS + 真实 FFmpeg 生成 WAV、音频物化幂等、正文变更失效、原剪辑不变；现有 DOM + HTTP 流程。没有真实 IndexTTS 模型音质、真实 Codex 编排或图形浏览器验收。
