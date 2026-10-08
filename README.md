# Dujiao-Next · slobys Fork

基于 [Dujiao-Next](https://github.com/dujiao-next/dujiao-next) 的数字商品商城二次开发版本，包含 **Go 后端、Vue 商城前台和 Vue 管理后台**。

[![CI](https://github.com/slobys/dujiao-next/actions/workflows/ci.yml/badge.svg)](https://github.com/slobys/dujiao-next/actions/workflows/ci.yml) [![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE) [![Docker Compose](https://img.shields.io/badge/Deploy-Docker%20Compose-2496ED?logo=docker&logoColor=white)](scripts/fork-deploy.sh)

> **本仓库是个人维护的 Fork，并非上游官方发行版。** 当前优先完善简易部署和运维；计划中的 AI 管理、自动报表、AI 页面设计等能力**尚未集成**。安装脚本可用性已经过静态和模拟测试，但仍需在目标服务器做完整首次安装验证。

**快速导航：** [一键部署](#-一键部署本-fork) · [管理与更新](#-日常管理) · [数据与备份](#-数据与备份) · [开发指南](#-本地开发) · [与上游的区别](#-与上游的区别)

## ✨ 主要功能

| 类型 | 能力 |
| --- | --- |
| 商品交易 | 商品分类、商品展示、订单、支付与数字商品交付（来自上游） |
| 商城界面 | Vue 3 前台及独立管理后台，多语言支持（来自上游） |
| 权限安全 | 管理员 RBAC、JWT、TOTP 2FA（来自上游） |
| 数据存储 | SQLite 或 PostgreSQL；Redis 用于缓存与异步任务（来自上游） |
| **Fork 增强** | **独立 Docker Compose 源码一键部署、数据持久化、状态/日志/重启、冷备份、源码更新** |

## 🚀 一键部署（本 Fork）

本 Fork **无需先发布 GitHub Release**：安装器会从 `slobys/dujiao-next` 的 `main` 分支克隆代码，并在服务器上构建完整镜像（含前后台）。

### 1. 服务器准备

- 建议：Ubuntu 22.04+ / Debian 12+，x86_64 或 arm64 Linux；其它系统尚未验证。
- 预装 [Docker Engine](https://docs.docker.com/engine/install/) 和 **Docker Compose V2**（命令为 `docker compose`，不是旧版 `docker-compose`）。
- 系统需具备 `git`、`python3`、`openssl`、`tar`、`flock`、`curl`；首次编译会下载 Go / Node 依赖，请预留足够内存、硬盘空间。
- 建议使用干净的测试服务器验证。安装器**不会接管**已有的上游 systemd 安装；如果目标端口已被占用，需要先调整端口。

### 2. 执行安装

先下载脚本、按需检查内容，再执行：

```bash
curl -fsSLo /tmp/dujiao-fork-deploy.sh \
  https://raw.githubusercontent.com/slobys/dujiao-next/main/scripts/fork-deploy.sh
sudo bash /tmp/dujiao-fork-deploy.sh install
```

脚本自动执行：克隆 Fork → 构建单镜像全栈应用 → 生成不同的随机密钥和管理员初始密码 → 启动独立 Redis → 持久化 SQLite、上传文件和日志 → 检查服务健康状态。

**默认地址：** `http://127.0.0.1:18080`（只允许服务器本机访问）。首次成功安装时，终端会显示随机后台路径和管理员初始密码；请立即保存并在首次登录后修改。

安装完成后可在服务器本机检查：

```bash
curl -fsS http://127.0.0.1:18080/health
sudo dujiao-fork status
```

### 3. 域名与 HTTPS

**安装器不自动申请证书或设置反向代理。** 正式上线请通过 Nginx、Nginx Proxy Manager (NPM) 或 Caddy 将 HTTPS 域名转发至本机 `127.0.0.1:18080`，并核对 `config.yml` 中的 `server.trusted_proxies`。

> 如果 NPM 运行在**另一个 Docker 容器**中，该容器内的 `127.0.0.1` 不是宿主机。请按实际 Docker 网络或宿主机地址配置上游，不要直接照搬 `127.0.0.1`。

仅在确实需要从其它容器/机器访问宿主机端口，并已做好防火墙限制时，才在**首次安装前**设置监听地址和端口：

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
└── data/
    ├── config.yml       # 应用配置和密钥
    ├── db/              # SQLite 数据库
    ├── uploads/         # 商品及站点上传文件
    ├── logs/            # 日志
    └── redis/           # Redis 持久化数据

/var/backups/dujiao-next-fork/
└── dujiao-next-fork-*.tar.gz  # 备份归档
```

执行 `sudo dujiao-fork backup` 时会短暂停止应用与 Redis 写入，备份 `.env`、Compose 配置及 `data/`，并校验归档的可读取性。**备份含有密钥和用户数据，必须限制访问、加密异地保存，并定期验证恢复流程。**

首次安装前可用环境变量调整安装目录：

```bash
sudo env DUJIAO_FORK_DIR=/opt/my-dujiao \
  bash /tmp/dujiao-fork-deploy.sh install
```

后续管理命令若使用自定义目录，也需传入同样的 `DUJIAO_FORK_DIR`。不要手工将已有数据文件夹直接覆盖到新安装目录；旧站迁移应单独执行。

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
go test ./...

# 前端请分别在对应目录运行：
cd frontend/user && pnpm run test && pnpm run build
cd ../admin && pnpm run test && pnpm run build
```

生产全栈构建会先将两个 SPA 打包进 `internal/web/dist/{admin,user}`，再使用 `go build -tags release,fullstack` 将页面嵌入 Go 二进制。构建流程见 [Dockerfile](Dockerfile) 与 [.goreleaser.yaml](.goreleaser.yaml)。健康检查入口为 `GET /health`。

**二次开发注意：** 后端遵守分层依赖规则；新增管理 API 需要同步维护 `internal/authz/bootstrap.go` 中的 Casbin 内置角色规则；前后台的用户界面文案须保持简体中文、繁体中文、英语三语一致。详细规则见 [AGENTS.md](AGENTS.md)。SQLite 单连接环境下必须避免事务内部绕过事务句柄查询。

## 🧩 后续规划（尚未实现）

- OpenClaw 专用管理工具：查询/创建商品、库存告警、审批后上架。
- n8n 每日订单与销售分析，通过 Telegram/邮件发送日报。
- Codex 协助装修页面，预览、代码审查及受控发布。

这些是后续二次开发方向，不应将其误认为当前版本已经具备的 AI 自动控制功能。

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
