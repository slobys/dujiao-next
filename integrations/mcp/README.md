# Dujiao-Next 通用 MCP 接入：OpenClaw / Codex / Claude Code

提供统一的 AI 接入方式：**推荐直接使用 Go 商城内置的、浏览器 OAuth 授权的远程 MCP**，无需 NAS 上的本地 Python/SSH 转发；同时保留使用 [MCP 官方 Python SDK](https://github.com/modelcontextprotocol/python-sdk) 的老版本地 stdio MCP 以兼容离线和内网场景。独立 AI 身份与管理员登录完全隔离，不会因为 MCP 连接而获得商城管理员 JWT。

**客户端接入实测状态（截至 2026-10-08）：** OpenClaw 2026.9.8 已在 NAS 完成 OAuth 授权与 MCP doctor 探测；Codex CLI、Claude Code 的官方 HTTP MCP 命令已核对，但尚未在本商城完成端到端登录实测。面向视频观众的三种客户端教程、回调处理、权限选择和验证方法见 **[MCP 客户端接入教程](../../docs/MCP-CLIENTS.md)**。

## AI 总控 5.0：一键开启／暂停网站 AI 管家

**升级后 AI 总开关默认关闭。** 安装器支持域名和公网 IPv4 两种由 Caddy 自动续期的公信 HTTPS 证书；无需域名时可先用 `sudo env DUJIAO_IP=实际公网IPv4 dujiao-fork configure-ip`，验证真实证书后再设置 MCP。 后台 **系统设置 → AI 接入管理** 中新增三个独立控制：

1. **AI 管家总开关**（`master_enabled`）：控制所有商城明确暴露的 AI 接口，包括新 `/mcp`、OAuth、旧版机器 Key `/api/v1/ai/*`。关闭后新 AI 调用被拒绝，但人类管理员、访客和支付回调正常工作。已有 MCP 会话也无法继续调用；已经开始执行的操作可能完成，已发生的变更不会自动回滚。
2. **远程 MCP**（`enabled`）：填写可信 HTTPS 域名或已签发公信证书的公网 IPv4 根地址并保存。仅开远程 MCP 不等于启动 AI 总开关。关闭远程 MCP 时还会自动清除网站编辑授权。
3. **网站内容编辑**（`website_write_enabled`）：只能在总开关和远程 MCP 都开启时使用。关闭它仅暂停网站内容写入，不阻断其他已经按 Scope 授权的 AI 查询与业务申请。重新启用网站编辑需要你再次确认。

以上总开关只由**有权的系统管理员**操作，数据库内保存状态并写入不包含密钥的变更审计；AI 本身没有开启或关闭它的 MCP 工具。暂停后原密钥只是暂时不能使用，重新开启时有效期内的旧密钥可能恢复；如需彻底断开，请撤销凭证。**直接交给其他 AI 的管理员 JWT、SSH/Root、GitHub 写权限并不是商城的 AI 专用入口，不受此总开关控制，应分别关闭或撤销。**

### 本次新增的真实网站内容管理能力

| 权限（OAuth 授权时默认不勾选） | 工具 | 能力 |
| --- | --- | --- |
| `site:content:read` | `list_website_articles`、`list_website_banners` | 查询网站文章、公告及首页 Banner 配置，不修改内容 |
| `site:content:write` | `create_website_article_draft`、`create_disabled_home_banner` | 创建**未发布**的公告/博客草稿，以及**停用**的首页 Banner 草稿 |

网站写入必须同时满足：总开关 ON、远程 MCP ON、网站编辑 ON、AI OAuth 授权含 `site:content:write`。文章由现有内容应用服务写入，强制 `is_published=false`；首页 Banner 由现有 Banner 服务创建，强制 `is_active=false`、`link_type=none`。文本拒绝 HTML 标签/危险控制字符；Banner 图片只接受本地 `/uploads/...` 路径（校验路径格式，但**不验证文件实际存在**）。AI 不会自动上线、不允许任意 JavaScript/SQL/Shell，也无法自发开启权限。

用法示例：让 AI 创建一篇新公告的未发布草稿、引用已有图片创建停用 Banner，然后由你在商城后台审核、调整并最终上线。

**注意：这里的“全站 AI 管理”仍是逐阶段扩展，并非已经可以操作后台每个按钮。** 此前交付商品草稿及上架人工审批、订单脱敏查询/售后、严格未付款取消和人工批准钱包退款；本批交付跨入口总开关与网站内容草稿。后续网站页面布局、已有内容编辑和部署审批仍需逐项接入，不建议给模型一个不受限的管理员 JWT/Root 接口。
## AI 接入中心 2.0：浏览器授权连接远程 MCP

**推荐新方案：** 不必在 NAS/Windows 安装或运行本目录的 Python 服务。商城在 **同一 Go 进程**内提供官方 SDK 的远程无状态 Streamable HTTP `/mcp`；它**默认关闭**，仅系统管理员可在后台开启，必须填写真实、可信的 HTTPS 域名或公信证书覆盖的公网 IPv4 地址。

升级已经部署的 Fork 时，先在部署商城的云服务器上运行 `sudo dujiao-fork backup` 备份并核验备份，再执行 `sudo dujiao-fork update` **构建包含新 Go 后端和 Vue 管理后台的镜像**；仅执行 `git pull` 不会更新正在运行的镜像。请先使用安装器菜单 9 完成域名或公网 IPv4 HTTPS，并将旧的公开 `18080` HTTP 入口改成仅本机访问，限制云服务商安全组和系统防火墙来源；不要从浏览器公开传输管理员密码。

升级后先在 **AI 接入管理**明确打开上述 **AI 总开关**，再打开商城**系统设置 → AI 接入管理 → 远程 MCP**，检查填写的真实站点 HTTPS 根地址（例如 `https://shop.example.com`，或公信证书覆盖的 `https://公网IPv4`），打开开关并保存。面板会自动显示当前真实域名对应的 `/mcp` 地址，并提供 Codex / Claude Code / OpenClaw 的**复制配置**功能。保存前以及关闭时不会生成可用连接命令。

在需要连接的设备上，只运行对应的客户端命令，无需配 NAS Python、SSH、JWT：

```bash
# Codex
codex mcp add dujiao --url https://shop.example.com/mcp
codex mcp login dujiao

# Claude Code
claude mcp add --transport http dujiao https://shop.example.com/mcp
claude mcp login dujiao
# 或进入 Claude Code 会话，执行 /mcp 并选择登录授权

# OpenClaw
openclaw mcp add dujiao --url https://shop.example.com/mcp --transport streamable-http --auth oauth
openclaw mcp login dujiao
```

首次登录时，AI 客户端会打开一个浏览器授权页面；如果未登录商城，先完成正常管理员登录（包括现有 2FA），再查看**客户端自报名称（未验证身份）、回调 URI 和请求的权限（建议首次只选只读）**，可以取消或只批准其中一部分。授权后浏览器返回客户端，客户端自动保存会话。用户无需把后台密码交给 AI 工具，也不需要将静态 AI Key 配置到客户端。

服务端拒绝包含 Unicode 双向文本控制符、零宽格式符、不可见行/段落分隔符或非法 UTF-8 的客户端显示名称，避免利用文字排版混淆授权对象；但**普通名称（例如 OpenClaw）仍可被任意客户端自报**，名称本身不是身份凭证。陌生回调地址或非本人发起的连接一律拒绝。

服务端使用 OAuth 授权码 + **PKCE S256**、服务端登记/校验的回调 URI、MCP Protected Resource Metadata 与 Authorization Server Metadata、兼容性动态客户端注册（DCR）；令牌绑定到当前商城 `/mcp` 资源，访问 token 有效期 **1 小时**，刷新 token 最长 **30 天**且每次刷新会轮换。令牌与授权码仅存储哈希，可通过撤销对应 AI 凭证立即使原连接失效；关闭远程 MCP 后所有远程工具/授权接口直接返回不可用。OAuth 客户端元数据文件（CIMD）尚未实现；使用不支持 DCR 回退的客户端时可能需要进一步兼容开发。

远程 MCP 仅授予只读 Scope 时可查询商品/分类、库存和营业日报。管理员额外授予相应 Scope 后，才可创建真实下架草稿、申请上架/下架、读取脱敏订单、提交售后审核，以及按严格条件申请钱包退款；实际钱包入账必须由管理员逐笔审批。**不提供任意原支付渠道退款、支付密钥修改、顾客隐私读取或任意代码执行。**启用服务不会让 AI 拥有浏览器的管理员权限。服务器持久化审计记录工具访问及密钥操作，不记录完整 bearer、客户明文内容或响应体。

**注意：** `https://shop.example.com/mcp` 是给 MCP 客户端使用的端点，不是网页，浏览器直接 GET 通常会收到 405。可以先通过 `https://shop.example.com/.well-known/oauth-protected-resource/mcp` 查看授权元数据；此元数据默认关闭时返回 404，开启并 HTTPS 正常时才可访问。自定义域名、CDN 与 TLS 反向代理必须保留原始 Host 和可信的 HTTPS 转发信号。不要用 `--insecure` 跳过证书检查，也不要直接公开 Go 后端 HTTP 端口。

## AI 接入中心 3.0：商品经营操作 + 人工审批

此阶段实现了**可用的端到端经营操作闭环**，而不是开放无条件的商城超级管理员权限。OpenClaw、Codex、Claude Code 可以继续使用 2.0 的 HTTPS 远程 MCP 地址和 OAuth 浏览器授权，不需要 NAS 部署 Python 服务或单独的 MCP 容器。

**新增两个独立的可选权限**（均默认不授权）：

| 权限 | MCP 工具 | 执行规则 |
| --- | --- | --- |
| `catalog:draft:write` | `create_product_draft` | AI 可立即在商城创建**真实商品草稿**；强制下架、手动交付、**库存 0**、无卡密、无支付密钥 |
| `catalog:publish:request` | `request_product_status_change`、`get_product_action_status` | AI 只能**提交**某个商品的上架或下架请求、查询自己发起的请求状态；商品本身不会立即改变 |

保留 2.0 的 `catalog:read`、`inventory:read`、`report:read` 三种只读权限，以及 `preview_product_draft` 的离线草稿预览。拥有只读权限的 AI **看不到**新增的写入/申请工具。浏览器 OAuth 的新写入/申请权限在授权页面**默认不勾选**，需要管理员逐一主动勾选批准；既有客户端重新授权前不会自动获得它们。

后台新增独立菜单：**系统设置 → AI 操作审批**。你可以看到请求来自哪个 AI Key、目标商品、提交时的价格、原上架状态、请求操作、有效期；每条请求点击“批准并执行”或“拒绝”。提交请求后 **24 小时**仍未批准即过期。批准还会在数据库写入时核对提交时的**商品价格、状态及更新时间**：若后来人工修改了商品，旧请求变成冲突、不会覆盖人工更改。系统将审批、操作状态与审计记录持久化；同一请求只允许审批执行一次，异常中断后不自动重试，以免重复操作。

三个参考指令：
- “新建一个 29.90 元的测试商品草稿，写好介绍，**不要上架**。”
- “申请上架刚建立的草稿。提交审核，不要绕过批准。”
- “查询刚才上架申请的状态，并告诉我有没有得到批准。”

**范围说明：** 这只是 3.0 的**第一批实际业务工具**，尚不能让 AI 管理全部订单、调价、退款、支付、卡密或任意服务器 Shell；也没有构建 Codex/Claude Code 的 GitHub 分支/PR/CI/发布审批系统。这些应分别沿同一审批框架逐个实现并测试，避免误以为“AI 已获得整个商城完全控制”。**真实退款、支付密钥、删除数据、生产部署始终必须经过独立审批与权限隔离。**

启用方式仍是先备份、`sudo dujiao-fork update`、在后台开启远程 MCP，再按原来的 Codex/Claude Code/OpenClaw 浏览器授权方式连接。更新后原有旧 Key 的权限不会自动扩大。若想尝试新权限，需要为目标 Agent 单独创建/授权合适的 scope。针对生产环境，建议**先只允许新建下架草稿**，测试通过再启用上架申请功能。

## AI 接入中心 3.0 第二批：订单状态查询与售后人工跟进

3.0 新增两项需主动授权的远程 MCP 权限，**浏览器 OAuth 授权时默认均不勾选**，原有客户端不会自动获得：

| 权限 | MCP 工具 | 行为 |
| --- | --- | --- |
| `orders:read` | `list_order_summaries`、`get_order_summary` | 只读订单编号、状态、金额、币种和时间；**不返回客户邮箱、姓名、IP、交付卡密、订单项秘密、支付凭证、退款账户等字段** |
| `orders:review:request` | `request_order_after_sales_review`、`get_order_after_sales_review_status` | AI 只能提交并追踪自己的售后工单；**不修改真实订单、不支付、不退款、不发货、不取消订单** |

申请工单时，AI 只能选择以下固定的售后原因代码：`refund_review`（建议人工审核退款）、`delivery_delay`（交付延迟）、`payment_exception`（支付异常）、`cancellation_request`（取消订单建议）、`other_exception`（其他异常）。不接受可执行退款金额、任意指令、客户个人资料或自动发货参数。AI 提交后工单最多等待 **48 小时**。

后台新增 **系统设置 → AI 订单售后审核** 菜单，展示请求的订单编号/状态/金额快照与原因，可点击：
1. **接收人工处理**：仅确认由管理员负责跟进，不改变订单状态。接收前会比对订单状态、金额、币种和更新时间；内容已改变的请求会转为“冲突”，必须重新核查。
2. **拒绝**：结束并审计请求。
3. **打开订单管理**：在原有商城订单页面手动核对客户支付事实、交付内容和退款记录，并使用原有的正式资金/售后操作流程。
4. **已完成跟进**：由管理员确认人工处理结束后关闭工单；这**只是工单状态，不代表系统执行退款或发货**。

工单状态 `pending → accepted → resolved` 或 `pending → rejected/conflict`。批准和状态变更都是一次性事务操作，附审计记录；到期、AI 身份撤销或权限不足阻止接收。AI 凭证轮换时，未接收的旧工单自动标记拒绝，防止旧任务被新的令牌继承。**此售后工单模块本身不执行订单更改；下一节的严格未付款取消是独立权限、独立逐单审批，不提供退款、支付或发货工具。**

AI 使用示例（在 OAuth 授权时选择相应权限）：
- “找出待发货和履约中的订单，用订单编号、金额、等待时间列表展示，不要读取客户邮箱或卡密。”
- “检查第 251 号订单状态。若存在交付延迟，创建售后人工审核工单，**不要直接发货或退款**。”
- “查看上一张售后工单是等待审批、等待人工跟进还是已标记完成。”

这一阶段进一步扩展的是 **AI 识别问题、整理待办、推动人工售后的能力**。并未声称 AI 已经能自动关闭财务订单、处理银行卡退款、查询交付卡密或主动联系客户。下一节已为极少数无支付记录的未付款订单增加独立受控取消；有支付尝试的订单和所有实际资金退款仍留在原有管理员流程，绝不能直接修改数据库订单状态。

## AI 接入中心 3.0 第三批：严格未付款订单取消审批

这次增加的是**少数确实能安全自动执行的订单取消**，不是任意订单退款。沿用 Go 远程 MCP 和 OAuth 浏览器登录，不需要额外部署。

**新增权限：** `orders:cancel:request`（浏览器授权时**默认不勾选**，与 `orders:read`、`orders:review:request` 完全独立）。

| 工具 | 行为 |
| --- | --- |
| `request_strict_unpaid_order_cancellation` | AI 只能**提交待审批取消请求**，不直接修改订单 |
| `get_strict_unpaid_order_cancellation_status` | AI 只能查询自己提交的请求状态 |

管理员进入**系统设置 → AI 未付款取消审批**，核对订单编号、金额、申请时间，再点击“批准并取消”或“拒绝”。申请 **1 小时**后失效；每次批准、拒绝、结果写入审计，同一申请只能执行一次。凭证撤销阻止旧请求审批，轮换凭证同时作废旧申请。

### 为什么范围这么严格？

审批后系统在一个数据库事务里**锁定订单并再次核验**，全部满足才调用商城现有取消服务及其库存释放流程：

- 必须仍是独立的 `pending_payment` 订单，且订单编号、金额和更新时间与申请时完全相同。
- **不能出现任何支付尝试**，包括成功、处理中、失败、过期以及已经软删除的付款记录。
- 无钱包付款金额、支付时间、退款金额或历史退款记录，也没有交付/发货记录。
- 无优惠券、推广返利、分销和关联子订单。

**任何一条不符合就拒绝，不会绕过支付校验，也不会退款或自动发货。** 多数已经打开过支付二维码的正常待付款订单依然不能让 AI 自动取消；这是刻意保留的安全边界。此类订单应走原生订单管理和售后流程。

**示例：** “检查订单 #123，如果确实没有任何支付记录，申请严格未付款取消，等我审批；否则发起售后人工审核。”

售后审核工具仍只能提出退款/交付异常建议。**此严格取消工具不会退款。** 4.0 新增的钱包退款提案是独立 scope、单独审批，AI 自身依旧无法直接入账；原支付渠道退款、支付密钥与发货仍没有暴露给 AI。上线真实商店前先检查数据库备份并在预发布环境验证支付并发。
## AI 接入中心 4.0：严格原钱包支付退款审批

第四批在既有 3.0 远程 MCP + 浏览器 OAuth 上增加一条**实际可执行、严格限权的钱包余额退款流程**。注意：这不是银行卡、支付宝、微信等支付渠道的原路退款。

**新增 OAuth 权限：** `orders:wallet-refund:request`，必须在浏览器授权页面由管理员主动勾选，默认关闭。AI 不会得到直接入账或通用退款接口。

| MCP 工具 | 能力 |
| --- | --- |
| `preview_strict_wallet_refund` | 只看指定订单和金额是否符合基本规则（只返回脱敏金额与状态，实际入账前还需事务复核） |
| `request_strict_wallet_refund` | 提交固定金额和原因的 **待审批**请求，绝不在此步骤改变余额 |
| `get_wallet_refund_request_status` | 只查看当前 AI 自己提交的退款申请状态与结果 |

参数：`order_id`、字符串 `amount`（精确到分，如 `20.00`）、原因 `reason`（仅 `customer_request`、`duplicate_purchase`、`undelivered`、`other`）。单次金额 **0.01–500.00 元 CNY**，申请 **30 分钟**后失效。

管理员后台新增 **系统设置 → AI 钱包退款审批**。审批前必须人工核对订单编号、原金额、已退款金额、拟退款额和原因。确认后，服务器才执行真正的**用户钱包余额入账**，并记录钱包交易流水、订单退款记录和审批审计。若不批准，AI 没有权限直接转移资金。

### 真实入账的严格安全规则

- 只允许 **CNY、注册用户、独立订单**，原始支付金额必须 **100% 来自商城钱包**；`online_paid_amount` 必须为零。游客订单、优惠券、推广返利、分销与关联子订单全部拒绝。
- 必须有已支付事实；订单状态限于允许的已支付/履约/已交付/已完成或已部分退款状态。
- **数据库中只要出现任何历史第三方支付尝试**（包括失败、过期或软删除记录）即拒绝；此检查与钱包信用入账在同一数据库事务中进行。
- 订单编号、币种、状态、总金额、已退款金额与更新时间必须与 AI 提交时的快照一致；金额不超过当前剩余可退余额和单次上限。
- 原有退款期限、余额信用、订单状态更新、退款记录与相关会计调整使用商城现有退款领域服务；代码不调用原支付渠道退款，不返回客户个人信息和卡密。
- 系统先**一次性领取并记录审批**，再进入钱包退款事务。审批号生成稳定的流水引用；因服务器异常导致退款已发生但审计结果尚未落库时，会保留“执行中”，不自动重试，以防重复转账。

**这是真实资金操作：** 管理员点击“批准钱包入账”，就可能使用户钱包余额增加。它不代表钱退回原付款账户。用户的真实资金、生产部署或支付配置修改依旧需要明确的人类操作。上线前请先备份，且在**预发布环境**模拟原钱包付款、部分退款、重复审批和并发操作。普通在线支付退款、银行卡/第三方渠道原路退款、非人民币及超额金额仍继续使用原生管理员退款工作流。

**示例请求：** “检查订单 123 是否原始全额钱包支付。如符合条件，申请退回 20.00 元到原商城钱包，原因 customer_request。先让我在后台审批；不符合条件就创建普通售后工单。”

### 撤销 AI 凭证：自动清理待审批任务

后台 **系统设置 → AI 接入管理 → 撤销** 现在会在同一数据库事务中撤销指定凭证，并自动拒绝该凭证提交、仍处于 **pending（未审批）** 的商品上/下架申请、售后审核工单、严格未付款取消申请和钱包退款申请；后面三类中涉及业务操作的请求均不会因撤销而执行。商品状态、订单取消与钱包退款审批列表会以简体中文、繁体中文或英文显示自动拒绝原因；售后审核工单当前仅显示“已拒绝”，需结合该凭证的撤销审计核对原因。

**不会清理其他 AI 身份的任务，也不会回滚已完成操作。** 已进入 `executing` 的操作、已经接收待人工跟进的工单保持原状态，仍需管理员核对真实订单/钱包流水；这不属于自动退款或自动取消。该操作与撤销审计一同提交，写入失败则整体回滚。轮换密钥仍按原规则失效旧待审批任务；商品变更申请及售后审核申请在保存时会重新校验密钥摘要、有效期和权限，以拒绝已撤销或已轮换的旧会话。关闭 AI 总开关只拦截新的 AI 请求，不会批量改写已有审批状态。

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

**旧版本地 Python stdio MCP 工具仍然只有读取与离线草稿预览能力。** 3.0 两个新增的可选写入/申请权限仅在商城原生的远程 Go MCP 中提供。商品草稿由 MCP **完全离线预览**，没有写入入口。Codex/Claude Code 本身的 Git 代码修改能力属于独立的开发/评审/发布权限，不由商城 AI Key 控制。

## 第一步：商城后台创建独立 AI Key

1. 在实际部署商城的 VPS 上先配置**可信 HTTPS 域名**（安装器菜单 9），从 NAS 远程访问 `http://公网IP:18080` 仍被 Python 客户端拒绝。首次开启此功能需更新 Go 后端及 Vue 管理后台；**仅 `git pull` 不会升级运行中的 Docker 镜像**。建议先执行 `sudo dujiao-fork backup` 并确认归档完整，再执行 `sudo dujiao-fork update`（该命令还会自动再备份并重建商城镜像）。数据库迁移会新增两张独立表；生产前请确认备份可恢复。
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
