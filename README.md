# RunDesk Video App · v0.20.0

文字驱动视频制作。你提供正文与已有素材，点击“交给 RunDesk 制作”，由 RunDesk 驱动 Codex 调用 App 的 MCP 服务完成配音、选材、剪辑、渲染和检查。页面通过阶段卡片显示工作产物，修改要求继续在右侧对话提出。

正文是权威内容，素材服务文字；不为迁就图片改写事实，也不固定为一句一图或一段一镜。内容与制作简报保持自由，DSL只约束剪辑。

## 日常使用

1. 新建项目、粘贴/导入正文；可选填制作要求，上传图片、视频和音频或导入素材目录。
2. 点“交给 RunDesk 制作”。自动保存正文与要求，连接项目并更新当前制作工具，再提交原有 RunDesk 会话。首次使用仍需由管理员配置 RunDesk 地址/访问权限、Codex运行环境、TTS和字体；按钮不能替代账户认证与服务部署。
3. 查看制作简报、配音与字幕、文字与画面安排、剪辑方案、初稿、检查报告等卡片。卡片依据实际保存的任务和记录更新，空阶段不会假报完成。
4. 对话提出调整。单帧抽查与机器校验不等于听过声音或看过全片；确认完整成片后才记录终稿。

手动配音、DSL、渲染、撤销与诊断保留在默认折叠的“高级工具与诊断”。制作阶段记录不作为中途审批；一键发起已授权自动完成初稿。

news2douyin新导入包的审核正文直接作为独立口播输入；旧项目可显式选择“使用已有正文”或“使用新闻原稿”填入编辑框，保存前不迁移旧镜头。

## 服务与边界

| 能力 | 本版实现 |
| --- | --- |
| RunDesk/Codex | 现有专用会话、Skills/MCP配置、任务回执恢复与对话修改 |
| 配音 | 现有IndexTTS HTTP服务；eSpeak机械预览；实测合成片段时序，非逐字强制对齐 |
| 素材 | 上传/目录导入、来源记录、图片/视频首帧检查、描述与标签 |
| 文本素材对齐 | 描述词项匹配基线；Codex决定选用理由与缺口，不称作视觉语义模型 |
| 图片/视频生成 | 自定义http_binary_v1服务适配，需管理员配置；非任意厂商原生API |
| 剪辑与渲染 | 多轨DSL、版本校验、局部修改与撤销/重做、FFmpeg、时间段预览、完整结果缓存 |
| 结果检查 | 实际成片指定时间单帧PNG、独立检查记录、终稿字节复核 |

App主程序为Go标准库与嵌入HTML/JS，无需Python或GPU。DSL渲染只需要FFmpeg/FFprobe，文字需要支持目标语言的字体。旧HyperFrames镜头模式仍需Node.js、Chromium及其原依赖。已有IndexTTS模型在独立服务中运行。

## 最小本机启动

Linux/Windows amd64程序位于bin；Windows仅交叉编译，未实机验收。

```bash
bin/video-app --data ./data --font /absolute/path/to/NotoSansCJK-Regular.ttc
```

默认 http://127.0.0.1:3220。令牌在data/access-token；也可设置不少于24字符的VIDEO_APP_TOKEN。RunDesk URL/token及TTS服务请在环境准备/高级工具配置；数据和Token升级时完整保留。现有RunDesk application connect、运行环境和MCP地址沿用原接口。

源码构建需Go 1.25.12或兼容版本：

```bash
bash scripts/build.sh
```

## Docker

部署详见[Docker与联调](docs/DOCKER.md)。保留原数据目录和配置，修改旧docker/.env中的VIDEO_IMAGE为新版标签，然后构建/启动：

```bash
bash scripts/docker.sh init
# 编辑 docker/.env
bash scripts/docker.sh build
bash scripts/docker.sh up
bash scripts/docker.sh token
bash scripts/docker.sh doctor
```

镜像默认rundesk-video-app:0.20.0。RunDesk的Codex工具运行环境和App渲染服务是各自的部署组件；首次环境与账户仍需配置。未在本次环境进行Docker或真实模型验收。

## 本版变化与验证

- 主界面从底层控制面板收拢为文字、素材、一键制作和阶段产物。
- 按版本安装并显式选择四份新Skill，保留已有定制文件；规程明确正文→简报→配音实测时序→画面覆盖表→DSL→完整渲染→真实画面抽查。
- 新增production-board、checkpoint、render-inspect，共39项MCP；阶段记录绑定精确正文hash与相关输入。新渲染的检查报告绑定具体renderId，不沿用旧报告。
- Go race测试、真实FFmpeg与模拟IndexTTS的工具级完整流程、实际PNG抽帧、Node回执测试、JSDOM+真实HTTP主流程已验证。真实Codex编排、IndexTTS模型音质、图形浏览器、Windows和Docker仍需部署验收。

## 文档

- [v0.20设计与验收](docs/TEXT-FIRST-0.20.md)、[升级](docs/UPGRADING.md)
- [制作Skill](integrations/video-project/SKILL.md)、[指令设计](integrations/video-project/WORKFLOW-DESIGN.md)
- [IndexTTS](docs/INDEXTTS.md)、[新闻交接](docs/NEWS-HANDOFF.md)
- [剪辑DSL](docs/EDIT-DSL.md)、[Schema](schemas/edit-dsl-v1.json)、[生成适配协议](docs/GENERATION-0.15.md)
- [局部修改/重做](docs/EDIT-PATCH-0.17.md)、[预览和终稿](docs/RELEASE-0.18.md)、[缓存](docs/CACHE-0.19.md)

旧版本文档保留对应版本的技术记录，不代表本版或所有模型已验收。
