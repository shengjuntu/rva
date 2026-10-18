# v0.10.0 第一阶段验证

- Go全量竞态测试：122项通过，1项跳过，0失败。原始记录见tests-0.10.jsonl。
- JavaScript语法检查通过；提交回执Node测试6项通过，包括配音与渲染命名空间分离、不确定请求保持原参数。
- 实际Go HTTP服务+JSDOM：正文/素材旧流程、能力面板、真实素材导入去重、未实现生成能力拒绝、统一任务面板与MCP图片内容通过。
- 新测试覆盖能力配置与模型列表探测分离、密钥不返回、并发素材导入去重、持久化回执、渲染请求冲突、任务跨项目隔离、幂等取消、未知选项拒绝、新建项目重命名后的回执、全局MCP参数契约。
- 统一TTS任务使用HTTP模型测试替身返回WAV，运行实际FFmpeg/FFprobe重采样、合并和产物读取；并发渲染测试用故意失败的渲染器，验证只分配一个任务，不证明成片质量。
- Linux amd64编译并用于联调；Windows amd64仅交叉编译。

## 限制

未完成图形浏览器视觉/播放、真实Codex、真实IndexTTS推理、完整HyperFrames视频、Docker构建及Windows运行验收。前次Chrome启动受本环境AF_UNIX socket限制；本版DOM验证不是图形浏览器测试。

图片生成、视频生成、语义对齐及剪辑DSL尚未实现；能力报告明确标记其状态。新版TTS仍读取旧镜头正文快照，这是后续内容闭环要扩展的接口。

## 复验

`go test -race ./...`、`node --check web/app.js`、`node --test tests/*.test.cjs`。

DOM脚本tests/dom-production.cjs使用已构建Linux程序、Node.js和独立安装的jsdom测试依赖：`NODE_PATH=<jsdom所在node_modules> node tests/dom-production.cjs`。该依赖不是App运行依赖。
