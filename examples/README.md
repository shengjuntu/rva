# 新闻交接演示包

两份 ZIP 由 news2douyin 0.4.1 的 smoke_video 工具实际生成，内容为软件验证演示，不是真实新闻。

先在视频 APP 导入 script_bundle.zip，再在同一项目接收 video_bundle.zip。后者使用离线 eSpeak NG 机械音，720×1280 / 24 fps，约 7.83 秒；不是 Go/HyperFrames 对当前分镜的重新渲染。

导入脚本不需要渲染器；接收成片需 FFprobe。导入成功后可连接 RunDesk 并升级/写入项目工具，再使用原稿和证据辅助制作。

## APP 字幕重渲染演示

subtitled-app.mp4 是 Go APP v0.4.0 采用本目录原片配音/SRT 后重新渲染的结果，1080×1920、30 fps、约 7.83 秒，含 AAC 配音与烧录字幕。该音频由离线 eSpeak NG 生成，属于软件流程演示，不是新闻报道；其机械音不代表生产配音质量。原始 video_bundle.zip 未改变。

## APP 自动配音演示

tts-app.mp4 为 v0.5.0 从脚本正文调用本机 eSpeak NG 重新配音、生成分句字幕后渲染的成片，1080×1920/30 fps，约 7.83 秒；tts-narration.wav 为本次生成的 24 kHz 单声道 PCM 配音。内容仍是软件流程验证，不是真实新闻。

`short_script_bundle.zip` 是 news2douyin 0.15.0 导出器生成的测试制作包：中文目标语言、分段证据、蓝色图片候选。全部为测试数据，非真实新闻。
