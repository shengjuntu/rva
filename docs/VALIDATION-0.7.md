# v0.7.0 验证记录

2026-10-13；配套 RunDesk 0.48.1，news2douyin 0.15.0。

## 已通过

- Video App `go test -race -json ./...`：37 个顶层测试通过，无失败；包括持久请求/回执、MCP、字幕/音轨/配音状态、锁互斥与错误释放、新运行环境预检查、新闻图片及语言交接、路径/大小/哈希/非图片拒绝。部分已有媒体测试使用本机 FFmpeg，未运行 HyperFrames 成片渲染。
- Linux amd64 与 Windows amd64 编译成功。Windows 仅交叉编译。
- 真实 Video App + 真实 RunDesk HTTP 联调 8 项：注册与会话复用、空会话 local→docker 更换、容器准备复用与真实业务 MCP 回调、MCP 项目读取、任务前置检查和同请求复用、显式新对话及重复提交、SIGKILL 后持久数据重启、整个链路只创建/启动一个环境且不构建/拉取镜像。
- 上述 Docker CLI、Codex 与 MCP 发现过程使用测试替身；代理实际请求 Video App tools/list 后替代 Demo Codex 的发现结果。不能据此声称真实 Docker/Codex 已通过。
- news2douyin 0.15.0 真实导出器与 Video App HTTP 导入器：中文语言字段、分段证据、空 event_key 短稿、一张 PNG 候选及字节一致性、来源/候选标记、重复导入返回原项目通过；源文章、模型、图片下载为测试数据。
- RunDesk 应用 runtime/镜像相关 Go 测试通过；11 个 profile 测试通过；内置构建上下文与 CLI 一致；58 份当前文档链接及 187 个 API 路径检查通过。
- JS/MJS 与 shell 语法检查、Compose YAML 结构检查、Docker CLI init 重入保留原配置通过。

## 尚未实机验证

- 当前环境无 Docker Engine：未 build/run 本版 Docker 镜像，未推送镜像仓库，未运行 Docker Compose 的最终配置解析或容器权限/网络检查。
- HyperFrames 浏览器下载因网络代理超时失败；环境安装也受系统权限限制。未在本轮生成或目视检查最终 HyperFrames 视频，未跑新版 WebUI 浏览器回归。
- 未使用用户 Codex 账号调用真实模型，未消耗新闻搜索服务。

## 部署后验收入口

```bash
bash scripts/docker.sh doctor
bash scripts/docker.sh smoke
```

`smoke` 应实际生成带测试音的 2 秒 1080×1920 H.264/AAC MP4；再在界面播放和检查中文/音轨，最后用已配置的 Codex 发送真实制作任务。自动元数据检查不能代替画面与音频验收。

协议联调可复跑：

```bash
python3 scripts/integration_rundesk.py --rundesk-root /path/to/rundesk-v0.48.1 --output /tmp/video-wire
```

此命令需要已构建的 `bin/rundesk` 与 `bin/video-app`，仍然是 Docker/Codex 测试替身模式。
