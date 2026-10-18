# ComfyUI MiniMax-H3 文生视频

本实现来自 shengjuntu/rva 的 `b8871a4`（内置 H3）及 `065e4c7`（安静音轨增益），本版与 v0.23.1 合并并补齐错误处理和测试。ComfyUI 独立运行模型，Video App 通过内置 Go 客户端提交工作流、查询结果和下载 MP4。IndexTTS 继续独立提供口播；H3 自带声音属于素材音轨，不等同于新闻主播配音。

## 配置

在 App 启动环境或现有 docker/.env 增加，随后重启 App：

```dotenv
VIDEO_VIDEO_GEN_URL=comfy://127.0.0.1:19897
VIDEO_VIDEO_GEN_MODEL=minimax_h3_fl2va
VIDEO_VIDEO_GEN_KEY=
VIDEO_COMFY_H3_AUDIO_GAIN_DB=18
```

`comfy://` 连接同一主机/端口的 HTTP ComfyUI；不接受账号密码、路径、查询串或片段。可选 KEY 以 Bearer 发送给该服务，不出现在能力目录或任务素材记录。它与普通 HTTP 生成适配器仍共用一组 VIDEO_VIDEO_GEN_* 配置，同一 App 进程当前选用一种视频生成提供方。

Linux host-network App 容器可访问宿主机的127.0.0.1；其他部署按实际网络配置服务地址。无需挂载 ComfyUI 模型或输出目录给 App，也不共享 App 的私有 data。ComfyUI 模型和节点仍需预先安装，本次不包含模型权重。

保留原工作流模型：

- `minimax_h3_fl2va_pruned_bf16.safetensors`
- `qwen3vl_32b_minimax_h3_int8_convrot.safetensors`
- `minimax_h3_video_vae_fp16.safetensors`
- `minimax_h3_audio_vae_fp32.safetensors`
- `minimax_h3_fl2v_turbo_4step_v1.2_768p_comfyui_bf16.safetensors`

工作流依赖 UNETLoader、CLIPLoader、VAELoader、LoraLoader、MiniMaxH3ImageToVideo、KSampler、VAEDecode、VAEDecodeAudio、VHS_VideoCombine。节点存在和配置探测不等于模型推理已验收。

## Agent 调用和输出

通过原有 `video_job_start`，设置 type=video_generation、当前 revision、新的24位十六进制 requestId，options 提供 prompt，可选 duration、aspectRatio。轮询 `video_job_get`；成功后重读项目与新增视频素材，检查实际输出再剪辑。主页面不增加手动生成操作。

当前是文生视频。虽然节点名含 ImageToVideo，现有工作流没有接入参考图输入；传入 referenceAssetIds 会明确拒绝，不向远端提交。

默认124帧、24fps。请求时长按24fps换算整数帧，限制到124..362帧；因此目标5秒可能生效为124帧，目标60秒也不能生成60秒。任务和素材来源记录保留目标值与实际提交参数、调整说明；实际视频时长仍以 ffprobe 为准。

| aspectRatio | 输出尺寸 |
| --- | --- |
| 缺省、16:9 | 768×432 |
| 9:16 | 432×768 |
| 1:1 | 512×512 |
| 4:3 | 640×480 |
| 3:4 | 480×640 |

保留原生音轨。默认增益+18dB，用于延续用户的安静音轨修复；附加限幅，不声称完成响度归一化或听审。VIDEO_COMFY_H3_AUDIO_GAIN_DB 接受-30..30的有限数值；0直接保留原始媒体。非零增益只重新编码音频，复制视频；缺失音轨或处理失败明确报错，不静默回退。实际成片中的原声、主播口播与音乐仍由 Codex 明确安排，避免重复叠加。

生成结果仍走原有 MP4/ffprobe、大小、哈希、来源和素材入库校验。不因 ComfyUI 返回完成就视作合格成片。

## 任务边界

App 先保存任务再后台执行。同 requestId+原始请求重试复用任务，改变内容返回409；不要换ID重试未知结果。ComfyUI错误优先于成功标志处理，查询/下载失败给出明确错误，不一直吞错等待。

取消只停止本地等待，不调用全局 /interrupt，不保证远端停止或释放显存。App 重启后的运行任务标 interrupted，不自动恢复远端 prompt。外部排队、推理进度没有可靠百分比时仍显示未知，不伪造进度。

本版使用模拟 ComfyUI HTTP 和真实短 FFmpeg 媒体验证协议及入库；用户的 H3 GPU、IndexTTS 音质和 Docker 镜像未在本环境实测。OpenMontage/video-use 的可选镜像接入不属于此次合并范围。
