ARG PYTHON_BASE_IMAGE=public.ecr.aws/docker/library/python:3.13-slim@sha256:70729b46c69b4f1e97c4822c1af3df53a1476cf5ddc6c087c0c10bc3a5678c2f
FROM ${PYTHON_BASE_IMAGE}

ARG TARGETARCH

WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt
COPY verify-cli-checksum.sh /usr/local/bin/verify-cli-checksum.sh
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl coreutils mawk \
    && rm -rf /var/lib/apt/lists/* \
    && case "$TARGETARCH" in amd64|arm64) ;; *) echo "Unsupported target architecture: $TARGETARCH" >&2; exit 1 ;; esac \
    && curl --fail --location --silent --show-error \
      "https://github.com/argoproj/argo-cd/releases/download/v3.2.0/argocd-linux-${TARGETARCH}" \
      --output /usr/local/bin/argocd \
    && curl --fail --location --silent --show-error \
      "https://github.com/argoproj/argo-cd/releases/download/v3.2.0/cli_checksums.txt" \
      --output /tmp/cli_checksums.txt \
    && /bin/sh /usr/local/bin/verify-cli-checksum.sh /tmp/cli_checksums.txt "argocd-linux-${TARGETARCH}" /usr/local/bin/argocd \
    && chmod 0755 /usr/local/bin/argocd \
    && rm /tmp/cli_checksums.txt
COPY server.py operation_inventory.json ./

EXPOSE 8765
ENTRYPOINT ["python", "/app/server.py"]
