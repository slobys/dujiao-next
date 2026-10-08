"""Offline tests of the provider-neutral Dujiao-Next MCP gateway.

All HTTP calls are mocked; no passwords, merchant orders or production host
are accessed. Run with the official MCP Python SDK installed.
"""
from __future__ import annotations

import asyncio
import importlib.util
import os
import pathlib
import stat
import sys
import tempfile
import unittest
from unittest import mock

from mcp import Client
from mcp.client.stdio import StdioServerParameters


HERE = pathlib.Path(__file__).resolve().parents[1]
SERVER_FILE = HERE / "server.py"
MODULE_SPEC = importlib.util.spec_from_file_location("dujiao_next_mcp", SERVER_FILE)
server = importlib.util.module_from_spec(MODULE_SPEC)
MODULE_SPEC.loader.exec_module(server)


def run_call(name: str, args: dict | None = None):
    async def invoke():
        async with Client(server.mcp) as client:
            return await client.call_tool(name, args or {})
    return asyncio.run(invoke())


class ToolRegistryTests(unittest.TestCase):
    def test_tools_listed_by_official_sdk(self):
        async def discover():
            async with Client(server.mcp) as client:
                return [tool.name for tool in (await client.list_tools()).tools]

        names = asyncio.run(discover())
        self.assertEqual(names, [
            "list_products", "list_categories", "list_inventory_alerts",
            "daily_sales_summary", "preview_product_draft",
        ])
        for dangerous in ("refund", "publish", "delete", "create_product", "write_sql", "set_price"):
            self.assertNotIn(dangerous, names)

    def test_legacy_and_modern_mcp_clients_discover_same_tools(self):
        async def discover(mode):
            async with Client(server.mcp, mode=mode) as client:
                return {tool.name for tool in (await client.list_tools()).tools}
        old = asyncio.run(discover("legacy"))
        current = asyncio.run(discover("2026-07-28"))
        self.assertEqual(old, current)
        self.assertEqual(len(current), 5)

    def test_preview_never_creates_product_or_needs_credentials(self):
        with mock.patch.object(server, "_readonly_get", side_effect=AssertionError("No HTTP")):
            result = run_call("preview_product_draft", {
                "category_id": 1, "slug": "test-draft", "title": "演示商品", "price": "10.50",
            })
        self.assertFalse(result.is_error)
        data = result.structured_content
        self.assertTrue(data["preview_only"])
        self.assertIs(data["written_to_store"], False)
        self.assertIs(data["can_publish"], False)
        self.assertIs(data["payload"]["is_active"], False)
        self.assertEqual(data["payload"]["manual_stock_total"], 0)
        self.assertEqual(data["payload"]["fulfillment_type"], "manual")

    def test_invalid_draft_rejected(self):
        for field in ({"slug": "INVALID SLUG"}, {"price": "NaN"}, {"price": "-5"}):
            args = {"category_id": 1, "slug": "sample", "title": "演示商品", "price": "10.50"}
            args.update(field)
            result = run_call("preview_product_draft", args)
            self.assertTrue(result.is_error)
            self.assertNotIn("Bearer", str(result.content))

    def test_read_product_sanitizes_all_other_fields(self):
        raw = [
            {"id": 10, "title": {"zh-CN": "月卡"}, "slug": "month",
             "price_amount": "19.90", "is_active": False,
             "card_secret": "PRIVATE_CARD_SECRET", "buyer_email": "private@example.com",
             "password": "hidden", "payment_details": {"api_key": "sensitive"}},
        ]
        with mock.patch.object(server, "_readonly_get", return_value=raw) as call:
            result = run_call("list_products", {"keyword": "月卡", "page": 1, "page_size": 10})
        self.assertFalse(result.is_error)
        data = result.structured_content
        # Lists are returned by MCP as {'result': <list>} structured output.
        rows = data.get("result", data) if isinstance(data, dict) else data
        self.assertEqual(rows[0]["title"], "月卡")
        self.assertEqual(rows[0]["price_amount"], "19.90")
        for secret in ("PRIVATE_CARD_SECRET", "private@example.com", "hidden", "sensitive"):
            self.assertNotIn(secret, str(result.content))
        self.assertEqual(call.call_args.args[0], "/api/v1/admin/products")
        self.assertEqual(call.call_args.kwargs["params"]["page_size"], 10)

    def test_search_invalid_page_rejected_before_http(self):
        with mock.patch.object(server, "_readonly_get", side_effect=AssertionError("No HTTP")):
            result = run_call("list_products", {"page": 1, "page_size": 5000})
        self.assertTrue(result.is_error)

    def test_categories_sanitize_unexpected_fields(self):
        rows = [{"id": 2, "name": {"zh-CN": "课程"}, "slug": "course",
                 "is_active": True, "card_secret": "PRIVATE_SECRET"}]
        with mock.patch.object(server, "_readonly_get", return_value=rows):
            response = run_call("list_categories")
        self.assertFalse(response.is_error)
        self.assertNotIn("PRIVATE_SECRET", str(response.content))
        self.assertIn("课程", str(response.structured_content))

    def test_inventory_alerts_redact_private_keys(self):
        rows = [{"product_id": 2, "product_title": {"zh-CN": "卡密商品"},
                 "alert_type": "out_of_stock", "available_stock": 0,
                 "card_secret": "CARD-NEVER-LEAK"}]
        with mock.patch.object(server, "_readonly_get", return_value=rows):
            response = run_call("list_inventory_alerts")
        self.assertFalse(response.is_error)
        self.assertIn("卡密商品", str(response.structured_content))
        self.assertNotIn("CARD-NEVER-LEAK", str(response.content))

    def test_sales_summary_uses_platform_api_and_whitelist(self):
        calls = []
        def get(path, *, params=None):
            calls.append((path, params))
            if path.endswith("/overview"):
                return {"currency": "CNY", "kpi": {
                    "orders_total": 5, "paid_orders": 4,
                    "gmv_paid": "99.00", "total_profit": "45.00",
                    "total_user_balance": "DO_NOT_EXPOSE",
                    "bank_account": "PRIVATE_BANK",
                }}
            return {"top_products": [
                {"title": "套餐", "paid_amount": "80.00", "quantity": 3,
                 "email": "PRIVATE_CUSTOMER"},
            ]}

        with mock.patch.object(server, "_readonly_get", side_effect=get):
            response = run_call("daily_sales_summary", {
                "day": "2026-10-07", "timezone": "Asia/Shanghai",
            })
        self.assertFalse(response.is_error)
        data = response.structured_content
        self.assertEqual(data["kpi"]["orders_total"], 5)
        self.assertEqual(data["kpi"]["gmv_paid"], "99.00")
        self.assertEqual(data["top_products"][0]["title"], "套餐")
        self.assertEqual(len(calls), 2)
        for endpoint, params in calls:
            self.assertTrue(endpoint.startswith("/api/v1/admin/dashboard/"))
            self.assertEqual(params["tz"], "Asia/Shanghai")
            self.assertEqual(params["range"], "custom")
        for secret in ("PRIVATE_CUSTOMER", "DO_NOT_EXPOSE", "PRIVATE_BANK"):
            self.assertNotIn(secret, str(response.content))

    def test_bad_data_refused(self):
        with mock.patch.object(server, "_readonly_get", return_value={"unexpected": "dictionary"}):
            response = run_call("list_products")
        self.assertTrue(response.is_error)

    def test_request_path_allowlist(self):
        with self.assertRaisesRegex(server.ToolError, "禁止"):
            server._readonly_get("/api/v1/admin/users")

    def test_backend_error_does_not_disclose_secret(self):
        secret = "THE_SERVER_ECHOED_A_SECRET"
        server._cached_client = None
        class BadClient:
            def __init__(self, *args, **kwargs):
                pass
            def call(self, *args, **kwargs):
                raise server.bridge.BridgeError(secret)
        with mock.patch.dict(os.environ, {"DUJIAO_BASE_URL": "https://shop.example.com"}), \
             mock.patch.object(server.bridge, "Client", BadClient):
            response = run_call("list_categories")
        self.assertTrue(response.is_error)
        self.assertNotIn(secret, str(response.content))
        server._cached_client = None

    def test_tool_listing_requires_no_api_credential(self):
        # Tool schema discovery must not attempt admin auth.
        with mock.patch.object(server, "_readonly_get", side_effect=AssertionError("No API")):
            async def discover():
                async with Client(server.mcp) as client:
                    return await client.list_tools()
            self.assertEqual(len(asyncio.run(discover()).tools), 5)


