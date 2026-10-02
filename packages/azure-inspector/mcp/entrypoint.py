"""Publish the unchanged upstream Azure SSE server outside its loopback bind.

The upstream version intentionally binds ListenLocalhost. Keep its code/image,
CLI and protocol intact while forwarding the public container port to that bind.
Python is the interpreter already bundled with Azure CLI inside this image.
"""
import asyncio
import os
import signal
import sys

UPSTREAM = "/app/docker-entrypoint.sh"
INTERNAL_PORT = 18084

async def serve(arguments):
    public_port = int(os.environ.get("AZMCP_PORT", "8084"))
    child_env = dict(os.environ, AZMCP_PORT=str(INTERNAL_PORT))
    child = await asyncio.create_subprocess_exec(UPSTREAM, *arguments, env=child_env)

    async def connect(reader, writer):
        upstream_writer = None
        tasks = []
        try:
            upstream_reader, upstream_writer = await asyncio.open_connection("127.0.0.1", INTERNAL_PORT)
            async def copy(source, destination):
                while data := await source.read(65536):
                    destination.write(data)
                    await destination.drain()
            tasks = [asyncio.create_task(copy(reader, upstream_writer)), asyncio.create_task(copy(upstream_reader, writer))]
            await asyncio.wait(tasks, return_when=asyncio.FIRST_COMPLETED)
        except (OSError, asyncio.CancelledError):
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
