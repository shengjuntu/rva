# v0.22：运行诊断与按文字定位剪辑

三阶段仍是素材准备和对齐、剪辑、精修；正文与制作要求保持自由，原生DSL或video_use EDL只表达剪辑。本版补充环境证据和镜头原文引用，让Codex能够解释制作阻塞，并按人的文字意见找到需要调整的片段。

## 运行诊断

MCP调用 `video_runtime_diagnose(projectId)`，HTTP为 `POST /api/projects/{id}/diagnostics`，请求体 `{}`，沿用App认证。页面入口在“高级工具与诊断”。返回包括 `schema:"video.runtime-diagnostics.v1"`、`checkedAt`、`projectId`、`editingBackend`、`ready`、`binding`、`rows`、可选 `runtime` 以及 `modelVisionVerified:false`。

每行包含 `id/title/state/summary`，可带 `code/remedy`。状态含义：

| state | 证据含义 |
| --- | --- |
| passed | 本次已执行该项检查并通过 |
| failed | 本次检查得到不匹配或失败的结果 |
| blocked | 缺少必要前提，需按remedy处理 |
| not_checked | 本次未执行该项，不能当作通过 |

`ready`依据本次后端所需检查汇总；不等于模型可用性、素材质量或完整制作已验收。模型视觉理解和可选生成服务未调用，不因这些未检查项单独阻塞全部制作。

| 检查 | 实际动作与边界 |
| --- | --- |
| App媒体工具 | 查找FFmpeg/ffprobe，不合成视频 |
| 项目绑定与运行环境 | 核对项目会话、实例、工作区、运行方式和已选环境；不自动启动容器或应用待更新镜像 |
| 认证配置与MCP工具 | 由RunDesk读取实际配置客户端的account/read和mcpServerStatus/list；未完成探测时不使用缓存元数据冒充通过 |
| MCP图片传输 | 在已运行的Codex Docker容器中读取当前MCP配置，调用固定PNG挑战工具，核对本次随机nonce、PNG数据SHA-256、类型与尺寸；不调用模型 |
| video_use运行时 | 核对video-build1.3.0、adapter协议2、固定video-use源码版本、Python依赖、FFmpeg和ffprobe；仅在所选容器探测，不退回宿主机 |
| 共享目录 | App写入随机挑战，容器读取并写回凭证，App复核原请求和响应；不是仅比较路径字符串 |
| 模型视觉/生成服务 | 明确未检查，不发起模型或计费任务 |

native不要求video_use与共享目录。本机模式的容器内PNG传输、配置探测未执行时如实显示未检查；这不证明本机链路失败或已通过。video_use需要运行中的Docker video-build环境。

诊断会执行固定容器命令，并在专用会话目录下临时创建/校验探针文件，结束后仅清理本次探针；不写制作工程、不执行Agent任务、不更换镜像。超时、配置连接忙、接口缺失或诊断期间绑定变化都有明确失败/未检查状态。管理员先准备环境，再按返回的具体remedy修复；不要把“诊断”当作安装或重建按钮。

在video_use付费生成和准备生产快照前诊断。同次制作且配置和环境未变时复用成功结果，新一轮制作或变化后刷新，不要求每条对话都重复执行。

## 精确原文引用

镜头可选 `textRefs:[{quote,occurrence?}]`，每个镜头最多32条。`quote`为原文中完全一致的1–16384字节有效UTF-8文本，不做空白、标点或Unicode归一化；拒绝空白引用、null、未知字段和不支持的控制字符。原文中的换行、回车、制表符可以保留。

唯一出现的引用可省略occurrence。重复出现时指定从1开始的occurrence，重叠出现也计数。例如原文“哈哈哈”的“哈哈”出现两次。显式occurrence必须为正整数。

这是一种多对多编辑关联：一个镜头可以服务几处文字，一处文字也可以关联多个镜头。背景、装饰、音乐和旧工程可不带引用；省略或空数组表示无关联，不接受 `textRefs:null`。不要求预先把整篇稿件分成句子或场景。

### 原生DSL

在现有DSL顶层增加可选 `textSourceHash`；存在非空引用时，必须是当前保存 `narrationText` 精确UTF-8字节的SHA-256，即board返回的sourceHash。引用放在现有clips条目上，例如：

```json
{
  "textSourceHash": "<当前board.sourceHash>",
  "clips": [
    {"id": "product", "textRefs": [{"quote": "发布了一款新芯片"}]},
    {"id": "schedule", "textRefs": [{"quote": "计划明年量产", "occurrence": 1}]}
  ]
}
```

