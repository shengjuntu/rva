# rva 合并记录

基线为 https://github.com/shengjuntu/rva 的 master `065e4c797ac249901bb138db2368ad31a7202d13`。仓库 v0.22.0 快照与本地 v0.22.0 源码一致，因此在用户分支上依次应用本地 `c941c55`、`749bb5f`、`d464f51`，保留用户提交历史，再提交本版集成修复。

保留 IndexTTS 核心与 Python 适配器，保留 comfyh3/workflow.go 的实际模型、节点图及原生音频。保留默认+18dB修复，增加可配置增益及失败显式处理。v0.23.1 的极简工作台、HyperFrames、自主制作指令、对话回执与历史版本能力一起合入。没有修改 RunDesk 或 Kun。

完整交付包可按 UPGRADING.md 替换程序/源码，保留已有 data 和 docker/.env。包中包含 Linux amd64 与 Windows amd64 二进制；Windows 未实机运行。验证证据见 validation-0.24.0.json。

## 保留 Git 历史

交付 ZIP 的 merge/rva-v0.24.0.bundle 包含以上述 rva master 为前提的增量提交。现有仓库先保存自己的未提交修改，再读取包内分支：

```bash
git bundle verify /absolute/path/merge/rva-v0.24.0.bundle
git fetch /absolute/path/merge/rva-v0.24.0.bundle refs/heads/merge/rva-indextts-comfyui-0.24.0:refs/heads/integration/video-app-v0.24.0
git switch integration/video-app-v0.24.0
```

如果当前 master 仍是 `065e4c7`，本集成分支是其直接后继。若之后已有新提交，正常合并并解决实际冲突，不强制覆盖仓库。该离线交付包生成时尚未推送，随后按用户授权将 a3e8d20 推送到了原仓库 master。包中 bundle 保留为离线导入与审阅材料。
