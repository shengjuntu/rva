# 升级到 0.28.0

保留原 `data`、项目、素材、会话、访问令牌和服务配置。源码重新构建，或将既有 `docker/.env` 中显式设置的 `VIDEO_IMAGE` 更新为 `rundesk-video-app:0.28.0` 后重建容器并刷新浏览器。新 UI 将制作与精修分为两个独立视图；原有输出在精修页保留，制作页只有明确关联当前制作请求的完整新片才作为本次结果展示。

顶栏项目视图居中，应用设置和退出登录收于系统菜单；设置页可保存当前浏览器访问 RunDesk 的地址。该地址只用于浏览器跳转，不改变服务端与 RunDesk 的连接。远程浏览器遇到服务端回环地址时不生成误导性链接。

项目增加最近打开时间、归档及设置页管理；删除会先移入服务器 `data/project-trash` 并记录请求墓碑，以免旧创建回执在删除后意外恢复。制作状态读取 RunDesk 当前运行结果；暂时无法读取时显示未知状态，不推断进度或结果。后台 MCP 增加可选的 `productionRequestId` 用于关联产物，旧调用仍可用，但未标记的产物只在历史中查看。旧项目无需迁移或重新创建；建议给制作环境更新 Video App MCP 工具说明以便 Codex 使用新参数。RunDesk 核心接口与自研 video-build 均无需更改。

详见 [UI 需求](UI-REQUIREMENTS.md)及 [验证记录](validation-0.28.0.md)。

---
以下保留历史版本说明。

# 升级到 0.27.0

保留原 data、项目、令牌、RunDesk 地址/凭据、模型服务配置和运行参数。源码构建或在旧 docker/.env 显式更新 `VIDEO_IMAGE=rundesk-video-app:0.27.0` 后 build/up，刷新浏览器。无需更改 RunDesk 核心接口或自研 video-build；当前目录能力可与 RunDesk 0.52.0 配套使用。

新建项目增加可搜索制作环境列表；第一次无项目时也先选择再创建。已有项目沿用原 productionProfile/runtimeBinding 和会话，不因默认配置、环境列表顺序或刷新而切换。原请求接收不确定时保留完整原请求，先核对或按原请求重试。停用、不可访问或默认配置失效会明确显示，不自动选择其他环境。

API/MCP 创建项目的原有字段与省略语义保持兼容。旧 RunDesk 没有多环境列表能力时，原默认环境在明确提示下继续使用；非空预设仍要求相应 capability。详情与实际验证范围见[多制作环境](PRODUCTION-PROFILES.md)和[本轮验证](validation-0.27.0.json)。

---
以下保留历史版本说明。

# 升级到 0.26.0

保留原 data、项目、认证、模型服务配置与运行参数。独立上游容器需要任意文本 TTS 时升级到本版；原任务接口向后兼容。更新 App 镜像后，只有明确使用 source=text 的调用才生成独立配音，不迁移或改写现有工程。新增运行配置详见 RunDesk 0.51.0 的独立上游视频文档；现有自研 video-build 不变。

# 升级到 v0.25.0

备份并保留完整data、令牌、RunDesk绑定、会话、原稿、素材与历史输出。旧docker/.env中显式更新VIDEO_IMAGE=rundesk-video-app:0.25.0，再build/up；不要用示例覆盖实际服务配置。IndexTTS和ComfyUI继续使用已有服务，不重装模型。

未设置VIDEO_PRODUCTION_PROFILE时继续使用原默认RunDesk环境，兼容旧服务。需要多个制作环境时先升级RunDesk到0.50.0，由管理员在应用中登记独立实例/工作区的预设并完成镜像、认证和Skills/MCP，再设置App启动默认或创建项目时传productionProfile。旧凭据不自动获得新预设权限，需签发包含presetIds的应用凭据。

创建后的项目保存所选预设，首次连接后保存精确绑定。改变启动默认不会迁移旧项目或未完成提交；已有会话按原身份继续。旧项目没有新字段时，从原会话/默认绑定恢复归属，不以新的启动默认替换。预设不受支持、禁用或绑定不一致时明确报错，不自动回退。

