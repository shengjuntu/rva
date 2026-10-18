# news2douyin → 视频 APP → RunDesk

news2douyin 继续负责新闻、审核稿和证据；本 APP 管制作项目；RunDesk 管 Agent 运行。接入沿用 RunDesk v0.5.4 的既有接口，没有新增或猜测 RunDesk 私有协议。三个服务不共享数据库。

## 使用步骤

1. 在 news2douyin >=0.3.0 的脚本工作台完成审核，点击“导出制作包”。
2. 在视频 APP 点击“导入新闻制作包”，选择 ZIP。正文完整映射到制作分镜；原稿、来源、审核记录和原始包独立保留。
3. 在“新闻来源与交接”中打开原稿与证据，核对内容。链接仅允许 HTTP/HTTPS，展示使用纯文本，来源中的指令不会成为应用命令。
4. 如已在 news2douyin >=0.4.0 生成视频，下载对应 video_bundle.zip，在同一 APP 项目接收。会核对导出标识、稿件版本和内容哈希，并使用 FFprobe 检查实际成片。
5. 原始视频可播放和下载，配音 WAV、字幕 SRT、清单与完整包也可下载。需要重用配音时，将下载的 WAV 上传到素材库并在音轨区选择，按其实际时长调整分镜。
6. 连接 RunDesk 并配置项目工具，在该专用实例完成用户自己的模型/登录配置。Agent 可通过 video_news_get 读取原稿及来源，通过既有 video_project_update 修改草稿，再使用原有渲染工具。

不会自动向模型发送稿件。连接配置不会执行模型推理；用户在对话中提交制作要求后才开始 Agent 运行。审批策略、停止和运行中补充指令均沿用既有 RunDesk 接口。

## 审核和时长的含义

“原稿审核通过”只说明制作包记录了审核流程，不代表 APP 重新核验事实。导入后的分镜始终属于制作草稿；后续文字修改没有继承原稿审核结论。

分镜按最多 220 字拆分，保留完整正文，不会悄悄截掉长稿。标题超过 APP 的 80 字限制时仅缩短项目/镜头显示标题，完整标题仍在原始快照。每个镜头 1–60 秒，最多 80 个，10 分钟总上限。初始时长按目标时长和文字分配并限制到镜头范围，属于估算；不保证总长等于新闻编辑目标，也不是音频对齐结果。

原始交接成片独立显示，不写入当前 APP 的渲染成功列表。改分镜不会改变它，也不能用它证明当前修改已渲染。Go APP 重新渲染仍使用 HyperFrames 1080×1920 / 30 fps 模板；交接原片允许 news2douyin 的三个既有预设。

## 包格式及接收限制

- 脚本：schema_version=1，kind=news2douyin.script_package；压缩包及全部解压数据均不超过 4 MiB。
- 文件须为 manifest.json、revision.json、script.txt、script.json、sources.json、assets_manifest.json、script.md 七个普通平面文件。
- 验证每个文件 SHA-256、revision.json 内容哈希、口播正文、选中来源，以及与 revision/state_version 对应的 approve 审核记录。
- 视频：schema_version=1，kind=news2douyin.video；压缩包及全部解压数据均不超过 256 MiB。
- 文件须为 manifest.json、video.mp4、narration.wav、subtitles.srt、cover.png、script_snapshot.json 六个普通平面文件。
- 验证每个文件哈希；视频 export_key、revision、script_hash 必须匹配导入稿。视频流需 H.264、音频流需 AAC，尺寸/帧率/帧数和各流时长匹配清单；配音为可探测 WAV 且时长匹配，封面为同尺寸 PNG，SRT 时间有序且正文匹配原稿（忽略空白）。
- 拒绝额外文件、重复文件名、目录、软链接、路径越界、过大解压数据、未知 schema 版本。不下载包内 URL，也不执行包内文件。
- 哈希用于完整性检查，**不是数字签名，也不能认证发布者或证明新闻事实**。使用者需确认包的来源。APP 不转写配音，因此不能自动证明音频实际说了字幕内容。

重复提交同一脚本 ZIP 返回已有项目，包括已有编辑；不会重建或覆盖。同一 package_key/export_key 但不同包字节返回冲突。不同审核版本建立独立项目。

每个来源项目最多接收一份原始成片；重复同一包返回已有结果，不同包返回冲突。接收成片需当前 revision，操作中发生编辑也会拒绝。已接收原片不自动切换为更新版本。

## 存储与恢复

原稿置于 projects/<id>/news/，成片置于 news/videos/<id>/；原 ZIP 和所有文件保留。project.json 中的 news 元数据只由导入处理器维护，普通 PUT 与 MCP 编辑不能覆盖。文件不在 Agent 工作区，读取由 API/MCP 提供。

脚本接收先在临时目录验证和完整写入，再以单次目录重命名发布。运行时失败会清理临时目录；突然断电可留下未发布的 news-upload-/news-project- 临时目录，不会当作项目加载。成片目录移动后才原子登记元数据；断电可能留下未引用的成片目录。没有自动清理或跨文件系统事务。

备份整个 data 目录。原稿/成片下载重新检查哈希；丢失或变动会返回错误，而不是继续当作正常产物提供。检查完整文件会增加大文件 Range 请求的磁盘读取开销。

## HTTP 与 MCP

所有新 HTTP 接口沿用 APP Bearer/Cookie、回环 Host 和同源限制。

| 接口 | 行为 |
| --- | --- |
| POST /api/news/import | multipart file；成功 201，重复同包 200；返回 Project |
| GET /api/projects/{id}/news | origin、manifest、snapshot、sources、storyboardStatus、timing |
| POST /api/projects/{id}/news/video | multipart file、revision；成功 201，重复同包 200 |
| GET /api/projects/{id}/news/script/files/{file} | 原稿固定文件或 bundle.zip |
| GET /api/projects/{id}/news/video/files/{file} | 原片固定文件或 bundle.zip；MP4 支持 Range |

下载加 `?download`。包结构问题 400，内容/媒体校验失败 422，版本、已有包或文件完整性冲突 409，不存在 404。

MCP 新增 `video_news_get(projectId)`，返回与 news 详情接口相同的数据；现有四个工具继续兼容。项目工具版本升级到 0.3.0，Skill 要求 Agent 把来源当作参考数据，使用证据并保留事实限定，避免把原始交接视频当成当前制作结果。

## 复现跨应用验收

```bash
# 在安装 news2douyin[video,voice-offline] 的环境中生成真实演示稿与成片：
python -I /path/to/news2douyin/tools/smoke_video.py --preset portrait --output-dir /tmp/news-demo

# 构建 APP 后执行。RunDesk 使用独立临时数据和显式 demo 模式：
python3 scripts/smoke-news.py \
  --app bin/video-app --rundesk /path/to/rundesk/bin/rundesk \
  --script /tmp/news-demo/script_bundle.zip --video /tmp/news-demo/video_bundle.zip
```

该检查实际启动 APP、RunDesk 和 stdio MCP，使用真实媒体，但 RunDesk 的 Agent 响应是演示模拟。不会读取已有 RunDesk 数据或调用付费模型。输出检查项与临时目录，退出时停止服务。
