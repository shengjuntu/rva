# v0.21：素材准备和对齐、剪辑、精修

## 使用方式

粘贴主播稿、提供已有素材，点击“交给 RunDesk 制作”。RunDesk 驱动 Codex 使用 App MCP 服务，三个阶段卡片持续展示真实保存的产物。制作记录不增加中途审批；用户可通过对话调整局部。初稿、调整、终稿是版本状态，与制作阶段分别记录。

| 阶段 | 保存的依据与产物 |
| --- | --- |
| 素材准备和对齐 | 完整原稿、实测配音时序、实际图片/视频、生成来源、原文引用与素材关联、未覆盖文字 |
| 剪辑 | 原生 DSL 或 video-use EDL、完整成片；局部预览与运行中的任务不视作初稿完成 |
| 精修 | 对具体成片的检查记录、修改后的工程和成片；人工确认后另外保存终稿记录 |

内容保留自由文本。关联表允许一个画面覆盖多处文字、多幅画面说明同一段文字；保存不完整关联时明确返回未覆盖原文。重复出现的引用使用一开始计数的 occurrence 区分。剪辑规则只描述镜头、时序、画幅、图层、字幕等操作，不规定文章结构。

## 生成素材

`video_job_start` 的 `image_generation` / `video_generation` 仍调用管理员配置的 HTTP 二进制适配器。除 prompt 外，支持可选 duration（视频目标秒数，最多60秒）、aspectRatio、referenceAssetIds（最多4张实际PNG/JPEG）。适配器接收 referenceImages 的真实Base64图片、ID、MIME、大小及SHA256。协议与示例见生成说明和代码测试；厂商原生异步接口需要另行适配。

生成任务完成后返回真实 assetId；项目修订会更新，Agent须重新读取。素材保存生成来源，随后通过真实查看、描述及关联表进入剪辑。请求参数是目标，不替代结果时长、内容或画幅检查。未知远端结果保持原请求，不自动换编号重复生成。

`video_asset_video_inspect` 接受1至8个递增时间点，按实际视频时长校验，返回时间标注与真实PNG。它没有听音或完整运动检查能力；RunDesk及所选模型还必须实际传递并接受图片。

## 两种剪辑后端

默认 native 保留现有多轨 DSL、裁剪、布局、字幕、音量、淡入淡出、局部修改、撤销和渲染缓存。

可选 video_use 使用配套 RunDesk video-build1.2.0。App准备冻结快照，Codex在共享工作区编写 work/edl.json，适配器真实调用固定版本上游视频提取和拼接，再映射整条外部配音及字幕。App按完成清单校验并复制进私人归档，展示成片。该工程不另写一份原生DSL。

首版 video_use 适配器支持单视觉轨连续图片/视频硬切、cover/contain、独立连续配音与字幕。尚无平移缩放、叠加层、转场或背景音乐；需要这些能力时选择现有 native 后端。切换后端保留两边历史文件。

每次精修创建新快照与EDL版本，已完成运行保持幂等。旧运行晚到可归档，但不会替换较新已接受的剪辑。检查报告绑定具体成片；终稿复核同时检查当前输入及归档视频/快照/EDL字节。

## 共享目录部署

保留当前App数据与RunDesk认证。升级RunDesk后构建video-build1.2.0，更新应用的执行环境。将已经存在的专用RunDesk应用工作目录同时挂入App；冻结目录为0700、文件为0600，两边需映射为同一个宿主UID；常规Docker可令App的VIDEO_UID/VIDEO_GID与RunDesk服务用户一致，rootless环境须核对实际映射。

```bash
docker compose --env-file docker/.env \
  -f docker/compose.yaml -f docker/compose.video-use.yaml up -d --build
```

在.env配置VIDEO_EXCHANGE_HOST_WORKSPACE为实际应用工作目录的绝对路径。App通过/exchange查看该目录，Codex继续使用RunDesk原有工作目录；交换路径为相对路径。App私人data、配置与令牌不用作交换目录。部署前后须核对输入清单和指纹；目录存在不等于容器已正确挂载。

详细协议见[EXCHANGE-0.21](EXCHANGE-0.21.md)，升级见[UPGRADING](UPGRADING.md)。

## 验证范围

实际执行Go race回归、Node回执测试、JSDOM与真实HTTP主流程；使用真实FFmpeg和固定上游源码验证静态图片、无声视频、连续WAV、SRT、输出抽帧、完成清单、私有归档及终稿复核。生成服务使用模拟HTTP提供者验证真实图片字节和参数传递。RunDesk侧执行适配器/构建配置Python测试、Go回归和内置模板一致性检查。

结果保存在validation-0.21.json、tests-0.21.jsonl及dom-validation-0.21.json。JSDOM不替代图形浏览器验收；没有在此环境实测Docker镜像构建、真实Codex自主制作、真实生成模型或IndexTTS模型音质。Windows仅交叉编译。