App日常界面保持简单；过程调试和预设管理在RunDesk。上游制作镜像需实际准备，本次没有自动引入OpenMontage源码、审批流程或新的渲染后端。详见 [多制作环境](PRODUCTION-PROFILES.md)，本轮验证见validation-0.25.0.json。

---
以下为历史说明。

# 升级到 v0.24.0

本版以 shengjuntu/rva 的 `065e4c7` 为基线，合入 v0.23.1 的 HyperFrames 与简化工作台，并加固新增 ComfyUI MiniMax-H3 文生视频后端。IndexTTS 源码和已有配置保留，不要求重装模型或重新登记音色。

保留并备份完整 data、访问令牌、RunDesk 身份/会话与服务设置。等待活动任务结束后替换程序，或在旧 docker/.env 中显式设置 VIDEO_IMAGE=rundesk-video-app:0.24.0，再 build/up。脚本不覆盖已有 .env；不要把示例文件覆盖到现有配置。

需要使用内置 H3 时，保持 VIDEO_VIDEO_GEN_URL=comfy://主机:端口、VIDEO_VIDEO_GEN_MODEL=minimax_h3_fl2va。已安装的 ComfyUI 节点和模型文件名保持原实现。VIDEO_COMFY_H3_AUDIO_GAIN_DB 默认18，设0可保留原始声音；此项只影响新的生成任务，不重新处理旧素材。详见 [ComfyUI H3](COMFYUI-H3.md)。通用 http_binary_v1 和 IndexTTS 服务继续可用。

已有 H3 参考图请求会明确拒绝，不假装实现图生视频；任务中的目标时长和生效帧数分别记录，实际时长以媒体探测为准。取消或 App 重启不能保证远端生成停止，不要用新 requestId 自动重试不确定结果。

UI 继续只保留项目、导入、一键初稿、成片、对话和导出，详细调试在 RunDesk。无需更新 RunDesk 或重新创建项目；已配置的 RunDesk 0.49.5/video-build 1.4.0 继续沿用。真实 IndexTTS/H3 模型和用户 Docker 运行环境仍需部署侧验收。

---
以下为历史说明。

# 升级到 v0.23.1

保留并备份完整 data、原稿、素材、工程与历史输出、RunDesk 身份/会话/工作区、访问令牌及已有服务设置。等待活动任务结束后替换 App 或重建 App 镜像；旧 docker/.env 显式更新 VIDEO_IMAGE=rundesk-video-app:0.23.1。不清空数据、不重建同名工作区，也不通过默认值覆盖已配置的 IndexTTS 或生成服务。

本版日常界面只保留项目列表、正文/本地目录或文件导入、一键初稿、实际成片/历史版本、对话修改和导出。多轨时间线、原文或片段的可视引用选择、HyperFrames 源码检查和详细阶段控制台留待后续；过程仅简要状态，详细调试在 RunDesk。手动 TTS、渲染、DSL 和对齐界面移除，后台 checkpoint、locate、review-timeline 等 MCP 和旧工程读写/渲染能力继续保留；页面简化不意味着必须迁移或重做既有项目。

独立设置页保留必要的 IndexTTS/配音配置，可通过已有写 API 保存；其他环境信息展示实际状态，渲染/生成服务沿用部署配置，模型、Skills/MCP、运行环境与详细调试由 RunDesk 管理。本版不声称页面可以写入所有服务配置，也不会为进入设置而重置账户、项目或工作区。

RunDesk 保持 0.49.5、video-build 1.4.0。已经按 v0.23.0 部署好的 HyperFrames 0.8.87、GSAP 3.15.0、字体和生成服务继续沿用，不因页面改变而要求重复安装。已有 native/video_use/旧镜头工程和历史成片保留原后端；可选 video_use 仍使用其原依赖与专用交换目录。

“素材准备和对齐、剪辑、精修”是产物分类，不是固定步骤或逐项审批。Codex 可以检查实际输出、局部修改和重做；Skill 中通常三轮建议不是程序硬上限。系统不因任务成功就认定新闻语义正确、看过全片或完成听审；终稿仍需明确人审具体完整输出。

升级后先用已有项目核对正文、素材、设置和历史结果，再做一次制作与“第二节重做”等对话调整。具体本版测试以实际发布记录为准，不把 v0.23.0 的渲染日志当作本版界面验收。Windows 仍仅交叉编译，降级恢复完整升级前备份。

