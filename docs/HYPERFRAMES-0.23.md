# 自主导演与 HyperFrames · v0.23

## 职责与数据

Codex 自主决定分镜、素材、图像/视频生成、TTS、动画和检查。RunDesk 提供环境、配置、运行控制和深入轨迹；Video App 提供可组合制作 MCP、素材/版本和多轨审阅，不内置固定场景编排器。

正文、自由导演简报、精确文字与素材关联、HyperFrames 工程各自独立。关联可多对多，不预设全文切分；工程保存原创 HTML/CSS/JavaScript、真实素材映射和可选审阅时间线。timeline 为 Agent 声明的编辑信息，不解析实际动画、不自动创建字幕或声音，也不是实测配音对齐。真正画面、字幕和音频必须由源工程实现。

## MCP 与 HTTP

| MCP | 用途 |
| --- | --- |
| video_hyperframes_get(projectId) | 读取完整源工程、当前 revision、正文与文档哈希、素材指纹和过期原因 |
| video_hyperframes_update(projectId,revision,document) | 完整保存原创工程；检查当前原文、真实素材及版本冲突，不运行源代码 |
| video_hyperframes_check(projectId,revision) | 冻结输入并进行实际 HyperFrames/browser 校验，返回日志和 rendered=false |
| video_job_start(type="render",...) | 统一异步完整渲染；保持幂等回执、取消和持久状态 |
| video_review_timeline_get(projectId,renderId?) | 查看当前或指定历史输出的轨道、声明来源和当前性 |
| video_edit_locate(projectId,quote,occurrence?,renderId?) | 通过已保存的精确引用定位稳定对象和编辑时间，缺关联不猜测 |

HTTP 工程读写为 GET/PUT /api/projects/{id}/hyperframes；校验为 POST /api/projects/{id}/hyperframes/check。渲染复用现有任务与渲染接口；细节以 MCP 实际 schema 为准。完整最小结构与原创动画示例见 [authoring reference](../integrations/video-project/references/hyperframes-authoring.md)。

工程结构为 schemaVersion=1、entrypoint=index.html、canvas={width,height,fps,duration}、hasAudio、files=[{path,content}]、assets=[{assetId,path}]、textSourceHash 和可选 timeline。素材指纹由 App 读取并保存；路径只在工程相对目录内。源文件 1–128 个、单文件不超过 512 KiB、合计不超过 1 MiB；最多 120 个素材映射与 1000 个审阅片段。渲染 canvas 最长 600 秒。新工程使用实际配音时长，不能把估算或声明当作测量。

## 渲染与版本

保存工程不切换用户选择的后端。新项目选择 hyperframes；旧项目保留历史选择。每次渲染冻结源文件、绑定素材、GSAP 与字体，以清单记录哈希；进行中的任务不读取后续修改过的活素材。HyperFrames 0.8.87 执行 check 和完整 render，FFprobe 核对实际尺寸、帧率、H.264、时长及声明音轨。渲染过程中不安装新依赖。

作者可引用 runtime/gsap.min.js 和自动生成的 runtime/font.css，字体名 VideoFont。需要真实本地音频/图片/视频时保存 assetId 映射并在源中引用；hasAudio 仅声明预期输出，不合成或附加音轨。主流程不要求共享生产目录；video_use 兼容流程继续使用原生产快照、共享目录与归档校验。

当前只支持完整 authored HyperFrames 渲染，不接受 range 局部预览。工程结构合法、校验通过、任务启动或单帧检查都不是完整初稿。渲染成功也不等于检查完成或终稿人审。

## 多轨审阅与局部修改

审阅通过同一版本的画面、配音、字幕、音乐轨道定位，显示其工程/输出身份和当前性。HyperFrames 轨道可缺省；缺失时明确为空，不从文本描述凭空生成。历史输出读取冻结版本，不能用旧时间直接修改已经改变的新工程。首版只读审阅，制作修改仍通过对话和完整修订保存；没有拖拽后不落盘的第二套状态。

正文改变需要重新核对引用、配音和工程。局部指令保留无关源文件、素材与稳定 ID；版本冲突重读并合并，不静默覆盖。Agent 检查须记录实际输出、采样时间及未听审/未观看范围。终稿只接受明确人审过的具体完整输出。

## 验证范围与原创性

本版接口、状态和本地渲染的验证结果以随发布的实际测试记录为准。真实 Codex 的自主创作、远端生成、IndexTTS 质量、Docker 与 Windows 运行不因接口测试通过而自动视为验收。

设计同时参考 OpenMontage 的阶段产物、素材组织与检查方法，以及 video-use 的文字驱动、剪辑定位和预览自检。HyperFrames 主路径的自有代码、数据契约和 Skill 独立编写，不分发 OpenMontage 的代码、Schema 或 Skill；参考流程不等于直接引入上游实现。既有可选 video_use 适配保留，相关固定上游资源继续遵循原 MIT 许可及依赖各自条款。HyperFrames、GSAP 和其他媒体工具遵循自身许可。native DSL、video_use 是显式兼容路径，不承诺与新动画后端能力相同。
