#!/usr/bin/python3
import json
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
    account = request.get("account")
    if not isinstance(account, str) or not account.startswith("state-"):
        reply("unavailable")
        raise SystemExit(0)
    secret_tool = "/usr/bin/secret-tool"
    common = ["service", request["service"], "account", account]
    if request.get("action") == "get":
        result = subprocess.run(
            [secret_tool, "lookup", *common],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            check=False,
        )
        if result.returncode != 0:
            reply("missing")
        else:
            encoded = result.stdout.rstrip(b"\r\n")
            reply("ok", encoded.decode("ascii"))
    elif request.get("action") == "set":
        secret = request.get("secret")
        if not isinstance(secret, str) or not secret:
            reply("unavailable")
            raise SystemExit(0)
        result = subprocess.run(
            [secret_tool, "store", "--label=AACT shared Vault", *common],
            input=secret.encode("ascii"),
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            check=False,
        )
        reply("ok" if result.returncode == 0 else "unavailable")
    else:
        reply("unavailable")
except Exception:
    reply("unavailable")