---
以下为历史说明。

# 升级到 v0.23.0

保留并备份完整 data、访问令牌、RunDesk 身份/会话、TTS 与生成服务配置。等待活动任务结束后替换 App 或重建镜像；旧 docker/.env 显式改 VIDEO_IMAGE=rundesk-video-app:0.23.0。刷新项目工具到本版版本化 Skill，保留旧版及用户自定义 Skill。

新项目默认 HyperFrames 自由动画，旧项目保留原 native/video_use/旧镜头后端，不静默转换。需要将旧项目用于新路径时，在高级制作设置显式选择 hyperframes，由 Codex 读取旧稿/素材，生成原创工程并保存；原工程和历史成片继续保留。后端选择不会把旧 DSL 自动翻译成 HTML，也不会自动安装依赖。

HyperFrames 使用固定 0.8.87 与 GSAP 3.15.0，需要 Node.js、Chromium、FFmpeg/FFprobe 和字体。已有 App Docker 渲染镜像包含这组依赖；本机先使用 scripts/setup-renderer.sh 准备依赖。App 检查/渲染时不安装包。新的工程通过 MCP 提交源文件与素材 ID，再由 App 冻结渲染，无需为此增加共享目录或安装 video-use。

配套 RunDesk 升到 0.49.5 后重新准备 video-build 1.4.0，并在运行环境中明确选择新镜像、按已有流程重建容器，保留 HOME、认证和工作区。仅替换源码 ZIP、镜像标签或更新 App 不会替换运行中的容器。工具镜像默认包含 HyperFrames/GSAP，video-use 改为显式按需安装；既有 video_use 项目应沿用已安装它的镜像或用 --with-video-use 构建配方，同时保留旧专用共享生产目录和权限。

多轨审阅根据同一工程或冻结输出展示画面、配音、字幕和音乐。HyperFrames timeline 是作者声明的编辑元数据，不能独立验证 DOM、字幕或音频；查看实际输出仍需抽帧与播放。首版时间线只读，精确修改继续通过对话交给 Codex；不会把浏览器试听静音当成工程混音设置。

先用短稿和已有图片验证保存工程、校验、完整渲染、查看版本和局部修改，再用真实材料验收自主制作质量。check 通过不是成片，PNG 传输不是模型视觉，终稿仍需要明确的人审。Windows 仍仅交叉编译；降级应恢复完整升级前备份。

[本版接口与边界](HYPERFRAMES-0.23.md) · [原创制作 Skill](../integrations/video-project/SKILL.md)

---
以下为历史说明。

# 升级到 v0.22.0

备份并保留完整data、RunDesk/TTS配置、访问令牌、账户与原会话，等待活动任务结束后停止旧App，替换程序或重建App镜像。旧docker/.env中的VIDEO_IMAGE需显式改为rundesk-video-app:0.22.0。新版补装并显式选用v0.22.0版本化Skills，共48项MCP；保留旧版本及自定义文件。

默认仍是native后端，内容无需迁移。原生DSL和video_use EDL的textRefs是可选关联，无引用的旧工程、背景片段继续有效；不会根据镜头或素材描述猜造引用。对新引用使用精确正文及重复出现次序；原生DSL同时绑定textSourceHash，EDL绑定生产快照manifest的原文与sourceHash。改稿后要重新验证，不能仅替换哈希冒充已核对。

使用video_use新功能时，升级RunDesk至0.49.3，重建video-build1.3.0（adapter协议2），在RunDesk明确选择新工具镜像，再按既有流程更新/重建应用执行容器；保留HOME、Codex认证与工作区。App渲染镜像与RunDesk工具镜像分别更新。仅替换ZIP、修改标签或更新App不会自动替换运行中的Docker容器。

沿用原共享应用工作目录和VIDEO_EXCHANGE_ROOT；两个容器访问同一物理目录，并保留匹配的运行用户权限。连接项目并更新工具后，在高级工具运行“制作环境诊断”。检查实际profile、适配器、MCP PNG传输与共享目录双向读写，按具体行的remedy处理；诊断自身不启动/重建环境或切换镜像。同次制作且环境配置未变可复用成功结果，新一轮制作或变化后再诊断。

