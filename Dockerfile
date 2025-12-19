FROM golang:1.24-alpine AS builder
RUN apk add --no-cache gcc musl-dev git sqlite-dev ffmpeg-dev

WORKDIR /app
COPY . .

# گو موڈ کی صفائی اور انیشلائزیشن
RUN rm -f go.mod go.sum || true
RUN go mod init impossible-bot
RUN go get go.mau.fi/whatsmeow@latest
RUN go get go.mongodb.org/mongo-driver/mongo@latest
RUN go get github.com/gin-gonic/gin@latest
RUN go get github.com/mattn/go-sqlite3@latest
RUN go get github.com/lib/pq@latest
RUN go mod tidy

# بلڈ کرنا
RUN go build -o bot .

FROM alpine:latest
RUN apk add --no-cache ca-certificates sqlite-libs ffmpeg
WORKDIR /app
COPY --from=builder /app/bot .
COPY --from=builder /app/web ./web

# ریلوے کا پورٹ اٹھانے کے لیے انوائرمنٹ
ENV PORT=8080
EXPOSE 8080

CMD ["./bot"]