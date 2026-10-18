# RunDesk Video App · v0.23.0

提供正文和素材，点击“交给 RunDesk 制作”，让 Codex 自主完成分镜、选材、图像与视频生成、TTS、动画和成片检查。Video App 提供制作 MCP 工具、素材与作品版本、多轨审阅；RunDesk 负责 Agent 运行、Skills/MCP 配置和轨迹调试。主要动画与合成后端为 HyperFrames。

正文是内容依据，画面服务文字。一个意思可以使用多个镜头，一个画面也可以覆盖多句；没有段落镜头模板。剪辑文档只记录时间、素材引用、字幕和声音安排，动画内部允许 Codex 编写 HTML、CSS、JavaScript 和 GSAP。

## 日常使用

1. 新建项目，粘贴或导入主播正文和制作要求，添加图片、视频、音频或素材目录。
2. 点“交给 RunDesk 制作”。保存输入、连接已有专用会话并提交制作任务。Codex 读取可用工具后自行安排工作，不在普通选材、分镜、配音和渲染之间反复等待确认。
3. 查看“素材准备和对齐、剪辑、精修”阶段产物。阶段是可检查的记录，允许反复补充，不限制 Agent 必须按固定顺序执行。
4. 在多轨审阅中联动查看文稿、画面、配音、字幕和音乐，通过对话提出局部修改；完整播放和人审后再记录终稿。

首次运行仍需配置 RunDesk 地址/权限、Codex 认证、渲染依赖和需要的生成服务。日常一键制作不会自动安装模型、改变其他应用配置或发布视频。复杂工具参数、模型交互和异常定位在 RunDesk 调试。

## 制作工具

| 能力 | 实现与边界 |
| --- | --- |
| 自主制作 | Codex 决定分镜、取材、生成和动画；App 不内置第二套 Agent 循环 |
| 配音 | 已配置的 IndexTTS HTTP 服务；eSpeak 机械预览；真实音频及合成片段时序，不冒充逐字强制对齐 |
| 素材观察 | 素材元数据、图片查看、视频多时间点真实抽帧、描述与来源记录 |
| 文字素材关联 | 精确原文引用、多对多素材关系、视频选段、选择理由和未覆盖原文；候选检索为词项基线 |
| 图像/视频生成 | 自定义 http_binary_v1 适配器；支持的目标时长、画幅、参考图按已配置服务使用，不是任意厂商 API 直连 |
| HyperFrames | 保存原创 HTML/CSS/JavaScript 动画工程及时间线，校验后由统一渲染任务保存独立快照和实际视频 |
| 多轨审阅 | 根据同一工程/输出的时间线显示画面、配音、字幕和音乐；选择与试听是审阅，不等于直接编辑工程 |
| 版本与检查 | 修订冲突检测、真实渲染抽帧、检查记录、历史输出和终稿字节复核 |
| 旧工程 | native DSL/FFmpeg、video_use EDL 和旧镜头工程兼容保留，选择后只使用一种工程作为渲染依据 |

新制作优先 HyperFrames。旧项目不静默转换；切换后端不会删除另一种工程。需要简单、无浏览器的合成时可以继续使用 native DSL；已有 video_use 工程继续沿用原共享目录与归档流程。video_use 不再是主制作路径。

## 启动与部署

```bash
bin/video-app --data ./data --font /absolute/path/to/NotoSansCJK-Regular.ttc
```

默认 http://127.0.0.1:3220；令牌在 data/access-token，也可设置不少于 24 字符的 VIDEO_APP_TOKEN。已有 RunDesk 身份、会话、数据和服务设置升级时完整保留。源码构建：

```bash
bash scripts/build.sh
```

Go 主程序使用标准库及嵌入网页。HyperFrames 渲染另外需要 Node.js、Chromium、HyperFrames 0.8.87、GSAP 3.15.0、FFmpeg/FFprobe 和字体；IndexTTS 模型独立部署，图像/视频生成服务按需配置。native DSL 渲染无需 Node/Chromium。

Docker 部署见 [DOCKER](docs/DOCKER.md)。在旧 docker/.env 显式更新 VIDEO_IMAGE=rundesk-video-app:0.23.0 后构建并启动：

```bash
bash scripts/docker.sh init
bash scripts/docker.sh build
bash scripts/docker.sh up
bash scripts/docker.sh token
bash scripts/docker.sh doctor
```

App 渲染容器与 RunDesk Codex 工具容器分别更新。配套 RunDesk 0.49.5 的 video-build 1.4.0 提供 HyperFrames/GSAP 和独立离线渲染检查，默认不安装 video-use。HyperFrames 主流程通过 App MCP 保存和渲染工程，不要求 App 与 Codex 共享私人项目数据或生产目录。旧 video_use 继续使用原有专用共享工作目录，并显式选择带 video-use 的工具镜像。

## 本版文档与验证

- [自主导演与 HyperFrames](docs/HYPERFRAMES-0.23.md)、[升级](docs/UPGRADING.md)
- [本版验证记录](docs/validation-0.23.json)，含真实短动画、音频与终稿版本检查
- [制作 Skill](integrations/video-project/SKILL.md)、[指令设计](integrations/video-project/WORKFLOW-DESIGN.md)
- [架构](docs/ARCHITECTURE.md)、[阶段计划](docs/ROADMAP.md)
- [IndexTTS](docs/INDEXTTS.md)、[生成适配](docs/GENERATION-0.15.md)、[新闻交接](docs/NEWS-HANDOFF.md)
- [旧剪辑 DSL](docs/EDIT-DSL.md)、[旧共享目录](docs/EXCHANGE-0.21.md)、[诊断与文字引用](docs/DIAGNOSTICS-TEXTREFS-0.22.md)

设计同时参考 [OpenMontage](https://github.com/calesthio/OpenMontage) 的阶段产物、素材组织和检查思路，以及 [video-use](https://github.com/browser-use/video-use) 的文字驱动剪辑、定位和预览自检方法。HyperFrames 主路径的自有代码、Skill 和数据契约独立编写，没有引入 OpenMontage 的代码、Schema 或 Skill；既有可选 video_use 适配保留，使用上游资源时继续遵循原 MIT 许可及依赖各自条款。设计参考不等于直接引入实现，详见 [THIRD_PARTY](THIRD_PARTY.md)。

自动测试和本地渲染只能验证对应接口与环境。真实 Codex 的自主创作、模型视觉、远端生成质量、IndexTTS 音质及部署环境仍需实测；工具调用成功和 PNG 传输不代表已经理解画面，抽帧不等于看过全片或听过声音。Windows 二进制仅交叉编译，未实机验收。旧版本文档保留历史边界，不代表所有后端能力一致。