诊断不会调用模型或生成服务，modelVisionVerified始终为false；通过图片传输只能说明容器收到正确PNG，真实Codex看图理解与自主制作仍须实际联调。native无需video_use与共享目录；本机模式未执行的Docker探测显示未检查，不用宿主机替代验证。

按文字修改时使用video_edit_locate；无匹配和unmappedClipIds表示尚无关联。指定renderId可查具有完整校验记录的历史成片，服务读取私有归档并返回current状态；current=false的旧时间不直接应用到当前工程。旧无引用成片仍可查看，但不会凭空获得镜头关联。

[本版实现与边界](DIAGNOSTICS-TEXTREFS-0.22.md) · [共享目录部署](EXCHANGE-0.21.md)

---
以下为历史说明。

# 升级到 v0.21.0

保留完整data、RunDesk/TTS配置、访问令牌与原会话，停止旧App后替换程序或重建镜像。Docker旧.env显式更新VIDEO_IMAGE=rundesk-video-app:0.21.0。项目会补装并选用v0.21.0版本化Skills，共45项MCP；保留已有定制文件。

默认仍使用原生剪辑后端。三阶段界面自动读取现有产物；旧自由素材记录不会被猜测转换为新关联表，可由Codex查看素材后补齐。配音时序仍是实测合成片段时序，不冒充逐字强制对齐。

使用video_use时：升级配套RunDesk0.49.2，重建并在运行环境中选择video-build1.2.0镜像，按现有流程重建/更新Codex容器（保留账户与工作区）。将同一个RunDesk应用工作目录挂入App并设置VIDEO_EXCHANGE_ROOT；Docker可叠加compose.video-use.yaml。随后在高级制作设置选video_use。只替换ZIP不会改变已经运行的Docker镜像或自动添加挂载。

video_use后端首版使用单视觉轨图片/视频、连续外部配音和字幕。复杂多轨、音乐及原生DSL特效继续选native；后端选择不会删除已有工程。每次精修导出新快照，归档结果独立保存，源素材与旧成片可复用。

生成服务沿用现有自定义HTTP二进制适配协议；新增duration/aspectRatio/referenceImages是可选扩展，供应商适配器需明确支持。参考图为项目内真实PNG/JPEG，最多4张。配置成功不等于真实模型验收。

[实现与验收](THREE-STAGE-0.21.md) · [共享目录](EXCHANGE-0.21.md)

---
以下为历史说明。

# 升级到 v0.20.0

保留完整data、TTS/RunDesk配置与Token，停止旧App后替换完整二进制/源码包，或重建Docker镜像。旧docker/.env已有VIDEO_IMAGE时显式改为rundesk-video-app:0.20.0，默认示例更新不会覆盖你的配置。

新版默认界面是正文/素材/一键RunDesk制作和产物卡；手动工具在高级工作区。一键制作会在会话空闲时更新项目工具到v0.20.0（39项MCP）并继续原会话。本版以video-project-v0-20-0等版本化名称补装并显式选择新规程，避免RunDesk只补缺失项而继续使用旧Skill。原有无版本Skill和用户定制文件保留；若管理员主动修改/禁用了同版本Skill，以其实际配置为准。

旧项目不会按镜头拼接或自动改写正文。空的独立正文可显式选择采用已有正文/新闻原稿，随后一键保存。旧渲染仍可查看；新检查记录需要带正文指纹的新渲染。ready是已有产物，review是Agent检查记录，不是人工终稿确认。

参见[本版说明](TEXT-FIRST-0.20.md)。下面为历史版本升级记录。

# 升级到 v0.11.0

保留完整data，停服务后替换二进制或重建Docker。已连接项目更新工具至v0.11.0（共25个MCP工具）。旧项目仍默认旧镜头模式，不自动迁移正文或音轨。

上传素材后在“剪辑时间线”创建示例或让Codex编写，校验、保存并启用。启用后渲染只读剪辑DSL，旧正文/音轨/字幕不自动混入；点击“使用旧镜头”可切回，DSL继续保留。

剪辑渲染只需FFmpeg/FFprobe；文字另需字体。可直接bin/video-app --data ./data --font <字体绝对路径>。旧模式继续依赖HyperFrames、GSAP、Node与浏览器。Docker镜像标签为0.11.0，保留原数据挂载与Token。

