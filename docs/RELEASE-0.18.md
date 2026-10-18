# v0.18 指定时间段预览与终稿记录

渲染 options.range={start,end}：仅DSL模式，kind=draft，范围必须位于时间线内，至少一帧、最多60秒。保存完整项目和原始时间IR；编码输出从0开始，长度为end-start，验证实际MP4尺寸/帧率/音轨/时长。沿用统一请求幂等与取消。原filter graph仍执行前段计算；这不是增量渲染，不保证提速，也没有缓存复用。

video_render_start也支持range。页面新增起止秒数和局部预览按钮；未知提交沿用原请求确认。完成结果在原任务列表查看。

新增video_final_get和video_final_save，总计35个MCP。save参数projectId,revision,renderId,reviewed=true。必须是当前完整DSL的完成任务；局部预览、旧DSL或改变过口播均拒绝。reviewed仅是人明确试听/检查该成片后提供的声明，编译成功不能替代人工检查。

保存终稿前读取实际final.mp4与snapshot.json并计算SHA256，核对快照内容与当前DSL/正文。项目保存当前designation，finals目录保留独立JSON记录，包含成片哈希、字节数、快照哈希、内容指纹、来源渲染ID、创建时间和人工检查声明；渲染目录保留原始文件。没有复制视频到另一目录，也不锁住继续编辑或自动发布。

GET返回current=true意味着designation对应当前DSL与口播，不会重新逐个读取媒体/成片或重新计算字节哈希。后续素材文件/字体外部修改的完整性检查、终稿下载复核、缓存键及增量预览仍待实现；不可将current视为对所有文件完整性的实时保证。

HTTP GET/POST /api/projects/{id}/final；保存需revision。页面终稿入口需完整原生渲染ID和人工检查勾选。更新项目工具取得新版Skill。

验证：Go race全套；真实FFmpeg完整3秒和范围1秒输出、拒绝局部终稿、拒绝未reviewed、终稿文件记录/hash、正文变化失效；Node6项与JSDOM/HTTP回归。真实Codex、图形浏览器和真实模型验收仍待用户部署环境。
