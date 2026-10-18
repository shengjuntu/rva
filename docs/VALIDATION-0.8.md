# v0.8.0 验证记录

验证日期：2026-10-13。基于已交付 v0.7.0 源码包开发，未修改运行中的用户数据或远端 IndexTTS。

## 已执行

| 检查 | 结果 |
| --- | --- |
| Go `go test -buildvcs=false -race -json ./...` | 42 个顶层测试通过；含子用例共 88 个通过，0 失败；1 个真实 eSpeak 合成测试因未安装引擎跳过 |
| IndexTTS HTTP 合成至采用 | HTTP 替身返回 22.05 kHz WAV，实际 FFmpeg/FFprobe 转码为 24 kHz PCM，校验 SRT/镜头时长/采用/改稿后拒绝旧结果 |
| 配音接口 | 配置保存/重载、Key 脱敏、换地址禁止复用旧 Key、环境变量优先、MCP 语言/音色/语速转发、旧版默认参数回执兼容 |
| 失败路径 | 401/422/503、JSON/损坏/超大响应、静音、重定向拒绝、取消、未完成产物不可下载、重启后幂等 |
| 原生 Python HTTP 适配器 | 4 个测试通过：鉴权/WAV/速度反向映射、输入校验、单 GPU 工作槽、参考音色路径；模型推理使用测试替身 |
| JavaScript | WebUI 与 Docker doctor 均通过 Node 语法检查；配音 DOM ID 与 HTML 一致 |
| 配置与脚本 | Compose YAML、Skill frontmatter 解析；Shell 语法检查；Python 启动帮助执行 |
| 构建 | Linux amd64、Windows amd64 静态二进制构建成功；Linux `--version` 返回 0.8.0 |

原始日志位于 `validation-0.8/go-tests.jsonl`、`validation-0.8/python-tests.log`。

## 本轮未验证

- 没有连接用户的 IndexTTS-2.5 服务，没有运行真实模型/GPU，没有试听音色、测量吞吐/显存或验证特定第三方部署协议。
- Python 适配器按官方 `IndexTTS2` Python API 编写，但未在 GPU 环境实际初始化上游模型。用户已经部署的 vLLM-Omni 可直接接入，不需要此适配器。
- 未实际构建或启动新版 Docker 镜像；未在 Windows 真机运行。
- 未进行浏览器交互/视觉验收，也未运行新的 HyperFrames 完整视频渲染。
- 本版字幕为实测合成段落时间，没有接入 Qwen3 Forced Aligner 或 ASR，不能宣称逐字对齐或发音自动校验。

首次部署验收见 [INDEXTTS](INDEXTTS.md)。
