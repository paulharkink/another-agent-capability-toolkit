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

    selected_image=$image
    attempt=1
    while :; do
        log_file=$(mktemp)
        if docker pull --platform "$platform" "$image" >"$log_file" 2>&1; then
            cat "$log_file"
            rm -f "$log_file"
            break
        fi
        if grep -Eiq 'data limit exceeded' "$log_file"; then
            case "$image" in
                public.ecr.aws/docker/library/python:3.14-alpine@sha256:f6a589d43c42b9e7f7dc67a12d37132491f362859a5d750607710cc56da3bc72)
                    fallback_image=docker.io/library/python:3.14-alpine@sha256:f6a589d43c42b9e7f7dc67a12d37132491f362859a5d750607710cc56da3bc72 ;;
                public.ecr.aws/docker/library/python:3.13-slim@sha256:70729b46c69b4f1e97c4822c1af3df53a1476cf5ddc6c087c0c10bc3a5678c2f)
                    fallback_image=docker.io/library/python:3.13-slim@sha256:70729b46c69b4f1e97c4822c1af3df53a1476cf5ddc6c087c0c10bc3a5678c2f ;;
                public.ecr.aws/docker/library/python:3.12-slim@sha256:a6e34c598f2467ed0e9a8d349809fcd8b5c603269512df273a0bb1784edc11b1)
                    fallback_image=docker.io/library/python:3.12-slim@sha256:a6e34c598f2467ed0e9a8d349809fcd8b5c603269512df273a0bb1784edc11b1 ;;
                *)
                    cat "$log_file" >&2
                    rm -f "$log_file"
                    echo "No verified Docker Hub fallback is configured for $image." >&2
                    exit 1 ;;
            esac
            cat "$log_file" >&2
            rm -f "$log_file"
            echo "ECR Public data quota exhausted; pulling the same pinned digest from Docker Hub: $fallback_image" >&2
            if ! docker pull --platform "$platform" "$fallback_image"; then
                echo "Failed to pull the verified Docker Hub fallback for $image." >&2
                exit 1
            fi
            digest=${fallback_image##*@}
            if ! docker image inspect --format '{{range .RepoDigests}}{{println .}}{{end}}' "$fallback_image" | grep -Fq "python@$digest"; then
                echo "Docker Hub fallback did not resolve to the expected digest $digest." >&2
                exit 1
            fi
            selected_image=$fallback_image
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
        echo "ECR Public rate-limited this pull; retrying in ${delay}s (attempt $((attempt + 1))/6)." >&2
        sleep "$delay"
        attempt=$((attempt + 1))
    done
    tag=${image##*python:}
    tag=${tag%%@*}
    output_suffix=$(printf '%s' "$tag" | tr '.-' '__' | tr '[:lower:]' '[:upper:]')
    output_key=$(printf 'python_%s' "$tag" | tr '.-' '__')
    if [ -n "${GITHUB_OUTPUT:-}" ]; then
        printf '%s=%s\n' "$output_key" "$selected_image" >> "$GITHUB_OUTPUT"
    fi
    if [ -n "${GITHUB_ENV:-}" ]; then
        printf 'AACT_PYTHON_BASE_IMAGE_%s=%s\n' "$output_suffix" "$selected_image" >> "$GITHUB_ENV"
    fi
    # Avoid bursting ECR Public if multiple pinned images are prefetched.
    sleep 2
done
