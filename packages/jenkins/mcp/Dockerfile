FROM node:22-alpine

RUN npm install --global --omit=dev jenkins-mcp@0.2.1 \
    && npm cache clean --force

COPY entrypoint.sh /usr/local/bin/aact-jenkins-mcp
RUN chmod 0755 /usr/local/bin/aact-jenkins-mcp

EXPOSE 9887
ENTRYPOINT ["/usr/local/bin/aact-jenkins-mcp"]
