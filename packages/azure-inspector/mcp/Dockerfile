FROM mcp/azure:latest@sha256:91452a19f4eb5e2797a1fd5fe0a99cdda48d480f1981c4515165e4a26ace77a1
COPY entrypoint.py /app/aact-entrypoint.py
ENTRYPOINT ["/opt/az/bin/python3", "/app/aact-entrypoint.py"]
