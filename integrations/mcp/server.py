#!/usr/bin/env python3
"""Provider-independent Dujiao-Next MCP server (stdio, read-only).

Uses the official MCP Python SDK. No new public ecommerce endpoints, no direct
database access, no arbitrary HTTP, and no tool that modifies the live store.
"""
from __future__ import annotations

import importlib.util
import os
import pathlib
import stat
import threading
from datetime import date
from types import SimpleNamespace
from typing import Any

from mcp.server import MCPServer
from mcp.server.mcpserver.exceptions import ToolError


MCP_NAME = "dujiao-next"
BRIDGE_PATH = pathlib.Path(__file__).resolve().parents[1] / "openclaw" / "dujiao-next" / "dujiao.py"
ALLOWED_ENV = frozenset({
    "DUJIAO_BASE_URL", "DUJIAO_ADMIN_TOKEN",
    "DUJIAO_ADMIN_USERNAME", "DUJIAO_ADMIN_PASSWORD",
})
ALLOWED_ROUTES = frozenset({
    "/api/v1/admin/categories",
    "/api/v1/admin/products",
    "/api/v1/admin/dashboard/inventory-alerts",
    "/api/v1/admin/dashboard/overview",
    "/api/v1/admin/dashboard/rankings",
})
SALES_KEYS = (
    "orders_total", "paid_orders", "completed_orders", "pending_payment_orders",
    "processing_orders", "gmv_paid", "total_cost", "total_profit", "profit_margin",
    "payment_fee", "payments_failed", "payment_success_rate",
    "out_of_stock_products", "low_stock_products", "new_users",
)


def load_private_credentials() -> None:
    """Parse a fixed-key 0600 file as data, never eval/source shell fragments."""
    filename = os.environ.get("DUJIAO_MCP_SECRETS_FILE", "")
    if not filename:
        # Default works for both NAS OpenClaw and SSH-launched MCP clients.
        filename = str(pathlib.Path.home() / ".config" / "dujiao-next-mcp.env")
        if not pathlib.Path(filename).exists():
            return
    path = pathlib.Path(filename).expanduser()
    if not path.is_absolute():
        raise ValueError("MCP 凭据文件必须使用绝对路径")
    try:
        info = path.lstat()
        if not stat.S_ISREG(info.st_mode):
            raise ValueError("MCP 凭据文件必须是普通文件，不接受符号链接")
        if os.name != "nt" and (info.st_mode & 0o077):
            raise ValueError("MCP 凭据文件权限过宽，请 chmod 600")
        if info.st_size > 8192:
            raise ValueError("MCP 凭据文件超过大小上限")
        content = path.read_text(encoding="utf-8")
    except FileNotFoundError as exc:
        raise ValueError("找不到 MCP 凭据文件") from exc
    except PermissionError as exc:
        raise ValueError("无法读取 MCP 凭据文件，请确认运行账号和权限") from exc
    seen: set[str] = set()
    for line in content.splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        key, delimiter, value = line.partition("=")
        if not delimiter or key not in ALLOWED_ENV or key in seen:
            raise ValueError("MCP 凭据文件包含未知或重复字段")
        seen.add(key)
        # Literal values only. Never interpolate or execute their contents.
        os.environ.setdefault(key, value.strip())


load_private_credentials()

_spec = importlib.util.spec_from_file_location("dujiao_next_bridge", BRIDGE_PATH)
if _spec is None or _spec.loader is None:
    raise RuntimeError("无法加载 Dujiao-Next 公共 API 客户端")
bridge = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(bridge)

mcp = MCPServer(
    MCP_NAME,
    description="Dujiao-Next storefront business information (read-only) and offline draft previews",
    instructions=(
        "Only read aggregate business metrics and non-sensitive product/category information. "
        "The draft tool is a local preview; it never creates or publishes a product. "
        "Never request administrator passwords or tokens from users in chat. "
        "All sales numbers are the store's existing dashboard accounting estimates."
    ),
    version="0.1.0",
)

_client_lock = threading.RLock()
_cached_client: Any = None


def _readonly_get(path: str, *, params: dict[str, Any] | None = None) -> Any:
    global _cached_client
    if path not in ALLOWED_ROUTES:
        raise ToolError("禁止访问未批准的商城 API 路径")
    with _client_lock:
        try:
            if _cached_client is None:
                base_url = os.environ.get("DUJIAO_BASE_URL", "")
                if not base_url:
                    raise ToolError("未配置 DUJIAO_BASE_URL")
                _cached_client = bridge.Client(base_url)
            return _cached_client.call("GET", path, params=params)
        except bridge.BridgeError:
            # Avoid leaking raw backend errors, credentials, URLs or private data
            # through MCP tool responses or model-visible exception messages.
            raise ToolError("商城查询失败：请检查 HTTPS、限权账号、令牌有效期和 API 权限") from None


