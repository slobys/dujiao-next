#!/usr/bin/env python3
"""Dujiao-Next OpenClaw bridge: bounded, least-privilege operations, stdlib only."""
from __future__ import annotations

import argparse
import getpass
import json
import os
import re
import sys
from datetime import date, datetime, time, timedelta
from decimal import Decimal, InvalidOperation
from urllib import error, parse, request
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

AI_READ_ROUTES = frozenset({
    "/api/v1/admin/categories",
    "/api/v1/admin/products",
    "/api/v1/admin/dashboard/inventory-alerts",
    "/api/v1/admin/dashboard/overview",
    "/api/v1/admin/dashboard/rankings",
})

MAX_RESPONSE_BYTES = 2_000_000
USER_AGENT = "dujiao-next-openclaw/1.0"


class BridgeError(Exception):
    """Safe error message; never include credentials or private response bodies."""


class NoRedirect(request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def validate_base_url(raw: str) -> str:
    url = parse.urlsplit(raw.strip())
    try:
        port = url.port  # validates the port and brackets
    except ValueError as exc:
        raise BridgeError("无效的商城地址端口") from exc
    if (
        url.scheme not in ("https", "http")
        or not url.hostname
        or url.username is not None
        or url.password is not None
        or url.path not in ("", "/")
        or url.query
        or url.fragment
        or (port is not None and not (1 <= port <= 65535))
    ):
        raise BridgeError("商城地址必须是 https://域名 或 http://127.0.0.1:端口，不支持路径/账号/参数")
    if url.scheme == "http" and url.hostname not in ("localhost", "127.0.0.1", "::1"):
        raise BridgeError("远程商城只允许 HTTPS；HTTP 仅允许回环地址")
    return f"{url.scheme}://{url.netloc}"


def make_opener():
    # Do not forward admin tokens through environment HTTP_PROXY or cross-origin redirects.
    return request.build_opener(request.ProxyHandler({}), NoRedirect())


def decode_envelope(raw: bytes):
    try:
        envelope = json.loads(raw)
    except (json.JSONDecodeError, UnicodeDecodeError) as exc:
        raise BridgeError("商城返回的不是有效 JSON") from exc
    if not isinstance(envelope, dict) or type(envelope.get("status_code")) is not int:
        raise BridgeError("商城 API 返回结构不符合预期")
    if envelope["status_code"] != 0:
        message = str(envelope.get("msg", "操作失败"))[:150]
        raise BridgeError(f"商城拒绝操作：{message}（业务码 {envelope['status_code']}）")
    return envelope.get("data")


class Client:
    def __init__(self, base_url: str, environ=None, opener=None):
        self.env = os.environ if environ is None else environ
        self.base_url = validate_base_url(base_url)
        self.opener = opener if opener is not None else make_opener()
        self._token = self.env.get("DUJIAO_ADMIN_TOKEN", "").strip()
        self._ai_token = self.env.get("DUJIAO_AI_TOKEN", "").strip()

    def _request(self, method: str, path: str, *, payload=None, params=None, token=""):
        if not (path.startswith("/api/v1/admin/") or path in {r.replace("/admin/", "/ai/", 1) for r in AI_READ_ROUTES}):
            raise BridgeError("未授权的商城 API 路径")
        address = self.base_url + path
        if params:
            address += "?" + parse.urlencode(params)
        data = json.dumps(payload, ensure_ascii=False).encode("utf-8") if payload is not None else None
        headers = {"Accept": "application/json", "User-Agent": USER_AGENT}
        if data is not None:
            headers["Content-Type"] = "application/json"
        if token:
            headers["Authorization"] = f"Bearer {token}"
        req = request.Request(address, data=data, headers=headers, method=method)
        try:
            with self.opener.open(req, timeout=15) as resp:
                raw = resp.read(MAX_RESPONSE_BYTES + 1)
                if len(raw) > MAX_RESPONSE_BYTES:
                    raise BridgeError("商城响应过大，已拒绝读取")
        except error.HTTPError as exc:
            # HTTPError contains the full URL: do not stringify it or its body.
            raise BridgeError(f"商城请求失败：HTTP {exc.code}，请检查权限/登录状态") from None
        except (error.URLError, TimeoutError, OSError) as exc:
            raise BridgeError(f"连接商城失败：{type(exc).__name__}（已省略敏感连接信息）") from None
        return decode_envelope(raw)

    def _login(self) -> str:
        username = self.env.get("DUJIAO_ADMIN_USERNAME", "").strip()
        password = self.env.get("DUJIAO_ADMIN_PASSWORD", "")
        if not username or not password:
            raise BridgeError("缺少 DUJIAO_ADMIN_TOKEN；或提供权限受限的 DUJIAO_ADMIN_USERNAME / DUJIAO_ADMIN_PASSWORD")
        result = self._request(
            "POST", "/api/v1/admin/login",
            payload={"username": username, "password": password},
        )
        if not isinstance(result, dict):
            raise BridgeError("登录响应格式无效")
        if result.get("requires_totp"):
            if not sys.stdin.isatty():
                raise BridgeError("此账号启用 2FA：非交互任务不能自动登录。请人工获取短期 JWT 或使用合规的机器凭据")
            code = getpass.getpass("请输入管理员 2FA 动态验证码：")
            if not re.fullmatch(r"\d{6,8}", code):
                raise BridgeError("2FA 动态码格式不正确")
            result = self._request(
                "POST", "/api/v1/admin/login/verify-2fa",
                payload={"challenge_token": result.get("challenge_token"), "code": code},
            )
        token = result.get("token") if isinstance(result, dict) else None
        if not isinstance(token, str) or not token:
            raise BridgeError("管理员登录没有返回访问令牌（可能需要验证码或额外验证）")
        return token

    def call(self, method: str, path: str, *, payload=None, params=None):
        if self._ai_token:
            if method != "GET" or path not in AI_READ_ROUTES or payload is not None:
                raise BridgeError("AI Key 仅能调用限定的只读 API，不能创建、修改或发布商品")
            path = path.replace("/admin/", "/ai/", 1)
            return self._request("GET", path, params=params, token=self._ai_token)
        if not self._token:
            self._token = self._login()
        return self._request(method, path, payload=payload, params=params, token=self._token)


def field_title(raw):
    if isinstance(raw, dict):
        for key in ("zh-CN", "zh-TW", "en-US"):
            if isinstance(raw.get(key), str) and raw[key].strip():
                return raw[key]
    return str(raw) if isinstance(raw, str) else ""


def simple_product(item):
    return {
        "id": item.get("id"),
        "slug": item.get("slug"),
        "title": field_title(item.get("title")),
        "price_amount": item.get("price_amount"),
        "is_active": item.get("is_active"),
        "fulfillment_type": item.get("fulfillment_type"),
        "manual_stock_total": item.get("manual_stock_total"),
    }


def draft_payload(args):
    if not 0 < args.category_id < 2**31:
        raise BridgeError("category-id 必须是有效的正整数")
    if not re.fullmatch(r"[a-z0-9](?:[a-z0-9-]{0,126}[a-z0-9])?", args.slug):
        raise BridgeError("slug 仅可包含小写英文字母、数字、连字符，长度不超过 128")
    title = args.title.strip()
    if not 1 <= len(title) <= 120:
        raise BridgeError("商品名称须为 1-120 个字符")
    try:
        price = Decimal(args.price)
    except InvalidOperation:
        raise BridgeError("价格必须是合法数字") from None
    if not price.is_finite() or not Decimal("0.01") <= price <= Decimal("999999.99") or price.as_tuple().exponent < -2:
        raise BridgeError("价格必须为 0.01～999999.99，最多两位小数")
    if len(args.description) > 1000:
        raise BridgeError("商品简介不能超过 1000 字")
    return {
        "category_id": args.category_id,
        "slug": args.slug,
        "title": {"zh-CN": title},
        "description": {"zh-CN": args.description},
        "price_amount": float(price),
        "fulfillment_type": "manual",
        "manual_stock_total": 0,
        "is_active": False,
    }


def report_window(day: date, tzname: str):
    try:
        timezone = ZoneInfo(tzname)
    except (ZoneInfoNotFoundError, ValueError, KeyError):
        raise BridgeError("未知报表时区，请使用 Asia/Shanghai 等 IANA 时区") from None
    start = datetime.combine(day, time.min, timezone)
    end = datetime.combine(day + timedelta(days=1), time.min, timezone) - timedelta(seconds=1)
    return {
        "range": "custom",
        "from": start.isoformat(timespec="seconds"),
        "to": end.isoformat(timespec="seconds"),
        "tz": tzname,
    }


def select_date(raw: str, tzname: str) -> date:
    try:
        timezone = ZoneInfo(tzname)
    except (ZoneInfoNotFoundError, ValueError, KeyError):
        raise BridgeError("未知报表时区") from None
    if raw == "yesterday":
        return datetime.now(timezone).date() - timedelta(days=1)
    if raw == "today":
        return datetime.now(timezone).date()
    try:
        return date.fromisoformat(raw)
    except ValueError:
        raise BridgeError("日期格式应为 YYYY-MM-DD、today 或 yesterday") from None


def fetch_report(client: Client, day: date, timezone: str):
    window = report_window(day, timezone)
    overview = client.call("GET", "/api/v1/admin/dashboard/overview", params=window)
    rankings = client.call("GET", "/api/v1/admin/dashboard/rankings", params=window)
    if not isinstance(overview, dict) or not isinstance(rankings, dict):
        raise BridgeError("商城报表数据结构不符合预期")
    return {
        "date": day.isoformat(),
        "timezone": timezone,
        "currency": overview.get("currency", ""),
        "kpi": overview.get("kpi", {}),
        "top_products": [
            {"title": item.get("title", ""), "paid_amount": item.get("paid_amount", ""), "quantity": item.get("quantity", 0)}
            for item in rankings.get("top_products", [])[:3] if isinstance(item, dict)
        ],
    }


def format_report(data):
    kpi = data["kpi"]
    cur = data["currency"]
    currency = f"{cur} " if cur else ""
    lines = [
        f"📊 Dujiao-Next 每日营业报表｜{data['date']} ({data['timezone']})",
        f"支付销售额：{currency}{kpi.get('gmv_paid', '—')}",
        f"订单总数：{kpi.get('orders_total', '—')}",
        f"已支付订单：{kpi.get('paid_orders', '—')}",
        f"已完成订单：{kpi.get('completed_orders', '—')}",
        f"待支付订单：{kpi.get('pending_payment_orders', '—')}",
        f"支付失败次数：{kpi.get('payments_failed', '—')}",
        f"利润（按平台规则估算）：{currency}{kpi.get('total_profit', '—')}",
        f"缺货商品：{kpi.get('out_of_stock_products', '—')}｜低库存商品：{kpi.get('low_stock_products', '—')}",
    ]
    if data["top_products"]:
        lines.append("热销商品 TOP 3：")
        for item in data["top_products"]:
            lines.append(f"• {item['title']}：{item['quantity']} 件，{currency}{item['paid_amount']}")
    lines.append("数据口径：平台 Dashboard，按所示时区的本地自然日统计。")
    return "\n".join(lines)


def send_telegram(message: str, environ=None, opener=None):
    env = os.environ if environ is None else environ
    bot = env.get("DUJIAO_TELEGRAM_BOT_TOKEN", "")
    chat = env.get("DUJIAO_TELEGRAM_CHAT_ID", "")
    if not re.fullmatch(r"\d{4,16}:[A-Za-z0-9_-]{20,}", bot) or not re.fullmatch(r"-?\d{5,20}", chat):
        raise BridgeError("缺少有效的 Telegram Bot Token 或 Chat ID 环境变量")
    opener = opener if opener is not None else make_opener()
    payload = parse.urlencode({"chat_id": chat, "text": message, "disable_web_page_preview": "true"}).encode()
    url = f"https://api.telegram.org/bot{bot}/sendMessage"
    req = request.Request(url, data=payload, method="POST", headers={"User-Agent": USER_AGENT})
    try:
        with opener.open(req, timeout=15) as resp:
            response_bytes = resp.read(32769)
            if len(response_bytes) > 32768:
                raise BridgeError("Telegram 响应过大")
        output = json.loads(response_bytes)
        if not isinstance(output, dict) or output.get("ok") is not True:
            raise BridgeError("Telegram 拒绝发送报表")
    except error.HTTPError as exc:
        raise BridgeError(f"Telegram 发送失败：HTTP {exc.code}（已隐藏 Bot Token）") from None
    except (error.URLError, TimeoutError, OSError):
        raise BridgeError("Telegram 连接失败（已隐藏 Bot Token）") from None
    except (ValueError, TypeError):
        raise BridgeError("Telegram 返回内容无法解析") from None


def build_parser():
    parser = argparse.ArgumentParser(description="Dujiao-Next OpenClaw 安全管理助手")
    sub = parser.add_subparsers(dest="command", required=True)

    products = sub.add_parser("products", help="查询商品（仅返回非敏感概要）")
    products.add_argument("--search", default="")
    products.add_argument("--page", type=int, default=1)
    products.add_argument("--page-size", type=int, default=20)

    sub.add_parser("categories", help="查询商品分类")
    sub.add_parser("inventory-alerts", help="查询缺货和低库存清单")

    draft = sub.add_parser("draft-product", help="生成商品草稿；默认仅预览，执行后商品仍为下架")
    draft.add_argument("--category-id", type=int, required=True)
    draft.add_argument("--slug", required=True)
    draft.add_argument("--title", required=True)
    draft.add_argument("--price", required=True)
    draft.add_argument("--description", default="")
    draft.add_argument("--execute", action="store_true", help="明确批准创建下架草稿")
    draft.add_argument("--confirm-slug", default="", help="二次核对即将创建的 slug")

    for name in ("daily-report", "daily-telegram"):
        rep = sub.add_parser(name, help="指定日期营业报表" if name == "daily-report" else "发送报表至 Telegram")
        rep.add_argument("--date", default="yesterday")
        rep.add_argument("--tz", default="Asia/Shanghai")
        if name == "daily-report":
            rep.add_argument("--json", action="store_true")

    return parser


def main(argv=None, environ=None, opener=None):
    args = build_parser().parse_args(argv)
    env = os.environ if environ is None else environ

    if args.command == "draft-product":
        payload = draft_payload(args)
        if not args.execute:
            print(json.dumps({"preview_only": True, "will_publish": False, "payload": payload}, ensure_ascii=False, indent=2))
            return 0
        if args.confirm_slug != args.slug:
            raise BridgeError("创建草稿前须确认：--confirm-slug 必须与 --slug 完全相同")
    base = env.get("DUJIAO_BASE_URL", "")
    if not base:
        raise BridgeError("请配置 DUJIAO_BASE_URL（推荐 HTTPS，或本机回环地址）")
    client = Client(base, environ=env, opener=opener)
    if args.command == "categories":
        items = client.call("GET", "/api/v1/admin/categories")
        print(json.dumps([{"id": c.get("id"), "slug": c.get("slug"), "name": c.get("name"), "is_active": c.get("is_active")} for c in items], ensure_ascii=False))
    elif args.command == "products":
        if not 1 <= args.page <= 10000 or not 1 <= args.page_size <= 50:
            raise BridgeError("页码或每页数量超出范围（每页最多 50）")
        items = client.call("GET", "/api/v1/admin/products", params={"search": args.search[:120], "page": args.page, "page_size": args.page_size})
        print(json.dumps([simple_product(p) for p in items], ensure_ascii=False))
    elif args.command == "inventory-alerts":
        items = client.call("GET", "/api/v1/admin/dashboard/inventory-alerts")
        print(json.dumps([{"product_id": i.get("product_id"), "product_title": field_title(i.get("product_title")), "alert_type": i.get("alert_type"), "available_stock": i.get("available_stock")} for i in items], ensure_ascii=False))
    elif args.command == "draft-product":
        product = client.call("POST", "/api/v1/admin/products", payload=payload)
        if not isinstance(product, dict) or product.get("is_active") is not False:
            raise BridgeError("创建请求已发送，但返回状态无法确认是下架草稿；请立即人工检查")
        print(json.dumps({"created_draft": True, "id": product.get("id"), "slug": product.get("slug"), "is_active": False}, ensure_ascii=False))
    else:
        day = select_date(args.date, args.tz)
        rep = fetch_report(client, day, args.tz)
        if args.command == "daily-report":
            print(json.dumps(rep, ensure_ascii=False) if args.json else format_report(rep))
        else:
            send_telegram(format_report(rep), environ=env, opener=opener)
            print(f"已请求 Telegram 发送 {day} 营业报表。")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except BridgeError as exc:
        print(f"[ERROR] {exc}", file=sys.stderr)
        sys.exit(1)
