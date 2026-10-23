ARG PYTHON_BASE_IMAGE=public.ecr.aws/docker/library/python:3.12-slim@sha256:a6e34c598f2467ed0e9a8d349809fcd8b5c603269512df273a0bb1784edc11b1
FROM ${PYTHON_BASE_IMAGE}

RUN pip install --no-cache-dir coding-agent-tools

ENTRYPOINT ["find-session"]
