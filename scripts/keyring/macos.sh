#!/usr/bin/python3
import json
import shlex
import subprocess
import sys


def reply(status, secret=None):
    value = {"status": status}
    if secret is not None:
        value["secret"] = secret
    sys.stdout.write(json.dumps(value, separators=(",", ":")) + "\n")


try:
    request = json.load(sys.stdin)
    if request.get("version") != 1 or request.get("service") != "AACT shared Vault":
        reply("unavailable")
        raise SystemExit(0)
    action = request.get("action")
    account = request.get("account")
    if not isinstance(account, str) or not account.startswith("state-"):
        reply("unavailable")
        raise SystemExit(0)

    security = "/usr/bin/security"
    if action == "get":
        result = subprocess.run(
            [security, "find-generic-password", "-s", request["service"], "-a", account, "-w"],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            check=False,
        )
        if result.returncode != 0:
            reply("missing")
        else:
            encoded = result.stdout.rstrip(b"\r\n")
            reply("ok", encoded.decode("ascii"))
    elif action == "set":
        secret = request.get("secret")
        if not isinstance(secret, str) or not secret:
            reply("unavailable")
            raise SystemExit(0)
        command_line = "add-generic-password -U -s {} -a {} -w {}\n".format(
            shlex.quote(request["service"]), shlex.quote(account), shlex.quote(secret)
        )
        result = subprocess.run(
            [security, "-i"],
            input=command_line.encode("utf-8"),
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )
        reply("ok" if result.returncode == 0 else "unavailable")
    else:
        reply("unavailable")
except Exception:
    reply("unavailable")