这里只展示新增字段；实际文档仍需原有version、canvas、tracks、素材和时序等合法字段。保存、校验、局部补丁和渲染都会核对引用及正文版本；改稿后不能只替换哈希，须重新核对原文和配音。引用不改变镜头的实际切点或声音。

### video_use EDL

现有 `clips[]` 增加同样的可选textRefs，不增加顶层textSourceHash。引用针对生产快照manifest.textPath指向的原文字节，必须匹配manifest.sourceHash，不能引用当前页面中尚未导入快照的新正文。

adapter2在探测/渲染媒体前验证引用；EDL、validation和归档保留这些关联。validation的textReferences报告原文验证、引用计数、关联覆盖字节和未映射镜头；它不证明画面内容或新闻事实。旧无引用EDL继续兼容；旧输入未提供textPath且没有引用时仍可渲染。不得为兼容性给旧镜头猜造引用。

精修创建新生产快照，更新导出路径，保留仍适用的镜头ID和引用，复核新原文。旧冻结输入和已归档成片保持独立。

## 按文字定位镜头

MCP为 `video_edit_locate(projectId,quote,occurrence?,renderId?)`，HTTP为 `POST /api/projects/{id}/edit/locate`。不传renderId时，native查询当前DSL；video_use查询对应当前输入的已归档成片，尚无当前归档时明确返回阻塞，可指定renderId查看历史。

```json
{"quote":"计划明年量产","occurrence":1}
```

返回包括项目revision、backend、sourceHash、current、可选renderId/productionRunId、query、matches、unmappedClipIds和可选warning。matches记录clipId、可选trackId/assetId、start/end以及textRefs/matchedRefs。start/end是剪辑时间（秒）；query.startRune/endRune是原文Unicode码点的半开区间，不是UTF-8字节下标，也不是配音逐字时间。

定位按已保存引用与查询原文的区间重叠取得镜头，支持一句话覆盖多个镜头或一个长引用内的局部文字。重复查询未指定次序会明确要求occurrence，改写或不存在的引用不会被相似搜索代替。无关联片段列入unmappedClipIds；空matches也是有效结果，不推断“应该是哪一段”。

指定renderId时只读取本项目完整成片的私有归档、冻结原文及工程，并核对已有指纹和证明记录，不读取Agent可变共享目录。current=false表示按历史原文与剪辑定位；不能把这些旧时间直接当作当前工程。缺少完整证明的旧归档无法凭空补出可信定位信息，但原视频继续可以查看。

Codex收到“这一句换图”后，先定位、检查相关镜头，再局部更新素材关联与剪辑，保留其他镜头ID、textRefs和素材。多匹配可按人的指令和上下文处理，不必一律暂停确认。定位结果只说明声明的编辑关系，不证明语义匹配、事件来源或字词发声时间。

## 升级与验证边界

App0.22共48项MCP，新增video_runtime_diagnose、video_edit_locate及固定诊断图像工具rundesk_diagnostic_image；后者供诊断链路调用，不是制作或视觉理解模型。

使用video_use文字引用与版本诊断，需RunDesk0.49.3、video-build1.3.0及adapter协议2。重新构建工具镜像，在RunDesk选择它并明确更新/重建执行容器，保留工作区、HOME和账户；App渲染镜像另行更新。共享目录继续按[现有部署](EXCHANGE-0.21.md)挂载，不能将App整个私有data挂给Agent。只改ZIP或镜像标签不会改变运行中的镜像与挂载。

软件验证：App 全量 Go race 测试 302 项通过、1 项因未配置本地 TTS 集成测试跳过；6 项 Node 测试、43 项 JSDOM 与真实 HTTP 检查通过。Schema 已核对 Go 结构及 JSON 格式。包含真实 FFmpeg、固定 video-use 适配器以及两端真实 Python/HTTP MCP 协议联调；Docker 容器和账户 RPC 在该协议测试中使用替身。Linux 程序已运行，Windows 仅交叉编译。详见 [验证记录](validation-0.22.json)。配套 RunDesk 全量 Go 测试 557 项、15 项适配器与 12 项 profile 测试通过，两个命令程序构建通过；详见配套包 `docs/validation-0.49.3.json`。固定工具与模拟服务测试不能代替真实Docker部署、Codex自主制作、模型视觉理解、IndexTTS音质或生成服务验收。诊断modelVisionVerified始终为false；最终还需在实际模型会话中让Codex观察并准确描述真实图片，再完成整片和对话修改联调。
