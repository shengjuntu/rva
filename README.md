# RunDesk Video App · v0.2.0

一个使用 **Go + HTML/CSS/原生 JavaScript** 的本地视频工作台。通过 RunDesk HTTP API 使用 Codex；项目、素材、分镜和渲染任务由视频 App 管理。

本仓库是独立 App 原型，不改变 RunDesk 的通用定位。暂用工作名称“视频工作台”。主程序只使用 Go 标准库，页面嵌入二进制。渲染器是独立安装的 HyperFrames CLI；它需要 Node.js、Chrome 和 FFmpeg。

![界面截图](docs/preview.png)

![音轨配置](docs/audio-preview.png)

## 当前可用

- 项目管理、素材上传、分镜编辑、镜头排序、时长和画面素材选择。
- 图片/视频/音频/文本/Markdown/PDF 上传；单文件 256 MiB。音频支持 WAV、MP3、M4A、FLAC、OGG。
- 一键在 RunDesk 创建专用实例、项目工作区、项目 Skill 和 stdio MCP 配置。
- 对话、运行中补充指令、停止 Agent、命令授权和可展开的工具记录。
- 分镜修订号检查，发现 Agent 与界面并发编辑时保留本地草稿、拒绝覆盖。
- 配音与背景音乐双音轨、视频起点、素材截取起点、0–200% 音量、背景音乐循环和素材试听。
- 独立后台渲染任务、取消任务、版本快照、日志、MP4 播放与下载。
- 固定竖版 1080×1920 / 30 fps 模板，使用真实 HyperFrames 渲染及 FFprobe 检查。

**v0.2 支持上传配音和背景音乐并混音输出。** 视频素材原声保持静音；未选音轨时输出无声视频。每个镜头 1–60 秒，最多 80 个镜头，总长不超过 10 分钟。一次运行一个渲染任务。尚未接入文字转语音（TTS），也没有自由时间线、多模板、自定义 HTML 双向编辑、断点续渲染和多人权限。

## 安装

按 [Ubuntu 安装指南](docs/UBUNTU.md) 准备依赖，然后运行：

```bash
chmod +x bin/video-app scripts/*.sh
./scripts/setup-renderer.sh
# RunDesk 开启令牌验证时，先设置同一个 RUNDESK_TOKEN。
./scripts/start.sh
```

打开 http://127.0.0.1:3220 。默认登录令牌位于 `data/access-token`；也可以提前设置至少 24 个字符的 `VIDEO_APP_TOKEN`。RunDesk 默认在 3210 端口。

从 v0.1 升级请先阅读 [升级说明](docs/UPGRADING.md)，保留原数据目录，并在项目中更新工具配置。

新建项目 → 连接 RunDesk → 在 RunDesk 的“视频制作 App”实例完成 Codex 登录/模型配置 → 添加素材 → 对话规划分镜 → 配置音轨 → 保存 → 生成视频。

**新实例不复制其他实例的凭据。** 单击“已配置”仅表示 Skill/MCP 配置已写入，不能代替 Codex 登录，也不能保证每个外部工具已启动。原生运行和复杂交互的完整诊断仍在 RunDesk 页面。

## 开发

需要 Go 1.25+：

```bash
go test -race ./...
./scripts/build.sh
./bin/video-app --version
```

没有 npm 前端构建。`runtime/package-lock.json` 固定渲染工具依赖，安装使用 `npm ci`。运行时通过 HyperFrames 官方环境开关关闭遥测。

- [音轨使用说明](docs/AUDIO.md)
- [架构与接口](docs/ARCHITECTURE.md)
- [验证记录与限制](docs/VALIDATION.md)
- [依赖说明](THIRD_PARTY.md)

MIT License。渲染器、字体和其他外部依赖遵循各自许可。
