# 文字驱动视频：本次制作指令调整

本次把按版本不断追加的 Skill 改为一份按实际工作顺序书写的操作规程。RunDesk/Codex 负责制作，Video App 提供可核验的服务与产物；界面通过阶段卡片展示结果。

## 内容依据

权威输入是项目中完整保存的 `narrationText`。图片、视频、字幕和剪辑都围绕这段文字组织；不能为了利用一张图片改写事实，不能用示意图或生成图冒充事件证据。规划时可自由使用引文、字符范围或自行组织的文本单元；它们不强制变成镜头，也不修改原文。

配音合成单元只用于服务调用和实测时序，不决定镜头数量。一个画面可跨多个文字单元，一个文字单元也可用多个画面。DSL 继续只表示剪辑：素材引用、轨道、时序、画面布局和已有的文字/音频能力。

## 借鉴与适配

参考项目：[browser-use/video-use](https://github.com/browser-use/video-use)，以及其 [SKILL.md](https://github.com/browser-use/video-use/blob/main/SKILL.md)。该项目强调以文本表面进行编辑决策、保存项目记录，并检查实际渲染结果。

这里的输入以用户撰写的口播为主，因而采用完整原文和文字引用作为主工作表面；没有把它改成一个以素材转写为入口的工作流。保留已有连续配音，不照搬素材拼接流程或每个视觉切点的音频淡化规则。图片、视频模型只使用当前已实现的服务；不引入其他项目的厂商或模型前提。用户一键制作初稿已经给出执行授权，计划记录不是重复审批。

## 实际产物与接口

| 步骤 | 必须留下的内容 | 实际工具或来源 |
| --- | --- | --- |
| 读取 | 完整正文、项目版本、素材、能力、已有剪辑 | `video_project_get`、`video_production_board_get`、`video_asset_list`、`video_capabilities_get`、`video_edit_get` |
| 计划 | 具体文字引用、视觉意图、证据/示意区别、备选方式 | `video_production_checkpoint(stage="plan")` |
| 配音 | 真实音频素材、实测合成单元和字幕区间 | `video_job_start(type="tts", options={"source":"narration"})`、`video_job_get`、`video_speech_materialize` |
| 覆盖表 | 对应文字、时间引用、真实素材 ID、选择理由、缺口/文字卡与观察限制 | `video_asset_inspect`、`video_asset_describe`、可选 `video_media_align`，然后 `video_production_checkpoint(stage="media")` |
| 剪辑 | 符合当前能力的 DSL 与真实完整成片 | `video_edit_validate`、`video_edit_update`、`video_job_start(type="render")`、`video_job_get` |
| 检查 | 具体成片、抽查时间、实际检查、剩余问题与未执行检查 | `video_render_inspect`、`video_production_checkpoint(stage="review",renderId=...)` |

阶段记录使用 board 返回的 `sourceHash`：精确保存正文的 UTF-8 SHA256。TTS 的 `scriptHash` 是内部合成投影摘要，二者不能互换。写阶段文档必须使用当前 revision；文档本身并不执行配音、匹配或渲染。

## 能力边界

- 素材检查返回图片或视频首帧；这不能证明视频后续动作或可用剪辑点。描述必须说明观察范围。
- 当前匹配是对明确描述的词项匹配，不能称作视觉语义模型或置信概率。
- TTS 时序是实测合成片段边界，可能包含补齐静音；不是逐字强制对齐，也不是转写核验。
- 渲染检查工具显示指定时刻的真实成片画面。抽样不等于观看全片，更不等于听过声音。
- 检查完成是 Agent 完成所记录的工作；终稿仍要求用户确认那一份完整输出。短预览、候选列表、计划写完和编译成功都不代表视频完成。
- 图片/视频生成走现有 `http_binary_v1` 服务，不声称已支持任意厂商 API、参考图或任意生成参数。

`bridge.go` 的基础 Skill 和随包 `SKILL.md` 保持完全一致；`workflow.go` 的三个阶段 Skill 是同一操作规程的重点说明。实际部署后需要更新项目工具，使 RunDesk 安装新的指令内容。
