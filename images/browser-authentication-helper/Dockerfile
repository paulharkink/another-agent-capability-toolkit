FROM python:3.10-slim-bookworm AS auth-dependencies

COPY requirements.txt /tmp/browser-authentication-helper-requirements.txt
RUN python -m pip install --no-cache-dir --target /opt/browser-authentication-helper-libs --no-deps \
      https://github.com/cclgroupltd/ccl_chromium_reader/archive/1bc482c0078cad4288ad0a0072ef46d7d7929596.zip \
    && python -m pip install --no-cache-dir --target /opt/browser-authentication-helper-libs \
      -r /tmp/browser-authentication-helper-requirements.txt

FROM kasmweb/chromium:1.18.0

LABEL org.aact.browser-authentication-helper="1" \
      org.aact.browser-access-controller="1"

ENV VNCOPTIONS="-disableBasicAuth -sslOnly=0" \
    APP_ARGS="--start-maximized --user-data-dir=/home/kasm-user/.config/chromium --remote-debugging-port=9222 --remote-debugging-address=127.0.0.1 --load-extension=/opt/aact-browser-access/runtime"

COPY --from=auth-dependencies /opt/browser-authentication-helper-libs /opt/browser-authentication-helper-libs
ENV PYTHONPATH="/opt/browser-authentication-helper-libs"
COPY browser_auth.py /opt/browser-authentication-helper/browser_auth.py
COPY --chmod=0755 browser-authentication-helper /usr/local/bin/browser-authentication-helper

# Kasm invokes this script as the browser user before launching Chromium.
USER root
RUN mkdir -p /opt/aact-browser-access/runtime \
    && cp /dockerstartup/custom_startup.sh /dockerstartup/aact_base_custom_startup.sh \
    && cp /etc/chromium/policies/managed/urlblocklist.json /opt/aact-browser-access/base_url_policy.json \
    && chmod 0644 /opt/aact-browser-access/base_url_policy.json \
    && chown -R 1000:1000 /opt/aact-browser-access /etc/chromium/policies/managed/urlblocklist.json
COPY browser_access.py /opt/aact-browser-access/browser_access.py
COPY browser_access_extension.js /opt/aact-browser-access/browser_access_extension.js
COPY configure_browser_access.py /opt/aact-browser-access/configure_browser_access.py
COPY --chmod=0755 custom_startup.sh /dockerstartup/custom_startup.sh
USER 1000
