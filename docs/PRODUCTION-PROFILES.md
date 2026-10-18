# 项目绑定 RunDesk 制作环境

Video App 0.25.0 接入 RunDesk 0.50.0 的 application-runtime-presets 能力。管理员在RunDesk为视频应用建立多个命名预设，每个预设绑定专用Codex Docker实例和工作区；App只选择批准的名称，不接收任意Docker镜像或Docker socket。

## 部署与选择

1. 在RunDesk应用配置中登记预设，准备实际镜像、模型认证、Skills/MCP与工作区。
2. App启动时设置 --production-profile hyperframes，或 VIDEO_PRODUCTION_PROFILE=hyperframes。名字须与RunDesk登记值一致。
3. 新建项目并按原流程导入、开始制作。项目保存创建时的选择，首次连接记录RunDesk返回的preset/instance/workspace，后续对话复用该绑定。

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

本轮验证以validation-0.25.0.json为准；模拟HTTP/容器协议证明接口与归属边界，不代替用户Docker、实际Codex和GPU模型验收。
