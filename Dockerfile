# ═══════════════════════════════════════════════════════════
# 1. Stage: Go Builder
# ═══════════════════════════════════════════════════════════
FROM golang:1.24-alpine AS go-builder
RUN apk add --no-cache gcc musl-dev git sqlite-dev ffmpeg-dev
WORKDIR /app
COPY . .
RUN rm -f go.mod go.sum || true
RUN go mod init impossible-bot && \
    go get go.mau.fi/whatsmeow@latest && \
    go get go.mongodb.org/mongo-driver/mongo@latest && \
    go get go.mongodb.org/mongo-driver/bson@latest && \
    go get github.com/gin-gonic/gin@latest && \
    go get github.com/mattn/go-sqlite3@latest && \
    go get github.com/lib/pq@latest && \
    go get github.com/gorilla/websocket@latest && \
    go get google.golang.org/protobuf/proto@latest && \
    go mod tidy
RUN go build -ldflags="-s -w" -o bot .

# ═══════════════════════════════════════════════════════════
# 2. Stage: Node.js Builder
# ═══════════════════════════════════════════════════════════
FROM node:20-alpine AS node-builder
RUN apk add --no-cache git 
WORKDIR /app
COPY package*.json ./
COPY lid-extractor.js ./
RUN npm install --production

# ═══════════════════════════════════════════════════════════
# 3. Stage: Final Runtime (The Powerhouse)
# ═══════════════════════════════════════════════════════════
FROM alpine:latest
# ہیوی لائبریریز: Python, FFmpeg, اور مکمل yt-dlp کے لئے ٹولز
RUN apk add --no-cache \
    ca-certificates \
    sqlite-libs \
    ffmpeg \
    python3 \
    py3-pip \
    curl \
    nodejs \
    npm \
    && curl -L https://github.com/yt-dlp/yt-dlp/releases/latest/download/yt-dlp -o /usr/local/bin/yt-dlp \
    && chmod a+rx /usr/local/bin/yt-dlp \
    && rm -rf /var/cache/apk/*

WORKDIR /app
COPY --from=go-builder /app/bot ./bot
COPY --from=node-builder /app/node_modules ./node_modules
COPY --from=node-builder /app/lid-extractor.js ./lid-extractor.js
COPY --from=node-builder /app/package.json ./package.json
COPY web ./web
COPY pic.png ./pic.png
RUN mkdir -p store logs
ENV PORT=8080
ENV NODE_ENV=production
EXPOSE 8080
CMD ["./bot"]