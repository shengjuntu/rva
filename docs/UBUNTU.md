# Ubuntu 安装指南

建议先在同一台 Ubuntu x86_64 机器、同一普通用户下运行 RunDesk v0.5.4 和本 App。保持 RunDesk 的原有数据目录、Codex 登录和权限策略。视频 App 使用独立数据目录和端口；不要把两个服务的数据目录设成同一个。

## 1. 解压程序

```bash
unzip rundesk-video-app-v0.5.0.zip
cd rundesk-video-app
chmod +x bin/video-app scripts/*.sh
./bin/video-app --version
```

应输出 `RunDesk Video App 0.5.0`。`bin/video-app` 是 Linux amd64 二进制。其他架构可用 Go 1.25+ 运行 `./scripts/build.sh` 从源码构建。包内 Windows amd64 二进制仅经过交叉编译，未实机验证。

## 2. 准备运行环境

需要 Node.js **22+** 和 npm。使用你已有的 Node 安装方式，先检查：

```bash
node --version
npm --version
sudo apt update
sudo apt install -y ffmpeg fonts-noto-cjk
# 可选：自动生成本机预览配音
sudo apt install -y espeak-ng
```

Chrome Headless Shell 还需要 Linux 动态库。Ubuntu 24.04 可安装：

```bash
sudo apt install -y libnss3 libnspr4 libatk1.0-0t64 libatk-bridge2.0-0t64 \
  libcups2t64 libdrm2 libxkbcommon0 libxcomposite1 libxdamage1 libxfixes3 \
  libxrandr2 libgbm1 libasound2t64 libpango-1.0-0 libcairo2
```

Ubuntu 22.04 对应名称通常没有 `t64` 后缀。具体缺失库可以对浏览器可执行文件运行 `ldd /绝对路径/chrome-headless-shell` 检查。

```bash
./scripts/setup-renderer.sh
```

脚本在项目内安装固定版本 HyperFrames 0.8.87、GSAP 3.15.0，并使用 HyperFrames 的 `browser ensure` 准备浏览器。需要访问 npm 和浏览器下载源；不安装全局 npm 包。首次下载后渲染此内置模板不依赖远程字体/CDN。

已有可用浏览器时可指定其绝对路径：

```bash
export HYPERFRAMES_BROWSER_PATH=/实际路径/chrome-headless-shell
```

中文字体默认 `/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc`。不同安装位置可设置：

```bash
export VIDEO_FONT=/实际路径/NotoSansCJKsc-Regular.otf
```

## 3. 启动

先启动你已经配置好的 RunDesk。如果 RunDesk 配置了访问令牌，将同一令牌传给 App；它只在 App 后端使用。

```bash
# 用你实际的 RunDesk 令牌替换；没有开启令牌验证时可以不设置。
read -rs -p 'RunDesk token: ' RUNDESK_TOKEN
export RUNDESK_TOKEN
export RUNDESK_URL=http://127.0.0.1:3210
export VIDEO_DATA="$HOME/rundesk-video-data"
./scripts/start.sh
```

在另一个终端查看首次自动生成的 App 令牌：

```bash
cat "$HOME/rundesk-video-data/access-token"
```

访问 http://127.0.0.1:3220 ，输入该令牌。也可在启动前设置 `VIDEO_APP_TOKEN`，此时使用该变量的值登录，服务不会生成令牌文件。不要在公开问题或截图中贴出真实令牌。

环境变量：

| 变量 | 默认 / 用途 |
| --- | --- |
| VIDEO_DATA | 程序目录下 data；建议设为固定绝对目录 |
| VIDEO_LISTEN | 127.0.0.1:3220，仅回环地址 |
| VIDEO_APP_TOKEN | 缺省自动创建并持久保存 |
| VIDEO_FONT | 上述 Noto CJK 字体文件 |
| VIDEO_ESPEAK | 可选，eSpeak NG 可执行文件绝对路径；默认从 PATH 查找 |
| RUNDESK_URL | http://127.0.0.1:3210 |
| RUNDESK_TOKEN | RunDesk 的后端 API 令牌 |
| HYPERFRAMES_BROWSER_PATH | 可选，已有浏览器绝对路径 |

