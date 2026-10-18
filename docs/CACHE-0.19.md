# v0.19 渲染结果复用与终稿字节复核

仅DSL渲染。在复制素材和字体到本次渲染快照后，计算实际文件SHA256；缓存键包括编译IR、range、引用素材ID与字节哈希、实际字体字节、FFmpeg版本、App版本和固定编码参数。只查同项目已完成ffmpeg_edit任务，要求其持久化CacheKey与校验记录匹配。

命中前校验旧final.mp4的hash和长度；损坏记录跳过。使用同一个已校验文件句柄复制，随后重新ffprobe校验新输出。当前项目快照和IR仍独立保存。render-proof.json记录key、videoHash、videoBytes、snapshotHash、reusedFrom；任务记录含CacheKey与ReusedFrom。旧版本没有proof的任务不会被复用。

这是完整结果缓存，不是片段缓存或增量渲染。即使命中，仍复制、读取并哈希输入与输出；不同range不能命中，改画布/正文字幕等导致IR变化会失效，未引用素材变化不影响键。App/FFmpeg版本升级也会失效。没有跨项目缓存、自动淘汰或强制绕过参数。

终稿保存进一步核对当前引用素材与字体的输入键，以及成片/快照与render-proof的哈希。旧版本完成渲染缺少proof，需重新渲染后再指定终稿。

新增video_final_verify(projectId)，GET /final/verify：读取实际引用素材、字体、FFmpeg版本、终稿与项目快照。inputsCurrent表示输入键/DSL/口播仍匹配；outputIntact表示成片与快照仍匹配指定终稿时记录；verified要求两者成立。video_final_get仍是轻量内容版本检查，不进行逐文件校验。页面提供“复核终稿完整性”，渲染列表标示复用来源与原生ID。MCP共36项，更新项目工具。

校验反映读取时观察到的文件；没有对外部文件修改者加锁。最终渲染使用自己的素材/字体快照，但校验与之后下载之间的外部修改不能被阻止。记录是本地完整性核对，不是带签名的防篡改账本。

验证：Go race全套；真实FFmpeg重复任务命中、损坏旧MP4拒绝复用并重渲染、终稿字节破坏检测、DSL变化失效；同名素材换字节和range变化导致不同键；6项Node及DOM/HTTP回归。真实Codex/图形浏览器/用户模型仍待部署验收。
