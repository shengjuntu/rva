# 0.27.0 验证记录

本轮新增 RunDesk 制作环境查询与可搜索选择器。完整 Go **-race** 回归 472 项通过（213 项顶层），0 失败，6 项依赖实际媒体/外部运行环境的 opt-in 测试跳过；Go vet、Linux 构建与版本启动、Windows amd64 交叉编译通过。Windows 未实机运行。

已发布 JavaScript 的 JSDOM 测试 21 项通过（新选择器 11 项、旧工作台/回执/生成设置 10 项）。最终 Go HTTP 服务与实际网页脚本联测 24 项通过；RunDesk 目录及 Agent 交互使用明确 fixture，App 项目创建、素材、回执和历史接口实际执行。目录后端另有 7 组 HTTP 测试覆盖当前凭据可见列表、默认/停用/缺失、状态、脱敏、边界与取消；全程只读，不启动容器或模型。

没有实际 Docker 生命周期、Codex/Kun 模型任务、IndexTTS/ComfyUI 推理、新闻成片质量或图形浏览器布局验收。普通新建和首访创建使用选择器；新闻包导入保留原创建路径。完整范围、跳过项和原始证据见 [validation-0.27.0.json](validation-0.27.0.json)。

---
以下保留历史验证。

# v0.2.0 验证记录

验证日期：2026-09-29。环境：Linux amd64、Go 1.27.1、Node.js 24.19.0、FFmpeg 6.1.1、HyperFrames 0.8.87、GSAP 3.15.0、Chrome Headless Shell、Playwright。

## 已验证

- `go test -race ./...`：通过。覆盖乐观版本冲突、字段校验、鉴权/Origin、素材上传和 Range、目录外符号链接拒绝、渲染重启状态、HTML 转义、快照、RunDesk 配置流程和 steer 前置参数。
- 新增音轨校验、旧客户端省略 audio 的保留行为、旧项目载入、活动会话阻止工具更新、渲染子进程不继承 App/RunDesk 令牌、启动失败释放锁测试。
- 真实 FFmpeg 混音测试：解码最终音轨为 PCM，检查起点前静音、配音增益和结束位置、背景音乐增益及循环，拒绝超出素材时长的截取起点。
- `go vet ./...`：通过。
- Linux amd64 可执行文件：实际启动与操作。
- Windows amd64：交叉编译通过；未实际运行。
- 对接 **RunDesk v0.5.4 演示模式**：专用实例、工作区、项目 Skill、MCP 配置、会话、消息、命令审批、运行中补充指令、增量事件。
- 真实 App stdio MCP 进程：读取项目成功。此项直接调用本 App MCP，不依赖演示 Agent 是否模拟执行工具。
- 浏览器：登录、载入项目、外部编辑产生冲突时保留草稿、工具详情刷新保持展开、成片播放、无 JS pageerror；音频上传、双音轨选材/起点/音量/循环保存，以及工具重新配置且保留会话均通过。
- 390px 移动布局：无文档横向溢出。
- **真实 HyperFrames 渲染**：两镜头、4 秒、1080×1920、30 fps、带 AAC 音轨的 MP4；check 和 FFprobe 验证通过，并在浏览器实际播放。样片见 `sample.mp4`，音频是合成测试音，不是真人配音。
- 后台渲染取消：任务转为 interrupted。

HyperFrames 的默认遥测请求曾被运行环境的自动审批拦截。核对该工具的官方环境开关后，App 渲染子进程与安装脚本均设置 `HYPERFRAMES_NO_TELEMETRY=1` / `DO_NOT_TRACK=1`，后续验证通过。

## 验证边界

**没有调用真实付费模型，没有完成用户机器上的 Codex 登录，也没有证明真实 Codex 已加载并调用所有工具。** RunDesk 演示模式验证了接口与 UI 交互；真实 MCP 进程和渲染器另行测试。最终还需要在用户的 RunDesk 专用实例上做一次真实 Agent 写分镜 → MCP 渲染验收。

未做完整 Ubuntu 新机器安装、全部素材编码兼容性、长视频性能、断电数据恢复或 Windows 子进程取消验收。这是面向本地单用户的 v0.2 原型，不能据此声称生产环境可用。

## 复现浏览器与渲染验收

准备独立 RunDesk v0.5.4 二进制、渲染依赖以及 Playwright。测试只启动临时数据目录和 RunDesk 演示模式，使用测试令牌；不访问已有 RunDesk 数据。测试端口 3321/3322 必须空闲。

```bash
export RUNDESK_BIN=/绝对路径/rundesk/bin/rundesk
export HYPERFRAMES_BROWSER_PATH=/绝对路径/chrome-headless-shell
export VIDEO_FONT=/绝对路径/NotoSansCJKsc-Regular.otf
# 若 Playwright 不在默认 Node 模块路径，指定它的模块绝对目录：
export PLAYWRIGHT_MODULE=/绝对路径/node_modules/playwright
# 缺省使用本项目 runtime，可另行指定依赖目录：
# export VIDEO_RENDERER_DIR=/绝对路径/渲染依赖目录
node scripts/smoke.cjs
```

脚本打印临时数据和截图目录，执行真实渲染。正常结束时停止两项服务；临时文件保留用于检查。
