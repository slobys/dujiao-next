# Dujiao-Next：OpenClaw / n8n 集成（第一阶段）

这是**独立的辅助脚本和 OpenClaw Skill**，不修改支付流程。现支持商城后台**系统设置 → AI 接入管理**创建的机器 Key，走独立 `/api/v1/ai/` 只读路由；旧版低权限管理员 JWT 仍兼容。Python 3 标准库即可运行。通用 MCP 连接方式见 [MCP 指南](../mcp/README.md)。

## 1. 安装 OpenClaw Skill

在 NAS 上克隆/拉取此 Fork，在**运行 OpenClaw 的同一用户账户**下执行：

```bash
mkdir -p "$HOME/.openclaw/workspace/skills"
cp -a integrations/openclaw/dujiao-next "$HOME/.openclaw/workspace/skills/"
python3 "$HOME/.openclaw/workspace/skills/dujiao-next/dujiao.py" --help
openclaw skills list
```

如果 OpenClaw 使用自定义 workspace 或沙箱路径，请把整个 `dujiao-next` 目录放在该 Agent 的 `skills/` 目录，确保 Python 可执行权限和网络连通性。重新开始会话使 Skill 生效。Skill 没有接管管理员登录和机器授权：你需要配置受限账户或临时令牌。

## 2. 连接管理员 API

**优先**在商城后台的 AI 接入管理创建独立只读机器 Key，为每个 AI 工具分配 `catalog:read`、`inventory:read` 和/或 `report:read` 及 1–90 天有效期。密钥只显示一次、可撤销/轮换；不需要保存管理员账号密码。旧版受限管理员 JWT 方式保留兼容，但不适合长期无人值守；严禁使用超级管理员或 root 凭据。

不把凭据提交到 Git、不通过聊天发送密码；在 NAS 的受限进程环境中注入这些环境变量：

```bash
DUJIAO_BASE_URL=https://shop.example.com
DUJIAO_AI_TOKEN=<商城后台创建的只读AI Key>
```

如果需要自动登录，可使用 **权限受限的专用管理员账号**通过 `DUJIAO_ADMIN_USERNAME` 和 `DUJIAO_ADMIN_PASSWORD` 登录，系统会遵守现有登录限流、CAPTCHA、2FA 检查：**含交互挑战时无人值守任务将失败，不会自动绕过**。保管好凭据并限制 API 来源 IP；无安全的机器凭证方案时不要让机器人长期持有后台密码。HTTP 仅支持本机 `localhost/127.0.0.1/::1`，外网必须 HTTPS；不会跟随 HTTP 跳转或使用环境代理传输 JWT。

环境变量名 | 说明
--- | ---
`DUJIAO_BASE_URL` | 商城根地址（不含 `/admin` 路径）
`DUJIAO_AI_TOKEN` | 推荐，独立的只读机器 Key；撤销/过期后立即失效
`DUJIAO_ADMIN_TOKEN` | 可选兼容，短期限权管理员 JWT
`DUJIAO_ADMIN_USERNAME` / `DUJIAO_ADMIN_PASSWORD` | 可选，只在无 JWT 时登录
`DUJIAO_TELEGRAM_BOT_TOKEN` | 可选，向 Telegram 发日报
`DUJIAO_TELEGRAM_CHAT_ID` | 可选，日报接收对象 ID

> 勿把 token 放在命令行参数、工作流节点文本、OpenClaw 提示或聊天内容里。可以使用宿主的私密环境变量注入机制。生产使用应充分限制 Agent 的 exec 权限；Skill 说明**不是**权限隔离边界。

## 3. 试运行

```bash
python3 "$HOME/.openclaw/workspace/skills/dujiao-next/dujiao.py" categories
python3 "$HOME/.openclaw/workspace/skills/dujiao-next/dujiao.py" products --search "测试"
python3 "$HOME/.openclaw/workspace/skills/dujiao-next/dujiao.py" inventory-alerts
python3 "$HOME/.openclaw/workspace/skills/dujiao-next/dujiao.py" daily-report --date yesterday --tz Asia/Shanghai
```

新建商品分两步（**默认预览、不写入**）：

```bash
python3 dujiao.py draft-product --category-id 1 --slug sample-001 --title "演示月卡" --price 29.90
# 用户确认字段正确后才能执行：
python3 dujiao.py draft-product --category-id 1 --slug sample-001 --title "演示月卡" --price 29.90 --execute --confirm-slug sample-001
```

执行后商品为**下架、人工交付、库存 0**。后续通过商城后台补充发货内容、SKU、图片与支付方式，并由人手动上架。不要让 Agent 执行任何自动发布操作。

**重要：** 使用 `DUJIAO_AI_TOKEN` 时，以上 `--execute` 即使手工加上也会被 API 客户端拒绝，AI Key 只有只读权限。草稿写入只属于旧版获得人工确认且有专门商品创建 RBAC 权限的管理员 JWT 流程；推荐改为在商城后台由人工创建/发布。

## 4. 每天 09:00 自动发 Telegram 报表（n8n）

本仓库提供 `n8n-daily-telegram.json` 工作流示例：

1. 在 n8n 中导入该 JSON；手动为 SSH 节点配置一个**只能登录 NAS 限权账号**的 SSH 私钥凭据；先执行一次手动测试。
2. 工作流时区设为 `Asia/Shanghai`，Schedule Trigger 每天 **09:00** 启动；通过 SSH 在 NAS 上运行本 Skill 的 `daily-telegram --date yesterday`。
3. NAS 用户的私有 `~/.config/dujiao-next-ai.env` 文件（文件权限 `0600`）保存环境变量，用 `set -a; . file; set +a` 在 SSH 目标机装载。**只从可信本地文件读取**；不要把文件复制到公开目录或工作流 JSON。
4. n8n 的 SSH 连接失败、报表 API 401、2FA/CAPTCHA 挑战或 Telegram 失败时应报警/人工处理，不会虚构营业数据或自动关闭商城安全机制。
5. 凭据和连接验证完成后，才启用 n8n workflow；示例导入状态默认为停用。

`n8n` 2.x 默认禁用高风险 **Execute Command** 节点，因此此模板使用 SSH，命令在你指定的 NAS 登录账户中执行，而不是 n8n 容器内执行。若 OpenClaw 与商城安装在不同主机，使用正确的 HTTPS 地址或经认证的内部网络连接。

> AI 接入管理现在已提供到期、轮换、撤销和访问审计；建议各 AI/工作流分别使用独立只读 Key，并在到期前手工创建新凭证。密钥轮换和失效需要同步更新 NAS 的私密凭据文件；如发送失败，请建立监控而不是关闭 2FA。

## 5. 运行测试

```bash
python3 -m unittest discover -s integrations/openclaw/dujiao-next/tests -v
```