详见EDIT-DSL.md与VALIDATION-0.11.md。Windows仅交叉编译。降级请恢复完整备份。

---
以下为历史说明。

# 升级到 v0.10.0

1. 停止App并备份完整data，替换二进制或重建Docker镜像；保留项目、素材、Token、RunDesk身份和TTS配置。
2. Docker镜像使用rundesk-video-app:0.10.0，保持原宿主数据挂载。
3. 刷新页面，在已连接项目中更新工具至v0.10.0。HTTP MCP地址不变，新增8个工具，共21个；RunDesk容器准备会检查完整工具集。
4. 打开“制作能力与任务”。默认只做本地能力检查；点击“检查已配置服务”会查询IndexTTS模型列表，不合成音频。
5. 旧项目继续使用，旧任务也能通过统一jobId读取。服务重启后未完成任务保持interrupted，不自动重试或重生成。
6. 浏览器不确定渲染请求暂存于sessionStorage；同一请求可通过再次点击生成确认原回执。主动关闭标签页可能丢失尚未抵达服务端的请求。
7. 剪辑DSL尚未实现，v0.9的正文分段只是兼容模式。第二阶段扩展剪辑模型，内容创作保持自由。

详细接口见PRODUCTION-SERVICE.md。降级须恢复完整备份。

---
以下为历史说明。

# 升级到 v0.9.0

1. 停止App，备份完整data，替换程序或重建Docker镜像；现有RunDesk、素材、配音设置和项目身份保留。
2. Docker的VIDEO_IMAGE使用rundesk-video-app:0.9.0；原宿主数据目录继续挂载。
3. 刷新浏览器，在旧项目点击“升级项目工具至v0.9.0”，更新实例Skill/MCP。服务接口地址不变，新增3个工具，共13个。
4. 旧分镜项目可继续使用；点击“保存正文与对齐表”后转入文本驱动模式。段落顺序及原文由正文入口统一管理。
5. 初稿可预览尚未人工核对但已正确绑定配音的字幕；终稿须核对字幕且对应当前内容已完成视频。旧版视频仍可播放，但无本版内容哈希，须重新渲染后才能确认为终稿。
6. 本版Windows二进制仅交叉编译；真实IndexTTS、Codex、Docker和完整HyperFrames成片须在你的部署环境验收。

详细流程见[TEXT-PRODUCTION](TEXT-PRODUCTION.md)。降级须恢复完整备份。

---
以下为历史说明。

# 升级到 v0.8.1

从 v0.8.0 升级无需重新部署 IndexTTS，也无需重填已保存的服务配置。本次主要增加音色列表、提交恢复与只读自检；可先运行 `bin/video-app speech-doctor --data ./data`，再用两句短稿试听。

1. 停止 Video App，备份整个 data（或 `/data` 挂载源）。保留 access-token、config.json、项目、RunDesk 回执和素材。
2. 替换本包程序/页面；Docker 用户保留原 `VIDEO_HOST_DATA` 等设置，将 `docker/.env` 的 `VIDEO_IMAGE` 改为 `rundesk-video-app:0.8.1`，使用新版 compose 执行 `bash scripts/docker.sh build`，再执行 `bash scripts/docker.sh up`。
3. 浏览器刷新，版本应为 0.8.1。“自动配音 → 配音服务设置”连接已有 IndexTTS，见 [INDEXTTS](INDEXTTS.md)。旧配置默认使用 eSpeak，不会自动切换音色或改写旧配音。
4. 新配置保存在 `data/speech-config.json`，其中可能包含 TTS API Key，备份时一并保护。配置在容器重建后保留；使用非空 VIDEO_TTS_* 环境变量时，页面显示由环境管理。
5. 若通过 Codex 生成配音，在当前 RunDesk 实例更新项目工具/业务 Skill（保持自定义设置；手动安装的容器 Skill 可按差异更新配音段落）。HTTP MCP 仍为同一个入口、10 个工具，只增加可选参数。
6. 创建两句测试稿 → 检查环境 → 生成 → 试听 → 采用 → 渲染。此版不包含或重装你的 IndexTTS 模型；RunDesk 0.48.0+ 与 news2douyin 的现有兼容版本可继续使用。

