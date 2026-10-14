"""Publish the unchanged upstream Azure SSE server outside its loopback bind.

The upstream version intentionally binds ListenLocalhost. Keep its code/image,
CLI and protocol intact while forwarding the public port to that bind. Python is
the interpreter already bundled with Azure CLI inside this image.
"""
import asyncio
import json
import os
import signal
import subprocess
import sys
from urllib.parse import urlsplit

UPSTREAM = "/app/docker-entrypoint.sh"
AZURE_CLI = "az"
INTERNAL_PORT = 18084
HEALTH_TIMEOUT_SECONDS = 10


async def has_usable_token(tenant_id, subscription_id):
    """Return only whether the selected account can obtain an access token."""
    try:
        account = await asyncio.create_subprocess_exec(
            AZURE_CLI,
            "account", "show", "--subscription", subscription_id, "--output", "json",
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        try:
            account_output, _ = await asyncio.wait_for(account.communicate(), HEALTH_TIMEOUT_SECONDS)
        except asyncio.TimeoutError:
            account.kill()
            await account.wait()
            return False
        if account.returncode != 0:
            return False
        selected = json.loads(account_output).get("tenantId", "")
        if not isinstance(selected, str) or selected.casefold() != tenant_id.casefold():
            return False

        token = await asyncio.create_subprocess_exec(
            AZURE_CLI,
            "account", "get-access-token", "--subscription", subscription_id, "--output", "none",
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
        )
        try:
            await asyncio.wait_for(token.communicate(), HEALTH_TIMEOUT_SECONDS)
        except asyncio.TimeoutError:
            token.kill()
            await token.wait()
            return False
        return token.returncode == 0
    except (OSError, subprocess.SubprocessError, json.JSONDecodeError, AttributeError):
        return False


async def backend_ready(internal_port):
    try:
        reader, writer = await asyncio.wait_for(
            asyncio.open_connection("127.0.0.1", internal_port), timeout=0.5
        )
        writer.close()
        await writer.wait_closed()
        return True
    except (OSError, asyncio.TimeoutError):
        return False


def health_response(authenticated):
    body = json.dumps({"authenticated": bool(authenticated)}, separators=(",", ":")).encode()
    return (
        b"HTTP/1.1 200 OK\r\n"
        b"Content-Type: application/json\r\n"
        + f"Content-Length: {len(body)}\r\nConnection: close\r\n\r\n".encode()
        + body
    )


async def handle_connection(reader, writer, *, child=None, tenant_id="", subscription_id="", internal_port=INTERNAL_PORT):
    upstream_writer = None
    tasks = []
    try:
        request_head = await reader.readuntil(b"\r\n\r\n")
        request_line = request_head.split(b"\r\n", 1)[0].split()
        if len(request_line) >= 2 and request_line[0] == b"GET" and urlsplit(request_line[1].decode("ascii", "ignore")).path == "/health":
            ready = child is None or child.returncode is None
            authenticated = (
                ready
                and bool(tenant_id)
                and bool(subscription_id)
                and await backend_ready(internal_port)
                and await has_usable_token(tenant_id, subscription_id)
                and (child is None or child.returncode is None)
            )
            writer.write(health_response(authenticated))
            await writer.drain()
            return

        upstream_reader, upstream_writer = await asyncio.open_connection("127.0.0.1", internal_port)
        upstream_writer.write(request_head)
        await upstream_writer.drain()

        async def copy(source, destination):
            while data := await source.read(65536):
                destination.write(data)
                await destination.drain()

        tasks = [
            asyncio.create_task(copy(reader, upstream_writer)),
            asyncio.create_task(copy(upstream_reader, writer)),
        ]
        await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
    except (OSError, asyncio.IncompleteReadError, asyncio.LimitOverrunError, asyncio.CancelledError):
        pass
    finally:
        for task in tasks:
            task.cancel()
        if tasks:
            await asyncio.gather(*tasks, return_exceptions=True)
        writer.close()
        if upstream_writer:
            upstream_writer.close()
        try:
            await writer.wait_closed()
            if upstream_writer:
                await upstream_writer.wait_closed()
        except OSError:
            pass


async def serve(arguments):
    public_port = int(os.environ.get("AZMCP_PORT", "8084"))
    tenant_id = os.environ.get("AACT_AZURE_TENANT_ID", "")
    subscription_id = os.environ.get("AACT_AZURE_SUBSCRIPTION_ID", "")
    child_env = dict(os.environ, AZMCP_PORT=str(INTERNAL_PORT))
    child = await asyncio.create_subprocess_exec(UPSTREAM, *arguments, env=child_env)

    async def connect(reader, writer):
        await handle_connection(
            reader, writer, child=child, tenant_id=tenant_id,
            subscription_id=subscription_id, internal_port=INTERNAL_PORT,
        )

    try:
        server = await asyncio.start_server(connect, "0.0.0.0", public_port)
        stopped = asyncio.Event()
        loop = asyncio.get_running_loop()
        for sig in (signal.SIGINT, signal.SIGTERM):
            loop.add_signal_handler(sig, stopped.set)
        wait_child = asyncio.create_task(child.wait())
        wait_stop = asyncio.create_task(stopped.wait())
        async with server:
            await asyncio.wait([wait_child, wait_stop], return_when=asyncio.FIRST_COMPLETED)
        wait_stop.cancel()
        if child.returncode is None:
            child.terminate()
            try:
                await asyncio.wait_for(wait_child, timeout=5)
            except asyncio.TimeoutError:
                child.kill()
                await wait_child
        return child.returncode or 0
    finally:
        if child.returncode is None:
            child.kill()
            await child.wait()


if __name__ == "__main__":
    arguments = sys.argv[1:]
    if arguments[:2] != ["server", "start"] or os.environ.get("AZMCP_TRANSPORT", "stdio") != "sse":
        os.execv(UPSTREAM, [UPSTREAM, *arguments])
    sys.exit(asyncio.run(serve(arguments)))
