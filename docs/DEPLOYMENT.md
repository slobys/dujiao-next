# 通用 VPS 部署与维护指南

[返回精简首页](../README.md) · [AI 客户端接入](MCP-CLIENTS.md)

本 Fork 在演示视频里使用 Vultr，但**安装器不依赖 Vultr**。标准 VPS 只要满足系统、架构、SSH、网络和 Docker 条件，通常按同一套命令安装。例如腾讯云、阿里云、AWS、DigitalOcean、Hetzner、Oracle Cloud 等；**这只是适用条件，不表示逐家云厂商已经实测通过**。

## 服务器规格参考

| 场景 | CPU | RAM | SSD |
| --- | --- | --- | --- |
| 轻量测试尝试 | 2 vCPU | 4 GB（建议 2–4 GB Swap） | 30 GB |
| 演示／日常运行推荐 | 4 vCPU | 8 GB | 50 GB 或以上 |

以上是资源规划参考，不是官方压力测试或保证成功的最低门槛。因为安装时会直接在 VPS 上从源码**编译 Go 和两个 Vue 前端**，4 GB 内存仍可能因构建并发和依赖下载而不足；不建议用 1–2 GB VPS 直接构建。运行阶段的资源消耗取决于访问量、数据库和插件等条件。

**明确自动支持的环境：** Ubuntu 22.04 / 24.04 / 26.04、Debian 12 / 13，架构 amd64 / arm64，需 root/sudo、网络访问及可用磁盘。安装器可自动补全 Docker Engine、Docker Compose V2、Buildx 等依赖。其他 Linux、NAS/DSM、Windows、ARM32/OpenWrt 等不属于自动补装适配范围，不能承诺一键成功。

**视频教程的跨厂商做法：** 先展示一台标准 Ubuntu/Debian 服务器，再提醒观众去自己云厂商的“安全组/防火墙”开放 SSH（来自本人管理 IP）以及 HTTPS 的 TCP 80/443。公网 HTTP 18080 只适合临时测试，不要在未配置 HTTPS 的端口输入真实后台密码。IPv4 可能来自 NAT/端口映射，DNS 及 CDN 均由各自服务商管理，脚本不会代替用户修改这些设置。

以下保留完整安装、域名、日志、备份、更新和网络故障排查细节。首次安装只需看前几个小节。

## 一键部署（本 Fork）

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

安装成功时输出的链接类似 `http://实际检测的公网IPv4:18080` 和 `http://实际检测的公网IPv4:18080/dj-随机路径`，**不会把 `0.0.0.0` 当成浏览器 URL**。若已配置并通过验证的 HTTPS 域名，则优先显示 `https://你的域名`，包括完整后台路径。公网 IPv4 检测不等于端口从外部已经放通；如公网浏览器仍无法打开，请检查云服务商安全组、端口映射和是否错误地选择了“仅本机”。

如果公网 IP 自动检测失败，会明确提示而非猜测。可以到云服务商控制台核对后手动指定：

```bash
sudo env DUJIAO_PUBLIC_IP=你的真实公网IPv4 dujiao-fork access
```

已安装的商城无需重新部署，就能再次打印完整访问链接：`sudo dujiao-fork access`，或进入管理菜单选择 **11）显示访问链接**。**不会再次显示初始化密码。**

### 3. 一键申请 HTTPS：域名或公网 IPv4（Caddy 自动续期）

两种 HTTPS 模式均需确保云服务商安全组和系统防火墙开放 **TCP 80/443**；Caddy 容器会负责签发与自动续期。域名模式需要 DNS A 记录指向服务器公网 IPv4；IP 模式无需域名，但必须拥有真实公网可达 IPv4。

已安装本 Fork 的服务器执行：

```bash
sudo dujiao-fork
# 选择菜单 9 → 1) 域名证书  或  2) 公网 IPv4 证书
```

**方式 A：域名 HTTPS（优先推荐）**。先把 DNS A 记录配置到公网 IP。Cloudflare 初次验证遇到问题可用灰云 DNS，启用代理时选择 **Full (strict)** 而非 Flexible。

非交互命令：

