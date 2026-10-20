# 项目绑定 RunDesk 制作环境

Video App 0.27.0 通过可搜索列表使用 RunDesk 的 application-runtime-presets 能力（0.50.0 起提供，当前配套 0.52.0）。管理员在RunDesk为视频应用建立多个命名预设，每个预设绑定专用Codex Docker实例和工作区；App只选择批准的名称，不接收任意Docker镜像或Docker socket。

## 部署与选择

1. 在RunDesk应用配置中登记预设，准备实际镜像、模型认证、Skills/MCP与工作区。
2. 打开 App“新建项目”，列表从 RunDesk 查询当前凭据获授权的环境，搜索名称/说明后选择。界面显示名称和真实可查询状态；系统保存 ID。可选 --production-profile / VIDEO_PRODUCTION_PROFILE 仅作为新项目预选值。
3. 点击创建，按原流程导入和开始制作。项目保存创建时的选择，首次连接记录 RunDesk 返回的 preset/instance/workspace，后续对话复用该绑定。

Docker Compose透传VIDEO_PRODUCTION_PROFILE。不设置或空值使用原默认绑定；旧RunDesk仍可用于该兼容路径。设置非空预设而服务不支持时明确拒绝，不静默切回默认环境。

创建项目HTTP POST /api/projects和MCP video_project_create可带productionProfile。省略用启动默认，显式空字符串选原默认；此字段不在普通项目PUT中修改。已有requestId的相同创建请求优先返回原项目，不因默认值改变重复创建；显式修改profile仍是不同请求。

## 会话与恢复

Project.productionProfile保存创建时解析的选择，runtimeBinding保存实际presetId、instanceId、workspaceId及管理身份。nil字段只用于旧项目迁移识别。App不再通过不断覆盖cfg全局实例在项目间切换。

改变启动默认不迁移已创建项目，即使其尚未连接；已有session的旧项目按原会话恢复，不套用新默认。连接、工具解析、准备、诊断、新消息和pending回执使用项目绑定。结果不确定时继续核对原session、原请求体与原幂等键，不把旧回执应用到另一环境。

绑定不一致、预设禁用或权限不足时，保留项目和历史并返回具体错误；不自动复制原生Agent线程或改另一环境。预设的镜像更新由RunDesk管理员按现有显式重建流程处理。

## 与渲染和生成服务的关系

运行预设选择Codex工具容器。App自有HyperFrames/FFmpeg渲染服务、IndexTTS和ComfyUI仍独立；相同生成服务可供不同制作环境使用。

预设名字如openmontage只是管理员标识，不自动下载上游源码、安装Skills、取消上游审批或适配其工程。其镜像和制作/结果适配需实际准备；本版不把选择容器等同于已跑通OpenMontage全流程。App当前HyperFrames、native、video_use剪辑后端仍按工程配置分别执行。

日常UI继续只保留项目、导入、一键初稿、结果、对话和导出。环境配置、认证、镜像差异、任务轨迹与深入调试进入RunDesk。

## video_use 的共享目录

HyperFrames/native不要求共享工作目录。video_use兼容流程需要App与所选Codex容器看到同一物理工作区；每个预设使用独立目录。VIDEO_EXCHANGE_ROOT仅保留给旧默认绑定，非空预设不能回退使用它。

为video_use预设显式配置 VIDEO_EXCHANGE_ROOTS，JSON键为该预设的RunDesk workspaceId，值为App容器内已挂载的绝对目录。每个根与其他映射、旧默认根必须互不相同且不为父子目录。示例自定义Compose叠加文件：

```yaml
services:
  video-app:
    environment:
      VIDEO_EXCHANGE_ROOTS: '{"RUNDESK_WORKSPACE_ID":"/exchanges/video-use"}'
    volumes:
      - type: bind
        source: /absolute/path/to/the/rundesk/workspace
        target: /exchanges/video-use
        bind:
          create_host_path: false
```

不要把RunDesk整个data、Codex HOME或Docker socket挂入App。管理员显式选择目录；App在导出前另做所选绑定的共享挑战，核对实际容器和目录，再冻结run绑定与根指纹。缺失映射、共享检查失败或映射改变时拒绝继续读写共享run，不把B项目材料导出到A目录。已私有归档的历史成片仍保留。

本轮验证以 [validation-0.27.0.json](validation-0.27.0.json) 为准；HTTP/界面与协议测试不代替用户 Docker、实际 Codex 和 GPU 模型验收。

本次列表选择用于普通“新建项目”和首访创建。“导入新闻制作包”保留独立导入入口及原环境/部署默认规则，未更改新闻包格式或 API 创建契约。

## 列表与状态 API

`GET /api/runtime-profiles` 是 App 服务端代理，使用已有 RunDesk 凭据读取本应用的运行预设，并单独查询原默认绑定；浏览器不会收到 RunDesk Token、Codex HOME 或原始运行配置。返回 `{items, defaultProfile, warnings}`。各项包含 ID、名称、说明、enabled、isDefault、state、selectable 和可选说明。`selectable` 表示可以为新项目保存该环境意图，不证明模型认证或制作质量已验证。

查询仅使用只读 GET，不会启动容器、初始化连接、安装模型或创建 Agent 会话。全新安装若尚未在 RunDesk 登记视频应用及其默认/预设绑定，列表会明确提示先到 RunDesk 配置；查询不会通过旧自动连接流程替用户创建环境。主列表的连接/权限错误明确失败；个别环境状态不可查询时标记不可选并保留具体状态。配置的默认预设缺失或无权限时显示不可选占位，不自动选择列表中的第一个环境。停用预设留在列表与既有项目历史中，不能作为新项目选择。

新建请求在浏览器保存完整名称、环境选择与 requestId。响应丢失或关闭重开继续核对原请求，不能给同一请求替换环境；原有不带 productionProfile 的待确认请求保持原始请求体。刷新目录不修改已有项目，切换制作环境应新建项目。状态是读取时的快照，执行前 RunDesk 仍会核对权限和绑定。
