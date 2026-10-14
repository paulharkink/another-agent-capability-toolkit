import asyncio
import importlib.util
import json
from pathlib import Path
from unittest import IsolatedAsyncioTestCase
from unittest.mock import AsyncMock, patch

ENTRYPOINT = Path(__file__).resolve().parents[1] / "entrypoint.py"
spec = importlib.util.spec_from_file_location("azure_entrypoint", ENTRYPOINT)
entrypoint = importlib.util.module_from_spec(spec)
spec.loader.exec_module(entrypoint)


class FakeProcess:
    def __init__(self, returncode, stdout=b"", stderr=b""):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr

    async def communicate(self):
        return self.stdout, self.stderr


class AzureTokenProbeTests(IsolatedAsyncioTestCase):
    async def test_token_probe_checks_selected_tenant_then_subscription_token(self):
        self.assertEqual(entrypoint.AZURE_CLI, "az")
        secret = b"synthetic-access-token"
        account = FakeProcess(0, stdout=b'{"tenantId":"tenant-selected"}', stderr=b"synthetic warning")
        token = FakeProcess(0, stdout=secret, stderr=b"synthetic warning")
        processes = [account, token]
        def fake_cli(*args, **kwargs):
            if "--tenant" in args and "--subscription" in args:
                return FakeProcess(2, stderr=b"--tenant and --subscription are mutually exclusive")
            return processes.pop(0)
        with patch.object(entrypoint.asyncio, "create_subprocess_exec", side_effect=fake_cli) as run:
            usable = await entrypoint.has_usable_token("tenant-selected", "subscription-selected")
        self.assertTrue(usable)
        self.assertEqual(run.call_count, 2)
        self.assertEqual(run.call_args_list[0].args[:], (
            entrypoint.AZURE_CLI, "account", "show", "--subscription", "subscription-selected", "--output", "json",
        ))
        token_args = run.call_args_list[1].args[:]
        self.assertEqual(token_args, (
            entrypoint.AZURE_CLI, "account", "get-access-token", "--subscription", "subscription-selected", "--output", "none",
        ))
        self.assertNotIn("--tenant", token_args)
        self.assertNotIn(secret.decode(), repr(usable))
        for call in run.call_args_list:
            self.assertIs(call.kwargs["stdout"], entrypoint.asyncio.subprocess.PIPE)
            self.assertIs(call.kwargs["stderr"], entrypoint.asyncio.subprocess.PIPE)

    async def test_tenant_mismatch_stops_before_token_acquisition(self):
        account = FakeProcess(0, stdout=b'{"tenantId":"another-tenant"}')
        with patch.object(entrypoint.asyncio, "create_subprocess_exec", return_value=account) as run:
            self.assertFalse(await entrypoint.has_usable_token("tenant-selected", "subscription-selected"))
        self.assertEqual(run.call_count, 1)

    async def test_token_probe_failure_is_false_and_suppresses_cli_output(self):
        account = FakeProcess(0, stdout=b'{"tenantId":"tenant-selected"}')
        token = FakeProcess(1, stdout=b"synthetic-token", stderr=b"revoked synthetic-token")
        processes = [account, token]
        def fake_cli(*args, **kwargs):
            if "--tenant" in args and "--subscription" in args:
                return FakeProcess(2, stderr=b"--tenant and --subscription are mutually exclusive")
            return processes.pop(0)
        with patch.object(entrypoint.asyncio, "create_subprocess_exec", side_effect=fake_cli):
            self.assertFalse(await entrypoint.has_usable_token("tenant-selected", "subscription-selected"))