```bash
sudo env DUJIAO_DOMAIN=shop.example.com DUJIAO_ACME_EMAIL=admin@example.com \
  dujiao-fork configure-domain
```

**方式 B：公网 IPv4 HTTPS（无需域名）**。仅支持**真实公网可达的 IPv4**，不支持私网、CGNAT、保留地址或 IPv6；需要公网 TCP 80（HTTP-01）和 TCP 443 直达本机：

```bash
sudo env DUJIAO_IP=你的实际公网IPv4 dujiao-fork configure-ip
# 也可以使用菜单 9 → 2) 公网 IPv4 证书
```

IP 模式显式使用 Let's Encrypt **`shortlived`** 证书（有效期 **160 小时**，约 6.7 天），并固定支持该 ACME Profile 的 **Caddy 2.11.7**；不会使用 Caddy 默认的本地自签 IP 证书。**两种模式都是 Caddy 内置自动续期，不需要 Cron**。Caddy 设置 `restart: unless-stopped`，证书及 ACME 账户持久化在 `data/caddy/data/`，配置保存在 `data/caddy/config/`。自动续期仍依赖公网 IPv4 不变、Caddy 持续运行、TCP 80/443 可达、CA 和网络正常；更换公网 IP 后必须重新申请并更新 MCP 客户端的地址。

安装器会检查证书类型、端口、Compose/Caddy 配置，再启动独立的 **Caddy** 容器。**申请结果必须同时通过公信证书链、目标域名/IP SAN 和商城 `/health` 严格校验，才会报告成功。**不会把本地自签证书当成可信证书。证书和 ACME 账户保存在 `data/caddy/data/`；重启及冷备份后仍可继续管理自动续期。

HTTPS 成功后，若原商城的 `18080` 仍监听公网，安装器会尝试将它改为 `127.0.0.1`，仅重建应用容器、不重编译、不修改 Redis 或数据库；Caddy 在 Docker 内网通过 `app:8080` 访问商城。若修改端口失败会明确警告。证书签发或域名/IP 切换失败，会尽力恢复先前的 HTTPS 配置。

```bash
sudo dujiao-fork https-status    # 校验证书链、域名/IP、到期时间与商城可访问性
sudo dujiao-fork logs            # Caddy + 商城 + Redis 日志
```

如果 80/443 已被 Nginx、Apache、NPM 或其他 Caddy 占用，安装器不会关闭或接管它们；请直接在现有反向代理中配置 HTTPS。**首次 HTTPS 申请与自动续期都必须能够通过公网 ACME 验证。** IP 模式仅由本安装器内置 Caddy 管理，无法保证对已有 NPM/Nginx 自动同步证书。安装器不会自动修改云服务商安全组或 Cloudflare DNS。反向代理需要按照实际网络设置 `server.trusted_proxies`；在另一个 Docker 容器中，`127.0.0.1` 不是宿主机。
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
| `sudo dujiao-fork configure-domain` | 域名 HTTPS 证书（自动续期） |
| `sudo dujiao-fork configure-ip` | 公网 IPv4 HTTPS 短期证书（自动续期） |
| `sudo dujiao-fork configure-https` | 交互选择域名或公网 IPv4 模式 |
| `sudo dujiao-fork https-status` | 检查有效证书和商城 HTTPS 健康状态 |
| `sudo dujiao-fork access` | 显示已验证的域名/IP HTTPS 地址，或公网访问地址 |

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

如果不需要公网直连，请保留 `127.0.0.1`，通过同机 Nginx/Caddy 配置 HTTPS。若设置 `0.0.0.0` 并直接用 IP 访问，**HTTP 管理员密码会明文传输**，只能用于临时测试；正式环境应收紧云服务商安全组规则。已经对外公开过的初始化密码请立即修改并启用管理员 2FA。

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
├── .https-domain        # 兼容旧版本的标记文件（域名或公网 IPv4）
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

> **通用提示：** 在非 Vultr 的 VPS 上，替换的是系统镜像、DNS、云安全组名称；安装器命令和 GitHub 仓库地址不变。云平台是否支持端口映射、公网 IPv4、外网 80/443，需要按照服务商的实际网络条件验证。
