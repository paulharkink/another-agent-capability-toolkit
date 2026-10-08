#!/bin/sh
set -eu

case "${JENKINS_READ_ONLY:-true}" in
    true|1) readonly_flag=1 ;;
    false|0) readonly_flag=0 ;;
    *) echo "JENKINS_READ_ONLY must be true or false" >&2; exit 2 ;;
esac

if [ -z "${jenkins_url:-}" ]; then
    echo "jenkins_url is required" >&2
    exit 2
fi
if [ -z "${jenkins_username:-}" ]; then
    echo "jenkins_username is required" >&2
    exit 2
fi
if [ -z "${jenkins_password:-}" ]; then
    echo "jenkins_password is required" >&2
    exit 2
fi

set -- --transport streamable-http --host 0.0.0.0 --port 9887 "$@"
if [ "$readonly_flag" = 1 ]; then
    set -- --read-only "$@"
fi

exec jenkins-mcp "$@"
