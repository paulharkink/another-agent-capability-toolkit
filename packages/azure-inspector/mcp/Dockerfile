FROM --platform=$BUILDPLATFORM mcr.microsoft.com/dotnet/sdk:9.0.102-bookworm-slim@sha256:6894a71619e08b47ef9df7ff1f436b21d21db160e5d864e180c294a53d7a12f2 AS build
WORKDIR /src

ADD --checksum=sha256:7436dba43fd5400b23ec674a5438b153c895a4d6e2adcfb6b307475be2de0b6a https://codeload.github.com/Azure/azure-mcp/tar.gz/1ea702cb489ba95c5d9bea8d41fc18e9343703f8 /tmp/azure-mcp.tar.gz
RUN tar -xzf /tmp/azure-mcp.tar.gz --strip-components=1 -C /src \
    && dotnet publish src/AzureMcp.csproj -c Release -o /app/publish

FROM mcr.microsoft.com/dotnet/aspnet:9.0.6-bookworm-slim@sha256:d02562e8e42f3ddfec764b05447dfe56eff84df95fbfd8b4f884054b21760df6
WORKDIR /app

RUN apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install --yes --no-install-recommends \
        apt-transport-https bash ca-certificates curl gnupg lsb-release \
    && mkdir -p /etc/apt/keyrings \
    && curl -fsSL https://packages.microsoft.com/keys/microsoft.asc | gpg --dearmor -o /etc/apt/keyrings/microsoft.gpg \
    && chmod go+r /etc/apt/keyrings/microsoft.gpg \
    && printf 'Types: deb\nURIs: https://packages.microsoft.com/repos/azure-cli/\nSuites: bookworm\nComponents: main\nArchitectures: %s\nSigned-by: /etc/apt/keyrings/microsoft.gpg\n' "$(dpkg --print-architecture)" > /etc/apt/sources.list.d/azure-cli.sources \
    && apt-get update \
    && DEBIAN_FRONTEND=noninteractive apt-get install --yes --no-install-recommends azure-cli=2.91.0-1~bookworm \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /app/publish/ ./
COPY --chmod=755 docker-entrypoint.sh /app/docker-entrypoint.sh
COPY entrypoint.py /app/aact-entrypoint.py

ENV AZMCP_TRANSPORT=stdio
ENV AZMCP_PORT=5008
ENTRYPOINT ["/opt/az/bin/python3", "/app/aact-entrypoint.py"]
CMD ["server", "start"]
