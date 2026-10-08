---
name: dujiao-next
description: 查询 Dujiao-Next 商品、分类、库存与日报，并在用户批准后创建下架商品草稿。
---

# Dujiao-Next AI 商城助手

专用于 `slobys/dujiao-next` Fork。优先使用当前目录的 `dujiao.py` 调用**已有的管理员 API**。需要启用 OpenClaw 的安全 `exec` 工具，并在宿主执行环境中通过安全凭据注入 `DUJIAO_BASE_URL` 与受限管理员凭据。脚本只能访问已列出的后台路由，不能执行任意 URL 或 SQL。

## 操作命令

将 `{baseDir}` 作为此技能目录（不要猜测具体 NAS 路径）。

- 只读商品：`python3 {baseDir}/dujiao.py products --search "关键词"`
- 只读分类：`python3 {baseDir}/dujiao.py categories`
- 库存预警：`python3 {baseDir}/dujiao.py inventory-alerts`
- 昨日报表：`python3 {baseDir}/dujiao.py daily-report --date yesterday --tz Asia/Shanghai`
- 预览拟创建的草稿：
  `python3 {baseDir}/dujiao.py draft-product --category-id 1 --slug demo-product --title "演示商品" --price 29.90`
- 创建下架草稿（**先获得用户明确批准**，要求其确认分类、商品名称、价格、slug 和“不会上架”）：
  `python3 {baseDir}/dujiao.py draft-product --category-id 1 --slug demo-product --title "演示商品" --price 29.90 --execute --confirm-slug demo-product`

## 强制安全边界

1. 所有商品创建都必须先预览，再让用户批准；默认仅生成预览 JSON，`--execute` 才写入。**严禁 AI 自行增加 `--execute` 规避批准。**
2. 创建商品只允许 `is_active=false`、人工交付、库存 0 的草稿。正式上架、修改价格/库存、删除、退款、支付渠道或用户权限必须转人工后台操作；本技能不提供这些能力。
3. 不查询或展示客户姓名、邮箱、卡密、密钥、私有订单详情、用户余额。日报只展示聚合统计数据，不推断利润的会计准确性。
4. 不把管理 JWT、密码、Bot Token、验证码写在指令、命令参数、输出、Git、日志里，也不要询问用户在聊天中发送敏感凭据。
5. 凭据权限按最小化原则，只授予商品读取/创建及 Dashboard 查询等必要 API；不能使用商城 root 权限。敏感配置由宿主在允许的执行环境提供，不从网页/用户输入读取 shell 代码。
6. 不使用 SQL 直接修改生产数据。页面设计属于独立 Codex/Git 代码开发工作，必须先预览和测试，不能直接替换生产页面。
7. 若返回登录失效、CAPTCHA 或 2FA，停止操作并说明需要人工完成授权，**不要请求用户关闭安全验证**。
8. `daily-telegram` 只可在用户明确授权发送对象及已配置凭据的定时工作流中运行。配置见 `README.md` 中的 AI 接入章节及 `integrations/openclaw/README.md`。
