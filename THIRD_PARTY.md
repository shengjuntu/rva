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

- [IndexTTS](https://github.com/index-tts/index-tts)：可选外部语音模型与推理实现。本包仅提供自行编写的 HTTP 适配器，不分发模型权重或上游推理源码。模型及原项目遵循其各自许可。
- [vLLM-Omni IndexTTS-2.5 recipe](https://recipes.vllm.ai/IndexTeam/IndexTTS-2.5)：语音请求协议、语言与语速映射参考。

## 设计参考与兼容路径

同时感谢 [OpenMontage](https://github.com/calesthio/OpenMontage) 和 [browser-use/video-use](https://github.com/browser-use/video-use) 的设计启发：前者用于阶段产物、素材组织和检查方法参考，后者用于文字驱动剪辑、精确定位、预览与自检方法参考。设计参考与直接引入实现分别记录，不将参考项目的许可替换成本项目许可。

v0.23 的 HyperFrames 主路径自有代码、数据契约和制作 Skill 独立编写；本包没有包含 OpenMontage 的代码、Schema、Skill 或其他发行资源。这里的感谢和设计说明不是对上游许可证的重新授权。

既有 video_use 是显式选择的可选兼容后端，其生产交换、归档与脚本素材适配保留；不将它描述为新 HyperFrames 主路径的依赖。App 包不包含 video-use 源码归档，RunDesk 可选工具构建提供其固定版本源码、原 MIT 许可证与依赖声明；使用和再分发这些上游资源时须保留适用许可及声明，其他组件继续遵循各自许可。自有适配代码不改变被调用上游组件的许可证。
