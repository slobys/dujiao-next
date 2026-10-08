# Dujiao-Next 通用 MCP 接入：OpenClaw / Codex / Claude Code

提供统一的 AI 接入方式：**推荐直接使用 Go 商城内置的、浏览器 OAuth 授权的远程 MCP**，无需 NAS 上的本地 Python/SSH 转发；同时保留使用 [MCP 官方 Python SDK](https://github.com/modelcontextprotocol/python-sdk) 的老版本地 stdio MCP 以兼容离线和内网场景。独立 AI 身份与管理员登录完全隔离，不会因为 MCP 连接而获得商城管理员 JWT。

## AI 接入中心 2.0：浏览器授权连接远程 MCP

**推荐新方案：** 不必在 NAS/Windows 安装或运行本目录的 Python 服务。商城在 **同一 Go 进程**内提供官方 SDK 的远程无状态 Streamable HTTP `/mcp`；它**默认关闭**，仅系统管理员可在后台开启，必须填写真实、可信的 HTTPS 根域名。

升级已经部署的 Fork 时，先在 Vultr 通过 `sudo dujiao-fork backup` 备份并核验备份，再执行 `sudo dujiao-fork update` **构建包含新 Go 后端和 Vue 管理后台的镜像**；仅执行 `git pull` 不会更新正在运行的镜像。请先使用安装器菜单 9 完成域名 HTTPS，并将旧的公开 `18080` HTTP 入口改成仅本机访问，限制 Vultr 防火墙来源；不要从浏览器公开传输管理员密码。

升级后打开商城**系统设置 → AI 接入管理 → 远程 MCP**，检查填写的真实站点 HTTPS 根域名（例如 `https://shop.example.com`），打开开关并保存。面板会自动显示当前真实域名对应的 `/mcp` 地址，并提供 Codex / Claude Code / OpenClaw 的**复制配置**功能。保存前以及关闭时不会生成可用连接命令。

在需要连接的设备上，只运行对应的客户端命令，无需配 NAS Python、SSH、JWT：

```bash
# Codex
codex mcp add dujiao --url https://shop.example.com/mcp
codex mcp login dujiao

# Claude Code
claude mcp add --transport http dujiao https://shop.example.com/mcp
# 进入 Claude Code 会话，执行 /mcp 并选择登录授权

# OpenClaw
openclaw mcp add dujiao --url https://shop.example.com/mcp --transport streamable-http --auth oauth
openclaw mcp login dujiao
```

首次登录时，AI 客户端会打开一个浏览器授权页面；如果未登录商城，先完成正常管理员登录（包括现有 2FA），再查看**真实客户端名称、回调 URI 和只读权限**，可以取消或只批准其中一部分。授权后浏览器返回客户端，客户端自动保存会话。用户无需把后台密码交给 AI 工具，也不需要将静态 AI Key 配置到客户端。

服务端使用 OAuth 授权码 + **PKCE S256**、服务端登记/校验的回调 URI、MCP Protected Resource Metadata 与 Authorization Server Metadata、兼容性动态客户端注册（DCR）；令牌绑定到当前商城 `/mcp` 资源，访问 token 有效期 **1 小时**，刷新 token 最长 **30 天**且每次刷新会轮换。令牌与授权码仅存储哈希，可通过撤销对应 AI 凭证立即使原连接失效；关闭远程 MCP 后所有远程工具/授权接口直接返回不可用。OAuth 客户端元数据文件（CIMD）尚未实现；使用不支持 DCR 回退的客户端时可能需要进一步兼容开发。

远程 MCP 当前仍仅提供商品/分类、库存、营业日报和**离线不写库**的商品草稿预览，不提供退款、支付配置、订单顾客隐私或任意代码执行。启用服务不会让 AI 拥有浏览器的管理员权限。服务器持久化审计记录工具访问及密钥操作，不记录完整 bearer、客户明文内容或响应体。

**注意：** `https://shop.example.com/mcp` 是给 MCP 客户端使用的端点，不是网页，浏览器直接 GET 通常会收到 405。可以先通过 `https://shop.example.com/.well-known/oauth-protected-resource/mcp` 查看授权元数据；此元数据默认关闭时返回 404，开启并 HTTPS 正常时才可访问。自定义域名、CDN 与 TLS 反向代理必须保留原始 Host 和可信的 HTTPS 转发信号。不要用 `--insecure` 跳过证书检查，也不要直接公开 Go 后端 HTTP 端口。

## 可选旧方案：本地 stdio MCP

以下 NAS、Windows、私网 SSH 与独立静态 AI Key 配置仍可作为可选兼容方式；**如果使用了上面的远程 MCP，一律不需要继续这些安装步骤**。

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
