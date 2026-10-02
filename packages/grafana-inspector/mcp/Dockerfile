FROM python:3.13-slim

WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt
COPY server.py ./

EXPOSE 8765
ENTRYPOINT ["python", "/app/server.py"]
COPY config.py ./
COPY auth.py ./
