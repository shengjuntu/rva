# v0.13 项目制作 Skills 与初稿派发

本版补齐第三阶段的 App 入口。每个项目保存有序的 Skills 选择与自由补充要求，独立正文和编辑 DSL 仍各自保存。

- 内置 video-narration：正文、配音、实测时间与音频物化。
- 内置 video-editing：检查素材、编排 DSL、校验并渲染初稿。
- 内置 video-review：核对实际任务产物与待人工检查事项。
- video-project 保留为基础工具使用指南。

更新项目工具会通过既有 RunDesk application connect 的 defaults 提供这四个 Skill。项目选择决定当前提交明确引用哪些制作 Skill，不会删除实例中的其他 Skill；defaults 的覆盖行为仍由 RunDesk 管理，不承诺自动替换用户自定义版本。提交前读取实例 configuration，取得实际启用 Skill 的路径；缺失时停止派发并提示配置。

App 先保存正文、制作要求，再点“交给 RunDesk 制作初稿”。当前任务未结束、存在待确认请求、对话框已有草稿或制作要求未保存时，入口会明确提示处理。指令生成不调用模型；实际生成由原 RunDesk 会话驱动 Codex。任务请求保存完整指令和所选 Skills，后续恢复沿用原始请求与已保存的实际请求体。取消继续使用现有 Agent 停止接口，TTS 与渲染任务需分别取消。

HTTP：GET/PUT /api/projects/{id}/workflow，保存需 revision；POST /draft/instruction 需 revision，返回初稿指令。POST /messages 增加可选 skills，支持三种内置制作 Skill 名称，重复请求不能改变选择。对话修改仍走原会话；本版不添加 Agent 自调用的 MCP 工具。业务 MCP 保持 27 项。

本版仍不支持任意外部 Skill 导入。生成图片、生成视频、语义素材匹配留第四阶段。正文或要求发生变化时，初稿指令要求 Codex核对并暂停；这个检查属于 Agent 指令，不是 App 对后续每次工具调用的全事务锁。真实 Codex 与真实 IndexTTS 的全流程验收还需部署环境。

验证：Go race 全套；项目配置版本冲突、Skill 验证、模拟 RunDesk 的实际路径引用与仅提交一次、重试时禁止改变 Skills；JSDOM + 真实 Go HTTP 验证正文草稿保留、制作要求保存和初稿指令生成、未连接时禁用按钮；Node 请求恢复测试。没有图形浏览器、真实模型或 Windows 运行验收。
