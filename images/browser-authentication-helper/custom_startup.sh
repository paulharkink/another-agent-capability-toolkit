#!/usr/bin/env bash
set -e
/usr/bin/python3 /opt/aact-browser-access/configure_browser_access.py
exec /dockerstartup/aact_base_custom_startup.sh "$@"
