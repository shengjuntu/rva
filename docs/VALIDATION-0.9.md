# v0.9.0 验证

- Go全量竞态测试：112通过，1跳过，0失败。原始记录见tests-0.9.jsonl，跳过项见validation-0.9.json。
- JavaScript语法检查通过；已有配音提交Node测试5项通过。
- JSDOM执行实际页面脚本，并连接真实Go HTTP/MCP服务：正文保存、段落对齐表、原文保护、上传图片选择、插入导语后保留素材、正文持久化、MCP实际PNG预览通过。
- 新增服务端测试覆盖正文字符保留、容量约束、版本冲突、段落绑定、持久化、终稿必须对应当前已完成视频、内容变化自动取消终稿、关闭字幕仍不能用旧配音确认终稿、未核对字幕预览不伪造审核及过期绑定拦截。
- Linux amd64编译并用于HTTP/DOM测试；Windows amd64仅交叉编译。

## 未验收范围

图形浏览器启动受到本环境AF_UNIX socket限制，未完成视觉布局/播放验收。DOM测试不等于图形浏览器测试。真实Codex语义选素材、真实IndexTTS推理、HyperFrames完整MP4、Docker构建和Windows运行均未在此环境验收。沿用旧功能不代表本版已经完成这些实测。

见validation-0.9.json与dom-validation-0.9.json。正文对齐由Agent操作，不内置图文模型。

## 复验

`go test -race ./...`，`node --test tests/*.test.cjs`，`node --check web/app.js`。

DOM联调脚本为tests/dom-production.cjs，使用已构建Linux bin/video-app、Node.js和单独安装的jsdom开发依赖；`NODE_PATH=<jsdom所在node_modules> node tests/dom-production.cjs`。该依赖仅用于测试，不属于App运行依赖。测试监听127.0.0.1:3321并建立临时数据目录。
