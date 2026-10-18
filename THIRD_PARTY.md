# 外部依赖

本仓库自有 Go/HTML/JS 代码采用 MIT。未将 Sandbox Agent 或其他管理项目源码打包。

- [eSpeak NG](https://github.com/espeak-ng/espeak-ng)：可选本机 TTS 工具与语音数据，单独安装，遵循其自身许可；本发行包未包含其二进制、动态库或语音数据。
- RunDesk：独立运行，按其发行包安装。此包不包含 RunDesk/Codex 二进制或凭据。
- [HyperFrames](https://github.com/heygen-com/hyperframes)：固定 npm 版本 0.8.87，独立渲染工具。通过 `runtime/package-lock.json` 安装。
- [GSAP](https://gsap.com/)：固定 npm 版本 3.15.0，遵循其发行包许可；不因为被本项目调用而变为 MIT。
- [FFmpeg](https://ffmpeg.org/)：系统依赖，许可取决于安装的构建配置。
- [Noto CJK](https://github.com/notofonts/noto-cjk)：用户安装的字体；本包不包含字体文件。
- Chrome Headless Shell：由 HyperFrames 准备，遵循其自身分发条款。

锁文件包含传递依赖；详细版权和许可见各安装包。发布包含字体、GSAP 或素材的工程文件时，应一并保留相应许可和素材授权信息。
