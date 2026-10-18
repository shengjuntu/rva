# RunDesk Video App · v0.22.0

文字驱动视频制作。你提供正文与已有素材，点击“交给 RunDesk 制作”，由 RunDesk 驱动 Codex 调用 App 的 MCP 服务完成配音、选材、剪辑、渲染和检查。页面通过阶段卡片显示工作产物，修改要求继续在右侧对话提出。

正文是权威内容，素材服务文字；不为迁就图片改写事实，也不固定为一句一图或一段一镜。内容与制作简报保持自由，DSL只约束剪辑。

## 日常使用

1. 新建项目、粘贴/导入正文；可选填制作要求，上传图片、视频和音频或导入素材目录。
2. 点“交给 RunDesk 制作”。自动保存正文与要求，连接项目并更新当前制作工具，再提交原有 RunDesk 会话。首次使用仍需由管理员配置 RunDesk 地址/访问权限、Codex运行环境、TTS和字体；按钮不能替代账户认证与服务部署。
3. 查看“素材准备和对齐、剪辑、精修”三张阶段卡片；展开可查看正文、配音、素材关联、工程、成片和检查记录。卡片依据实际保存的任务和记录更新，空阶段不会假报完成。
4. 对话提出调整。单帧抽查与机器校验不等于听过声音或看过全片；确认完整成片后才记录终稿。

手动配音、DSL、渲染、撤销与诊断保留在默认折叠的“高级工具与诊断”。制作阶段记录不作为中途审批；一键发起已授权自动完成初稿。

news2douyin新导入包的审核正文直接作为独立口播输入；旧项目可显式选择“使用已有正文”或“使用新闻原稿”填入编辑框，保存前不迁移旧镜头。

## 服务与边界

| 能力 | 本版实现 |
| --- | --- |
| RunDesk/Codex | 现有专用会话、Skills/MCP配置、任务回执恢复与对话修改 |
| 配音 | 现有IndexTTS HTTP服务；eSpeak机械预览；实测合成片段时序，非逐字强制对齐 |
| 素材 | 上传/目录导入、来源记录、图片查看、视频最多8个真实时间点抽帧、描述与标签和生成来源 |
| 文本素材对齐 | 精确原文引用、自由画面意图、素材与视频选段、选用理由、未覆盖原文和过期检测；词项检索仅辅助候选 |
| 图片/视频生成 | 自定义http_binary_v1服务适配，支持目标时长/画幅/真实参考图；需适配器支持，非任意厂商原生API |
| 剪辑与渲染 | 原生多轨DSL/FFmpeg；可选video_use共享目录EDL制作与私有成片归档；两种工程分别管理 |
| 结果检查 | 实际成片指定时间单帧PNG、独立检查记录、终稿字节复核 |
| 运行诊断 | 当前环境、认证配置、MCP工具与真实PNG传输；video_use额外检查适配器版本和共享目录双向读写 |
| 按文字修改 | 可选剪辑textRefs绑定精确原文；查询当前或已归档成片的稳定镜头ID与剪辑时间 |

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

镜像默认rundesk-video-app:0.22.0。RunDesk的Codex工具运行环境和App渲染服务是各自的部署组件；首次环境与账户仍需配置。未在本次环境进行Docker或真实模型验收。

## 本版变化与验证

- 三阶段与一键制作保持不变。运行诊断和按原文定位放在“高级工具与诊断”，日常调整继续通过对话发起。
- `video_runtime_diagnose`检查当前绑定、运行环境、认证配置及MCP工具；在已运行的Codex Docker容器内，通过当前MCP配置接收真实PNG并核对随机挑战。使用video_use时还验证共享目录双向读写、video-build1.3.0、适配器协议2和固定video-use源码版本。
- 诊断不启动模型、不发起生成、不重建环境或切换镜像。`modelVisionVerified:false`明确表示没有验证模型理解图像；PNG传输通过不等于Codex实际看图正确。native不要求video_use和共享目录，本机模式未执行的容器检查明确列为未检查。
- 原生DSL可选`textSourceHash`，两种后端的镜头均可选`textRefs:[{quote,occurrence?}]`，支持自由的多对多文字关联。`video_edit_locate`返回稳定镜头ID、剪辑时间、原文版本及未映射镜头；历史成片查询读取私有归档并校验指纹，过时结果不冒充当前工程。
- 未引用背景和旧DSL/EDL继续有效，不自动猜造文字关联；正文变化后重新核对已有引用。DSL/EDL继续只约束剪辑，不限制内容表达。
- 新增3项工具，共48项MCP。使用video_use新引用功能需升级RunDesk0.49.3，重建并选择video-build1.3.0镜像。仅替换源码包或镜像标签不会更新已经运行的容器。

实现与验证边界见[诊断与文字引用](docs/DIAGNOSTICS-TEXTREFS-0.22.md)。三阶段历史设计见[v0.21记录](docs/THREE-STAGE-0.21.md)。真实Codex自主制作、视觉理解、生成模型、IndexTTS音质、图形浏览器、Windows运行和Docker部署仍需在实际环境验收。

可选video-use部署见[共享目录与归档](docs/EXCHANGE-0.21.md)。App与RunDesk容器共享同一个应用工作目录；App私人项目数据单独保存。使用`docker/compose.video-use.yaml`叠加挂载，`VIDEO_EXCHANGE_HOST_WORKSPACE`设为已经存在的RunDesk视频应用工作区绝对路径，冻结文件使用私有权限，两边须映射为同一个宿主UID；常规Docker令App的VIDEO_UID/VIDEO_GID与RunDesk服务一致。

## 文档

- [v0.22诊断与文字引用](docs/DIAGNOSTICS-TEXTREFS-0.22.md)、[升级](docs/UPGRADING.md)
- [v0.21三阶段设计与验收](docs/THREE-STAGE-0.21.md)
- [制作Skill](integrations/video-project/SKILL.md)、[指令设计](integrations/video-project/WORKFLOW-DESIGN.md)
- [IndexTTS](docs/INDEXTTS.md)、[新闻交接](docs/NEWS-HANDOFF.md)
- [剪辑DSL](docs/EDIT-DSL.md)、[Schema](schemas/edit-dsl-v1.json)、[生成适配协议](docs/GENERATION-0.15.md)
- [局部修改/重做](docs/EDIT-PATCH-0.17.md)、[预览和终稿](docs/RELEASE-0.18.md)、[缓存](docs/CACHE-0.19.md)

旧版本文档保留对应版本的技术记录，不代表本版或所有模型已验收。
