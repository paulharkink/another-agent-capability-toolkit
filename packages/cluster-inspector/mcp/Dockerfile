FROM public.ecr.aws/docker/library/python:3.13-slim@sha256:70729b46c69b4f1e97c4822c1af3df53a1476cf5ddc6c087c0c10bc3a5678c2f

ARG TARGETARCH
ARG KUBECTL_VERSION=v1.34.1

COPY select-kubectl-arch.sh /usr/local/bin/select-kubectl-arch.sh

RUN KUBECTL_ARCH="$(/usr/local/bin/select-kubectl-arch.sh)" \
    && apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl \
    && curl --fail --location --silent --show-error --output /usr/local/bin/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${KUBECTL_ARCH}/kubectl" \
    && curl --fail --location --silent --show-error --output /tmp/kubectl.sha256 "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${KUBECTL_ARCH}/kubectl.sha256" \
    && echo "$(cat /tmp/kubectl.sha256)  /usr/local/bin/kubectl" | sha256sum --check \
    && chmod +x /usr/local/bin/kubectl \
    && apt-get purge --yes curl \
    && apt-get autoremove --yes \
    && rm -rf /var/lib/apt/lists/* /tmp/kubectl.sha256

WORKDIR /app
COPY requirements.txt ./requirements.txt
RUN pip install --no-cache-dir -r requirements.txt
COPY config.py server.py ./
COPY tekton_tools.py ./
COPY kubernetes_auth.py ./kubernetes_auth.py
COPY tests ./tests

ENV KUBECONFIG=/state/kubeconfig
EXPOSE 8765
ENTRYPOINT ["python", "/app/server.py"]
COPY auth.py ./
