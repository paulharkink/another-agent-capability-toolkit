import json
import tempfile
import sys
import unittest
from pathlib import Path
from unittest.mock import patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import browser_access
import configure_browser_access


class AccessConfigTests(unittest.TestCase):
    def test_absent_or_empty_config_keeps_access_unrestricted(self):
        self.assertEqual(browser_access.parse_access_config(None), {})
        self.assertEqual(browser_access.parse_access_config("{}"), {})
        self.assertEqual(browser_access.parse_access_config('{"allowed_urls":[]}'), {})

    def test_parses_allowlists_and_limits(self):
        raw = json.dumps({
            "allowed_urls": ["https://app.example", "https://identity.example/login"],
            "blocked_urls": ["https://identity.example/blocked"],
            "popup_allowed_urls": ["https://app.example"],
            "tab_limits": {
                "max_tabs": 3,
                "hard_max_tabs": 8,
                "per_pattern": [{"pattern": "https://app.example:8443/app", "max_tabs": 2}],
                "popup_exemptions": [{
                    "pattern": "https://identity.example:*",
                    "max_tabs": 3,
                    "grace_seconds": 10,
                }],
            },
        })
        config = browser_access.parse_access_config(raw)
        self.assertEqual(config["allowed_urls"], ["https://app.example", "https://identity.example/login"])
        self.assertEqual(config["blocked_urls"], ["https://identity.example/blocked"])
        self.assertEqual(config["tab_limits"]["hard_max_tabs"], 8)

    def test_rejects_unsupported_or_malformed_policy_patterns(self):
        for pattern in (
            "file://*", "<all_urls>", "https://user@example.invalid",
            "https://example.invalid:0", "https://example.invalid:65536",
            "https://example.invalid/path?x=1", "https://example.invalid/path#fragment",
            "https://example.*.invalid", "https://example.invalid/path*",
        ):
            with self.subTest(pattern=pattern):
                with self.assertRaises(browser_access.AccessConfigError):
                    browser_access.parse_access_config(json.dumps({"allowed_urls": [pattern]}))

    def test_accepts_chromium_numeric_or_wildcard_port_and_policy_path_prefix(self):
        config = browser_access.parse_access_config(json.dumps({"allowed_urls": [
            "http://localhost:8080/dev", "https://example.invalid:*"
        ]}))
        self.assertEqual(config["allowed_urls"], ["http://localhost:8080/dev", "https://example.invalid:*"])

    def test_path_patterns_require_ascii_percent_encoding(self):
        with self.assertRaises(browser_access.AccessConfigError):
            browser_access.parse_access_config(json.dumps({"blocked_urls": ["https://example.invalid/café"]}))
        config = browser_access.parse_access_config(json.dumps({
            "blocked_urls": ["https://example.invalid/caf%C3%A9"]
        }))
        self.assertEqual(config["blocked_urls"], ["https://example.invalid/caf%C3%A9"])

    def test_url_filter_matcher_follows_chrome_host_port_and_path_semantics(self):
        self.assertTrue(browser_access.url_pattern_matches("https://app.example/start", "https://example"))
        self.assertTrue(browser_access.url_pattern_matches("https://app.example:8443/start", "https://example"))
        self.assertTrue(browser_access.url_pattern_matches("https://app.example:8443/start", "https://example:*"))
        self.assertTrue(browser_access.url_pattern_matches("https://elsewhere.invalid/start", "https://*"))
        self.assertFalse(browser_access.url_pattern_matches("https://evil.invalid/start", "https://example"))
        self.assertFalse(browser_access.url_pattern_matches("https://sub.example/start", "https://.example"))
        self.assertTrue(browser_access.url_pattern_matches("https://example/start", "https://.example"))
        self.assertTrue(browser_access.url_pattern_matches("https://app.example:8443/private/item", "https://example:8443/private"))
        self.assertFalse(browser_access.url_pattern_matches("https://app.example/private/item", "https://example:8443/private"))
        self.assertFalse(browser_access.url_pattern_matches("https://app.example/public", "https://example:8443/private"))
        self.assertTrue(browser_access.url_pattern_matches("https://app.example/foobar", "https://app.example/foo"))

    def test_rejects_unknown_fields_and_inconsistent_or_out_of_range_limits(self):
        invalid = [
            {"other": True},
            {"tab_limits": {}},
            {"tab_limits": {"max_tabs": 4, "hard_max_tabs": 3}},
            {"tab_limits": {"max_tabs": 1, "hard_max_tabs": 101}},
            {"tab_limits": {"max_tabs": 1, "hard_max_tabs": 3, "unexpected": 1}},
            {"tab_limits": {"max_tabs": 1, "hard_max_tabs": 3,
                             "per_pattern": [{"pattern": "https://x.example/*", "max_tabs": 4}]}},
            {"tab_limits": {"max_tabs": 1, "hard_max_tabs": 3,
                             "popup_exemptions": [{"pattern": "https://x.example/*", "max_tabs": 1,
                                                   "grace_seconds": 31}]}},
        ]
        for value in invalid:
            with self.subTest(value=value):
                with self.assertRaises(browser_access.AccessConfigError):
                    browser_access.parse_access_config(json.dumps(value))

    def test_policy_preserves_file_block_and_adds_configured_url_rules(self):
        policy = browser_access.managed_policy({"allowed_urls": ["https://app.example"]})
        self.assertEqual(policy["URLBlocklist"], ["file://*", "*"])
        self.assertEqual(policy["URLAllowlist"], ["https://app.example"])
        self.assertNotIn("URLAllowlist", browser_access.managed_policy({}))

    def test_blocked_url_patterns_compile_to_bounded_dnr_frame_rules(self):
        config = browser_access.parse_access_config(json.dumps({
            "allowed_urls": ["https://example.invalid"],
            "blocked_urls": ["https://example.invalid/private", "https://.login.example:443/auth"],
        }))
        rules = browser_access.dnr_navigation_rules(config)
        self.assertEqual([rule["id"] for rule in rules], [1001, 2001, 2002])
        self.assertEqual(rules[0]["action"], {"type": "allow"})
        self.assertEqual(rules[0]["priority"], 2)
        self.assertEqual(rules[1]["condition"]["regexFilter"],
                         r"^https://(?:[A-Za-z0-9-]+\.)*example\.invalid(?::[0-9]+)?/private")
        self.assertEqual(rules[1]["condition"]["resourceTypes"], ["main_frame", "sub_frame"])
        self.assertEqual(rules[1]["action"], {"type": "block"})
        self.assertEqual(rules[1]["priority"], 3)
        self.assertTrue(browser_access.dnr_rule_matches_url(rules[1], "https://example.invalid/private/item"))
        self.assertTrue(browser_access.dnr_rule_matches_url(rules[1], "https://a.example.invalid/private"))
        self.assertFalse(browser_access.dnr_rule_matches_url(rules[1], "https://example.invalid/public"))
        self.assertFalse(browser_access.dnr_rule_matches_url(rules[2], "https://sub.login.example/auth"))
        self.assertTrue(browser_access.dnr_rule_matches_url(rules[2], "https://login.example:443/auth"))
        barrier = browser_access.dnr_block_all_rules(config)
        self.assertEqual(barrier[0]["priority"], 1)
        self.assertEqual(barrier[0]["action"], {"type": "block"})

    def test_dnr_domain_rule_does_not_match_a_hostname_suffix_confusable(self):
        config = browser_access.parse_access_config(json.dumps({"blocked_urls": ["https://example.invalid"]}))
        rule = browser_access.dnr_navigation_rules(config)[1]
        self.assertTrue(browser_access.dnr_rule_matches_url(rule, "https://sub.example.invalid/path"))
        self.assertFalse(browser_access.dnr_rule_matches_url(rule, "https://example.invalid.evil/path"))

    def test_blacklist_rejects_pattern_that_exceeds_dnr_regex_bound(self):
        with self.assertRaises(browser_access.AccessConfigError):
            browser_access.parse_access_config(json.dumps({
                "blocked_urls": ["https://example.invalid/" + "." * 1001]
            }))

    def test_explicit_popup_sources_override_the_default_allowed_site_sources(self):
        policy = browser_access.managed_policy({
            "allowed_urls": ["https://app.example", "https://login.example"],
            "popup_allowed_urls": ["https://app.example"],
        })
        self.assertEqual(policy["PopupsAllowedForUrls"], ["https://app.example"])

    def test_auto_allow_popups_uses_only_allowed_initiators_and_keeps_explicit_sources(self):
        config = browser_access.parse_access_config(json.dumps({
            "allowed_urls": ["https://app.example", "https://login.example"],
            "popup_allowed_urls": ["https://idp.example"],
            "auto_allow_popups": True,
        }))
        policy = browser_access.managed_policy(config)
        self.assertEqual(policy["PopupsAllowedForUrls"], [
            "https://app.example", "https://login.example", "https://idp.example"
        ])

    def test_auto_allow_popups_requires_a_nonempty_allowed_urls_list(self):
        with self.assertRaises(browser_access.AccessConfigError):
            browser_access.parse_access_config(json.dumps({"auto_allow_popups": True}))

    def test_auto_allow_popups_is_strictly_boolean(self):
        for value in (1, "true", None):
            with self.subTest(value=value), self.assertRaises(browser_access.AccessConfigError):
                browser_access.parse_access_config(json.dumps({
                    "allowed_urls": ["https://app.example"], "auto_allow_popups": value,
                }))

    def test_startup_generates_narrow_extension_permissions_and_resets_policy(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            base_policy = root / "base.json"
            policy = root / "policy.json"
            extension = root / "extension"
            source = root / "extension.js"
            base_policy.write_text('{"URLBlocklist":["file://*"]}', encoding="utf-8")
            policy.write_text('{"URLBlocklist":["file://*"]}', encoding="utf-8")
            source.write_text("// synthetic extension source", encoding="utf-8")
            with patch.object(configure_browser_access, "BASE_POLICY", base_policy), \
                    patch.object(configure_browser_access, "POLICY", policy), \
                    patch.object(configure_browser_access, "EXTENSION_DIR", extension), \
                    patch.object(configure_browser_access, "EXTENSION_SOURCE", source):
                configure_browser_access.configure(json.dumps({"allowed_urls": ["https://app.example"],
                    "blocked_urls": ["https://app.example/private"],
                    "auto_allow_popups": True,
                    "tab_limits": {"max_tabs": 2, "hard_max_tabs": 5,
                    "per_pattern": [{"pattern": "https://app.example:8443/app", "max_tabs": 3}]}}))
                manifest = json.loads((extension / "manifest.json").read_text())
                self.assertEqual(manifest["host_permissions"], ["https://*.app.example:8443/*", "https://app.example:8443/*"])
                self.assertNotIn("tabs", manifest["permissions"])
                self.assertIn("declarativeNetRequest", manifest["permissions"])
                self.assertEqual(manifest["declarative_net_request"]["rule_resources"][0]["id"], "blocked-urls")
                barrier = json.loads((extension / "blocked_urls.json").read_text())
                navigation_rules = json.loads((extension / "navigation_rules.json").read_text())
                self.assertEqual(barrier[0]["action"], {"type": "block"})
                self.assertEqual(barrier[0]["priority"], 1)
                self.assertEqual([rule["action"]["type"] for rule in navigation_rules], ["allow", "block"])
                self.assertEqual([rule["priority"] for rule in navigation_rules], [2, 3])
                self.assertEqual(json.loads(policy.read_text())['PopupsAllowedForUrls'], ["https://app.example"])
                self.assertEqual(json.loads(policy.read_text())["URLBlocklist"], ["file://*", "*"])
                configure_browser_access.configure(None)
                reset_policy = json.loads(policy.read_text())
                self.assertEqual(reset_policy, {"URLBlocklist": ["file://*"]})

    def test_host_permissions_do_not_apply_domain_wildcards_to_ip_literals(self):
        self.assertEqual(configure_browser_access.host_permission("http://127.0.0.1:8000/path"), [
            "http://127.0.0.1:8000/*"
        ])


if __name__ == "__main__":
    unittest.main()
