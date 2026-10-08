# 本地二次开发

[返回精简首页](../README.md) · [AI 编码约束](../AGENTS.md)

这里只面向需要改源码、做前后台二次开发的用户；**只要部署商城，不需要在本机额外安装 Go/Node.js**。

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

生产全栈构建会先将两个 SPA 打包进 `internal/web/dist/{admin,user}`，再使用 `go build -tags release,fullstack` 将页面嵌入 Go 二进制。构建流程见 [Dockerfile](../Dockerfile) 与 [.goreleaser.yaml](../.goreleaser.yaml)。健康检查入口为 `GET /health`。

**二次开发注意：** 后端遵守分层依赖规则；新增管理 API 需要同步维护 `internal/authz/bootstrap.go` 中的 Casbin 内置角色规则；前后台的用户界面文案须保持简体中文、繁体中文、英语三语一致。详细规则见 [AGENTS.md](../AGENTS.md)。SQLite 单连接环境下必须避免事务内部绕过事务句柄查询。
