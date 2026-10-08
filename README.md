# Dujiao-Next · slobys Fork

基于 [Dujiao-Next](https://github.com/dujiao-next/dujiao-next) 的数字商品商城二次开发版本，包含 **Go 后端、Vue 商城前台和 Vue 管理后台**。

[![CI](https://github.com/slobys/dujiao-next/actions/workflows/ci.yml/badge.svg)](https://github.com/slobys/dujiao-next/actions/workflows/ci.yml) [![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE) [![Docker Compose](https://img.shields.io/badge/Deploy-Docker%20Compose-2496ED?logo=docker&logoColor=white)](scripts/fork-deploy.sh)

> **本仓库是个人维护的 Fork，并非上游官方发行版。** 已提供菜单式部署工具和第一阶段 OpenClaw / n8n 集成示例。AI 操作仅涵盖安全的商品草稿和汇总查询，尚不支持自动发布页面或长期机器令牌；部署与集成已做本地自动化测试，仍需在目标服务器验证。

**快速导航：** [一键部署](#-一键部署本-fork) · [管理与更新](#-日常管理) · [数据与备份](#-数据与备份) · [AI 接入](#-ai-接入openclaw--n8n-第一阶段) · [开发指南](#-本地开发) · [与上游的区别](#-与上游的区别)

## ✨ 主要功能

| 类型 | 能力 |
| --- | --- |
| 商品交易 | 商品分类、商品展示、订单、支付与数字商品交付（来自上游） |
| 商城界面 | Vue 3 前台及独立管理后台，多语言支持（来自上游） |
| 权限安全 | 管理员 RBAC、JWT、TOTP 2FA（来自上游） |
| 数据存储 | SQLite 或 PostgreSQL；Redis 用于缓存与异步任务（来自上游） |
| **Fork 增强** | **独立 Docker Compose 源码一键部署、数据持久化、状态/日志/重启、冷备份、源码更新** |
| **AI 初版** | **OpenClaw 商品/库存只读查询、下架草稿创建、经营报表与 n8n 定时 Telegram 推送模板** |

## 🚀 一键部署（本 Fork）

本 Fork **无需先发布 GitHub Release**：安装器会从 `slobys/dujiao-next` 的 `main` 分支克隆代码，并在服务器上构建完整镜像（含前后台）。

### 1. 服务器准备（依赖自动安装）

- **无需单独安装 Docker / Docker Compose V2。** 在 Ubuntu **22.04 / 24.04 / 26.04**、Debian **12 / 13**（amd64 或 arm64）上，首次执行安装命令会自动检测并补齐 Docker Engine、Compose V2、Buildx、Git、Python 3、OpenSSL、tar、curl、GnuPG、证书工具等缺失依赖。
- 新系统通过 **Docker 官方签名 APT 软件源**安装 Docker，不调用 `get.docker.com` 的远程安装脚本；如果 Docker、Compose 和 Buildx 已正常安装，直接复用，不重装或升级现有 Docker。
- 如果机器已有 Docker 但缺 Compose/Buildx，只尝试补装缺失的 CLI 插件；APT 预检若发现会卸载/升级 Docker 或容器运行时，**拒绝执行**，保护正在运行的其他容器。
- **需要 root/sudo 和可访问的软件源网络。** 首次获取本安装脚本只需要 `curl` 或 `wget` 其中之一；如果两者都没有，需要借助其他可信方式下载脚本。NAS 等非 Debian/Ubuntu 系统只有在所需工具已存在时才能运行本部署流程，不会自动改动其底层 Docker。
- 安装不会接管上游 systemd 商城，不会修改其他 Docker Compose 项目；如果端口已被占用，请更换 `DUJIAO_PORT`。首次从源码编译会下载 Go/Node 依赖，请预留足够的内存、磁盘和网络带宽。

> 注意：自动安装 Docker 依赖不等于自动配置域名、HTTPS 或防火墙。Docker 的端口映射可能绕过 UFW 等防火墙规则，请遵循 [Docker 官方防火墙说明](https://docs.docker.com/engine/install/ubuntu/#firewall-limitations) 配置访问控制。

### 2. 执行安装

**安装器会自动补齐缺少的软件**。先下载脚本、按需检查内容，再执行以下命令：

```bash
curl -fsSLo /tmp/dujiao-fork-deploy.sh \
  https://raw.githubusercontent.com/slobys/dujiao-next/main/scripts/fork-deploy.sh
sudo bash /tmp/dujiao-fork-deploy.sh install
```

脚本自动执行：检测系统与 Docker → 安装缺失依赖 → 克隆 Fork → 构建单镜像全栈应用 → 生成不同的随机密钥和管理员初始密码 → 启动独立 Redis → 持久化 SQLite、上传文件和日志 → 检查服务健康状态。如果安装 Docker 需要替换机器现有容器运行时，或官方 APT 源不可达，安装会安全停止并给出原因，而不会偷偷卸载已有服务。

**安装成功后会自动显示真实可复制的商城和后台链接**：有可信 HTTPS 域名时优先显示域名，否则在公网监听模式检测服务器公网 IPv4。只有选了“仅本机访问”才输出 `127.0.0.1`。后台初始密码只在首次安装时显示一次，请妥善保管并立即修改。

安装完成后可在服务器本机检查：

```bash
curl -fsS http://127.0.0.1:18080/health
sudo dujiao-fork status
```

### 首次安装选择与可复制链接

在 **SSH 交互终端**执行 `install`，安装器会询问如何访问：

- **1（默认）公网 IP + 端口：** Docker 监听 `0.0.0.0:18080`，安装后通过可信 HTTPS 服务自动检测真实公网 IPv4，直接输出完整商城地址和带随机后台路径的管理地址。公网 HTTP 仅适合初始调试，请尽快通过菜单 **9** 配置域名及 HTTPS。
- **2 仅本机：** Docker 只监听 `127.0.0.1:18080`，适合已经使用 HTTPS 反向代理的环境；公网浏览器无法直接访问。

在自动化、没有交互终端的环境中，为防止意外公开后台，仍**默认仅本机监听**；需要公网访问时明确设置 `DUJIAO_BIND=0.0.0.0`。

```bash
# 无人值守安装且需要通过公网 IP 临时访问：
sudo env DUJIAO_BIND=0.0.0.0 DUJIAO_PORT=18080 \
  bash /tmp/dujiao-fork-deploy.sh install
```

安装成功时输出的链接类似 `http://实际检测的公网IPv4:18080` 和 `http://实际检测的公网IPv4:18080/dj-随机路径`，**不会把 `0.0.0.0` 当成浏览器 URL**。若已配置并通过验证的 HTTPS 域名，则优先显示 `https://你的域名`，包括完整后台路径。公网 IPv4 检测不等于端口从外部已经放通；如公网浏览器仍无法打开，请检查 Vultr 云防火墙、端口映射和是否错误地选择了“仅本机”。

如果公网 IP 自动检测失败，会明确提示而非猜测。可以到 Vultr 控制台核对后手动指定：

```bash
sudo env DUJIAO_PUBLIC_IP=你的真实公网IPv4 dujiao-fork access
```

已安装的商城无需重新部署，就能再次打印完整访问链接：`sudo dujiao-fork access`，或进入管理菜单选择 **11）显示访问链接**。**不会再次显示初始化密码。**
### 3. 一键配置域名 + HTTPS（Caddy 自动申请和续期）

先在域名 DNS 中添加 **A 记录**，例如将 `shop.example.com` 指向 Vultr **公网 IPv4**，在 Vultr 云防火墙和系统防火墙开放 **TCP 80/443**。检查错误的 AAAA/IPv6 记录。使用 Cloudflare 时，首次申请证书建议先用**仅 DNS（灰云）**；启用代理后选 **Full (strict)**，不要使用 Flexible。

已安装本 Fork 的服务器执行：

```bash
sudo dujiao-fork
# 选择 9) 一键绑定域名并申请 HTTPS 证书，按提示输入 shop.example.com
```

也支持自动化命令：

```bash
sudo env DUJIAO_DOMAIN=shop.example.com DUJIAO_ACME_EMAIL=admin@example.com \
  dujiao-fork configure-domain
```

安装器会验证域名格式和 DNS，检测 80/443 是否被已有容器或服务占用，然后新增独立 **Caddy Docker 服务**，先校验配置再启动。Caddy 自动申请公信 CA 证书并负责自动续期与 HTTP → HTTPS 跳转。**只有通过直连 Caddy 的可信 HTTPS 证书链、域名和商城 `/health` 检查，才会报告成功。**证书和 ACME 账户数据会在 `data/caddy/data/` 持久化，且包含在备份中。

HTTPS 成功后，若原商城的 `18080` 仍监听公网，安装器会尝试将它改为 `127.0.0.1`，仅重建应用容器、不重编译、不修改 Redis 或数据库；Caddy 在 Docker 内网通过 `app:8080` 访问商城。若修改端口失败会明确警告。证书签发或域名更换失败，会尽力恢复先前的 HTTPS 配置。

```bash
sudo dujiao-fork https-status    # 校验证书、域名与商城可访问性
sudo dujiao-fork logs            # Caddy + 商城 + Redis 日志
```

如果 80/443 已被 Nginx、Apache、NPM 或其他 Caddy 占用，安装器不会关闭或接管它们；请直接在现有反向代理中配置 HTTPS。**首次 HTTPS 申请必须确保公网能够验证域名。**安装器不会自动修改 Vultr 云防火墙或 Cloudflare DNS。反向代理需要按照实际网络设置 `server.trusted_proxies`；在另一个 Docker 容器中，`127.0.0.1` 不是宿主机。
仅在确实需要从其它容器/机器访问宿主机端口，并已做好防火墙限制时，才在**首次安装时**指定监听地址和端口；**已安装的商城**请使用下方的 `sudo dujiao-fork configure-network` 菜单功能：

```bash
sudo env DUJIAO_BIND=0.0.0.0 DUJIAO_PORT=18080 \
  bash /tmp/dujiao-fork-deploy.sh install
```

**注意：`0.0.0.0` 会扩大端口暴露范围，切勿在没有 TLS、反向代理和访问控制的情况下直接公开管理后台。**

## 🛠 日常管理

安装成功后，以下命令在服务器上使用（均需 `sudo`）：

| 命令 | 作用 |
| --- | --- |
| `sudo dujiao-fork status` | 查看应用及 Redis 容器状态 |
| `sudo dujiao-fork logs` | 查看应用和 Redis 的最近日志 |
| `sudo dujiao-fork restart` | 重启服务并检查健康状态 |
| `sudo dujiao-fork backup` | 停止服务写入、备份并验证归档，随后重启 |
| `sudo dujiao-fork update` | 自动备份 → 更新 Fork `main` → 构建新镜像 → 重启并检查 |
| `sudo dujiao-fork help` | 查看命令与可配置参数 |
| `sudo dujiao-fork configure-network` | 交互式修改监听 IP、端口，自动重建应用，失败尝试回滚 |
| `sudo dujiao-fork configure-domain` | 一键绑定域名、申请 Caddy HTTPS 证书和自动续期 |
| `sudo dujiao-fork https-status` | 检查有效证书和商城 HTTPS 健康状态 |
| `sudo dujiao-fork access` | 输出实际公网 IPv4 或已验证 HTTPS 域名的商城/后台完整链接 |

运行 **`sudo dujiao-fork`**（不带参数）可进入中文交互式菜单；脚本自动化仍可以使用上表的独立命令。菜单不会增加自动卸载、公开数据或修改支付设置等高危操作。

### 修改监听 IP / 端口（已安装的商城）

如果通过公网 IP 无法访问，通常是因为安全默认值 `DUJIAO_BIND=127.0.0.1` **仅允许本机连接**。现在不用手工编辑 `.env`，直接运行：

```bash
sudo dujiao-fork
# 选择 8) 修改监听 IP / 端口
```

也可以使用独立命令：

```bash
sudo dujiao-fork configure-network
```

菜单会显示**当前**监听 IP 和端口，按回车表示保持原值。选择 `0.0.0.0` 或其它非本机回环 IP 时需要在终端输入 `PUBLIC` 再确认。脚本会检查 IP、端口和 Docker Compose 配置，**只重建 `app` 容器**（无需重新编译、不重启 Redis、不清除数据）。重建或健康检查失败时会尝试恢复原先的 `.env` 和应用端口。

如需无人值守地修改（仅供已配置防火墙和 HTTPS 的场景）：

```bash
sudo env DUJIAO_BIND=0.0.0.0 DUJIAO_PORT=18080 DUJIAO_CONFIRM_PUBLIC=YES \
  dujiao-fork configure-network
```

如果不需要公网直连，请保留 `127.0.0.1`，通过同机 Nginx/Caddy 配置 HTTPS。若设置 `0.0.0.0` 并直接用 IP 访问，**HTTP 管理员密码会明文传输**，只能用于临时测试；正式环境应收紧 Vultr 云防火墙规则。已经对外公开过的初始化密码请立即修改并启用管理员 2FA。

现有部署若需要先获得新管理菜单，**只需同步 Git 代码**，不必重建已有 Go/Vue 商城镜像：

```bash
sudo git -C /opt/dujiao-next-fork/src pull --ff-only
sudo dujiao-fork
```


### 更新的行为与限制

- 更新使用 `git fetch` + `git merge --ff-only`，**源码存在未提交改动时直接拒绝**，不会强行覆盖。
- 更新不会重置 `config.yml`、SQLite、Redis AOF 或上传文件；构建的应用标记为 `source`，以防站内自更新错误替换成上游版本。
- **更新失败不会自动回滚数据库/旧镜像。** 数据库迁移可能发生在新版本启动时；务必先确认冷备份可用，再更新生产环境。需要回退时，应同时评估数据库版本和应用代码的兼容性。
- 安装器没有自动卸载/删库命令；**不要直接运行 `docker compose down -v` 或删除数据目录。**

## 💾 数据与备份

默认目录结构：

```text
/opt/dujiao-next-fork/
├── src/                 # 本 Fork 源码（Git 仓库）
├── compose.yaml         # 管理器生成的 Compose 配置
├── .env                 # Redis 密码、监听端口等敏感配置
├── compose.https.yaml  # HTTPS 启用后生成的 Caddy Compose 附加文件
├── .https-domain        # 当前受本管理器维护的域名
└── data/
    ├── config.yml       # 应用配置和密钥
    ├── db/              # SQLite 数据库
    ├── uploads/         # 商品及站点上传文件
    ├── logs/            # 日志
    ├── redis/           # Redis 持久化数据
    └── caddy/           # HTTPS 代理配置、自动续期账户和证书

/var/backups/dujiao-next-fork/
└── dujiao-next-fork-*.tar.gz  # 备份归档
```

执行 `sudo dujiao-fork backup` 时会短暂停止商城与 Redis（启用 HTTPS 时还包括 Caddy），备份 `.env`、Compose 配置、域名、证书及 `data/`，并校验归档的可读取性。**备份含有密钥和用户数据，必须限制访问、加密异地保存，并定期验证恢复流程。**

首次安装前可用环境变量调整安装目录：

```bash
sudo env DUJIAO_FORK_DIR=/opt/my-dujiao \
  bash /tmp/dujiao-fork-deploy.sh install
```

后续管理命令若使用自定义目录，也需传入同样的 `DUJIAO_FORK_DIR`。不要手工将已有数据文件夹直接覆盖到新安装目录；旧站迁移应单独执行。

## 🤖 AI 接入（OpenClaw / n8n 第一阶段）

新增本 Fork 的 [OpenClaw 技能](integrations/openclaw/dujiao-next/SKILL.md)、[Python 管理命令](integrations/openclaw/dujiao-next/dujiao.py) 和 [n8n 每日报表示例](integrations/openclaw/n8n-daily-telegram.json)。此阶段复用商城自带的后台 API，不修改支付、退款和数据库核心逻辑。

目前可实现：**读取商品/分类、查看库存预警、按时区生成每日经营报表、通过 Telegram 发送报表，以及在明确批准后创建“下架且库存为 0”的人工交付商品草稿**。高级 AI 智能调价、自动上架、独立长期机器认证、页面自主发布尚未实现。

在 OpenClaw 的 NAS 工作区安装技能：

```bash
mkdir -p "$HOME/.openclaw/workspace/skills"
cp -a integrations/openclaw/dujiao-next "$HOME/.openclaw/workspace/skills/"
openclaw skills list
```

配置受限管理员 API 凭据后，OpenClaw 可使用：

```bash
python3 "$HOME/.openclaw/workspace/skills/dujiao-next/dujiao.py" categories
python3 "$HOME/.openclaw/workspace/skills/dujiao-next/dujiao.py" daily-report --date yesterday --tz Asia/Shanghai
```

n8n 模板可每天北京时间 09:00 通过 SSH 在 NAS 执行日报发送；导入后仍需自行配置 SSH 凭据、商城授权及 Telegram Token，并由用户启用工作流。**短期 JWT 到期、2FA 或 CAPTCHA 会阻止无人值守登录，现阶段不能保证所有认证配置下的日报都能自动持续发送。**不得为自动化关闭安全验证。

完整设置、权限安全、草稿审批和 n8n 操作步骤见 **[AI 接入说明](integrations/openclaw/README.md)**。

## 💻 本地开发

### 技术栈

| 部分 | 技术 |
| --- | --- |
| 后端 | Go 1.26、Gin、GORM、Viper |
| 数据层 | SQLite / PostgreSQL；Redis / asynq |
| 前台 | Vue 3、Vite、TypeScript、Tailwind CSS |
| 后台 | Vue 3、shadcn-vue、reka-ui |
| 权限 | JWT、Casbin RBAC、TOTP 2FA |
| 构建 | pnpm 10.34.3、GoReleaser、Docker 多阶段构建 |

本地开发需要 Go、Node.js 24、Corepack/pnpm。先从 `config.yml.example` 复制 `config.yml` 并按注释替换占位密钥、设置数据库/Redis 连接后启动：

```bash
git clone https://github.com/slobys/dujiao-next.git
cd dujiao-next
cp config.yml.example config.yml
# 编辑 config.yml，至少设置 app.secret_key / jwt.secret / user_jwt.secret

go run ./cmd/server     # 默认 :8080，仅 API
# 新终端：
cd frontend/user && corepack enable && pnpm install && pnpm run dev    # :5173
# 另一个终端（在仓库根目录执行）：
cd frontend/admin && corepack enable && pnpm install && pnpm run dev   # :5174
```

两个 Vite 开发服务会将 API 请求代理到 `localhost:8080`。**pnpm 命令须在对应的前端子目录执行**；不要用 `pnpm --dir`，避免选错 pnpm 版本。

### 项目结构

```text
cmd/server/              Go 入口与运维子命令
internal/modules/        业务模块（领域 / 应用 / 基础设施 / HTTP）
internal/bootstrap/      模块依赖装配
internal/app/            HTTP、后台任务与组合入口
internal/authz/          权限策略与内置角色
internal/web/            前后台 SPA 静态资源嵌入
frontend/user/           商城前台（含 classic/vault 模板）
frontend/admin/          管理后台
scripts/fork-deploy.sh   本 Fork 专用一键安装管理器
scripts/tests/           安装脚本回归测试
scripts/dujiao-next-manager.sh  上游 systemd 管理器（非 Fork 部署）
```

### 构建与验证

```bash
bash -n scripts/fork-deploy.sh
bash scripts/tests/fork-deploy_test.sh
python3 -m unittest discover -s integrations/openclaw/dujiao-next/tests -v
go test ./...

# 前端请分别在对应目录运行：
cd frontend/user && pnpm run test && pnpm run build
cd ../admin && pnpm run test && pnpm run build
```

生产全栈构建会先将两个 SPA 打包进 `internal/web/dist/{admin,user}`，再使用 `go build -tags release,fullstack` 将页面嵌入 Go 二进制。构建流程见 [Dockerfile](Dockerfile) 与 [.goreleaser.yaml](.goreleaser.yaml)。健康检查入口为 `GET /health`。

**二次开发注意：** 后端遵守分层依赖规则；新增管理 API 需要同步维护 `internal/authz/bootstrap.go` 中的 Casbin 内置角色规则；前后台的用户界面文案须保持简体中文、繁体中文、英语三语一致。详细规则见 [AGENTS.md](AGENTS.md)。SQLite 单连接环境下必须避免事务内部绕过事务句柄查询。

## 🧩 后续规划（尚未实现）

- 为 AI 建立专用且可撤销的长期服务凭证、细粒度权限与审计。
- n8n 连接真实经营环境后的完整链路验证、异常重试与通知监控。
- Codex 修改商城主题并在测试环境预览、审核后发布。

这些是后续开发方向，和上面已经实现的初版工具严格区分。

## 🔀 与上游的区别

| 安装途径 | 下载/构建来源 | 适用场景 |
| --- | --- | --- |
| **本 Fork：`scripts/fork-deploy.sh`** | `slobys/dujiao-next` 的最新 `main` 源码 | 使用本仓库定制代码与后续二次开发 |
| 上游：`scripts/dujiao-next-manager.sh` | `dujiao-next/dujiao-next` GitHub Release | 使用上游官方 Linux/systemd 发行包 |
| 上游 Docker 镜像 `dujiaonext/dujiao-next` | 上游预构建镜像 | 使用上游版本，不会包含本 Fork 的修改 |

两套部署方式**不要在同一目录混用**。原版安装文档见 [Dujiao-Next 官网](https://dujiao-next.com/deploy/)。

## 📄 上游项目与许可证

本项目基于开源仓库 [dujiao-next/dujiao-next](https://github.com/dujiao-next/dujiao-next) 进行二次开发，感谢上游维护者与贡献者。项目以仓库 [LICENSE](LICENSE) 所示的 **GNU GPL v3** 许可条款发布；再分发、修改和商用时请遵守相关开源许可义务。

发现问题可在本 Fork 的 [Issues](https://github.com/slobys/dujiao-next/issues) 提交，建议附上版本/提交号、相关日志（请脱敏）及复现步骤。
