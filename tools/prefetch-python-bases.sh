#!/bin/sh
set -eu

platform=${1:?usage: prefetch-python-bases.sh <linux/platform> <image-ref>...}
shift
if [ "$#" -eq 0 ]; then
    echo 'At least one digest-pinned image reference is required.' >&2
    exit 2
fi

for image in "$@"; do
    case "$image" in
        public.ecr.aws/docker/library/python:*@sha256:*) ;;
        *) echo "Refusing non-ECR or unpinned Python base: $image" >&2; exit 2 ;;
    esac
    attempt=1
    while :; do
        log_file=$(mktemp)
        if docker pull --platform "$platform" "$image" >"$log_file" 2>&1; then
            cat "$log_file"
            rm -f "$log_file"
            break
        fi
        if ! grep -Eiq 'toomanyrequests|rate exceeded|429 Too Many Requests' "$log_file" || [ "$attempt" -ge 6 ]; then
            cat "$log_file" >&2
            rm -f "$log_file"
            exit 1
        fi
        cat "$log_file" >&2
        rm -f "$log_file"
        case "$attempt" in
            1) delay=10 ;;
            2) delay=20 ;;
            3) delay=40 ;;
            4) delay=80 ;;
            *) delay=120 ;;
        esac
        echo "ECR Public throttled this pull; retrying in ${delay}s (attempt $((attempt + 1))/6)." >&2
        sleep "$delay"
        attempt=$((attempt + 1))
    done
    # ECR Public limits anonymous image pulls to one per second per Region.
    sleep 2
done
