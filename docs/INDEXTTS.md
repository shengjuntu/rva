# 接入已部署的 IndexTTS-2.5

本版连接你的现有模型服务，不重新安装模型。Video App/RunDesk/Codex 容器均不需要加载 IndexTTS；由独立 TTS 服务使用 GPU，Video App 保存生成音频、字幕和清单。

## 一、确定现有服务接口

| 现有部署 | 接入方式 |
| --- | --- |
| vLLM-Omni 或兼容服务，提供 `POST /v1/audio/speech` | 直接填写完整接口地址与已注册音色 ID |
| 官方 IndexTTS Python/WebUI，只有 Gradio 页面 | 在现有 Python 环境运行下方适配入口，复用模型目录 |
| 第三方 `/tts`、multipart、Gradio `/call/...` 或返回 JSON 文件路径 | 本版不能直接连接；需匹配本文协议或使用随包适配入口 |

**模型版本一致不代表 HTTP 协议相同。** 本版请求 JSON，要求响应直接为 RIFF/WAVE 二进制；不下载响应中的任意文件 URL，不读取服务端本地路径。

## 二、在 Video App 配置

打开项目 → 自动配音 → 配音服务设置：

| 配置项 | 示例 |
| --- | --- |
| 默认引擎 | IndexTTS-2.5 |
| 语音接口完整地址 | `http://127.0.0.1:8092/v1/audio/speech` |
| 模型名 | `IndexTeam/IndexTTS-2.5`，以服务实际名称为准 |
| 默认音色 ID | `news_zh`，必须先在 TTS 服务配置，不能随意编造 |
| 默认语言 | 中文 `zh` |
| 每段超时 | 180 秒；首次加载慢可调大，范围 10–900 |
| API Key | 服务要求的 Bearer Token；无认证时留空 |

填好地址和 API Key 后，可先点击“读取可用音色”，在默认音色输入框选择服务返回的 ID，再保存。此操作不修改配置，也不生成音频；服务未提供列表时仍可手动填写。环境变量管理时按钮读取已配置服务。

保存后点击“检查配音环境”。检查会访问同一服务的 `/v1/models` 并确认模型名，且检查本地 FFmpeg/FFprobe。它不运行模型或测试音色；服务没有模型列表接口会显示“无法自动确认”，仍允许启动短句合成。

语言控制朗读，不负责翻译。英文新闻要生成中文声音，先将分镜正文写成中文，再选择中文。`zhen` 是 vLLM-Omni 的额外混合文本模式；随包原生适配器仅接受 `zh/en/ja/es/ar`，中文夹少量英文可先使用 `zh` 试听。

服务地址以 **Video App 进程**的网络为准：Linux 随包 Compose 使用 host 网络，同机可用 127.0.0.1；另一台 GPU 服务器使用其局域网地址。若 TTS 本身在 Docker 中，需要映射服务端口。浏览器能打开 Gradio 页面并不说明 App 容器能连通对应语音 API。

## 三、只有官方 WebUI 时：复用现有安装

这会启动另一个模型进程。如果 WebUI 已占用同一 GPU，可先停止 WebUI，或为适配器指定空闲 GPU；不是把两个进程绑定为同一份显存模型。

1. 在 GPU 服务器准备一段清晰、单人、音量稳定的参考录音，作为固定新闻播音音色。复制 `integrations/indextts/voices.example.json` 为自己的 `voices.json`，填写实际文件路径，例如：

```json
{
  "news_zh": "/srv/voices/news_zh.wav",
  "news_en": "/srv/voices/news_en.wav"
}
```

也可使用相对路径，以 voices.json 所在目录为基准。ID 使用字母、数字、下划线、点或连字符。参考录音留在服务端，App 只发送音色 ID。

2. 使用你现有 IndexTTS-2.5 的 Python 环境启动。以下路径均需替换为实际位置：

```bash
# 当前已激活且能运行 IndexTTS-2.5 的 Python 环境
CUDA_VISIBLE_DEVICES=0 python /path/to/video-app/integrations/indextts/server.py \
  --repo /path/to/index-tts \
  --model-dir /path/to/index-tts/checkpoints \
  --voices /srv/voices/voices.json \
  --host 127.0.0.1 --port 8092
```

使用官方 uv 环境时，将上面的 `python` 换成 `uv run --project /path/to/index-tts python`。服务导入官方 `indextts.infer_v2_5.IndexTTS2`，默认 BF16；需要全精度可加 `--fp32`。不新增 Python 包依赖、不执行安装命令，但上游初始化仍可能尝试补齐缺失的辅助模型，离线部署应先确保模型资源齐全。

App 位于另一台机器时，先设置 `INDEXTTS_API_KEY`，再用 `--host 0.0.0.0`；Video App 填相同 API Key。不要将密钥提交到源码或写入镜像。

3. 启动完成后，服务提供：

- `GET /health`：已加载/忙状态。
- `GET /v1/models`：实际模型 ID。
- `GET /v1/audio/voices`：配置的音色 ID，不暴露录音路径。
- `POST /v1/audio/speech`：生成 WAV。

