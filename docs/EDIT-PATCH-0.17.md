# v0.17 跨对象修改、字段差异与重做

沿用 v0.16 局部修改接口，新增 target=clip/track/anchor/canvas，默认clip以兼容旧请求。轨道/锚点/片段支持add/update/delete；canvas固定id=canvas且仅支持update。不同类型可有同名ID。对象重命名需新增替代对象、修复引用、删除旧对象；一批修改完成后统一严格解码和编译，不要求中间状态有效。

示例：
```json
{"revision":10,"changes":[{"target":"anchor","id":"opening","op":"add","fields":{"time":2}},{"target":"clip","id":"title","op":"update","fields":{"start":null,"anchor":"opening"}},{"target":"canvas","id":"canvas","op":"update","fields":{"width":1080,"height":1920}}]}
```

预览新增 fieldDiffs：target、id、field、before、after。字段级比较以顶层字段为单位；嵌套layout/source仍整体替换和展示，不支持深层路径。affectedClipIds来自编译后时间线变化，不代表完备渲染依赖分析；画布背景等变化可能不改变任何片段IR。完整before/after仍返回供核对。

video_edit_redo(projectId,revision)，对应POST /edit/redo。撤销把当前快照保存进重做分支；重做前校验当前DSL与模式，重做目标重新编译，保存增加修订。支持连续撤销/重做；新局部修改、完整DSL保存会清除重做分支。历史随项目保存，重启可恢复。上限仍为20个局部修改快照。尚不支持对象重排和任意历史跳转。

页面差异预览显示字段变化及受影响片段，增加重做按钮。MCP总计33项，安装后更新项目工具。

验证：Go race全套；跨类型添加/删除/定位修改与编译、画布更改、严格字段校验、字段差异、连续撤销/重做、新分支失效和重启恢复；JSDOM+真实HTTP局部修改、撤销、重做回归；6项Node请求测试。没有真实Codex或图形浏览器验收。