class PrivateCredentialsTests(unittest.TestCase):
    def test_secure_credential_file_parsed_as_data(self):
        # Integration with the new revocable AI machine token (no admin JWT).
        with tempfile.TemporaryDirectory() as temp:
            path = pathlib.Path(temp) / "ai-machine.env"
            path.write_text(
                "DUJIAO_BASE_URL=https://shop.example.com\n"
                "DUJIAO_AI_TOKEN=djai_test_secret\n", encoding="utf-8",
            )
            path.chmod(0o600)
            with mock.patch.dict(os.environ, {"DUJIAO_MCP_SECRETS_FILE": str(path)}, clear=True):
                server.load_private_credentials()
                self.assertEqual(os.environ["DUJIAO_AI_TOKEN"], "djai_test_secret")
                self.assertNotIn("DUJIAO_ADMIN_TOKEN", os.environ)
        with tempfile.TemporaryDirectory() as temp:
            path = pathlib.Path(temp) / "secret.env"
            path.write_text(
                "# locally owned, chmod 600\n"
                "DUJIAO_BASE_URL=https://shop.example.com\n"
                "DUJIAO_ADMIN_TOKEN=a.b.c\n", encoding="utf-8",
            )
            path.chmod(0o600)
            with mock.patch.dict(os.environ, {"DUJIAO_MCP_SECRETS_FILE": str(path)}, clear=True):
                server.load_private_credentials()
                self.assertEqual(os.environ["DUJIAO_ADMIN_TOKEN"], "a.b.c")
                self.assertEqual(os.environ["DUJIAO_BASE_URL"], "https://shop.example.com")

    def test_rejects_world_readable_credentials(self):
        if os.name == "nt":
            self.skipTest("Windows uses ACLs rather than Unix mode bits")
        with tempfile.TemporaryDirectory() as temp:
            path = pathlib.Path(temp) / "secret.env"
            path.write_text("DUJIAO_ADMIN_TOKEN=secret\n", encoding="utf-8")
            path.chmod(0o644)
            with mock.patch.dict(os.environ, {"DUJIAO_MCP_SECRETS_FILE": str(path)}, clear=True):
                with self.assertRaisesRegex(ValueError, "chmod 600"):
                    server.load_private_credentials()

    def test_refuses_unknown_fields_and_symlinks(self):
        with tempfile.TemporaryDirectory() as temp:
            path = pathlib.Path(temp) / "secret.env"
            path.write_text("DANGEROUS_SHELL_CMD=some command\n")
            path.chmod(0o600)
            with mock.patch.dict(os.environ, {"DUJIAO_MCP_SECRETS_FILE": str(path)}, clear=True):
                with self.assertRaisesRegex(ValueError, "未知或重复"):
                    server.load_private_credentials()
            link = pathlib.Path(temp) / "link.env"
            link.symlink_to(path)
            with mock.patch.dict(os.environ, {"DUJIAO_MCP_SECRETS_FILE": str(link)}, clear=True):
                with self.assertRaisesRegex(ValueError, "符号链接"):
                    server.load_private_credentials()

    def test_rejects_duplicate_or_oversized_files(self):
        with tempfile.TemporaryDirectory() as temp:
            path = pathlib.Path(temp) / "secret.env"
            path.write_text("DUJIAO_ADMIN_TOKEN=one\nDUJIAO_ADMIN_TOKEN=two\n")
            path.chmod(0o600)
            with mock.patch.dict(os.environ, {"DUJIAO_MCP_SECRETS_FILE": str(path)}, clear=True):
                with self.assertRaisesRegex(ValueError, "重复"):
                    server.load_private_credentials()
            path.write_text("DUJIAO_ADMIN_TOKEN=" + "x" * 8200 + "\n")
            with mock.patch.dict(os.environ, {"DUJIAO_MCP_SECRETS_FILE": str(path)}, clear=True):
                with self.assertRaisesRegex(ValueError, "大小上限"):
                    server.load_private_credentials()


class ActualStdioTests(unittest.TestCase):
    def test_mcp_stdio_subprocess_with_offline_preview(self):
        async def smoke():
            config = StdioServerParameters(
                command=sys.executable,
                args=[str(SERVER_FILE)],
                env={"DUJIAO_MCP_SECRETS_FILE": ""},
            )
            async with Client(config) as client:
                tools = await client.list_tools()
                self.assertIn("preview_product_draft", {t.name for t in tools.tools})
                result = await client.call_tool("preview_product_draft", {
                    "category_id": 1, "slug": "stdio-demo",
                    "title": "远程模拟", "price": "29.90",
                })
                self.assertFalse(result.is_error)
                self.assertIs(result.structured_content["written_to_store"], False)
        asyncio.run(smoke())


if __name__ == "__main__":
    unittest.main()
