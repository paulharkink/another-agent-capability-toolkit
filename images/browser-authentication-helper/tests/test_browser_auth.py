import base64
import json
import hashlib
import importlib.util
import io
import logging
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import MagicMock, patch


sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import browser_auth


class SessionStorageSelectionTests(unittest.TestCase):
    def store_context(self, store):
        context = MagicMock()
        context.__enter__.return_value = store
        return context

    def profile(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        profile = Path(temp.name)
        session_dir = profile / "Session Storage"
        session_dir.mkdir()
        (session_dir / "CURRENT").write_text("synthetic", encoding="utf-8")
        return profile

    def test_selects_only_requested_origin_and_prefix(self):
        records = [
            SimpleNamespace(key="msal.client.rt.a", value="one", is_deleted=False),
            SimpleNamespace(key="msal.client.at.a", value="two", is_deleted=False),
            SimpleNamespace(key="unrelated", value="three", is_deleted=False),
        ]
        store = SimpleNamespace(
            iter_hosts=lambda: iter(["https://example.invalid", "https://other.invalid"]),
            iter_records_for_host=lambda host, **_kwargs: iter(records if host == "https://example.invalid" else [
                SimpleNamespace(key="msal.client.rt.b", value="wrong-origin", is_deleted=False)
            ]),
        )
        profile = self.profile()

        with patch.object(browser_auth, "open_session_store", return_value=self.store_context(store)):
            with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
                result = browser_auth.read_session_storage(
                    profile,
                    origin="https://example.invalid",
                    name="msal.client",
                    match="prefix",
                )

        self.assertEqual(result, [
            {"key": "msal.client.at.a", "value": "two"},
            {"key": "msal.client.rt.a", "value": "one"},
        ])

    def test_ignores_unparseable_unrelated_host_when_selecting_requested_origin(self):
        records = [SimpleNamespace(key="msal.rt", value="synthetic", is_deleted=False)]
        store = SimpleNamespace(
            iter_hosts=lambda: iter(["chrome-extension://synthetic-id", "https://example.invalid"]),
            iter_records_for_host=lambda host, **_kwargs: iter(records if host == "https://example.invalid" else []),
        )
        profile = self.profile()

        with patch.object(browser_auth, "open_session_store", return_value=self.store_context(store)):
            with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
                result = browser_auth.read_session_storage(
                    profile,
                    origin="https://example.invalid",
                    name="",
                    match="all",
                )

        self.assertEqual(result, [{"key": "msal.rt", "value": "synthetic"}])

    def test_near_origin_hosts_do_not_match_but_single_root_slash_does(self):
        records_by_host = {
            "https://example.invalid///": [SimpleNamespace(key="wrong", value="multi-slash", is_deleted=False)],
            "https://example.invalid.": [SimpleNamespace(key="wrong", value="trailing-dot", is_deleted=False)],
            "https://example.invalid/": [SimpleNamespace(key="right", value="canonical", is_deleted=False)],
        }
        store = SimpleNamespace(
            iter_hosts=lambda: iter(records_by_host),
            iter_records_for_host=lambda host, **_kwargs: iter(records_by_host[host]),
        )
        profile = self.profile()

        with patch.object(browser_auth, "open_session_store", return_value=self.store_context(store)):
            with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
                result = browser_auth.read_session_storage(
                    profile, origin="https://example.invalid", name="", match="all"
                )

        self.assertEqual(result, [{"key": "right", "value": "canonical"}])

    def test_unicode_casefold_confusable_host_does_not_match_ascii_origin(self):
        records_by_host = {
            "https://faß.de": [SimpleNamespace(key="wrong", value="unicode", is_deleted=False)],
            "https://fass.de": [SimpleNamespace(key="right", value="ascii", is_deleted=False)],
        }
        store = SimpleNamespace(
            iter_hosts=lambda: iter(records_by_host),
            iter_records_for_host=lambda host, **_kwargs: iter(records_by_host[host]),
        )
        profile = self.profile()

        with patch.object(browser_auth, "open_session_store", return_value=self.store_context(store)):
            with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
                result = browser_auth.read_session_storage(
                    profile, origin="https://fass.de", name="", match="all"
                )

        self.assertEqual(result, [{"key": "right", "value": "ascii"}])

    def test_offline_session_storage_rejects_symlinked_store_directory(self):
        with tempfile.TemporaryDirectory() as outside:
            profile = self.profile()
            session_dir = profile / "Session Storage"
            (session_dir / "CURRENT").unlink()
            session_dir.rmdir()
            session_dir.symlink_to(outside, target_is_directory=True)
            with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
                with self.assertRaisesRegex(browser_auth.BrowserAuthError, "symbolic link"):
                    browser_auth.read_session_storage(
                        profile, origin="https://example.invalid", name="key", offline=True
                    )


    def test_ignores_malformed_unrelated_host_when_selecting_requested_origin(self):
        records_by_host = {
            "https://[bad": [SimpleNamespace(key="wrong", value="malformed", is_deleted=False)],
            "https://example.invalid": [SimpleNamespace(key="right", value="canonical", is_deleted=False)],
        }
        store = SimpleNamespace(
            iter_hosts=lambda: iter(records_by_host),
            iter_records_for_host=lambda host, **_kwargs: iter(records_by_host[host]),
        )
        profile = self.profile()

        with patch.object(browser_auth, "open_session_store", return_value=self.store_context(store)):
            with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
                result = browser_auth.read_session_storage(
                    profile, origin="https://example.invalid", name="", match="all"
                )

        self.assertEqual(result, [{"key": "right", "value": "canonical"}])

    def test_requires_one_exact_match_and_never_returns_tombstones(self):
        records = [
            SimpleNamespace(key="session-key", value="synthetic-secret", is_deleted=False, leveldb_sequence_number=2),
            SimpleNamespace(key="session-key", value=None, is_deleted=True, leveldb_sequence_number=1),
            SimpleNamespace(key="session-key-extra", value="other", is_deleted=False, leveldb_sequence_number=1),
        ]
        store = SimpleNamespace(
            iter_hosts=lambda: iter(["https://example.invalid/"]),
            iter_records_for_host=lambda *_args, **_kwargs: iter(records),
        )
        profile = self.profile()
        with patch.object(browser_auth, "open_session_store", return_value=self.store_context(store)):
            with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
                result = browser_auth.read_session_storage(
                    profile,
                    origin="https://example.invalid",
                    name="session-key",
                    match="exact",
                )
        self.assertEqual(result, [{"key": "session-key", "value": "synthetic-secret"}])

    def test_retries_read_when_profile_changes_during_parse(self):
        records = [SimpleNamespace(key="session-key", value="synthetic-secret", is_deleted=False)]
        store = SimpleNamespace(
            iter_hosts=lambda: iter(["https://example.invalid"]),
            iter_records_for_host=lambda *_args, **_kwargs: iter(records),
        )
        profile = self.profile()
        with patch.object(browser_auth, "open_session_store", return_value=self.store_context(store)):
            with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
                with patch.object(browser_auth, "_fingerprint_tree", side_effect=[("before",), ("after",), ("stable",), ("stable",)]):
                    result = browser_auth.read_session_storage(
                        profile, origin="https://example.invalid", name="session-key"
                    )
        self.assertEqual(result, [{"key": "session-key", "value": "synthetic-secret"}])

    def test_allow_empty_is_opt_in_for_sites_without_a_cached_credential(self):
        store = SimpleNamespace(
            iter_hosts=lambda: iter(["https://example.invalid"]),
            iter_records_for_host=lambda *_args, **_kwargs: iter([
                SimpleNamespace(key="unrelated", value="not-selected", is_deleted=False, leveldb_sequence_number=1)
            ]),
        )
        profile = self.profile()
        with patch.object(browser_auth, "open_session_store", return_value=self.store_context(store)):
            with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
                with self.assertRaisesRegex(browser_auth.BrowserAuthError, "did not match"):
                    browser_auth.read_session_storage(profile, origin="https://example.invalid", name="missing")
        with patch.object(browser_auth, "open_session_store", return_value=self.store_context(store)):
            with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
                self.assertEqual(
                    browser_auth.read_session_storage(
                        profile, origin="https://example.invalid", name="missing", allow_empty=True
                    ),
                    [],
                )

    def test_allow_empty_handles_profile_without_session_storage_directory(self):
        with tempfile.TemporaryDirectory() as temp:
            profile = Path(temp)
            with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
                with self.assertRaisesRegex(browser_auth.BrowserAuthError, "sessionStorage is unavailable"):
                    browser_auth.read_session_storage(
                        profile, origin="https://example.invalid", name="missing", allow_empty=False
                    )
            with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
                self.assertEqual(
                    browser_auth.read_session_storage(
                        profile, origin="https://example.invalid", name="missing", allow_empty=True
                    ),
                    [],
                )

    def test_allow_empty_handles_origin_absent_from_valid_store(self):
        store = SimpleNamespace(iter_hosts=lambda: iter([]))
        profile = self.profile()
        with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
            with patch.object(browser_auth, "open_session_store", return_value=self.store_context(store)):
                self.assertEqual(
                    browser_auth.read_session_storage(
                        profile, origin="https://example.invalid", name="missing", allow_empty=True
                    ),
                    [],
                )

    def test_allow_empty_does_not_hide_profile_parse_errors(self):
        store = SimpleNamespace(iter_hosts=lambda: (_ for _ in ()).throw(ValueError("synthetic corruption")))
        profile = self.profile()
        with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
            with patch.object(browser_auth, "open_session_store", return_value=self.store_context(store)):
                with self.assertRaisesRegex(browser_auth.BrowserAuthError, "could not be parsed safely"):
                    browser_auth.read_session_storage(
                        profile, origin="https://example.invalid", name="missing", allow_empty=True
                    )

    def test_all_match_returns_all_keys_for_only_the_requested_origin(self):
        records = [
            SimpleNamespace(key="rt-home-env", value="synthetic-refresh", is_deleted=False, leveldb_sequence_number=1),
            SimpleNamespace(key="theme", value="dark", is_deleted=False, leveldb_sequence_number=2),
        ]
        store = SimpleNamespace(
            iter_hosts=lambda: iter(["https://example.invalid"]),
            iter_records_for_host=lambda *_args, **_kwargs: iter(records),
        )
        profile = self.profile()
        with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
            with patch.object(browser_auth, "open_session_store", return_value=self.store_context(store)):
                result = browser_auth.read_session_storage(
                    profile, origin="https://example.invalid", name="", match="all"
                )
        self.assertEqual(result, [
            {"key": "rt-home-env", "value": "synthetic-refresh"},
            {"key": "theme", "value": "dark"},
        ])


class LocalStorageSelectionTests(unittest.TestCase):
    def store_context(self, store):
        context = MagicMock()
        context.__enter__.return_value = store
        return context

    def profile(self):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        profile = Path(temp.name)
        leveldb = profile / "Local Storage" / "leveldb"
        leveldb.mkdir(parents=True)
        (leveldb / "CURRENT").write_text("synthetic", encoding="utf-8")
        return profile

    def test_offline_selects_latest_origin_values_and_applies_key_prefix(self):
        records = [
            SimpleNamespace(script_key="auth.access", value="old", leveldb_seq_number=1, is_live=False),
            SimpleNamespace(script_key="auth.access", value="synthetic-access", leveldb_seq_number=2, is_live=True),
            SimpleNamespace(script_key="auth.refresh", value="synthetic-refresh", leveldb_seq_number=3, is_live=True),
            SimpleNamespace(script_key="theme", value="dark", leveldb_seq_number=4, is_live=True),
            SimpleNamespace(script_key="auth.deleted", value=None, leveldb_seq_number=5, is_live=False),
        ]
        store = SimpleNamespace(
            iter_storage_keys=lambda: iter(["https://example.invalid", "https://other.invalid"]),
            iter_records_for_storage_key=lambda storage_key, **_kwargs: iter(
                records if storage_key == "https://example.invalid" else [
                    SimpleNamespace(script_key="auth.access", value="wrong-origin", leveldb_seq_number=6, is_live=True)
                ]
            ),
        )
        profile = self.profile()

        with patch.object(browser_auth, "open_local_store", return_value=self.store_context(store)):
            with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
                with patch.object(browser_auth, "read_from_live_browser", side_effect=AssertionError("offline read contacted CDP")):
                    result = browser_auth.read_local_storage(
                        profile, origin="https://example.invalid", name="auth.", match="prefix", offline=True
                    )

        self.assertEqual(result, [
            {"key": "auth.access", "value": "synthetic-access"},
            {"key": "auth.refresh", "value": "synthetic-refresh"},
        ])

    def test_live_local_storage_uses_getters_only(self):
        target = {"webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/page/abc"}
        with patch.object(browser_auth, "_websocket_cdp", return_value={"result": {"value": [
            {"key": "auth.access", "value": "synthetic-access"},
        ]}}) as cdp:
            result = browser_auth._cdp_records_for_target(
                target, "https://example.invalid", "auth.", "prefix", "localStorage"
            )
        expression = cdp.call_args.args[2]["expression"]
        self.assertEqual(cdp.call_args.args[1], "Runtime.evaluate")
        self.assertIn("const storage=localStorage", expression)
        self.assertIn("storage.getItem", expression)
        self.assertNotIn("storage.setItem", expression)
        self.assertEqual(result, [{"key": "auth.access", "value": "synthetic-access"}])

    def test_retries_read_when_leveldb_changes_during_parse(self):
        store = SimpleNamespace(
            iter_storage_keys=lambda: iter(["https://example.invalid"]),
            iter_records_for_storage_key=lambda *_args, **_kwargs: iter([
                SimpleNamespace(script_key="token", value="synthetic", leveldb_seq_number=1, is_live=True)
            ]),
        )
        profile = self.profile()
        with patch.object(browser_auth, "open_local_store", return_value=self.store_context(store)):
            with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
                with patch.object(browser_auth, "read_from_live_browser", return_value=None):
                    with patch.object(browser_auth, "_fingerprint_tree", side_effect=[("before",), ("after",), ("stable",), ("stable",)]):
                        result = browser_auth.read_local_storage(profile, origin="https://example.invalid", name="token")
        self.assertEqual(result, [{"key": "token", "value": "synthetic"}])

    def test_offline_local_storage_rejects_symlinked_leveldb_directory(self):
        with tempfile.TemporaryDirectory() as outside:
            profile = self.profile()
            leveldb = profile / "Local Storage" / "leveldb"
            for item in leveldb.iterdir():
                item.unlink()
            leveldb.rmdir()
            leveldb.symlink_to(outside, target_is_directory=True)
            with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
                with self.assertRaisesRegex(browser_auth.BrowserAuthError, "symbolic link"):
                    browser_auth.read_local_storage(
                        profile, origin="https://example.invalid", name="key", offline=True
                    )

    def test_offline_local_storage_rejects_symlinked_leveldb_file(self):
        with tempfile.TemporaryDirectory() as outside:
            profile = self.profile()
            outside_file = Path(outside) / "000001.log"
            outside_file.write_text("synthetic", encoding="utf-8")
            (profile / "Local Storage" / "leveldb" / "000001.log").symlink_to(outside_file)
            with self.assertRaisesRegex(browser_auth.BrowserAuthError, "symbolic link"):
                browser_auth._fingerprint_tree(profile / "Local Storage" / "leveldb")

    def test_dependency_warning_never_writes_record_values_to_stderr(self):
        sentinel = "SYNTHETIC_LOCALSTORAGE_SENTINEL"
        store = SimpleNamespace(
            iter_storage_keys=lambda: iter(["https://example.invalid"]),
            iter_records_for_storage_key=lambda *_args, **_kwargs: iter([
                SimpleNamespace(script_key="key", value="safe", leveldb_seq_number=1, is_live=True)
            ]),
        )

        class WarningStore:
            def __enter__(self):
                logging.getLogger("ccl_chromium_reader.ccl_chromium_localstorage").warning(
                    "bad value: %r", sentinel
                )
                return store

            def __exit__(self, *_args):
                return False

        profile = self.profile()
        stderr = io.StringIO()
        with patch.object(browser_auth, "open_local_store", return_value=WarningStore()):
            with patch.object(browser_auth, "_validate_profile_path", return_value=profile):
                with redirect_stderr(stderr):
                    browser_auth.read_local_storage(
                        profile, origin="https://example.invalid", name="key", offline=True
                    )
        self.assertNotIn(sentinel, stderr.getvalue())


class OfflineAdapterCommandTests(unittest.TestCase):
    def test_offline_command_requires_read_only_mount_and_never_uses_cdp(self):
        request = {
            "profile_path": "/profile",
            "origin": "https://example.invalid",
            "selector": {"kind": "localStorage", "name": "auth.", "match": "prefix"},
            "adapter": {"source": "def run(credentials, config): return {'status': 'ok'}", "config": {}},
        }
        stdin = type("Input", (), {"buffer": io.BytesIO(json.dumps(request).encode())})()
        stdout = io.StringIO()
        stderr = io.StringIO()
        with patch.object(sys, "argv", ["browser-authentication-helper", "run-adapter-offline"]):
            with patch.object(sys, "stdin", stdin):
                with patch.object(browser_auth, "require_read_only_profile") as readonly:
                    with patch.object(browser_auth, "read_local_storage", return_value=[{"key": "auth.x", "value": "synthetic"}]) as read:
                        with patch.object(browser_auth, "run_adapter", return_value={"status": "ok"}) as adapter:
                            with patch.object(browser_auth, "read_from_live_browser", side_effect=AssertionError("offline command contacted CDP")):
                                with redirect_stdout(stdout), redirect_stderr(stderr):
                                    result = browser_auth.main()

        self.assertEqual(result, 0)
        self.assertEqual(stdout.getvalue(), '{"status":"ok"}\n')
        self.assertEqual(stderr.getvalue(), "")
        readonly.assert_called_once_with(Path("/profile"))
        read.assert_called_once_with(
            Path("/profile"), origin="https://example.invalid", name="auth.",
            match="prefix", allow_empty=False, offline=True,
        )
        adapter.assert_called_once()

    def test_offline_command_refuses_writable_profile_filesystem(self):
        with tempfile.TemporaryDirectory() as temp:
            with self.assertRaisesRegex(browser_auth.BrowserAuthError, "read-only profile mount"):
                browser_auth.require_read_only_profile(Path(temp))


class AdapterTransportTests(unittest.TestCase):
    def test_rejects_oversized_or_non_object_adapter_output(self):
        for output in (b"[]", b"{} trailing", b"{" + b"x" * (browser_auth.MAX_ADAPTER_OUTPUT + 1)):
            with self.subTest(output_length=len(output)):
                with self.assertRaises(browser_auth.BrowserAuthError):
                    browser_auth.parse_adapter_output(output)

    @unittest.skipUnless(sys.platform == "linux" and hasattr(__import__("os"), "memfd_create"), "Linux memfd required")
    def test_response_is_json_only_and_does_not_echo_secret_on_adapter_failure(self):
        secret = "synthetic-refresh-token"
        result = browser_auth.execute_adapter_for_test(
            source='raise RuntimeError("adapter failed")',
            config={},
            credential=[{"key": "rt", "value": secret}],
        )
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn(secret.encode(), result.stdout)
        self.assertNotIn(secret.encode(), result.stderr)

    @unittest.skipUnless(sys.platform == "linux" and hasattr(__import__("os"), "memfd_create"), "Linux memfd required")
    def test_adapter_receives_selected_value_only_on_stdin_and_returns_json(self):
        source = "def run(credentials, config):\n    return {'status': config['status'], 'seen': credentials[0]['value']}\n"
        result = browser_auth.execute_adapter_for_test(
            source=source,
            config={"status": "refreshed"},
            credential=[{"key": "session-key", "value": "synthetic-secret"}],
        )
        self.assertEqual(result.returncode, 0)
        self.assertEqual(
            browser_auth.parse_adapter_output(result.stdout),
            {"status": "refreshed", "seen": "synthetic-secret"},
        )


class ReadOnlyCDPTests(unittest.TestCase):
    def test_malformed_requested_origins_are_sanitized(self):
        for origin in ("https://[bad", "https://example.invalid:99999"):
            with self.subTest(origin=origin):
                with self.assertRaisesRegex(browser_auth.BrowserAuthError, "invalid origin"):
                    browser_auth._normalized_origin(origin)

    def test_live_origin_matching_preserves_trailing_dot_and_rejects_unicode_confusables(self):
        targets = [
            {"type": "page", "url": "https://faß.de/", "webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/page/a"},
            {"type": "page", "url": "https://example.invalid./", "webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/page/b"},
            {"type": "page", "url": "https://example.invalid/path", "webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/page/c"},
        ]
        with patch.object(browser_auth, "_cdp_targets", return_value=targets):
            with patch.object(browser_auth, "_cdp_records_for_target", return_value=[{"key": "sid", "value": "synthetic"}]) as query:
                result = browser_auth.read_from_live_browser(
                    origin="https://example.invalid", name="sid", match="exact", kind="sessionStorage"
                )

        self.assertEqual(result, [{"key": "sid", "value": "synthetic"}])
        query.assert_called_once_with(
            targets[2], "https://example.invalid", "sid", "exact", "sessionStorage"
        )

    def test_queries_only_matching_open_origin_and_rejects_conflicting_tabs(self):
        targets = [
            {"type": "page", "url": "https://example.invalid/account", "webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/page/abc"},
            {"type": "page", "url": "https://other.invalid/", "webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/page/def"},
        ]
        with patch.object(browser_auth, "_cdp_targets", return_value=targets):
            with patch.object(browser_auth, "_cdp_records_for_target", return_value=[{"key": "sid", "value": "synthetic"}]) as query:
                result = browser_auth.read_from_live_browser(
                    origin="https://example.invalid", name="sid", match="exact", kind="sessionStorage"
                )
        self.assertEqual(result, [{"key": "sid", "value": "synthetic"}])
        query.assert_called_once_with(
            targets[0], "https://example.invalid", "sid", "exact", "sessionStorage"
        )

        targets.append({"type": "page", "url": "https://example.invalid/second", "webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/page/ghi"})
        with patch.object(browser_auth, "_cdp_targets", return_value=targets):
            with patch.object(browser_auth, "_cdp_records_for_target", side_effect=[
                [{"key": "sid", "value": "one"}], [{"key": "sid", "value": "two"}]
            ]):
                with self.assertRaisesRegex(browser_auth.BrowserAuthError, "different storage"):
                    browser_auth.read_from_live_browser(
                        origin="https://example.invalid", name="sid", match="exact", kind="sessionStorage"
                    )

    def test_rejects_more_matching_tabs_than_the_checked_limit(self):
        targets = [
            {
                "type": "page",
                "url": f"https://example.invalid/tab-{index}",
                "webSocketDebuggerUrl": f"ws://127.0.0.1:9222/devtools/page/{index}",
            }
            for index in range(33)
        ]
        with patch.object(browser_auth, "_cdp_targets", return_value=targets):
            with patch.object(browser_auth, "_cdp_records_for_target", return_value=[]):
                with self.assertRaisesRegex(browser_auth.BrowserAuthError, "too many matching tabs"):
                    browser_auth.read_from_live_browser(
                        origin="https://example.invalid", name="sid", match="exact", kind="sessionStorage"
                    )

    def test_session_storage_command_uses_getters_only(self):
        target = {"webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/page/abc"}
        with patch.object(browser_auth, "_websocket_cdp", return_value={"result": {"value": [{"key": "sid", "value": "synthetic"}]}}) as cdp:
            records = browser_auth._cdp_records_for_target(
                target, "https://example.invalid", "sid", "exact", "sessionStorage"
            )
        command = cdp.call_args.args[1]
        expression = cdp.call_args.args[2]["expression"]
        self.assertEqual(command, "Runtime.evaluate")
        self.assertIn("const storage=sessionStorage", expression)
        self.assertIn("storage.getItem", expression)
        self.assertNotIn("storage.setItem", expression)
        self.assertEqual(records, [{"key": "sid", "value": "synthetic"}])

        with patch.object(browser_auth, "_websocket_cdp", return_value={"result": {"value": [
            {"key": "rt-home-env", "value": "synthetic-refresh"},
            {"key": "theme", "value": "dark"},
        ]}}) as cdp:
            records = browser_auth._cdp_records_for_target(
                target, "https://example.invalid", "", "all", "sessionStorage"
            )
        self.assertIn("mode==='all'", cdp.call_args.args[2]["expression"])
        self.assertEqual(len(records), 2)

    def test_websocket_target_cannot_redirect_helper_away_from_loopback(self):
        with patch.object(browser_auth.socket, "create_connection") as connect:
            with self.assertRaisesRegex(browser_auth.BrowserAuthError, "invalid browser target"):
                browser_auth._websocket_cdp(
                    "ws://attacker.example:9222/devtools/page/abc", "Runtime.evaluate", {}
                )
        connect.assert_not_called()


class ReloadCommandTests(unittest.TestCase):
    def access_config(self, allowed_urls=None):
        temp = tempfile.TemporaryDirectory()
        self.addCleanup(temp.cleanup)
        path = Path(temp.name) / "access-config.json"
        config = {} if allowed_urls is None else {"allowed_urls": allowed_urls}
        path.write_text(json.dumps(config), encoding="utf-8")
        return path

    def target(self, url, suffix):
        return {
            "type": "page",
            "url": url,
            "webSocketDebuggerUrl": f"ws://127.0.0.1:9222/devtools/page/{suffix}",
        }

    def test_reload_uses_page_reload_only_for_one_exact_configured_origin(self):
        config = self.access_config(["https://app.example", "https://login.example"])
        target = self.target("https://app.example/account", "one")
        unrelated = self.target("https://app.example.evil.invalid/", "two")
        with patch.object(browser_auth, "ACCESS_CONFIG_PATH", config):
            with patch.object(browser_auth, "_cdp_targets", return_value=[target, unrelated]):
                with patch.object(browser_auth, "_websocket_cdp", return_value={}) as send:
                    result = browser_auth.reload_configured_origin("https://app.example")

        self.assertEqual(result, {"status": "reloaded", "tabs": 1})
        send.assert_called_once_with(
            target["webSocketDebuggerUrl"], "Page.reload", {},
            wait_for_event="Page.loadEventFired", timeout=browser_auth.CDP_RELOAD_TIMEOUT,
        )

    def test_reload_reports_no_matching_tab_without_navigating(self):
        config = self.access_config(["https://app.example"])
        with patch.object(browser_auth, "ACCESS_CONFIG_PATH", config):
            with patch.object(browser_auth, "_cdp_targets", return_value=[self.target("https://other.example/", "one")]):
                with patch.object(browser_auth, "_websocket_cdp") as send:
                    result = browser_auth.reload_configured_origin("https://app.example")

        self.assertEqual(result, {"status": "not_found", "tabs": 0})
        send.assert_not_called()

    def test_reload_refuses_multiple_matching_tabs_without_reloading_either(self):
        config = self.access_config(["https://app.example"])
        targets = [self.target("https://app.example/one", "one"), self.target("https://app.example/two", "two")]
        with patch.object(browser_auth, "ACCESS_CONFIG_PATH", config):
            with patch.object(browser_auth, "_cdp_targets", return_value=targets):
                with patch.object(browser_auth, "_websocket_cdp") as send:
                    result = browser_auth.reload_configured_origin("https://app.example")

        self.assertEqual(result, {"status": "ambiguous", "tabs": 2})
        send.assert_not_called()

    def test_reload_reports_not_configured_for_page_outside_nonempty_allowlist(self):
        config = self.access_config(["https://app.example"])
        with patch.object(browser_auth, "ACCESS_CONFIG_PATH", config):
            with patch.object(browser_auth, "_cdp_targets", return_value=[self.target("https://login.example/", "one")]):
                with patch.object(browser_auth, "_websocket_cdp") as send:
                    result = browser_auth.reload_configured_origin("https://login.example")

        self.assertEqual(result, {"status": "not_configured", "tabs": 0})
        send.assert_not_called()

    def test_reload_policy_gate_accepts_domain_filter_for_exact_subdomain_page(self):
        config = self.access_config(["https://example"])
        target = self.target("https://app.example/sso/redirect", "one")
        with patch.object(browser_auth, "ACCESS_CONFIG_PATH", config):
            with patch.object(browser_auth, "_cdp_targets", return_value=[target]):
                with patch.object(browser_auth, "_websocket_cdp", return_value={}) as send:
                    result = browser_auth.reload_configured_origin("https://app.example")

        self.assertEqual(result, {"status": "reloaded", "tabs": 1})
        send.assert_called_once()

    def test_reload_policy_gate_rejects_hostname_suffix_confusables(self):
        config = self.access_config(["https://example"])
        target = self.target("https://evilns.nl/sso", "one")
        with patch.object(browser_auth, "ACCESS_CONFIG_PATH", config):
            with patch.object(browser_auth, "_cdp_targets", return_value=[target]):
                with patch.object(browser_auth, "_websocket_cdp") as send:
                    result = browser_auth.reload_configured_origin("https://evilns.nl")

        self.assertEqual(result, {"status": "not_configured", "tabs": 0})
        send.assert_not_called()

    def test_reload_policy_gate_observes_port_and_path_filters(self):
        cases = [
            ("https://example:8443/private", "https://app.example:8443/private/login", "reloaded"),
            ("https://example:8443/private", "https://app.example/private/login", "not_found"),
            ("https://example:8443/private", "https://app.example:8443/public", "not_configured"),
        ]
        for pattern, url, expected in cases:
            with self.subTest(pattern=pattern, url=url, expected=expected):
                config = self.access_config([pattern])
                target = self.target(url, "one")
                with patch.object(browser_auth, "ACCESS_CONFIG_PATH", config):
                    with patch.object(browser_auth, "_cdp_targets", return_value=[target]):
                        with patch.object(browser_auth, "_websocket_cdp", return_value={}) as send:
                            if expected == "reloaded":
                                result = browser_auth.reload_configured_origin("https://app.example:8443")
                                self.assertEqual(result, {"status": expected, "tabs": 1})
                                send.assert_called_once()
                            elif expected == "not_found":
                                result = browser_auth.reload_configured_origin("https://app.example:8443")
                                self.assertEqual(result, {"status": expected, "tabs": 0})
                                send.assert_not_called()
                            else:
                                result = browser_auth.reload_configured_origin("https://app.example:8443")
                                self.assertEqual(result, {"status": expected, "tabs": 0})
                                send.assert_not_called()

    def test_reload_allows_exact_origin_when_navigation_policy_is_unrestricted(self):
        target = self.target("https://app.example/account", "one")
        for allowed_urls in (None, []):
            with self.subTest(allowed_urls=allowed_urls):
                config = self.access_config(allowed_urls)
                with patch.object(browser_auth, "ACCESS_CONFIG_PATH", config):
                    with patch.object(browser_auth, "_cdp_targets", return_value=[target]):
                        with patch.object(browser_auth, "_websocket_cdp", return_value={}) as send:
                            result = browser_auth.reload_configured_origin("https://app.example")

                self.assertEqual(result, {"status": "reloaded", "tabs": 1})
                send.assert_called_once()

    def test_reload_accepts_chrome_exact_host_pattern_for_its_canonical_host(self):
        config = self.access_config(["https://.app.example"])
        target = self.target("https://app.example/account", "one")
        with patch.object(browser_auth, "ACCESS_CONFIG_PATH", config):
            with patch.object(browser_auth, "_cdp_targets", return_value=[target]):
                with patch.object(browser_auth, "_websocket_cdp", return_value={}) as send:
                    result = browser_auth.reload_configured_origin("https://app.example")

        self.assertEqual(result, {"status": "reloaded", "tabs": 1})
        send.assert_called_once_with(
            target["webSocketDebuggerUrl"], "Page.reload", {},
            wait_for_event="Page.loadEventFired", timeout=browser_auth.CDP_RELOAD_TIMEOUT,
        )

    def test_reload_cli_accepts_bounded_origin_json_and_returns_sanitized_status(self):
        stdin = type("Input", (), {"buffer": io.BytesIO(b'{"origin":"https://app.example"}')})()
        stdout = io.StringIO()
        stderr = io.StringIO()
        with patch.object(sys, "argv", ["browser-authentication-helper", "reload"]):
            with patch.object(sys, "stdin", stdin):
                with patch.object(browser_auth, "reload_configured_origin", return_value={"status": "reloaded", "tabs": 1}) as reload_site:
                    with redirect_stdout(stdout), redirect_stderr(stderr):
                        result = browser_auth.main()

        self.assertEqual(result, 0)
        self.assertEqual(stdout.getvalue(), '{"status":"reloaded","tabs":1}\n')
        self.assertEqual(stderr.getvalue(), "")
        reload_site.assert_called_once_with("https://app.example")

    def test_reload_cli_rejects_unknown_request_fields_without_echoing_input(self):
        stdin = type("Input", (), {"buffer": io.BytesIO(b'{"origin":"https://app.example","url":"https://evil.invalid"}')})()
        stdout = io.StringIO()
        stderr = io.StringIO()
        with patch.object(sys, "argv", ["browser-authentication-helper", "reload"]):
            with patch.object(sys, "stdin", stdin):
                with redirect_stdout(stdout), redirect_stderr(stderr):
                    result = browser_auth.main()

        self.assertEqual(result, 1)
        self.assertEqual(stdout.getvalue(), "")
        self.assertEqual(stderr.getvalue(), "browser_auth_error=reload request fields are invalid\n")

    def test_reload_cli_returns_not_configured_as_success_json(self):
        stdin = type("Input", (), {"buffer": io.BytesIO(b'{"origin":"https://app.example"}')})()
        stdout = io.StringIO()
        stderr = io.StringIO()
        with patch.object(sys, "argv", ["browser-authentication-helper", "reload"]):
            with patch.object(sys, "stdin", stdin):
                with patch.object(browser_auth, "reload_configured_origin", return_value={"status": "not_configured", "tabs": 0}):
                    with redirect_stdout(stdout), redirect_stderr(stderr):
                        result = browser_auth.main()

        self.assertEqual(result, 0)
        self.assertEqual(stdout.getvalue(), '{"status":"not_configured","tabs":0}\n')
        self.assertEqual(stderr.getvalue(), "")

    def test_reload_cdp_failure_stays_an_error_after_dispatch(self):
        config = self.access_config(["https://app.example"])
        target = self.target("https://app.example/", "one")
        with patch.object(browser_auth, "ACCESS_CONFIG_PATH", config):
            with patch.object(browser_auth, "_cdp_targets", return_value=[target]):
                with patch.object(browser_auth, "_websocket_cdp", side_effect=browser_auth.BrowserAuthError("browser reload timed out")) as send:
                    with self.assertRaisesRegex(browser_auth.BrowserAuthError, "browser reload timed out"):
                        browser_auth.reload_configured_origin("https://app.example")

        send.assert_called_once()

    def test_cdp_reload_waits_for_both_acknowledgement_and_load_event(self):
        class FakeSocket:
            def __init__(self):
                self.response = bytearray()
                self.sent = []

            def __enter__(self):
                return self

            def __exit__(self, *_args):
                return False

            def settimeout(self, _timeout):
                pass

            def sendall(self, value):
                self.sent.append(value)
                if value.startswith(b"GET "):
                    request_key = next(
                        line.split(b":", 1)[1].strip()
                        for line in value.split(b"\r\n") if line.startswith(b"Sec-WebSocket-Key:")
                    )
                    accept = base64.b64encode(
                        hashlib.sha1(request_key + browser_auth.CDP_MAGIC.encode("ascii")).digest()
                    )
                    self.response.extend(
                        b"HTTP/1.1 101 Switching Protocols\r\nSec-WebSocket-Accept: "
                        + accept + b"\r\n\r\n"
                    )

            def recv(self, size):
                if not self.response:
                    return b""
                result = self.response[:size]
                del self.response[:size]
                return bytes(result)

        sock = FakeSocket()
        responses = [
            b'{"id":1,"result":{}}',
            b'{"id":2,"result":{}}',
            b'{"method":"Page.loadEventFired","params":{"timestamp":1}}',
        ]
        with patch.object(browser_auth.socket, "create_connection", return_value=sock):
            with patch.object(browser_auth, "_recv_ws_message", side_effect=responses) as receive:
                result = browser_auth._websocket_cdp(
                    "ws://127.0.0.1:9222/devtools/page/abc",
                    "Page.reload", {}, wait_for_event="Page.loadEventFired", timeout=1,
                )

        self.assertEqual(result, {})
        self.assertEqual(receive.call_count, 3)
        self.assertEqual(len(sock.sent), 3)  # handshake, Page.enable, Page.reload


class CookieSelectionTests(unittest.TestCase):
    def test_named_cookie_list_from_live_cdp_target_preserves_request_order(self):
        target = {
            "type": "page",
            "url": "https://example.invalid/account",
            "webSocketDebuggerUrl": "ws://127.0.0.1:9222/devtools/page/abc",
        }
        with patch.object(browser_auth, "_cdp_targets", return_value=[target]):
            with patch.object(browser_auth, "_websocket_cdp", return_value={"cookies": [
                {"name": "csrf", "value": "synthetic-csrf"},
                {"name": "other", "value": "ignored"},
                {"name": "sid", "value": "synthetic-session"},
            ]}) as cdp:
                with patch.object(browser_auth, "_validate_profile_path", return_value=Path("/synthetic/profile")):
                    with patch.object(browser_auth, "read_cookie_rows") as offline:
                        result = browser_auth.read_cookies(
                            Path("/synthetic/profile"), origin="https://example.invalid", names=["sid", "csrf"]
                        )
        self.assertEqual(result, [
            {"key": "sid", "value": "synthetic-session"},
            {"key": "csrf", "value": "synthetic-csrf"},
        ])
        self.assertEqual(cdp.call_count, 1)
        offline.assert_not_called()

    def test_named_cookie_list_uses_one_selection_and_fails_if_any_name_is_missing(self):
        rows = [
            {"host_key": ".example.invalid", "name": "sid", "value": "synthetic-session", "encrypted_value": b""},
            {"host_key": ".example.invalid", "name": "csrf", "value": "synthetic-csrf", "encrypted_value": b""},
        ]
        with patch.object(browser_auth, "read_cookie_rows", return_value=rows) as read_rows:
            with patch.object(browser_auth, "_validate_profile_path", return_value=Path("/synthetic/profile")):
                result = browser_auth.read_cookies(
                    Path("/synthetic/profile"), origin="https://example.invalid", names=["sid", "csrf"]
                )
        self.assertEqual(result, [
            {"key": "sid", "value": "synthetic-session"},
            {"key": "csrf", "value": "synthetic-csrf"},
        ])
        read_rows.assert_called_once()

        with patch.object(browser_auth, "read_cookie_rows", return_value=rows[:1]):
            with patch.object(browser_auth, "_validate_profile_path", return_value=Path("/synthetic/profile")):
                with self.assertRaisesRegex(browser_auth.BrowserAuthError, "exactly one cookie"):
                    browser_auth.read_cookies(
                        Path("/synthetic/profile"), origin="https://example.invalid", names=["sid", "missing"]
                    )

    def test_helper_request_accepts_named_cookie_lists_and_storage_selectors(self):
        base = {"profile_path": "/profile", "origin": "https://example.invalid", "adapter": {"source": "def run(credentials, config): return {}", "config": {}}}
        cookie_request = {**base, "selector": {"kind": "cookie", "names": ["sid", "csrf"], "match": "exact"}}
        legacy_cookie_request = {**base, "selector": {"kind": "cookie", "name": "sid", "match": "exact"}}
        storage_request = {**base, "selector": {"kind": "sessionStorage", "name": "auth.", "match": "prefix"}}
        all_storage_request = {**base, "selector": {"kind": "sessionStorage", "name": "", "match": "all", "allow_empty": True}}
        local_storage_request = {**base, "selector": {"kind": "localStorage", "name": "auth.", "match": "prefix"}}
        self.assertEqual(browser_auth.parse_request(json.dumps(cookie_request).encode()), cookie_request)
        self.assertEqual(browser_auth.parse_request(json.dumps(legacy_cookie_request).encode()), legacy_cookie_request)
        self.assertEqual(browser_auth.parse_request(json.dumps(storage_request).encode()), storage_request)
        self.assertEqual(browser_auth.parse_request(json.dumps(all_storage_request).encode()), all_storage_request)
        self.assertEqual(browser_auth.parse_request(json.dumps(local_storage_request).encode()), local_storage_request)

    def test_parent_host_only_cookie_is_not_visible_to_subdomain(self):
        rows = [{"host_key": "example.invalid", "name": "sid", "value": "wrong-host", "encrypted_value": b""}]
        with patch.object(browser_auth, "read_cookie_rows", return_value=rows), patch.object(browser_auth, "_validate_profile_path", return_value=Path("/synthetic/profile")):
            with self.assertRaises(browser_auth.BrowserAuthError):
                browser_auth.read_cookies(Path("/synthetic/profile"), origin="https://child.example.invalid", names=["sid"], offline=True)

    def test_parent_domain_cookie_is_visible_to_subdomain(self):
        rows = [{"host_key": ".example.invalid", "name": "sid", "value": "domain-cookie", "encrypted_value": b""}]
        with patch.object(browser_auth, "read_cookie_rows", return_value=rows), patch.object(browser_auth, "_validate_profile_path", return_value=Path("/synthetic/profile")):
            result = browser_auth.read_cookies(Path("/synthetic/profile"), origin="https://child.example.invalid", names=["sid"], offline=True)
        self.assertEqual(result, [{"key": "sid", "value": "domain-cookie"}])

    def test_plaintext_cookie_is_selected_by_origin_and_name(self):
        rows = [
            {"host_key": ".example.invalid", "name": "sid", "value": "synthetic-cookie", "encrypted_value": b""},
            {"host_key": ".example.invalid", "name": "other", "value": "ignored", "encrypted_value": b""},
            {"host_key": ".other.invalid", "name": "sid", "value": "wrong-origin", "encrypted_value": b""},
        ]
        with patch.object(browser_auth, "read_cookie_rows", return_value=rows):
            with patch.object(browser_auth, "_validate_profile_path", return_value=Path("/synthetic/profile")):
                result = browser_auth.read_cookie(
                    Path("/synthetic/profile"), origin="https://example.invalid", name="sid"
                )
        self.assertEqual(result, [{"key": "sid", "value": "synthetic-cookie"}])

    def test_offline_cookie_database_rejects_symlink_escape(self):
        with tempfile.TemporaryDirectory() as root, tempfile.TemporaryDirectory() as outside:
            profile = Path(root)
            outside_db = Path(outside) / "Cookies"
            outside_db.write_bytes(b"synthetic")
            network = profile / "Network"
            network.mkdir()
            (network / "Cookies").symlink_to(outside_db)
            with self.assertRaisesRegex(browser_auth.BrowserAuthError, "symbolic link"):
                browser_auth._cookie_db(profile)

    def test_offline_cookie_database_rejects_symlinked_parent_directory(self):
        with tempfile.TemporaryDirectory() as root, tempfile.TemporaryDirectory() as outside:
            profile = Path(root)
            network = Path(outside) / "Network"
            network.mkdir()
            (network / "Cookies").write_bytes(b"synthetic")
            (profile / "Network").symlink_to(network, target_is_directory=True)
            with self.assertRaisesRegex(browser_auth.BrowserAuthError, "symbolic link"):
                browser_auth._cookie_db(profile)

    def test_unknown_encrypted_cookie_scheme_fails_closed(self):
        rows = [{"host_key": "example.invalid", "name": "sid", "value": "", "encrypted_value": b"v11encrypted"}]
        with patch.object(browser_auth, "read_cookie_rows", return_value=rows):
            with patch.object(browser_auth, "_validate_profile_path", return_value=Path("/synthetic/profile")):
                with self.assertRaisesRegex(browser_auth.BrowserAuthError, "unsupported encrypted cookie"):
                    browser_auth.read_cookie(
                        Path("/synthetic/profile"), origin="https://example.invalid", name="sid"
                    )

    def test_active_cookie_wal_is_read_through_readonly_transaction(self):
        with tempfile.TemporaryDirectory() as temp:
            cookie_db = Path(temp) / "Cookies"
            writer = __import__("sqlite3").connect(cookie_db)
            writer.execute("PRAGMA journal_mode=WAL")
            writer.execute("CREATE TABLE cookies(host_key TEXT, name TEXT, value TEXT, encrypted_value BLOB, expires_utc INTEGER, is_secure INTEGER)")
            writer.execute("INSERT INTO cookies VALUES(?,?,?,?,?,?)", ("example.invalid", "sid", "synthetic", b"", 0, 0))
            writer.commit()
            wal = Path(str(cookie_db) + "-wal")
            self.assertTrue(wal.exists() and wal.stat().st_size > 0)
            before = (cookie_db.stat().st_size, cookie_db.stat().st_mtime_ns, wal.read_bytes())
            with patch.object(browser_auth, "_cookie_db", return_value=cookie_db):
                rows = browser_auth.read_cookie_rows(Path(temp), host="example.invalid", name="sid")
            self.assertEqual(rows[0]["value"], "synthetic")
            after = (cookie_db.stat().st_size, cookie_db.stat().st_mtime_ns, wal.read_bytes())
            self.assertEqual(after, before)
            writer.close()

    @unittest.skipUnless(importlib.util.find_spec("cryptography"), "cryptography package required")
    def test_decrypts_linux_v10_basic_cookie_from_synthetic_fixture(self):
        from cryptography.hazmat.primitives import hashes, padding
        from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
        from cryptography.hazmat.primitives.kdf.pbkdf2 import PBKDF2HMAC

        host_key = ".example.invalid"
        key = PBKDF2HMAC(algorithm=hashes.SHA1(), length=16, salt=b"saltysalt", iterations=1).derive(b"peanuts")
        clear = hashlib.sha256(host_key.encode()).digest() + b"synthetic-cookie"
        padder = padding.PKCS7(128).padder()
        padded = padder.update(clear) + padder.finalize()
        encryptor = Cipher(algorithms.AES(key), modes.CBC(b" " * 16)).encryptor()
        encrypted = b"v10" + encryptor.update(padded) + encryptor.finalize()
        rows = [{"host_key": host_key, "name": "sid", "value": "", "encrypted_value": encrypted}]

        with patch.object(browser_auth, "read_cookie_rows", return_value=rows):
            with patch.object(browser_auth, "_validate_profile_path", return_value=Path("/synthetic/profile")):
                result = browser_auth.read_cookie(
                    Path("/synthetic/profile"), origin="https://example.invalid", name="sid"
                )
        self.assertEqual(result, [{"key": "sid", "value": "synthetic-cookie"}])


if __name__ == "__main__":
    unittest.main()
