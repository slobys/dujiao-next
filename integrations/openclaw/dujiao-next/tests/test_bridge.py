"""No-network contract tests for the OpenClaw bridge."""
import contextlib
import importlib.util
import io
import json
import pathlib
import unittest
from datetime import date
from unittest.mock import patch
from urllib import error

MODULE_PATH = pathlib.Path(__file__).resolve().parents[1] / "dujiao.py"
SPEC = importlib.util.spec_from_file_location("dujiao_bridge", MODULE_PATH)
bridge = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(bridge)


def packet(data, status_code=0):
    return {"status_code": status_code, "msg": "success", "data": data}


class Opener:
    def __init__(self, *responses):
        self.responses = list(responses)
        self.calls = []

    def open(self, req, timeout=None):
        self.calls.append(req)
        result = self.responses.pop(0)
        if isinstance(result, Exception):
            raise result
        return io.BytesIO(json.dumps(result).encode("utf-8"))


class SafeURLTests(unittest.TestCase):
    def test_tls_and_local_loopback(self):
        self.assertEqual(bridge.validate_base_url("https://shop.example.com/"), "https://shop.example.com")
        self.assertEqual(bridge.validate_base_url("http://127.0.0.1:18080"), "http://127.0.0.1:18080")

    def test_http_remote_and_credentials_rejected(self):
        for url in [
            "http://shop.example.com", "http://192.168.2.1:18080",
            "https://user:pass@shop.example.com", "https://shop.example.com/admin",
            "https://shop.example.com?abc=1", "https://shop.example.com/#fragment",
            "file:///etc/passwd", "https://shop.example.com:999999",
        ]:
            with self.subTest(url=url), self.assertRaises(bridge.BridgeError):
                bridge.validate_base_url(url)

    def test_api_rejects_bad_envelope(self):
        for value in (b"not json", json.dumps({"data": []}).encode()):
            with self.assertRaises(bridge.BridgeError):
                bridge.decode_envelope(value)

    def test_application_errors_fail_closed(self):
        with self.assertRaisesRegex(bridge.BridgeError, "业务码 403"):
            bridge.decode_envelope(json.dumps(packet(None, 403)).encode())


class DraftTests(unittest.TestCase):
    def payload(self, **overrides):
        from argparse import Namespace
        data = dict(category_id=2, slug="test-product", title="测试商品",
                    price="29.90", description="展示信息")
        data.update(overrides)
        return bridge.draft_payload(Namespace(**data))

    def test_draft_is_never_published(self):
        value = self.payload()
        self.assertEqual(value["price_amount"], 29.9)
        self.assertEqual(value["is_active"], False)
        self.assertEqual(value["manual_stock_total"], 0)
        self.assertEqual(value["fulfillment_type"], "manual")

    def test_invalid_amount_slug_title(self):
        for override in (
            {"price": "NaN"}, {"price": "-1"}, {"price": "0"}, {"price": "1.123"},
            {"price": "Infinity"}, {"slug": "../other"}, {"slug": "UPPER"},
            {"category_id": 0}, {"title": ""}, {"title": "x" * 121},
        ):
            with self.subTest(override=override), self.assertRaises(bridge.BridgeError):
                self.payload(**override)

    def test_dry_run_uses_no_credentials_or_network(self):
        output = io.StringIO()
        with contextlib.redirect_stdout(output):
            self.assertEqual(bridge.main(["draft-product", "--category-id", "1",
                                          "--slug", "demo", "--title", "测试",
                                          "--price", "3.20"], environ={}), 0)
        self.assertIn('"preview_only": true', output.getvalue())
        self.assertIn('"is_active": false', output.getvalue())

    def test_execute_requires_exact_confirmation(self):
        with self.assertRaisesRegex(bridge.BridgeError, "confirm-slug"):
            bridge.main(["draft-product", "--category-id", "1", "--slug", "demo",
                         "--title", "测试", "--price", "3.20", "--execute"],
                        environ={"DUJIAO_BASE_URL": "https://shop.example.com"})

    def test_execute_posts_offline_draft_only(self):
        opener = Opener(packet({"id": 42, "slug": "demo", "is_active": False}))
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            bridge.main(["draft-product", "--category-id", "1", "--slug", "demo",
                         "--title", "测试", "--price", "3.20", "--execute",
                         "--confirm-slug", "demo"],
                        environ={"DUJIAO_BASE_URL": "https://shop.example.com",
                                 "DUJIAO_ADMIN_TOKEN": "fake-jwt"}, opener=opener)
        req = opener.calls[0]
        self.assertEqual(req.get_method(), "POST")
        self.assertEqual(req.full_url, "https://shop.example.com/api/v1/admin/products")
        body = json.loads(req.data)
        self.assertIs(body["is_active"], False)
        self.assertEqual(body["manual_stock_total"], 0)
        self.assertEqual(req.get_header("Authorization"), "Bearer fake-jwt")
        self.assertIn('"created_draft": true', out.getvalue())

    def test_execute_refuses_unexpected_online_product(self):
        opener = Opener(packet({"id": 42, "is_active": True}))
        with self.assertRaisesRegex(bridge.BridgeError, "人工检查"):
            bridge.main(["draft-product", "--category-id", "1", "--slug", "demo",
                         "--title", "测试", "--price", "3.20", "--execute",
                         "--confirm-slug", "demo"],
                        environ={"DUJIAO_BASE_URL": "https://shop.example.com",
                                 "DUJIAO_ADMIN_TOKEN": "jwt"}, opener=opener)


