ARG PYTHON_BASE_IMAGE=public.ecr.aws/docker/library/python:3.13-slim@sha256:70729b46c69b4f1e97c4822c1af3df53a1476cf5ddc6c087c0c10bc3a5678c2f
FROM ${PYTHON_BASE_IMAGE}

ARG TARGETARCH
ARG KUBECTL_VERSION=v1.34.1
ARG OC_VERSION=4.18.30

COPY select-kubectl-arch.sh /usr/local/bin/select-kubectl-arch.sh

RUN KUBECTL_ARCH="$(/usr/local/bin/select-kubectl-arch.sh)" \
    && apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates curl \
    && curl --fail --location --silent --show-error --output /usr/local/bin/kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${KUBECTL_ARCH}/kubectl" \
    && curl --fail --location --silent --show-error --output /tmp/kubectl.sha256 "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${KUBECTL_ARCH}/kubectl.sha256" \
    && echo "$(cat /tmp/kubectl.sha256)  /usr/local/bin/kubectl" | sha256sum --check \
    && chmod +x /usr/local/bin/kubectl \
    && case "$KUBECTL_ARCH" in \
         amd64) OC_ARCHIVE="openshift-client-linux-${OC_VERSION}.tar.gz"; OC_SHA=2895ac69b001a9683a71c7d08ff6ffeda9dcdbd96175cc29acde6d0eaff99e1d ;; \
         arm64) OC_ARCHIVE="openshift-client-linux-arm64-${OC_VERSION}.tar.gz"; OC_SHA=620502cdfafe46953b10f618fd669d3c7e7e7daf5af5f2891351d93f76df9181 ;; \
         *) exit 1 ;; \
       esac \
    && curl --fail --location --silent --show-error --output /tmp/oc.tar.gz "https://mirror.openshift.com/pub/openshift-v4/clients/ocp/${OC_VERSION}/${OC_ARCHIVE}" \
    && echo "$OC_SHA  /tmp/oc.tar.gz" | sha256sum --check \
    && tar -xzf /tmp/oc.tar.gz -C /usr/local/bin oc \
    && apt-get purge --yes curl \
    && apt-get autoremove --yes \
    && rm -rf /var/lib/apt/lists/* /tmp/kubectl.sha256 /tmp/oc.tar.gz

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
