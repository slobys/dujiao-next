# Dujiao-Next 通用 MCP 接入：OpenClaw / Codex / Claude Code

这一层提供**真正的 stdio MCP 服务**，可以供多个 AI 客户端共享商城工具。使用 [MCP 官方 Python SDK](https://github.com/modelcontextprotocol/python-sdk) v2；新增的独立 AI 机器凭证由商城 Go 后端签发和校验，**不会赋予管理员 JWT 权限，也不会开放未认证的 MCP 公网端口**。已部署旧版商城的用户首次启用 AI Key 需要备份并更新商城容器；之后各 AI 客户端本地注册 MCP 不需要再次重建商城。

## 当前五个工具

| 工具 | 能力 | 写入商城 |
| --- | --- | --- |
| `list_products` | 检索商品名称、价格、状态、Slug、库存摘要 | 否 |
| `list_categories` | 列出商品分类 | 否 |
| `list_inventory_alerts` | 查看库存预警 | 否 |
| `daily_sales_summary` | 查询营业额、订单数、估算利润、热销榜 | 否 |
| `preview_product_draft` | 离线生成默认下架、零库存的商品草稿 JSON | **否** |

**目前 AI Key 的三个可选权限均为只读。**不允许 AI Key 创建/上架商品、改价、退款、支付配置、读取卡密或客户个人信息、执行 SQL/脚本。商品草稿由 MCP **完全离线预览**，没有写入入口。Codex/Claude Code 本身的 Git 代码修改能力属于独立的开发/评审/发布权限，不由商城 AI Key 控制。

## 第一步：商城后台创建独立 AI Key

1. 在 Vultr 商城先配置**可信 HTTPS 域名**（安装器菜单 9），从 NAS 远程访问 `http://公网IP:18080` 仍被 Python 客户端拒绝。首次开启此功能需更新 Go 后端及 Vue 管理后台；**仅 `git pull` 不会升级运行中的 Docker 镜像**。建议先执行 `sudo dujiao-fork backup` 并确认归档完整，再执行 `sudo dujiao-fork update`（该命令还会自动再备份并重建商城镜像）。数据库迁移会新增两张独立表；生产前请确认备份可恢复。
2. 用拥有 `system_admin` 权限的正常管理员账户登录商城后台，打开**系统设置 → AI 接入管理**。为 OpenClaw、Codex、Claude Code **分别创建**不同的 Key，设置名称、有效期 **1～90 天**，按需勾选 `catalog:read`（商品和分类）、`inventory:read`（库存预警）、`report:read`（经营报表）。不要授予所有人相同的 Key。
3. 点击创建后**完整 Key 只会显示一次**。妥善保存到对应的 NAS/Windows 私密凭据文件；以后只显示名称、Key ID、权限、过期/使用时间。随时可以在后台**撤销**（立即生效）或**轮换**（原 Key 立即失效，新 Key 需要重新保存并更新到客户端）。轮换不会自动延长既有有效期。
4. Go 服务验证 SHA-256 哈希、有效期和逐路由 scope，只接受 `GET /api/v1/ai/products`、`/categories`、`/dashboard/inventory-alerts`、`/dashboard/overview`、`/dashboard/rankings`；机器 Key **不能访问 `/api/v1/admin`，不能伪装管理员 JWT**。后台审计记录创建、轮换、撤销及已授权 API 访问，不保存明文 Key、用户内容或 API 响应体。

历史管理员 JWT 方式暂时保留用于兼容已有任务，但推荐把所有长期自动化迁移到专用 AI Key，避免重复处理 2FA 和登录过期。

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
DUJIAO_AI_TOKEN=从商城后台AI接入管理复制的专用机器密钥
```

优先使用 `DUJIAO_AI_TOKEN`：它不需要后台登录，不会触发旧管理员 JWT 自动登录；实际 scope 和过期时间由商城服务端强制校验。创建/轮换后要修改本地凭据文件并重新启动相关 MCP 会话。若有遗留客户端仍用 `DUJIAO_ADMIN_TOKEN`、`DUJIAO_ADMIN_USERNAME` 或 `DUJIAO_ADMIN_PASSWORD`，依旧受后台 2FA/CAPTCHA/限流与 RBAC 控制。凭据文件只接受明确的固定键，**不执行 shell source/eval**；Unix 上权限过宽会拒绝读取。需要自定义文件路径可使用 `DUJIAO_MCP_SECRETS_FILE=/绝对路径/私密文件`。

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

Windows 不必重新保存商城 AI Key。让 Codex/Claude Code 以 stdio 方式通过 SSH 在 NAS 上启动受限账户的 Python MCP 程序。先验证**私钥无交互登录**（NAS 请使用你自己受信任的 Tailscale/ZeroTier 地址）：

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

**本阶段已实现**可撤销/轮换的独立机器凭证、到期限制、三个只读 scope 和访问审计。下一阶段可以探索管理员审批后创建下架商品草稿、订单异常告警、可选 IP 限制及严格权限下的远程 Streamable HTTP，以及 Codex/Claude Code 修改页面后的受控发布；这些**尚未实现**。