旧项目、eSpeak 任务、音轨和成片继续读取；新任务额外记录后端/模型/语言/语速。若降级，恢复升级前完整备份。

---
以下为历史升级说明。

# 升级到 v0.7.0

配套 RunDesk 0.48.0+，推荐 0.48.1。停止旧 App，备份完整 data，替换二进制或构建新镜像，沿用原数据目录、RunDesk URL、Token 与安装身份。Docker 配置和步骤见 [DOCKER](DOCKER.md)。

项目新增 previousSessionIds，config.json 新增 workspaceId；原请求回执、成片、素材和历史会话保留。新版采用系统文件锁，server.lock 文件存在不代表服务仍运行，**不要删除运行中实例的锁文件**。

首次更换为 Docker 后，在 RunDesk 的当前容器配置视频 MCP 和 Skill，再回 App 重新连接：空会话会更换为 Docker 会话；已有原生对话须点“新建当前环境对话”，不会搬迁或删除旧历史。与旧版本一致，默认配置不会覆盖管理员编辑。

news2douyin 制作包可新增 storyboard.json、evidence_snapshot.json 和 images/；单文件 4 MiB、整个脚本包 32 MiB、最多 4 张候选图片。保存或渲染前应确认素材适用性。旧平面脚本包继续可导入。

降级前恢复原版完整数据备份，避免旧版本忽略新字段。

---
以下为旧版升级历史。

# 从 v0.5.0 升级到 v0.5.1

1. 先将 RunDesk 升级到 0.6.0 或更新版本；本次联调版本为 0.6.1。等待活动 Agent 任务结束，停止 App，备份完整数据目录。
2. 替换程序，保留原 `VIDEO_DATA`、RunDesk 地址、实例与认证。`bin/video-app --version` 应为 0.5.1。Linux 与 Windows amd64 二进制均在 bin；Windows 仅完成交叉编译，未实机运行。
3. 在使用中的项目点击“升级项目工具至 v0.5.1”。保留工作区和会话，通用 Skill 写入实例；不删除旧项目 Skill，后续提交显式选择实例文件。实例有活动任务时暂缓更新。
4. “环境准备”可查看模型、Skill 与 MCP。配置写入成功不代表模型认证或工具调用已经成功；首次使用需在自己的实例中验证。
5. 旧会话不重写来源标签，新会话记录 `source.appId=rundesk-video-app`、`taskId=项目编号`。旧版运行只有事件记录证明属于该项目时才允许停止；无法确认时到 RunDesk 核对。

`data/rundesk-requests/` 保存提交前的请求编号、不可变请求体与回执。项目新增 `agentSubmission`、`agentRunId`，config.json 新增 RunDesk 地址与实例 Skill 路径。备份必须包含这些文件；有待确认请求时不要换数据目录、清理请求日志或切换 RunDesk 后端。浏览器使用同一标签页的 sessionStorage 保留尚未到达服务器的消息；服务器已收到的待确认消息保存在项目里。主动关闭标签页前，先确认未到达服务器的消息状态。

HTTP 失败、processing/unconfirmed 均不能当成未执行。重试沿用原编号与内容；只有原回执明确不存在才允许重新提交同一操作。已请求停止但仍待确认的任务不会因轮询而被重新启动。消息内容要修改时，先确认原提交，再发送新指令。降级须恢复升级前备份，以免旧版本丢弃新增字段。

渲染、配音、字幕依赖及制作能力与 0.5.0 相同。

# 从 v0.4.0 升级到 v0.5.0

1. 停止 APP，备份完整 data，替换程序和页面，保留原 VIDEO_DATA、RunDesk 登录与实例配置。
2. 确认 `bin/video-app --version` 为 0.5.0。旧音轨、字幕、原片、渲染和会话继续保留，无需重新生成。
3. 需要自动配音时另外安装 eSpeak NG 和 FFmpeg；默认通过 PATH 查找，或用 VIDEO_ESPEAK / --espeak 指定。未装 eSpeak 不影响旧有编辑与渲染功能。
4. 等 Agent 空闲，在使用中的项目点击“升级项目工具至 v0.5.0”，更新到 10 个 MCP 工具；保留既有会话和工作区。
5. 先生成一个短配音任务，试听后采用，再生成视频。生成任务不会自动覆盖音轨，采用会替换配音/字幕与镜头时长。

