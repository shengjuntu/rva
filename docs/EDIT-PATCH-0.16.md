# v0.16 精确剪辑修改与撤销

新增 MCP video_edit_patch_preview、video_edit_patch_apply、video_edit_undo，总计32项。更新项目工具以取得新版制作指南。

请求示例：
```json
{"projectId":"项目ID","revision":10,"changes":[{"id":"opening_title","op":"update","fields":{"text":"新标题","duration":4}}]}
```

add/update/delete 仅针对 clips。update 合并顶层字段；嵌套 layout/source 整个替换，不能当作深层局部合并。null 可清除可选定位字段，例如清除 start 后提供 after。不能修改 ID；add 追加对象，删除被引用对象必须在同一修改集合修复相关引用。未知字段、重复ID、无效编译均拒绝。

preview 返回完整 before/after 和编译 IR、affectedClipIds，包含因 after/anchor 时间依赖而改变的对象。它不是渲染预览，不是字段级可视化 diff，也不判断创意效果。应用使用相同 changes 和基础 revision；期间项目变化会409。App 保留当前激活模式，不自动切换渲染器。

撤销只反转最近的局部修改，最多保留20个快照；若当前 DSL 或模式与修改后的状态不一致，会拒绝覆盖撤销。恢复也会编译校验。暂无 redo、历史任意回退、轨道/画布/锚点局部编辑及增量渲染。历史随项目保存，渲染仍使用完整快照。

页面可点击时间线选择对象、输入局部修改JSON、预览完整前后DSL、应用、撤销。自然语言对话由 Codex 翻译为上述修改集合；App 不自带第二个语言模型。

HTTP：POST /edit/patch/preview、POST /edit/patch/apply、POST /edit/undo。全部需要revision。预览只读，应用和撤销增加修订。

验证：Go race全套；预览无写入、时间依赖对象变化、应用/撤销、修订冲突、拒绝覆盖后续编辑、拒绝未知字段；JSDOM+真实HTTP局部修改/撤销回归，6项Node请求测试。未做图形浏览器或真实Codex对话验收。
