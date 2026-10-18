# v0.11.0 第二阶段验证

- Go全量竞态测试127项通过，1项本地eSpeak实测跳过，0失败。
- Node提交回执测试6项通过；页面JavaScript语法检查通过。
- JSON Schema Draft2020-12元模式及最小示例校验通过。
- 实际Go HTTP/MCP + JSDOM验证：新剪辑示例、保存激活、编译时间线展示、保留DSL切回旧模式；旧素材/正文功能继续通过。
- 实际FFmpeg/FFprobe生成MP4并检查：多轨合成，视频入出点及2倍速（源视频颜色随时间变化），after/锚点顺序，半透明画面位置及像素，字面文字像素，音频入场前静音及入场后非零波形，时长/尺寸/帧率/音轨，后续编辑不改变旧渲染快照。
- 编译器验证循环、悬空引用、未知字段、越界、时长与源范围不一致、版本冲突、独立于正文的保存和持久化。
- Linux amd64编译并用于联调；Windows amd64仅交叉编译。

示例见examples/edit-preview.mp4与对应IR/滤镜/日志。测试素材由代码和FFmpeg生成，使用DejaVuSans英文文字；不是新闻内容或真实模型生成。

未完成图形浏览器播放/布局、真实Codex编排、真实IndexTTS、中文字体效果、Docker和Windows运行验收。先前浏览器受本环境AF_UNIX限制；DOM检查不等于图形浏览器检查。

## 复验

`go test -race ./...`、`node --test tests/*.test.cjs`；`VIDEO_EDIT_TEST_EXPORT=examples/edit-preview.mp4 go test -run TestEditFFmpeg -v`导出并验证实际成片。

页面联调使用tests/dom-production.cjs与独立jsdom开发依赖；Go运行时未新增包依赖。完整记录见tests-0.11.jsonl、dom-validation-0.11.json、validation-0.11.json。
