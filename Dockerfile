# ═══════════════════════════════════════════════════════════
# 1. Stage: Go Builder
# ═══════════════════════════════════════════════════════════
FROM golang:1.24-bookworm AS go-builder

RUN apt-get update && apt-get install -y \
    gcc libc6-dev git libsqlite3-dev ffmpeg \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY . .

# فریش انسٹال
RUN rm -f go.mod go.sum || true
RUN go mod init impossible-bot && \
    go get go.mau.fi/whatsmeow@latest && \
    go get go.mongodb.org/mongo-driver/mongo@latest && \
    go get go.mongodb.org/mongo-driver/bson@latest && \
    go get github.com/redis/go-redis/v9@latest && \
    go get github.com/gin-gonic/gin@latest && \
    go get github.com/mattn/go-sqlite3@latest && \
    go get github.com/lib/pq@latest && \
    go get github.com/gorilla/websocket@latest && \
    go get google.golang.org/protobuf/proto@latest && \
    go get github.com/showwin/speedtest-go && \
    go get google.golang.org/genai && \
    go mod tidy

RUN CGO_ENABLED=1 GOOS=linux go build -v -ldflags="-s -w" -o bot .

# ═══════════════════════════════════════════════════════════
# 2. Stage: Node.js Builder
# ═══════════════════════════════════════════════════════════
FROM node:20-bookworm-slim AS node-builder
RUN apt-get update && apt-get install -y git && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY package*.json ./
COPY lid-extractor.js ./
RUN npm install --production

# ═══════════════════════════════════════════════════════════
# 3. Stage: Final Runtime (PIPER TTS - PRO SERVER EDITION)
# ═══════════════════════════════════════════════════════════
FROM python:3.10-slim-bookworm

# ✅ لاگز فوراً دکھانے کے لیے
ENV PYTHONUNBUFFERED=1

# ✅ سسٹم ٹولز (curl اور tar لازمی ہیں)
RUN apt-get update && apt-get install -y \
    ffmpeg imagemagick curl sqlite3 libsqlite3-0 nodejs npm \
    ca-certificates libgomp1 megatools libwebp-dev webp \
    libwebpmux3 libwebpdemux2 libsndfile1 tar \
    && rm -rf /var/lib/apt/lists/*

# YT-DLP
RUN curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /usr/local/bin/yt-dlp \
    && chmod a+rx /usr/local/bin/yt-dlp

# ✅ PIPER TTS DOWNLOAD (With Curl - More Stable)
RUN curl -L -o piper.tar.gz https://github.com/rhasspy/piper/releases/download/2023.11.14-2/piper_linux_x86_64.tar.gz \
    && tar -xvf piper.tar.gz -C /usr/local/bin/ \
    && rm piper.tar.gz \
    && chmod +x /usr/local/bin/piper/piper

# ✅ URDU MODEL DOWNLOAD (RETRY LOGIC ADDED)
# ہم 'curl' استعمال کر رہے ہیں Retry کے ساتھ تاکہ اگر نیٹ ورک فیل ہو تو دوبارہ کوشش کرے
RUN mkdir -p /app/models \
    && curl -L --retry 5 --retry-delay 2 -o /app/models/ur_pk.onnx https://huggingface.co/rhasspy/piper-voices/resolve/main/ur/ur_pk/medium/ur_pk-medium.onnx \
    && curl -L --retry 5 --retry-delay 2 -o /app/models/ur_pk.onnx.json https://huggingface.co/rhasspy/piper-voices/resolve/main/ur/ur_pk/medium/ur_pk-medium.onnx.json

# ✅ Python Libraries (Torch CPU Version - Fast)
RUN pip3 install --no-cache-dir \
    torch torchaudio --index-url https://download.pytorch.org/whl/cpu \
    && pip3 install --no-cache-dir \
    fastapi uvicorn python-multipart requests \
    faster-whisper scipy

WORKDIR /app

# کاپی فائلز
COPY --from=go-builder /app/bot ./bot
COPY --from=node-builder /app/node_modules ./node_modules
COPY --from=node-builder /app/lid-extractor.js ./lid-extractor.js
COPY --from=node-builder /app/package.json ./package.json

COPY web ./web
COPY pic.png ./pic.png
COPY ai_engine.py ./ai_engine.py

RUN mkdir -p store logs
ENV PORT=8080
ENV NODE_ENV=production
EXPOSE 8080

CMD ["/app/bot"]
