# 本机自动配音 · v0.5.0

本版用独立 eSpeak NG 命令行进程生成预览配音。Go 主程序仍只依赖标准库，配音流程不需要 Python、Node 或模型账号。视频渲染继续使用既有 HyperFrames/Node/Chrome/FFmpeg。机械音适合验证流程；尚未接入神经网络配音、音色克隆、ASR 或逐字强制对齐。

## 安装与使用

Ubuntu/Debian 可安装发行版的程序和语音数据：

```bash
sudo apt install espeak-ng ffmpeg
espeak-ng --version
espeak-ng --voices=cmn
```

APP 默认在 PATH 查找 `espeak-ng`。自定义位置可用 `bin/video-app --espeak /absolute/path/espeak-ng`；使用启动脚本时设置 `VIDEO_ESPEAK`。Windows 需自行安装 eSpeak NG 和 FFmpeg，并通过 `--espeak` 指向对应 exe；本版未做 Windows 真机验收。官方安装说明见 [eSpeak NG guide](https://github.com/espeak-ng/espeak-ng/blob/master/docs/guide.md)。工具、字体及语音数据不在本发行包内。

1. 创建或打开项目，保存各镜头的正文；它们就是配音内容，镜头标题和项目说明不朗读。
2. 在“自动配音”点击环境检查。它检查 eSpeak NG、FFmpeg、FFprobe 可执行文件；完整语音数据和语言支持在实际任务中验证。
3. 选择中文 `cmn` 或英语 `en-us`，语速 80–300，默认 170（引擎的近似词/分钟参数），点击生成。
4. 查看任务进度。可以继续编辑，也可以单独取消配音。生成不会替换当前声音、字幕、镜头时长或原始新闻视频。
5. 完成后试听 WAV，必要时下载 SRT、清单、日志。点击“采用配音与字幕”后，系统才替换当前配音/字幕，并使用该任务测出的逐镜头时长。背景音乐、画面素材、标题、说明保留。
6. 核对新时长下的音乐起点与画面，生成视频。改写正文后重新配音；旧任务与原成片均保留。

采用需要当前项目 revision，以及与任务一致的镜头 ID、顺序和正文。只修改标题、素材或说明可以采用；拆镜头、换序、改正文则需新任务。多次采用同一任务会复用素材副本。原副本若被外部修改则拒绝采用，需要保留损坏证据并使用新的配音任务，而不会偷偷覆盖文件。

## 时间如何得到

每个镜头的正文按句末标点拆分，单段最多 80 个字符；较长段优先在空白或逗号处拆开。空白格式会规范化，但不会删去正文字符。每段**先独立合成**，再由 FFmpeg 转成 24 kHz、单声道、16-bit PCM；根据实际样本数记录该段起止时间。不是对既有音频按字数分配时间。

各段音频原样拼接。镜头末尾补少量静音，保证最少 1 秒并向上对齐到 30 fps 帧边界；无正文镜头按原时长补静音并对齐帧边界。一个视频帧对应 800 个 PCM 样本，避免逐镜头舍入累积造成音画漂移。字幕不延长到末尾补入的静音中。单镜头超过 60 秒或总长超过 600 秒会失败，需拆分镜头/项目或调整语速。

`alignment` 与采用后的字幕绑定方法为 `synthesized_sentence_durations`，表示**实测合成分句边界**，不表示每个字已对齐，也不保证发音、数字和专有名词正确。仍需试听。若更换音频或修改字幕，沿用既有字幕核对流程。

新绑定额外记录严格的镜头正文/结构哈希，保留词间空格差异；例如 `now here` 改为 `nowhere` 会使绑定失效。旧版字幕绑定继续兼容原规则，再次采用/核对时会写入新规则。

## 任务、取消与恢复

- 一次最多运行一个配音任务；可同时有一个独立视频渲染。停止 Agent、取消配音、取消渲染分别控制。
- 每个项目最多保留 100 个配音任务，每次 1–300 个合成分段。配音不自动排队、重试或断点续跑。
- 每个合成/转码子进程限时 45 秒，FFprobe 限时 30 秒，整个任务限时 20 分钟。Linux 取消会终止相应进程组；Windows 为直接子进程终止，复杂子进程树仍待实机验证。
- 运行中的进度和任务状态持久保存；正常退出会取消配音，重启会将未完成任务标为 interrupted。失败/取消后可生成新任务，旧错误及日志保留。
- 创建任务使用 requestId 幂等键。同一键、相同 revision/voice/rate 返回既有任务；不同参数返回 409。失败后想重新生成应换新 requestId。
- 任务创建、进度更新和完成不增加编辑 revision；任务采用才增加 revision。生成期间的新编辑不会被后台完成回写覆盖。
- WAV、SRT、清单和输入快照只有完成后可下载；读取核对 SHA-256 和大小，WAV 支持 Range。日志可在未完成时查看。

目录为 `projects/<id>/speech/<speechId>/`，其中保留 `snapshot.json`、`manifest.json`、`narration.wav`、`subtitles.srt`、`speech.log`。采用后在 `workspace/assets/` 有独立 WAV 副本，渲染时还会复制并复核哈希。备份需包含整个 data。突然断电或失败可能留下未完成文件和未引用素材，没有自动垃圾回收；任务状态可防止把它们当成成功产物。

## HTTP 与 MCP

沿用 APP 的令牌、同源和回环 Host 限制。可执行路径只能通过服务启动配置指定，API/Agent 不能传入任意命令或 shell 参数。文本通过本机 UTF-8 文件输入 eSpeak NG，未启用 SSML；子进程剔除 APP/RunDesk 令牌。

| 接口 | 请求与结果 |
| --- | --- |
| GET /api/speech/capabilities | 后端、工具存在性、默认语音和语速范围 |
| POST /api/projects/{id}/speech | `{revision,requestId,voice?,rate?}`；新任务 202，原请求重试 200 |
| POST /api/projects/{id}/speech/{sid}/stop | `{}`；取消该任务 |
| POST /api/projects/{id}/speech/{sid}/adopt | `{revision}`；核对当前正文/结构后采用，返回 Project |
| GET /api/projects/{id}/speech/{sid}/files/{file} | WAV、SRT、manifest.json、snapshot.json、speech.log；下载加 `?download` |
| GET /api/projects/{id} | `speech` 数组包含状态、进度、原 revision、语音、时长及产物哈希 |

requestId 需为 24 位十六进制串。voice 支持合法的本机 eSpeak 语音名；页面先提供 cmn/en-us 两个预设。普通项目 PUT 不能改写 speech 任务。

MCP 新增 `video_speech_start(projectId,revision,requestId,voice?,rate?)`、`video_speech_stop(projectId,speechId)`、`video_speech_adopt(projectId,speechId,revision)`，共 10 个工具。读任务仍用 `video_project_get`。Skill 要求先保存并读取项目、根据用户指令生成和采用，不能把完成的旧任务直接冒充当前配音。

## 开发验收

```bash
# 没有该变量时真实 TTS 用例会明确 skip；发布验收需显式设置。
VIDEO_TEST_ESPEAK=/absolute/path/to/espeak-ng go test -race ./...

# 在已有浏览器/渲染验收中加入自动配音，再实际渲染：
VIDEO_TEST_ESPEAK=/absolute/path/to/espeak-ng \
PLAYWRIGHT_MODULE=/absolute/path/to/playwright \
HYPERFRAMES_BROWSER_PATH=/absolute/path/to/chrome \
VIDEO_FONT=/absolute/path/to/chinese-font.otf \
VIDEO_TEST_OUTPUT=/tmp/speech-validation node scripts/smoke-subtitles.cjs

# 真正调用 stdio MCP 生成、采用、取消配音；RunDesk 用隔离 demo：
python3 scripts/smoke-news.py --app bin/video-app \
  --rundesk /path/to/rundesk/bin/rundesk \
  --script examples/script_bundle.zip --video examples/video_bundle.zip \
  --espeak /absolute/path/to/espeak-ng
```

本版本验证详情见 [VALIDATION-0.5.md](VALIDATION-0.5.md)。
