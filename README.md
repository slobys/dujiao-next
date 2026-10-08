# Dujiao-Next · slobys Fork

**一个可以自己部署、还能用 AI 辅助管理的数字商品商城。**

基于开源项目 [Dujiao-Next](https://github.com/dujiao-next/dujiao-next) 二次开发，保留商品、订单、数字卡密、支付、会员和多语言商城，并增加 **一键部署、备份更新、AI 商城管家**。本仓库是个人维护的 Fork，**不是官方发行版**。

[![CI](https://github.com/slobys/dujiao-next/actions/workflows/ci.yml/badge.svg)](https://github.com/slobys/dujiao-next/actions/workflows/ci.yml)　[![GPL-3.0](https://img.shields.io/badge/license-GPL--3.0-blue)](LICENSE)

**快速导航：** [部署教程](docs/DEPLOYMENT.md) · [AI 客户端接入](docs/MCP-CLIENTS.md) · [AI 功能及权限](integrations/mcp/README.md) · [本地开发](docs/DEVELOPMENT.md)

## 能做什么？

- **开商城：** 管理商品、订单、库存和数字商品交付，支持前台与独立后台。
- **一键安装：** 自动检测、补齐 Docker 等依赖，从本 Fork 源码构建，不需要事先下载上游镜像。
- **方便运维：** 中文菜单、更新、日志、服务健康检查、数据库与上传文件冷备份、域名 HTTPS。
- **AI 商城管家：** OpenClaw、Codex、Claude Code 可通过 HTTPS MCP 查询商品/订单、创建草稿、发起售后申请；高风险操作仍须管理员批准。
- **随时暂停 AI：** 后台提供“AI 管家总开关”，关闭后不影响正常商城与人工后台。

**注意：AI 目前不是无条件的全站超级管理员。** 商品上架、严格范围内的取消/钱包退款需独立审核；支付密钥、服务器 Shell、任意代码部署等功能并未开放给模型。

## 服务器配置及兼容性

**Vultr 只是视频演示所用的 VPS。** 阿里云、腾讯云、AWS、Oracle Cloud、Hetzner、DigitalOcean 等标准 Linux 云服务器，满足下列环境要求时也可使用本安装器，**不是逐家实测认证**。

| 项目 | 轻量测试起步参考 | 教程演示／正式使用推荐 |
| --- | --- | --- |
| CPU | **2 vCPU** | **4 vCPU 或更多** |
| 内存 | **4 GB RAM**（建议 2–4 GB Swap） | **8 GB RAM 或更多** |
| 硬盘 | **30 GB SSD** | **50 GB SSD 或更多** |
| 系统 | Ubuntu 22.04 / 24.04 / 26.04 或 Debian 12 / 13 | 同左 |
| 架构 | `amd64`（x86-64）或 `arm64` | 同左 |

> **资源说明：** 以上是部署规划参考，**并非已在不同机型测出的硬性最低值**。本 Fork 安装时会在服务器上编译 Go + 两个 Vue 前端，首次构建比日常运行更吃资源。4 GB 的 VPS 即使有 Swap，也可能因并行构建或下载依赖而内存不足；1–2 GB 内存不建议直接源码构建。

安装脚本需要 **root/sudo**，以及可访问 GitHub、Docker 软件源、Go/Node 依赖仓库的网络。对以上 Ubuntu/Debian 支持自动补装 Docker Engine、Compose V2、Buildx 等。**CentOS/Rocky、Alpine、NAS、OpenWrt、Windows 等不在一键自动安装的明确支持范围内**，不可套用相同指令保证成功。

## 快速入门（首次源码编译可能较久）

### 第一步：一键下载并安装

SSH 登录 **Ubuntu / Debian 云服务器**，复制下面**这一整行命令**执行：

```bash
curl -fsSLo /tmp/dujiao-fork-deploy.sh https://raw.githubusercontent.com/slobys/dujiao-next/main/scripts/fork-deploy.sh && sudo bash /tmp/dujiao-fork-deploy.sh install
```

脚本会自动检查系统、补齐所需依赖、部署商城并生成后台地址及初始管理员密码。下载失败时不会执行安装；建议先确认脚本来自本仓库。

首次安装按提示选择访问方式：**公网 IP + 端口**（仅供临时测试）或**仅本机**（适合已有反向代理）。**初始密码只显示一次，录视频请打码；正式登录后台前应配置 HTTPS。**

### 第二步：打开管理菜单，查看商城地址

安装完成后执行：

```bash
sudo dujiao-fork
```

进入**中文管理菜单**后，选择 **`2` 查看服务状态**，选择 **`11` 查看商城与后台地址**。无需退出菜单，接着完成第三步。如果首次安装选择了“仅本机”模式，需配置 HTTPS 后才能从外部浏览器访问。

默认临时访问端口为 `18080`；公网地址打不开时，检查**云厂商安全组、系统防火墙和端口映射**，不要把 `0.0.0.0` 当作浏览器地址。

### 第三步：通过菜单申请 HTTPS 证书

**继续在第二步打开的管理菜单中操作**：选择 **`9` 申请 HTTPS 证书**，再选 **`1` 域名证书**或 **`2` 公网 IPv4 证书**。域名方式需提前设置 DNS A 记录；IP 方式无需域名，但必须是真实公网 IPv4。

两种方式都需要**公网 TCP 80/443 可达**，证书由 Caddy 自动续期。IP 证书采用 Let's Encrypt `shortlived` 模式，有效期 **160 小时**。申请成功后会自动显示 HTTPS 商城地址；返回菜单选择 **`10`**，还可以检查证书与访问状态。

如果已有 Nginx / Nginx Proxy Manager 占用 80/443，安装器不会抢占端口，请使用现有反向代理配置 HTTPS。详见 [通用 VPS 部署指南](docs/DEPLOYMENT.md)。

## 让 AI 管理商城（可随时关闭）

AI 通过商城内置的 `https://shop.example.com/mcp` 或 `https://你的公网IPv4/mcp` 连接，**无需在商城 VPS 上安装大模型**。OpenClaw/Codex/Claude Code 运行在你自己的电脑或 NAS，网站只提供安全受限的 MCP 接口。

先登录后台 **系统设置 → AI 接入管理**：

1. 配置并开启“**远程 MCP**”，填写真实可信 HTTPS 域名或已取得公信证书的公网 IPv4 地址。
2. 手动开启“**AI 管家总开关**”（安装或升级后默认关闭）。
3. 只有需要 AI 创建网站文章、公告或 Banner 草稿时，才另行开启“**网站内容编辑**”。
4. AI 客户端首次连接时，在浏览器中核对客户端与授权范围；建议先仅授予只读权限。

**三个客户端的接入方式：**

```bash
# OpenClaw（已在 NAS 实际验证 OAuth 与 MCP doctor）
openclaw mcp add dujiao --url https://shop.example.com/mcp --transport streamable-http --auth oauth
openclaw mcp login dujiao

# Codex CLI（官方命令已核对；本商城尚待实机授权测试）
codex mcp add dujiao --url https://shop.example.com/mcp
codex mcp login dujiao

# Claude Code（官方命令已核对；本商城尚待实机授权测试）
claude mcp add --transport http --scope user dujiao https://shop.example.com/mcp
claude mcp login dujiao
```

将 `shop.example.com` 改为**你的实际商城域名**。**不要复制别人的网站域名，也不要把 OAuth 的 `code=`、访问 Token 或管理员密码展示在视频中。** 客户端连接、OAuth 回调失败处理、验证工具调用、移除配置等见 [MCP 客户端接入教程](docs/MCP-CLIENTS.md)。

**暂停 AI：** 后台点“立即暂停所有 AI”即可暂停商城内置 MCP/OAuth/旧 AI Key 接口；不会停止网站。此开关不能撤销单独交给 Agent 的 SSH、GitHub 写权限或管理员 JWT，需去对应服务单独收回。

## 日常管理

| 命令 | 用途 |
| --- | --- |
| `sudo dujiao-fork` | 中文管理菜单 |
| `sudo dujiao-fork status` | 看服务运行状态 |
| `sudo dujiao-fork logs` | 查看日志 |
| `sudo dujiao-fork access` | 找到后台/商城网址 |
| `sudo dujiao-fork backup` | 备份站点数据（会短暂停止服务） |
| `sudo dujiao-fork update` | 先备份再更新源码、构建、重启 |
| `sudo dujiao-fork https-status` | 检查 HTTPS |

**默认路径：** `/opt/dujiao-next-fork`（源码、Compose、SQLite、Redis、上传文件、日志），备份位于 `/var/backups/dujiao-next-fork`。备份可能含密钥和订单数据，须妥善保管。更新**不保证自动回滚数据库迁移**，请先验证备份可恢复。

## 更多资料

- [服务器与部署详解（Vultr / 其他 VPS 通用）](docs/DEPLOYMENT.md)
- [OpenClaw、Codex、Claude Code 接入和验证](docs/MCP-CLIENTS.md)
- [AI 工具权限、订单审批及停用](integrations/mcp/README.md)
- [本地二次开发 / 技术栈](docs/DEVELOPMENT.md) · [AI 编码规范](AGENTS.md)
- [上游 Dujiao-Next](https://github.com/dujiao-next/dujiao-next) · [问题反馈](https://github.com/slobys/dujiao-next/issues)

**注意版本：** 本 Fork 的 `scripts/fork-deploy.sh` 从 `slobys/dujiao-next` 源码构建；上游 `scripts/dujiao-next-manager.sh` 和 `dujiaonext/dujiao-next` 发行镜像**不包含本 Fork 的定制修改**。不同安装方法不要混用。

## 许可

遵守仓库 [GNU GPL v3 许可证](LICENSE)。修改、再分发与商业使用时请遵守对应开源许可义务。
