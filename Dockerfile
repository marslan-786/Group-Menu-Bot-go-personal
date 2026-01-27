# ═══════════════════════════════════════════════════════════
# 1. Stage: Go Builder
# ═══════════════════════════════════════════════════════════
FROM golang:1.24-bookworm AS go-builder

RUN apt-get update && apt-get install -y \
    gcc libc6-dev git libsqlite3-dev ffmpeg \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY . .

# گو موڈیولز کو صاف ستھرا رکھیں
# Chromedp یہاں سے ہٹا دیا گیا ہے، باقی سب ویسا ہی ہے
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
# 3. Stage: Final Runtime (Fast & Compliant)
# ═══════════════════════════════════════════════════════════
FROM python:3.10-slim-bookworm

ENV PYTHONUNBUFFERED=1

# 🛠️ سسٹم لائبریریز (Chromium یہاں سے ہٹا دیا، Playwright خود سنبھالے گا)
RUN apt-get update && apt-get install -y \
    ffmpeg imagemagick curl sqlite3 libsqlite3-0 \
    nodejs npm \
    atomicparsley \
    ca-certificates libgomp1 megatools libwebp-dev webp \
    libwebpmux3 libwebpdemux2 libsndfile1 \
    && rm -rf /var/lib/apt/lists/*

# 🛠️ CRITICAL FIX: yt-dlp needs 'node', not 'nodejs'
RUN ln -sf /usr/bin/nodejs /usr/local/bin/node

# yt-dlp انسٹالیشن
RUN curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /usr/local/bin/yt-dlp \
    && chmod a+rx /usr/local/bin/yt-dlp

# 🐍 Python Libraries
# یہاں Playwright ایڈ کیا گیا ہے
RUN pip3 install --no-cache-dir \
    torch torchaudio --index-url https://download.pytorch.org/whl/cpu \
    && pip3 install --no-cache-dir \
    fastapi uvicorn python-multipart requests \
    faster-whisper scipy gTTS playwright

# 🌍 Playwright Browsers انسٹالیشن (یہ سب سے اہم لائن ہے)
# یہ Chromium اور اس کے لیے ضروری تمام سسٹم فائلز انسٹال کرے گا
RUN playwright install --with-deps chromium

WORKDIR /app

# کاپی کریں
COPY --from=go-builder /app/bot ./bot
COPY --from=node-builder /app/node_modules ./node_modules
COPY --from=node-builder /app/lid-extractor.js ./lid-extractor.js
COPY --from=node-builder /app/package.json ./package.json

COPY web ./web
COPY pic.png ./pic.png
COPY ai_engine.py ./ai_engine.py
COPY tiktok_nav.py ./tiktok_nav.py
# 👇 Python والا نیا براؤزر سکرپٹ یہاں کاپی ہو رہا ہے
COPY browser_dl.py ./browser_dl.py

RUN mkdir -p store logs
ENV PORT=8080
ENV NODE_ENV=production
EXPOSE 8080

CMD ["/app/bot"]
