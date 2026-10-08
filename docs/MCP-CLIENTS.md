# OpenClaw、Codex、Claude Code：MCP 接入教程

[返回 README](../README.md) · [AI 权限与安全说明](../integrations/mcp/README.md)

三种客户端共用商城的 **HTTPS MCP** 地址，但**各自独立完成 OAuth 授权**。连接时不会把管理员密码、JWT 或 SSH 权限交给模型，客户端只能使用你在商城授权页面批准的业务工具。

## 第一步：在商城启用远程 MCP

升级到本 Fork 的 AI 总控 5.0 后，进入 **系统设置 → AI 接入管理**：

1. **远程 MCP：开启**，填写可信 HTTPS 域名 `https://shop.example.com`；或者在取得浏览器认可的 IP 证书后填写 `https://你的公网IPv4`。
2. **AI 管家总开关：开启**。升级后默认关闭；只打开远程 MCP 并不能让 AI 访问。
3. 如果要让 AI 创建文章、公告、Banner 草稿，可再开启**网站内容编辑**。
4. 首次 OAuth 授权只勾选查询权限（例如 `catalog:read`、`inventory:read`、`report:read`）；跑通后再按需授予创建草稿、申请操作等权限。

所有客户端连接地址格式相同，**域名 / 公网 IPv4 二选一**：

```text
https://shop.example.com/mcp
https://你的公网IPv4/mcp
```

第一种需要可信域名证书；第二种需要 Let's Encrypt 签发、包含对应 IP SAN 的公信短期证书（**160 小时**）及 Caddy 自动续期。**证书实际签发且严格 TLS 验证成功后**才能使用公网 IP MCP。更换服务器 IP 后须重签证书、更新客户端地址；不要使用 `--insecure` 绕过证书校验。不需要在 NAS 或电脑上额外安装商城的 Python MCP 服务。浏览器直接打开 `/mcp` 可能返回 HTTP 405，并不代表服务损坏。

## 第二步：按客户端选择一种方式

### A. OpenClaw — 已验证

**已验证的范围：** NAS 上 OpenClaw `2026.9.8`，浏览器 OAuth 授权成功，`openclaw mcp doctor dujiao --probe` 返回 `dujiao: ok`。此测试不代表所有资金和商品写入操作都已经过生产实测。

```bash
openclaw mcp add dujiao \
  --url https://shop.example.com/mcp \
  --transport streamable-http \
  --auth oauth

openclaw mcp login dujiao
openclaw mcp status --verbose
openclaw mcp doctor dujiao --probe
```

**常见回调问题：** 如果 OpenClaw 在 NAS 上运行，而授权浏览器在 Windows 上，当浏览器跳到 `http://127.0.0.1:PORT/oauth/callback?...` 时，`127.0.0.1` 是 Windows 自己，并非 NAS，会出现 `ERR_CONNECTION_REFUSED`。

OpenClaw 2026.9.8 在终端提供人工粘贴一次性授权码的方法。根据终端提示，复制回调 URL 中 `code=` 后面、下一个 `&` 前面的**完整** `djcode_...` 值，并在 NAS 上执行：

```bash
openclaw mcp login dujiao --code '粘贴新的、完整的 djcode_ 授权码'
```

**不要公开授权码、包含 `code=` 的完整 URL 或 OAuth Token。** 如果录屏不小心暴露，重新授权并撤销可疑凭证。避免同时启动多个重叠的登录流程。

### B. Codex CLI — 官方命令已核对，待实机连接验证

在准备使用 Codex 的 Windows/macOS/Linux 电脑运行：

```bash
codex mcp add dujiao --url https://shop.example.com/mcp
codex mcp login dujiao
codex mcp list
```

随后进入 `codex`，输入 `/mcp` 检查工具。Codex 官方支持远程 Streamable HTTP MCP 和 OAuth。本站 OAuth 实现支持 **DCR 动态客户端注册**，尚未实现 CIMD；如果自动登录选择了不兼容的注册方式，可以在**支持该参数的 Codex 版本**中明确改为：