同一进程一次只执行一个 GPU 推理请求，忙时返回 503。适配器不添加自动重试；App 显示错误后，可待空闲再创建任务。客户端取消会停止后续段落并丢弃结果，但不能保证立即打断上游已进入 CUDA 的当前段；该段结束前 GPU 工作槽保持占用。

## 四、接口约定

请求示例（App 实际调用的形状）：

```json
{
  "model": "IndexTeam/IndexTTS-2.5",
  "input": "今天的新闻播报开始了。",
  "voice": "news_zh",
  "speed": 1.0,
  "response_format": "wav",
  "extra_params": {"lang": "zh", "text_normalization": true}
}
```

`speed` 为 0.5–2.0，大于 1 更快。原生适配器映射为 `duration_factor=1/speed`，不会通过改变采样率变调。响应须为 WAV，单段上限 32 MiB；服务应返回真实 HTTP 错误状态，不把错误对象包装成成功音频。

App 单段超时默认 180 秒，不跟随重定向，不把服务响应正文直接写入错误日志，避免泄露凭据或路径。远程 WAV 再经过实际解码、长度、静音检查，才参与拼接。

## 五、字幕与首次验收

使用两镜头短稿，包含日期、金额、人名和英文缩写：

1. 保存正文，选择音色与 1.0 倍语速，生成任务。
2. 检查任务后端为 IndexTTS、模型/语言/语速正确，试听 WAV，检查漏字、重字、多音字和段落连接。
3. 下载 SRT/清单。字幕时间来自每段实测音频长度，目前没有 ASR 校验或逐字对齐。
4. 采用配音与字幕，核对各镜头实测时长，再渲染成片。
5. 若提交后网络断开或等待超过 30 秒，点击“确认上次配音请求”。当前标签页刷新后仍沿用原请求和参数；先确认原任务，再新建任务。合成段落超时仍按服务配置，和提交响应的 30 秒不是同一计时。
6. 修改一段稿件后旧结果不可直接采用；新建任务会重新合成本任务全部段落，目前没有跨任务片段缓存。相同 requestId 的原样重试会复用回执，不重复合成。

本发行环境没有连接你的 GPU 服务，也未对真实 IndexTTS 音质、吞吐或显存进行验收，详见 [VALIDATION-0.8.1](VALIDATION-0.8.1.md)。

## 六、环境变量方式

可不使用设置页，改在 App 进程或新版 Compose `.env` 配置：

```dotenv
VIDEO_TTS_BACKEND=indextts
VIDEO_TTS_URL=http://127.0.0.1:8092/v1/audio/speech
VIDEO_TTS_MODEL=IndexTeam/IndexTTS-2.5
VIDEO_TTS_VOICE=news_zh
VIDEO_TTS_LANGUAGE=zh
VIDEO_TTS_TIMEOUT_SECONDS=180
VIDEO_TTS_API_KEY=
```

任一 VIDEO_TTS_* 非空时，环境变量覆盖已保存配置对应字段，整个设置页只读；其他字段沿用已保存值或默认值。要恢复页面管理，移除这些非空环境变量并重启。环境变量中的值不写回 `speech-config.json`。

协议依据：[官方 IndexTTS 仓库](https://github.com/index-tts/index-tts)、[官方 vLLM-Omni 接入说明](https://recipes.vllm.ai/IndexTeam/IndexTTS-2.5)。

## 七、命令行联调检查（v0.8.1）

在 **Video App 所在机器或容器**运行；从浏览器电脑执行不能代表容器网络可达。

```bash
# 使用保存的设置；与启动 App 一样继承 VIDEO_TTS_* 环境变量
bin/video-app speech-doctor --data ./data

# 输出诊断报告供排查
bin/video-app speech-doctor --data ./data > speech-doctor.json

# 随包 Docker 已启动时（在 docker 目录执行）
docker compose exec -T video-app /opt/video-app/bin/video-app speech-doctor --data /data
```

输出 JSON：`ready` 为基础检查结果，`checks` 给出配置、FFmpeg、FFprobe、模型和音色检查项，`voices` 列出可选 ID。退出码 0 表示基础检查通过，1 表示检查失败或模型接口无法确认，2 表示参数用法有误。音色列表不受支持时标记 `unknown`，不单独阻止基础检查通过；仍需实际短句试听。

检查只读取配置，不创建项目、不写 App 数据、不调用语音合成，也不在输出中包含 API Key。`synthesisTested` 始终为 false，不能将 `ready:true` 当成真实音质或 GPU 验收。若默认音色不在服务公开列表，诊断会提示核对；服务列表不完整时可在 UI 手动填写并短句验证。

音色列表协议依据：[vLLM-Omni Speech API](https://docs.vllm.ai/projects/vllm-omni/en/stable/serving/speech_api/#voices-endpoint)。随包原生适配器使用同样的 `voices` 数组。