class AzureHealthAndTransportTests(IsolatedAsyncioTestCase):
    async def test_health_route_reports_auth_only_when_backend_is_ready_and_child_is_alive(self):
        async def backend(reader, writer):
            writer.close()
            await writer.wait_closed()
        backend_server = await asyncio.start_server(backend, "127.0.0.1", 0)
        backend_port = backend_server.sockets[0].getsockname()[1]
        child = type("Child", (), {"returncode": None})()
        front = await asyncio.start_server(
            lambda reader, writer: entrypoint.handle_connection(
                reader, writer, child=child, tenant_id="tenant-selected",
                subscription_id="subscription-selected", internal_port=backend_port,
            ),
            "127.0.0.1", 0,
        )
        front_port = front.sockets[0].getsockname()[1]
        try:
            with patch.object(entrypoint, "has_usable_token", new=AsyncMock(return_value=True)) as probe:
                first = await self.request_health(front_port)
                child.returncode = 1
                second = await self.request_health(front_port)
            self.assertEqual(json.loads(first.split(b"\r\n\r\n", 1)[1]), {"authenticated": True})
            self.assertEqual(json.loads(second.split(b"\r\n\r\n", 1)[1]), {"authenticated": False})
            self.assertEqual(probe.await_count, 1)
            self.assertIn(b"HTTP/1.1 200", first)
            self.assertIn(b"HTTP/1.1 200", second)
        finally:
            front.close()
            backend_server.close()
            await front.wait_closed()
            await backend_server.wait_closed()

    async def test_wrapper_shutdown_terminates_owned_child(self):
        class Child:
            def __init__(self):
                self.returncode = None
                self.exited = asyncio.Event()
                self.terminated = False
            async def wait(self):
                await self.exited.wait()
                return self.returncode
            def terminate(self):
                self.terminated = True
                self.returncode = 0
                self.exited.set()
            def kill(self):
                self.returncode = -9
                self.exited.set()

        class Server:
            async def __aenter__(self):
                return self
            async def __aexit__(self, *_):
                return None

        child = Child()
        loop = asyncio.get_running_loop()
        with patch.object(entrypoint.asyncio, "create_subprocess_exec", return_value=child), \
             patch.object(entrypoint.asyncio, "start_server", return_value=Server()), \
             patch.object(loop, "add_signal_handler", side_effect=lambda _signal, callback: loop.call_soon(callback)):
            result = await entrypoint.serve(["server", "start"])
        self.assertTrue(child.terminated)
        self.assertEqual(result, 0)

    async def test_health_stays_false_until_backend_is_listening(self):
        temporary = await asyncio.start_server(lambda r, w: None, "127.0.0.1", 0)
        unused_port = temporary.sockets[0].getsockname()[1]
        temporary.close()
        await temporary.wait_closed()
        front = await asyncio.start_server(
            lambda reader, writer: entrypoint.handle_connection(
                reader, writer, child=type("Child", (), {"returncode": None})(),
                tenant_id="tenant-selected", subscription_id="subscription-selected",
                internal_port=unused_port,
            ),
            "127.0.0.1", 0,
        )
        front_port = front.sockets[0].getsockname()[1]
        try:
            with patch.object(entrypoint, "has_usable_token", new=AsyncMock(return_value=True)) as probe:
                response = await self.request_health(front_port)
            self.assertEqual(json.loads(response.split(b"\r\n\r\n", 1)[1]), {"authenticated": False})
            probe.assert_not_awaited()
        finally:
            front.close()
            await front.wait_closed()

    async def request_health(self, port):
        reader, writer = await asyncio.open_connection("127.0.0.1", port)
        writer.write(b"GET /health HTTP/1.1\r\nHost: localhost\r\n\r\n")
        await writer.drain()
        response = await reader.read()
        writer.close()
        await writer.wait_closed()
        return response

    async def test_non_health_sse_bytes_are_forwarded_unchanged(self):
        request = b"GET /sse HTTP/1.1\r\nHost: localhost\r\nX-Synthetic: exact\r\n\r\n"
        upstream_response = b"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\n\r\ndata: exact\n\n"
        received = bytearray()
        async def backend(reader, writer):
            received.extend(await reader.readexactly(len(request)))
            writer.write(upstream_response)
            await writer.drain()
            writer.close()
            await writer.wait_closed()
        backend_server = await asyncio.start_server(backend, "127.0.0.1", 0)
        backend_port = backend_server.sockets[0].getsockname()[1]
        front = await asyncio.start_server(
            lambda reader, writer: entrypoint.handle_connection(reader, writer, internal_port=backend_port),
            "127.0.0.1", 0,
        )
        front_port = front.sockets[0].getsockname()[1]
        try:
            reader, writer = await asyncio.open_connection("127.0.0.1", front_port)
            writer.write(request)
            await writer.drain()
            actual_response = await reader.read()
            writer.close()
            await writer.wait_closed()
        finally:
            front.close()
            backend_server.close()
            await front.wait_closed()
            await backend_server.wait_closed()
        self.assertEqual(bytes(received), request)
        self.assertEqual(actual_response, upstream_response)