新任务在 projects/<id>/speech 下，项目 JSON 保存 speech 元数据。降级应恢复升级前备份；旧版本保存工程会丢弃任务字段。旧字幕的原有绑定规则继续兼容，再次核对或采用时使用更严格的正文/结构哈希。HyperFrames/GSAP 版本未变。

# 从 v0.3.0 升级到 v0.4.0

1. 停止 APP，备份完整 data；替换程序、源码与页面，保留原 VIDEO_DATA、RunDesk 实例与登录。
2. 确认 `bin/video-app --version` 为 0.4.0。旧工程、音轨、原片、成片记录自动保留；旧项目默认没有字幕轨，不需要强制采用。
3. 等 Agent 任务结束，在使用中的项目点击“升级项目工具至 v0.4.0”，保留会话与工作区，更新到七个 MCP 工具。
4. 原片采用会替换当前配音/字幕并缩放镜头，先检查界面说明。需要字幕但未有原片时，可以上传自己的音频和 SRT。
5. 渲染依赖不变：HyperFrames 0.8.87、GSAP 3.15.0；字幕核对需要 FFprobe，重新渲染需要既有 Node/Chrome/FFmpeg/中文字体。

字幕随 project.json 保存，采用的配音是 workspace/assets 中的独立副本，备份需一起保存。旧版不认识字幕字段；如需降级，恢复升级前备份，避免旧版保存丢弃字幕绑定。

# 从 v0.2.0 升级到 v0.3.0

1. 停止视频 APP，备份完整 data 目录。保留 RunDesk 专用实例和凭据。
2. 替换 APP 源码、页面、脚本与 bin 二进制，继续使用原来的 VIDEO_DATA。不要用发行包覆盖数据目录。
3. 核对 `bin/video-app --version` 输出 0.3.0。旧项目无需迁移，仍可编辑和渲染。
4. 等 Agent 当前任务结束，在继续使用的项目中点击“升级项目工具至 v0.3.0”。保留原会话和工作区，只更新该应用管理的 Skill/MCP。
5. 导入新闻制作包会建立独立项目，原项目不会被覆盖。news 字段由服务维护，项目 PUT 和 MCP 编辑不能改写它。

新增文件在 projects/<id>/news/ 下，备份时必须包含。降级前应恢复备份；旧版本保存项目会忽略并丢弃 news 元数据，不能直接降级后继续编辑新闻项目。

# 从 v0.1.0 升级到 v0.2.0

1. 停止视频 App，备份视频 App 的整个数据目录。保留 RunDesk 数据、专用实例、登录与模型配置。
2. 将 v0.2.0 解压到新的临时目录，核对版本后替换原安装目录里的源码、页面、脚本与二进制。**不要用发行包覆盖你的 data 或环境配置。** 保持 `VIDEO_DATA` 指向原目录。
3. 渲染依赖版本未改变：HyperFrames 0.8.87、GSAP 3.15.0。已有 runtime/node_modules 可继续使用；新安装目录可以重新运行 setup-renderer.sh。
4. 启动 App，刷新浏览器，选择原项目。旧项目默认为没有音轨；旧成片、分镜、素材和会话保留。
5. 等专用实例的 Agent 任务结束，点击“升级项目工具至 v0.2.0”。每个需要继续创作的旧项目分别更新一次。这会更新该项目的应用 Skill 和实例的 video_project MCP，不新建会话，也不复制或清除登录凭据。
6. 上传配音/音乐，保存并渲染一个短项目确认效果。

如果更换了安装路径、端口或 App 令牌，即使版本相同，也可点击“重新配置工具”修复 MCP command/args/env。正在运行或等待授权的任务会阻止配置刷新；先完成或停止任务。

启动参数错误、令牌过短或端口占用不会再留下新锁文件。v0.1 异常退出已经留下的锁仍需在确认旧进程结束后人工删除。强制终止进程同样可能残留锁。

更新后的 project.json 含 audio 和 toolsVersion，旧版 App 不理解这些字段，回退旧版可能丢失新增音轨配置；需要回退时恢复升级前的完整备份。