class AuthAndReadTests(unittest.TestCase):
    def test_products_redact_unnecessary_fields(self):
        opener = Opener(packet([{"id": 1, "slug": "a", "title": {"zh-CN": "卡"},
                                 "price_amount": "1.00", "buyer_email": "buyer@local",
                                 "card_secret": "top-secret"}]))
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            bridge.main(["products"], environ={"DUJIAO_BASE_URL": "https://shop.example.com",
                                                 "DUJIAO_ADMIN_TOKEN": "secret-jwt"}, opener=opener)
        self.assertIn('"title": "卡"', out.getvalue())
        self.assertNotIn("buyer@local", out.getvalue())
        self.assertNotIn("top-secret", out.getvalue())

    def test_login_uses_existing_admin_endpoint(self):
        opener = Opener(packet({"requires_totp": False, "token": "short-jwt"}),
                        packet([{"id": 1, "slug": "main", "name": {"zh-CN": "分类"}}]))
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            bridge.main(["categories"], environ={"DUJIAO_BASE_URL": "https://shop.example.com",
                                                    "DUJIAO_ADMIN_USERNAME": "ops",
                                                    "DUJIAO_ADMIN_PASSWORD": "not-logged"}, opener=opener)
        self.assertEqual(len(opener.calls), 2)
        self.assertNotIn("Authorization", opener.calls[0].headers)
        self.assertEqual(opener.calls[1].get_header("Authorization"), "Bearer short-jwt")
        self.assertEqual(opener.calls[0].full_url, "https://shop.example.com/api/v1/admin/login")

    def test_noninteractive_2fa_stops(self):
        opener = Opener(packet({"requires_totp": True, "challenge_token": "challenge-secret"}))
        with patch.object(bridge.sys.stdin, "isatty", return_value=False):
            with self.assertRaisesRegex(bridge.BridgeError, "不能自动登录"):
                bridge.main(["categories"], environ={"DUJIAO_BASE_URL": "https://shop.example.com",
                                                        "DUJIAO_ADMIN_USERNAME": "ops",
                                                        "DUJIAO_ADMIN_PASSWORD": "secret"}, opener=opener)
        self.assertEqual(len(opener.calls), 1)

    def test_http_error_hides_url_and_token(self):
        secret = "DO_NOT_PRINT_THIS_TOKEN"
        http_err = error.HTTPError(f"https://shop.example.com/api/?secret={secret}", 401,
                                   "Unauthorized", None, None)
        opener = Opener(http_err)
        with self.assertRaises(bridge.BridgeError) as raised:
            bridge.main(["categories"], environ={"DUJIAO_BASE_URL": "https://shop.example.com",
                                                    "DUJIAO_ADMIN_TOKEN": secret}, opener=opener)
        self.assertNotIn(secret, str(raised.exception))


