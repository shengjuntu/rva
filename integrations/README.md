# 当前容器的业务工具配置

RunDesk 的本机实例和 Docker 环境使用独立 Codex home。Video App 首次注册会播种当前环境的初始配置；后来切换到 Docker 时，在 RunDesk 管理界面为当前环境完成以下一次性配置。

1. 安装 `video-project/SKILL.md` 到视频应用实例的 Skill 目录，启用该 Skill。
2. 添加名称为 `video_project` 的 HTTP MCP：

```json
{
  "url": "http://host.docker.internal:3220/api/mcp",
  "http_headers": {"Authorization": "Bearer <App Token>"},
  "enabled": true
}
```

把 `<App Token>` 换成 `scripts/docker.sh token` 显示的值；这是占位说明，不要直接复制占位字符串。自定义端口/部署位置时使用你的 `VIDEO_APP_MCP_URL`。

3. 在该容器完成 Codex 模型认证。在 App 重连/新建当前环境对话，然后“环境准备 → 启动并检查 Codex 容器”。应发现 10 个视频工具。

权限与工具审批在 RunDesk 中按该应用配置。重连不覆盖管理员修改；已有 MCP 中改过的参数、删除/禁用的工具不会被自动恢复。
