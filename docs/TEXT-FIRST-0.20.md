# v0.20 文字驱动制作与产物工作台

本版回应v0.19测试反馈：默认界面不要求人分别操作配音、对齐、DSL、渲染。正常流程是提供原稿/素材→一键RunDesk制作→看产物→对话调整。底层服务仍作为MCP能力，手动工具进入折叠的高级工作区。

## 从video-use吸收的内容

参考2026-10-18读取的上游README与SKILL：
- https://github.com/browser-use/video-use
- https://github.com/browser-use/video-use/blob/main/SKILL.md

video-use入口是原始拍摄素材→逐字转写→紧凑带时码文字→Agent编辑决策→渲染与检查。我们采用已有原稿→TTS实测时序→画面服务文字。借鉴文字主阅读面、按需视觉证据、明确执行规程、成片后检查和持久记录；没有移植其强制逐段拼接、每个画面切点音频淡化、指定ASR厂商或每次计划审批。连续口播无需随每张图片淡化。未引入上游运行依赖或代码。

## UI

中栏只保留正文、可选要求、素材和一个制作按钮，下方阶段卡。首次配置RunDesk/Codex、TTS和字体仍是环境准备工作；已有配置下按钮自动保存、连接/升级项目工具、派发。核对现有RunDesk源码发现application connect只安装缺失Skill，不替换同名文件；本版使用video-project-v0-20-0等版本化名称，连接时补装、提交时显式选择实际路径。原无版本的自定义Skill保留，不静默覆盖。未发送的修改意见不被覆盖，不确定提交沿用原始requestId恢复。制作和对话不会被静默同时启动。自己上传素材造成的修订变更可以安全推进本地草稿基线；来自Agent/其他操作的版本冲突仍需核对。

卡片：权威正文、制作简报、配音/字幕、文字画面安排、已保存剪辑、初稿/调整、检查报告、终稿。ready仅表示有对应产物/已保存记录，不是人工验收。plan/media/review由Agent通过checkpoint保存；音频与视频来自实际native任务。空记录不会模拟百分比或成功。

## 工具

- video_production_board_get(projectId)，GET /api/projects/{id}/production-board。返回revision、精确正文sourceHash、卡片状态与同项目认证产物URL。
- video_production_checkpoint(projectId,revision,sourceHash,stage,status,summary,document,renderId?)，POST /production-checkpoints。stage为plan/media/review；status为working/completed/blocked。自由文档最多32000字节，每项目保留最近30条。与精确UTF-8原文hash绑定，不能混用TTS投影scriptHash。plan基于正文/工作流，media另包含视觉素材与描述，review另含DSL/激活模式/被检查渲染。有关输入变更显示stale。
- 完成review需引用本版记录了sourceHash的当前完整DSL成片。生成更新完整成片后旧报告显示过时，且卡片链接它实际检查过的旧成片。stage记录本身不执行制作。
- video_render_inspect(projectId,renderId,time)，POST /renders/{rid}/inspect。返回≤960边长、≤4MiB的真实PNG。只接受完成且属于项目的产物与有效秒数。时间相对输出，局部预览从0计。MCP返回image block和单帧限制说明。保护根目录与私有临时复制防止任意文件路径越界；复制大成片有I/O开销。

39项MCP。主Skill按工作顺序重写，原稿与素材都是内容数据；人授权外的事实改写/新服务费用/真正阻塞才请求决定。检查卡不等于听过声音，单帧不是整片视觉证据。阶段产物不是审批关卡。只有用户明确检查完整成片后才能保存终稿。

## 兼容与验证

保留旧项目、任务、配音、DSL、撤销、渲染缓存与高级工具。新news2douyin审核包导入时保存完整原稿到narrationText；旧项目需显式采用已有正文或新闻原稿，不拼接旧镜头猜正文。旧渲染缺少SourceHash时保留视频，但不能作为本版当前检查报告的新引用；重新渲染即可。

测试覆盖：卡片空状态/源hash/过时依赖、checkpoint修订冲突、报告绑定renderId、MCP工具清单/真实图像block；模拟IndexTTS→真实音频处理→自由文字卡+配音DSL→真实FFmpeg完整出片→实际帧检查→检查记录→改视觉后保留配音而检查失效；图片帧颜色/缩放/越界与输出上限。UI通过JSDOM+真实HTTP测试一键保存/连接、产物卡、未发送对话保护、不确定回执重试、正文后上传素材、发送对话调整前保存主输入与旧稿显式采用。未使用真实Codex模型编排、真实IndexTTS模型、图形浏览器或Docker/Windows运行验收。