class ReportTests(unittest.TestCase):
    def test_custom_daily_range_is_inclusive_last_second(self):
        window = bridge.report_window(date(2026, 10, 7), "Asia/Shanghai")
        self.assertEqual(window["range"], "custom")
        self.assertEqual(window["from"], "2026-10-07T00:00:00+08:00")
        self.assertEqual(window["to"], "2026-10-07T23:59:59+08:00")

    def test_dst_transition_inclusivity(self):
        window = bridge.report_window(date(2026, 11, 1), "America/New_York")
        self.assertEqual(window["from"], "2026-11-01T00:00:00-04:00")
        self.assertEqual(window["to"], "2026-11-01T23:59:59-05:00")

    def test_dashboard_uses_platform_accounting(self):
        opener = Opener(packet({"currency": "CNY", "kpi": {
            "gmv_paid": "100.20", "orders_total": 5, "paid_orders": 3,
            "total_profit": "48.50", "out_of_stock_products": 1,
        }}), packet({"top_products": [
            {"title": "热销", "paid_amount": "90.20", "quantity": 2, "buyer_email": "no"}
        ]}))
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            bridge.main(["daily-report", "--date", "2026-10-07"],
                        environ={"DUJIAO_BASE_URL": "https://shop.example.com",
                                 "DUJIAO_ADMIN_TOKEN": "jwt"}, opener=opener)
        self.assertIn("CNY 100.20", out.getvalue())
        self.assertIn("热销", out.getvalue())
        self.assertNotIn("buyer_email", out.getvalue())
        self.assertIn("range=custom", opener.calls[0].full_url)
        self.assertIn("tz=Asia%2FShanghai", opener.calls[0].full_url)

    def test_telegram_error_never_exposes_bot_token(self):
        token = "12345678:" + "x" * 30
        opener = Opener(error.HTTPError("https://api.telegram.org/bot" + token, 403,
                                       "Forbidden", None, None))
        with self.assertRaises(bridge.BridgeError) as raised:
            bridge.send_telegram("日报", environ={
                "DUJIAO_TELEGRAM_BOT_TOKEN": token,
                "DUJIAO_TELEGRAM_CHAT_ID": "-123456789"
            }, opener=opener)
        self.assertNotIn(token, str(raised.exception))

    def test_telegram_requires_destination(self):
        with self.assertRaises(bridge.BridgeError):
            bridge.send_telegram("日报", environ={})


class IntegrationAssetTests(unittest.TestCase):
    def test_openclaw_skill_has_valid_minimum_frontmatter(self):
        path = pathlib.Path(__file__).resolve().parents[1] / "SKILL.md"
        text = path.read_text(encoding="utf-8")
        self.assertTrue(text.startswith("---\nname: dujiao-next\n"))
        self.assertIn("\ndescription:", text[:250])
        self.assertIn("is_active=false", text)

    def test_n8n_template_is_inactive_and_contains_no_credentials(self):
        path = pathlib.Path(__file__).resolve().parents[2] / "n8n-daily-telegram.json"
        raw = path.read_text(encoding="utf-8")
        template = json.loads(raw)
        self.assertIs(template["active"], False)
        self.assertEqual(template["settings"]["timezone"], "Asia/Shanghai")
        self.assertNotIn("credentials", raw)
        self.assertEqual(template["nodes"][0]["parameters"]["rule"]["interval"][0]["triggerAtHour"], 9)
        self.assertEqual(template["nodes"][1]["type"], "n8n-nodes-base.ssh")
        self.assertIn("daily-telegram", template["nodes"][1]["parameters"]["command"])


if __name__ == "__main__":
    unittest.main()
