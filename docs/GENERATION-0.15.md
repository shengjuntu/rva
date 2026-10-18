# v0.15 图片与视频生成 HTTP 适配器

此页保留 v0.15 HTTP 协议说明。当前版本已支持可选 duration、aspectRatio、referenceAssetIds（由适配器实际能力决定）；另有内置 [ComfyUI MiniMax-H3 文生视频](COMFYUI-H3.md)，无需外部 http_binary_v1 适配器。后文的旧工具数量与仅 prompt 限制不是当前版本的完整能力。

App 负责请求、任务、产物校验和保存，RunDesk/Codex 负责制作。这里是自定义 http_binary_v1 协议，不是直接支持任何厂商原生 API，也不是 ComfyUI 原生接口。现有模型服务需加适配端点。

启动 App 前设置管理员环境变量：

```
VIDEO_IMAGE_GEN_URL=http://your-adapter:8080/image
VIDEO_IMAGE_GEN_MODEL=your-image-model
VIDEO_IMAGE_GEN_KEY=optional-secret
VIDEO_VIDEO_GEN_URL=http://your-adapter:8080/video
VIDEO_VIDEO_GEN_MODEL=your-video-model
VIDEO_VIDEO_GEN_KEY=optional-secret
```

URL 是完整 POST 端点。App 不接受 MCP 提供 URL、密钥或本地命令。凭据只作为 Authorization: Bearer 发送，不写任务记录、报错或能力目录，也不传入渲染子进程。禁止跟随 HTTP 重定向。单次请求最多15分钟。

请求 JSON：

```json
{"schema":"video.generation-request.v1","requestId":"24位十六进制","type":"image_generation","model":"your-image-model","prompt":"画面描述"}
```

发送 Idempotency-Key=requestId。适配服务应实现幂等；App 无法保证远端幂等或取消计费。成功响应 HTTP 200，正文是原始 PNG/JPEG 或 MP4 二进制；不接受下载URL、服务器文件路径、Base64 JSON或异步轮询票据。厂商异步任务应由适配服务等待并转换。

Codex 使用 video_job_start(type=image_generation/video_generation, options={prompt})，提供当前 revision 和新 requestId。轮询 video_job_get；jobId 为 generation:<nativeId>。成功 artifact URL 最后一段是实际素材 ID。重读项目、检查素材、保存描述、编排 DSL。生成只添加素材，不修改剪辑，不给图片自动写虚构语义描述。

限制：每实例最多2个并行生成、每项目最多100个生成任务；图片最多32MiB/3200万像素/单边8192，完整解码校验PNG/JPEG；视频最多128MiB，MP4头与ffprobe视频流/时长校验，最长600秒。视频未逐帧验证。现版仅 prompt，不支持参考图、seed、尺寸、采样参数。

重复相同请求复用任务记录；改变请求内容会409。超时、取消、服务重启不自动重试。取消是 interrupted，远端可能继续执行。无法确认远端是否生成成功时，应人工核对，不能换requestId无脑重跑。生成失败只报告状态码/通用错误，不返回远端正文。

能力目录 implemented=true 表示适配代码存在；configured 表示环境变量合法，ready=false 保留，因为没有模型探测协议。MCP仍29项。安装后更新项目工具。

验证：Go race全套；模拟HTTP生成服务返回真实PNG与包内真实FFmpeg MP4，导入、哈希、项目修订、重复请求、请求冲突、取消、非法图片、重启中断；Node及JSDOM/HTTP回归。真实模型、图形浏览器、Windows运行和厂商原生API尚未验收。