```bash
codex mcp login dujiao --oauth-client-registration dcr
```

**这是官方支持的命令，但目前没有记录在本商城上完成实际 Codex OAuth 握手的结果。**

[Codex MCP 官方文档](https://developers.openai.com/codex/mcp)

### C. Claude Code — 官方命令已核对，待实机连接验证

在准备使用 Claude Code 的电脑运行：

```bash
claude mcp add --transport http --scope user dujiao https://shop.example.com/mcp
claude mcp list
claude mcp login dujiao
```

`--scope user` 表示该用户的不同 Claude Code 项目均可使用此 MCP。也可以进入 `claude` 会话，用 `/mcp` 查看连接并执行浏览器认证。当前官方 Claude Code 文档支持 `claude mcp login`；旧版本若不支持，可升级或使用 `/mcp`。

**这是官方支持的命令，但目前没有记录在本商城上完成实际 Claude Code OAuth 握手的结果。**

[Claude Code MCP 官方文档](https://code.claude.com/docs/en/mcp)

## 第三步：确认不仅“保存配置”，还真的能使用

在任一客户端对话中输入：

> 请查找 dujiao MCP 工具，调用 list_products 和 list_categories，列出当前商城前 5 个商品及其价格。只读取，不修改。必须基于真实 MCP 返回数据回答，不要编造。

确认真正的商城数据返回后，再尝试申请创建草稿等写入功能。若看不到工具，先检查商城总开关、远程 MCP、OAuth 勾选的 Scope，以及客户端的工具审批设置。

**本项目测试状态：**

| 客户端 | 命令有官方依据 | 在此商城完成实际端到端授权验证 |
| --- | --- | --- |
| OpenClaw 2026.9.8（NAS） | 是 | **已验证：OAuth 保存 + MCP doctor 探测成功** |
| Codex CLI | 是 | **尚未完成实机登录验证** |
| Claude Code | 是 | **尚未完成实机登录验证** |

不要把“官方支持通用 MCP”写成“已经验证所有客户端都能连接本商城”。

## SSH 远程运行时的 OAuth 回调注意事项

浏览器和 MCP 客户端尽量运行在**同一台电脑**，这样 `127.0.0.1` 的回调监听比较容易成功。如果 Codex/Claude Code 在 VPS 或 NAS 的 SSH 会话里运行，Windows 浏览器的 `127.0.0.1` 并不是远程机器。

- **OpenClaw：** 使用上面版本已验证的 `--code` 方法。
- **Claude Code：** 官方文档说明遇到浏览器重定向无法连接时，可按它的**终端提示把完整回调 URL 粘贴回来**。不要把 OpenClaw 的 `--code` 写法照搬到 Claude Code。
- **Codex：** 可以查看官方 `mcp_oauth_callback_url` 和 `mcp_oauth_callback_port` 配置或使用安全 SSH 端口转发；优先在本地电脑试通，勿直接将 OAuth 回调监听端口暴露到公网。

## 暂时不用与永久撤销

暂时不用时，在商城后台点击 **“立即暂停所有 AI”**。这样既保留客户端的 MCP 配置，也阻断商城内置 AI 接口后续调用，正常商城继续运行。

如果不再信任某个客户端，请**同时**在商城后台撤销对应 OAuth/AI 凭证；只删除客户端本地的 MCP 配置，不等于保证服务器端的旧令牌已被撤销。

Claude Code 客户端的连接可以通过 `claude mcp remove dujiao` 删除。Codex 可先运行 `codex mcp --help` 查看当前版本的移除命令；OpenClaw 可检查 `openclaw mcp --help`。移除客户端配置不会卸载你的商城。

**视频教程保护：** 请遮挡管理员初始密码、真实 OAuth 回调中的 `code=`、API Token、私人邮箱、密钥、数据库与包含凭证的终端输出。