远程 Ubuntu 通过 SSH 转发访问，保留本机端口：

```bash
ssh -L 3220:127.0.0.1:3220 -L 3210:127.0.0.1:3210 jx@你的服务器
```

在自己电脑访问 `http://127.0.0.1:3220`。这一版未提供公网反向代理配置或多用户权限。

## 4. 完成专用实例配置

1. 创建视频项目，点击“连接 RunDesk 并配置工具”。
2. 点击“打开 RunDesk”，进入实例配置，选择 **视频制作 App**。
3. 按实例页面显示的 **CODEX_HOME 和 Codex 可执行文件**完成登录。典型形式为 `env CODEX_HOME='/该实例的绝对目录' codex login`。不使用 sudo。
4. 如果你使用自定义模型提供方，在该实例配置模型与凭据；新实例不会自动复制默认实例的设置。
5. 在 RunDesk 重新加载空闲连接，检查账号、项目 Skill 和 MCP 的运行状态。
6. 回到视频 App，输入：“根据项目说明规划 4 个镜头，先不要生成视频。”
7. 可在“自动配音”生成并试听采用，也可上传配音或背景音乐；之后，在“声音”区选择音频、设置起点与音量，点击“保存分镜”和“生成视频”。也可以明确让 Agent 调用渲染工具。

手动创建、编辑分镜并渲染不依赖 Codex 登录，适合先单独检查渲染环境。环境弹窗只做程序/文件存在性与 RunDesk 连通检查，不代替真实渲染验证。

## 5. 后台服务（可选）

可用 systemd 用户服务运行。以下假设程序在 `~/apps/rundesk-video-app`、Node/FFmpeg 已在指定 PATH 中；按实际位置修改。

创建 `~/.config/rundesk-video.env`，权限设为 `600`，内容按需填写：

```ini
RUNDESK_URL=http://127.0.0.1:3210
RUNDESK_TOKEN=替换为真实令牌
VIDEO_DATA=/home/你的用户名/rundesk-video-data
```

创建 `~/.config/systemd/user/rundesk-video.service`：

```ini
[Unit]
Description=RunDesk Video App
After=network.target

[Service]
Type=simple
WorkingDirectory=%h/apps/rundesk-video-app
Environment=PATH=/usr/local/bin:/usr/bin:/bin
EnvironmentFile=%h/.config/rundesk-video.env
ExecStart=%h/apps/rundesk-video-app/scripts/start.sh
Restart=on-failure
RestartSec=5
TimeoutStopSec=30

[Install]
WantedBy=default.target
```

若 Node 由 nvm 等工具安装，把 **实际 Node bin 目录**加入该 PATH；systemd 不读取交互 shell 初始化文件。然后：

```bash
systemctl --user daemon-reload
systemctl --user enable --now rundesk-video
journalctl --user -u rundesk-video -f
```

如果需要退出登录后仍运行，由管理员按本机要求启用用户 linger。

## 排障和升级

- `server.lock`：一个数据目录只允许一个 App 服务。正常停止或配置/端口导致的启动失败会移除锁；强制终止留下锁时，先确认原进程已退出，再删除数据目录中的残留 `server.lock`。
- 渲染失败：打开该版本的“日志”，检查 Node 版本、浏览器动态库、字体、FFmpeg。不要把失败当成已完成。
- Codex 未登录：检查 **视频制作 App 专用实例**，默认实例已登录不代表新实例已登录。
- MCP 启动失败：RunDesk 和 App 必须在同一台机器、同一可访问文件环境；App 二进制绝对路径必须可执行；App 服务应保持运行。
- 令牌/路径变更：更换 App 令牌、端口或程序路径后，在项目里点击“重新配置工具”。此操作保留原会话，更新应用管理的 Skill/MCP。专用实例还有运行中的任务时会拒绝更新。
- 更新前停止服务、备份视频 App 整个数据目录，同时保留 RunDesk 数据及专用实例。替换二进制时保持程序绝对路径；之后重新启动。
- 停止 Agent、取消配音与取消渲染分别控制。关闭页面不会停止后台任务；正常停止 App 会取消当前配音与渲染；服务重启后将未完成任务标记为已中断，不自动续跑。