def _as_records(raw: Any) -> list[dict[str, Any]]:
    if not isinstance(raw, list):
        raise ToolError("商城返回格式与预期不符（应为列表）")
    if len(raw) > 1000:
        raise ToolError("商城结果数量过大，拒绝将其发送给 AI")
    if any(not isinstance(item, dict) for item in raw):
        raise ToolError("商城结果包含无效的记录")
    return raw


@mcp.tool()
def list_products(keyword: str = "", page: int = 1, page_size: int = 20) -> list[dict[str, Any]]:
    """只读：检索商品概要（名称、价格、状态、Slug、库存），不返回卡密、客户信息。"""
    if not isinstance(keyword, str) or len(keyword) > 120:
        raise ToolError("搜索关键词不能超过 120 字符")
    if type(page) is not int or type(page_size) is not int:
        raise ToolError("页码必须是整数")
    if not (1 <= page <= 10000 and 1 <= page_size <= 50):
        raise ToolError("页码范围 1–10000、每页最多 50")
    rows = _as_records(_readonly_get("/api/v1/admin/products", params={
        "search": keyword, "page": page, "page_size": page_size,
    }))
    return [bridge.simple_product(row) for row in rows]


@mcp.tool()
def list_categories() -> list[dict[str, Any]]:
    """只读：列出商品分类名称、ID、Slug 和启用状态。"""
    rows = _as_records(_readonly_get("/api/v1/admin/categories"))
    return [
        {
            "id": row.get("id"),
            "slug": row.get("slug"),
            "name": bridge.field_title(row.get("name")),
            "is_active": row.get("is_active"),
        }
        for row in rows
    ]


@mcp.tool()
def list_inventory_alerts() -> list[dict[str, Any]]:
    """只读：查询缺货和低库存告警，不读取或返回卡密内容。"""
    rows = _as_records(_readonly_get("/api/v1/admin/dashboard/inventory-alerts"))
    return [
        {
            "product_id": row.get("product_id"),
            "sku_id": row.get("sku_id"),
            "product_title": bridge.field_title(row.get("product_title")),
            "alert_type": row.get("alert_type"),
            "available_stock": row.get("available_stock"),
        }
        for row in rows
    ]


class _ReadOnlyReportingClient:
    def call(self, method: str, path: str, *, params: dict[str, Any] | None = None) -> Any:
        if method != "GET":
            raise ToolError("营业数据仅允许 GET")
        return _readonly_get(path, params=params)


@mcp.tool()
def daily_sales_summary(day: str = "yesterday", timezone: str = "Asia/Shanghai") -> dict[str, Any]:
    """只读：获取指定日期（today/yesterday/YYYY-MM-DD）的销售额、订单数、利润估算、缺货与 TOP3。"""
    if not isinstance(day, str) or not isinstance(timezone, str):
        raise ToolError("日期和时区必须是字符串")
    if len(day) > 32 or len(timezone) > 80:
        raise ToolError("日期或时区长度无效")
    try:
        period = bridge.select_date(day, timezone)
        raw = bridge.fetch_report(_ReadOnlyReportingClient(), period, timezone)
    except bridge.BridgeError:
        raise ToolError("营业报表请求失败，请检查日期、时区或账号权限") from None
    kpi = raw.get("kpi")
    if not isinstance(kpi, dict):
        raise ToolError("商城营业统计数据格式无效")
    return {
        "date": raw["date"],
        "timezone": raw["timezone"],
        "currency": raw.get("currency", ""),
        "kpi": {key: kpi[key] for key in SALES_KEYS if key in kpi},
        "top_products": [
            {
                "title": item.get("title"),
                "quantity": item.get("quantity"),
                "paid_amount": item.get("paid_amount"),
            }
            for item in raw.get("top_products", [])[:3] if isinstance(item, dict)
        ],
        "accounting_note": "金额与利润均按商城 Dashboard 既有口径统计；不构成审计结论",
    }


@mcp.tool()
def preview_product_draft(
    category_id: int, slug: str, title: str, price: str, description: str = "",
) -> dict[str, Any]:
    """离线预览：生成默认下架、人工交付、库存 0 的商品草稿；绝不写入商城。"""
    if type(category_id) is not int or any(not isinstance(v, str) for v in (slug, title, price, description)):
        raise ToolError("商品字段类型无效")
    try:
        payload = bridge.draft_payload(SimpleNamespace(
            category_id=category_id,
            slug=slug,
            title=title,
            price=price,
            description=description,
        ))
    except bridge.BridgeError:
        raise ToolError("商品草稿字段不符合要求；检查分类、Slug、价格及名称") from None
    return {
        "preview_only": True,
        "written_to_store": False,
        "can_publish": False,
        "payload": payload,
        "next_step": "如需创建，请人工审核后在商城后台操作；MCP 不提供商品写入工具",
    }


if __name__ == "__main__":
    # stdout is exclusively owned by the official SDK's MCP stdio transport.
    mcp.run(transport="stdio")
