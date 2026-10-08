# Dujiao-Next 通用 MCP 接入：OpenClaw / Codex / Claude Code

这一层提供**真正的 stdio MCP 服务**，可以供多个 AI 客户端共享商城工具。使用 [MCP 官方 Python SDK](https://github.com/modelcontextprotocol/python-sdk) v2，不新增商城公开 API、不直连数据库，也不要求重新构建 Go/Vue 商城镜像。

## 当前五个工具

| 工具 | 能力 | 写入商城 |
| --- | --- | --- |
| `list_products` | 检索商品名称、价格、状态、Slug、库存摘要 | 否 |
| `list_categories` | 列出商品分类 | 否 |
| `list_inventory_alerts` | 查看库存预警 | 否 |
| `daily_sales_summary` | 查询营业额、订单数、估算利润、热销榜 | 否 |
| `preview_product_draft` | 离线生成默认下架、零库存的商品草稿 JSON | **否** |

**第一阶段不会开放**创建/上架商品、改价、退款、支付配置、卡密、客户隐私、SQL 或任意脚本执行。Codex/Claude Code 可以通过其本来的开发权限修改 Git 仓库，但那属于独立的代码开发/测试/发布流程，不是商城 MCP 权限。

## 第一步：准备商城权限与 HTTPS

1. 在 Vultr 商城配置有效的 **HTTPS 域名**（可使用安装管理菜单 9）。现有 Python API 客户端**拒绝跨机器的裸 HTTP**；不能拿 `http://Vultr公网IP:18080` 作为远程管理 API。
2. 在商城后台的 RBAC 中创建**独立、最小权限的 AI 操作员**。只允许：`GET /admin/products`、`GET /admin/categories`、`GET /admin/dashboard/overview`、`GET /admin/dashboard/rankings`、`GET /admin/dashboard/inventory-alerts`（实际请求 URL 会有 `/api/v1` 前缀）。绝不直接使用超级管理员账号。
3. 本版本通过后台现有 JWT 鉴权；临时 JWT 会过期。若启用 TOTP/验证码，**不会自动绕过认证**。长期可撤销的 AI 专用服务令牌仍需后续开发。

## 第二步：在 NAS 安装（普通用户，不用 root）

在 NAS 的 Fork 仓库根目录执行：

```bash
git pull --ff-only
mkdir -p "$HOME/.venvs"
python3 -m venv "$HOME/.venvs/dujiao-mcp"
"$HOME/.venvs/dujiao-mcp/bin/python" -m pip install -r integrations/mcp/requirements.txt
```

MCP 只需要 Python 3.10+，不需要再构建商城容器。准备一个**仅当前用户可读**的本地凭据文件：

```bash
mkdir -p -m 700 "$HOME/.config"
umask 077
nano "$HOME/.config/dujiao-next-mcp.env"
chmod 600 "$HOME/.config/dujiao-next-mcp.env"
```

凭据内容示例（下面都只是占位符，不要把真实密钥发到聊天或放进 Git）：

```dotenv
DUJIAO_BASE_URL=https://shop.example.com
DUJIAO_ADMIN_TOKEN=本地安全取得的受限管理员短期JWT
```

也可以使用 `DUJIAO_ADMIN_USERNAME` 和 `DUJIAO_ADMIN_PASSWORD`，但仅限低权限专用账号；启用 2FA/CAPTCHA 时仍无法在无人值守任务中自动登录。凭据文件只接受固定的四个配置键，**不使用 shell source/eval**；Unix 权限不为 `0600` 会拒绝。若需要自定义文件路径，可使用环境变量 `DUJIAO_MCP_SECRETS_FILE=/绝对路径/私密文件`。

先本地试运行（正常会等待 MCP 客户端，不会打印 HTTP 页面或启动端口）：

```bash
"$HOME/.venvs/dujiao-mcp/bin/python" "$(pwd)/integrations/mcp/server.py"
# Ctrl+C 退出
```

## 第三步：在 OpenClaw 注册（NAS）

在 NAS 仓库根目录：

```bash
REPO_DIR="$(pwd)"
openclaw mcp add dujiao-next \
  --command "$HOME/.venvs/dujiao-mcp/bin/python" \
  --arg "$REPO_DIR/integrations/mcp/server.py" \
  --include 'list_products,list_categories,list_inventory_alerts,daily_sales_summary,preview_product_draft'
openclaw mcp doctor dujiao-next --probe
openclaw mcp tools dujiao-next
```

也可以进入 OpenClaw 控制界面 **Settings → MCP → Add server**，选 Stdio。注册以后还要确保当前 Agent 的工具策略允许这些工具；单纯注册不意味着所有 Agent 都可调用。原有 OpenClaw Skill 可以继续使用，但推荐逐步统一到 MCP。

## 第四步：Codex 和 Claude Code

### Linux/macOS 同机运行

若 Codex 或 Claude Code 运行在已经安装 MCP 的同一台机器：

```bash
codex mcp add dujiao-next -- /绝对路径/dujiao-mcp/bin/python /绝对路径/dujiao-next/integrations/mcp/server.py
codex mcp list

claude mcp add --scope user dujiao-next -- /绝对路径/dujiao-mcp/bin/python /绝对路径/dujiao-next/integrations/mcp/server.py
claude mcp list
```

Claude Code 会话里可以用 `/mcp` 检查。请将所有 `/绝对路径/` 替换成实际路径，不要照字面执行。

### Windows 上的 Codex 和 Claude Code，通过私网 SSH 复用 NAS

Windows 不必重新保存商城 JWT。让 Codex/Claude Code 以 stdio 方式通过 SSH 在 NAS 上启动受限账户的 Python MCP 程序。先验证**私钥无交互登录**（NAS 请使用你自己受信任的 Tailscale/ZeroTier 地址）：

```powershell
ssh -T -o BatchMode=yes mcp-user@NAS私网地址 echo ok
```

然后使用如下模板，替换账号和 NAS 上的绝对路径：

```powershell
codex mcp add dujiao-next -- ssh -T -o BatchMode=yes mcp-user@NAS私网地址 /home/mcp-user/.venvs/dujiao-mcp/bin/python /home/mcp-user/dujiao-next/integrations/mcp/server.py
codex mcp list

claude mcp add --scope user dujiao-next -- ssh -T -o BatchMode=yes mcp-user@NAS私网地址 /home/mcp-user/.venvs/dujiao-mcp/bin/python /home/mcp-user/dujiao-next/integrations/mcp/server.py
claude mcp list
```

SSH 登录用户**不要是 root**，只授予读取程序及自己凭据的必要权限。不要开放一个没有 OAuth/鉴权的公共 MCP HTTP 端口；不要把密钥明文放到注册命令或仓库。SSH 远端 shell 不应向标准输出打印欢迎词，标准输出专属于 MCP 协议。

官方接入参考：[OpenClaw MCP](https://docs.openclaw.ai/tools/mcp)、[Codex MCP](https://developers.openai.com/learn/docs-mcp)、[Claude Code MCP](https://github.com/modelcontextprotocol/python-sdk/blob/main/docs/get-started/real-host.md)。

## 测试、实例用法与下一阶段

例子：“查看昨天商城销售额、订单量与热销商品”；“列出库存告警”；“帮我制作一个默认下架的新商品草稿，先不要发布”。所有商品读取与营业报表由商城已有后台 API 计算；利润只是 Dashboard 估算而非审计结果。

```bash
"$HOME/.venvs/dujiao-mcp/bin/python" -m unittest discover -s integrations/mcp/tests -v
python3 -m unittest discover -s integrations/openclaw/dujiao-next/tests -v
```

上述测试采用官方 SDK 内存客户端和真实 stdio 子进程，API 请求全部由测试 Mock，**不会连接用户真实商店**。实际连通性需要有正确的限权凭据和安全网络连接。第一阶段仅提供本地 stdio 传输；远程 Streamable HTTP 若要实现，必须先完成 OAuth/授权与访问控制，不能直接暴露公网。

后续再做专门的 AI 机器凭证（可撤销、可轮换、细粒度权限）、审计日志、创建下架商品的人工审批工作流、订单聚合与异常告警，以及 Codex/Claude Code 修改页面后的受控发布。
