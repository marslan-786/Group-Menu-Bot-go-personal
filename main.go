package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"os/signal"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
	"strconv"
	"sort"

	"github.com/gorilla/websocket"
	_ "github.com/lib/pq"            // Postgres
	_ "github.com/go-sql-driver/mysql" // ✅ Added MySQL Driver
	"github.com/redis/go-redis/v9"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	waLog "go.mau.fi/whatsmeow/util/log"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// 📦 STRUCT FOR MESSAGE HISTORY
// 📦 STRUCT FOR MESSAGE HISTORY (Compatible with MongoDB & MySQL)
type ChatMessage struct {
	ID           int64     `bson:"-" json:"id"`
	BotID        string    `bson:"bot_id" json:"bot_id"`
	ChatID       string    `bson:"chat_id" json:"chat_id"`
	Sender       string    `bson:"sender" json:"sender"`
	SenderName   string    `bson:"sender_name" json:"sender_name"`
	MessageID    string    `bson:"message_id" json:"message_id"`
	Timestamp    time.Time `bson:"timestamp" json:"timestamp"`
	Type         string    `bson:"type" json:"type"`
	Content      string    `bson:"content" json:"content"`
	IsFromMe     bool      `bson:"is_from_me" json:"is_from_me"`
	IsGroup      bool      `bson:"is_group" json:"is_group"`
	IsChannel    bool      `bson:"is_channel" json:"is_channel"`
	QuotedMsg    string    `bson:"quoted_msg" json:"quoted_msg"`
	QuotedSender string    `bson:"quoted_sender" json:"quoted_sender"`
	IsSticker    bool      `bson:"is_sticker" json:"is_sticker"`

	HasMedia bool   `bson:"has_media,omitempty" json:"has_media,omitempty"`
	MediaRef string `bson:"media_ref,omitempty" json:"media_ref,omitempty"` // usually same as message_id
}

// 📦 Chat Item Structure
type ChatItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

var (
	client                *whatsmeow.Client
	container             *sqlstore.Container
	dbContainer           *sqlstore.Container
	historyDB             *sql.DB // ✅ MySQL Connection for Chat History
	rdb                   *redis.Client
	ctx                   = context.Background()
	mediaCollection *mongo.Collection
    statusCollection *mongo.Collection // optional (statuses کیلئے)
	persistentUptime      int64
	groupCache            = make(map[string]*GroupSettings)
	cacheMutex            sync.RWMutex
	upgrader              = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	wsClients             = make(map[*websocket.Conn]bool)
	botCleanIDCache       = make(map[string]string)
	botPrefixes           = make(map[string]string)
	prefixMutex           sync.RWMutex
	clientsMutex          sync.RWMutex
	activeClients         = make(map[string]*whatsmeow.Client)
	globalClient          *whatsmeow.Client
	ytCache               = make(map[string]YTSession)
	ytDownloadCache       = make(map[string]YTState)
	cachedMenuImage       *waProto.ImageMessage
	mongoClient           *mongo.Client
	chatHistoryCollection *mongo.Collection
)

// ✅ 1. Redis Connection
func initRedis() {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		fmt.Println("⚠️ [REDIS] Warning: REDIS_URL is empty! Defaulting to localhost...")
		redisURL = "redis://localhost:6379"
	} else {
		fmt.Println("📡 [REDIS] Connecting to Redis Cloud...")
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Fatalf("❌ Redis URL parsing failed: %v", err)
	}
	rdb = redis.NewClient(opt)
	_, err = rdb.Ping(ctx).Result()
	if err != nil {
		log.Fatalf("❌ Redis connection failed: %v", err)
	}
	fmt.Println("🚀 [REDIS] Connection Established!")
}

// ✅ 2. Load Global Settings
func loadGlobalSettings() {
	if rdb == nil {
		return
	}
	val, err := rdb.Get(ctx, "bot_global_settings").Result()
	if err == nil {
		dataMutex.Lock()
		json.Unmarshal([]byte(val), &data)
		dataMutex.Unlock()
		fmt.Println("✅ [SETTINGS] Bot Settings Restored from Redis")
	}
}

// ✅ 3. Load Persistent Uptime
func loadPersistentUptime() {
	if rdb != nil {
		val, err := rdb.Get(ctx, "total_uptime").Int64()
		if err == nil {
			persistentUptime = val
		}
	}
	fmt.Println("⏳ [UPTIME] Persistent uptime loaded from Redis")
}

// ✅ 4. Start Persistent Uptime Tracker
func startPersistentUptimeTracker() {
	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		for range ticker.C {
			persistentUptime += 60
			if rdb != nil {
				rdb.Set(ctx, "total_uptime", persistentUptime, 0)
			}
		}
	}()
}

func main() {
	fmt.Println("🚀 IMPOSSIBLE BOT | STARTING (HYBRID MODE)")

	// ----------------------------------------------------
	// 1) Init Core Services
	// ----------------------------------------------------
	initRedis()
	loadPersistentUptime()
	loadGlobalSettings()
	startPersistentUptimeTracker()
	SetupFeatures()
	KeepServerAlive()

	// 🔥 START PYTHON ENGINE (BACKGROUND)
	// یہ کوڈ بیک گراؤنڈ میں Python سرور کو چلائے گا
	go func() {
		fmt.Println("🐍 Starting Python AI Engine...")
		cmd := exec.Command("python3", "ai_engine.py")
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("❌ Python Engine Crash: %v\n", err)
		}
	}()

	// ----------------------------------------------------
	// 2) MongoDB (Optional) - Chat history + Media + Status
	// ----------------------------------------------------
    // ... (نیچے والا کوڈ ویسے ہی رہے گا)

	mongoURL := os.Getenv("MONGO_URL")
	if mongoURL != "" {
		mCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		mClient, err := mongo.Connect(mCtx, options.Client().ApplyURI(mongoURL))
		if err != nil {
			fmt.Println("❌ MongoDB Connection Error:", err)
		} else {
			if err := mClient.Ping(mCtx, nil); err != nil {
				fmt.Println("❌ MongoDB Ping Failed:", err)
			} else {
				mongoClient = mClient

				db := mClient.Database("whatsapp_bot")
				chatHistoryCollection = db.Collection("messages")
				mediaCollection = db.Collection("media")
				statusCollection = db.Collection("statuses")

				fmt.Println("🍃 [MONGODB] Connected for Chat History + Media + Status!")

				// ✅ Ensure indexes (best-effort)
				go func() {
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()

					// ----------------------------
					// MESSAGES Indexes
					// ----------------------------
					// Fast paging: bot_id + chat_id + timestamp desc + message_id desc
					_, _ = chatHistoryCollection.Indexes().CreateMany(ctx, []mongo.IndexModel{
						{
							Keys: bson.D{
								{Key: "bot_id", Value: 1},
								{Key: "chat_id", Value: 1},
								{Key: "timestamp", Value: -1},
								{Key: "message_id", Value: -1},
							},
						},
						// ✅ unique per bot (so multi-bot collision na ho)
						{
							Keys:    bson.D{{Key: "bot_id", Value: 1}, {Key: "message_id", Value: 1}},
							Options: options.Index().SetUnique(true).SetSparse(true),
						},
					})

					// ----------------------------
					// MEDIA Indexes
					// ----------------------------
					if mediaCollection != nil {
						_, _ = mediaCollection.Indexes().CreateMany(ctx, []mongo.IndexModel{
							{
								Keys: bson.D{
									{Key: "bot_id", Value: 1},
									{Key: "created_at", Value: -1},
								},
							},
							{
								Keys:    bson.D{{Key: "bot_id", Value: 1}, {Key: "message_id", Value: 1}},
								Options: options.Index().SetUnique(true).SetSparse(true),
							},
						})
					}

					// ----------------------------
					// STATUSES Indexes
					// ----------------------------
					if statusCollection != nil {
						_, _ = statusCollection.Indexes().CreateMany(ctx, []mongo.IndexModel{
							{
								Keys: bson.D{
									{Key: "bot_id", Value: 1},
									{Key: "timestamp", Value: -1},
								},
							},
							{
								Keys: bson.D{
									{Key: "bot_id", Value: 1},
									{Key: "chat_id", Value: 1},
									{Key: "timestamp", Value: -1},
								},
							},
						})
					}

					fmt.Println("✅ [MONGODB] Indexes ensured!")
				}()
			}
		}
	} else {
		fmt.Println("⚠️ MONGO_URL not found! Chat history/media/status will not be saved.")
	}

	// ----------------------------------------------------
	// 3) Postgres (Sessions / WhatsMeow Store)
	// ----------------------------------------------------
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("❌ FATAL ERROR: DATABASE_URL environment variable is missing!")
	}

	fmt.Println("🐘 [DATABASE] Connecting to PostgreSQL...")
	rawDB, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("❌ Failed to open Postgres connection: %v", err)
	}

	// Pool tuning
	rawDB.SetMaxOpenConns(20)
	rawDB.SetMaxIdleConns(5)
	rawDB.SetConnMaxLifetime(30 * time.Minute)
	fmt.Println("✅ [TUNING] Postgres Pool Configured")

	// ----------------------------------------------------
	// 4) WhatsMeow SQL Container
	// ----------------------------------------------------
	dbLog := waLog.Stdout("Database", "ERROR", true)
	container = sqlstore.NewWithDB(rawDB, "postgres", dbLog)

	err = container.Upgrade(context.Background())
	if err != nil {
		log.Fatalf("❌ Failed to initialize database tables: %v", err)
	}
	fmt.Println("✅ [DATABASE] Tables verified/created successfully!")
	dbContainer = container

	// ----------------------------------------------------
	// 5) Multi-Bot System
	// ----------------------------------------------------
	fmt.Println("🤖 Initializing Multi-Bot System from Database...")
	StartAllBots(container)
	InitLIDSystem()

	// ----------------------------------------------------
	// 🌐 ROUTES (Bot UI + Web View)
	// ----------------------------------------------------
	http.HandleFunc("/", serveHTML)
	http.HandleFunc("/pic.png", servePicture)
	http.HandleFunc("/ws", handleWebSocket)

	// Bot Pair / Session Management
	http.HandleFunc("/api/pair", handlePairAPI)
	http.HandleFunc("/link/pair/", handlePairAPILegacy)
	http.HandleFunc("/link/delete", handleDeleteSession)
	http.HandleFunc("/del/all", handleDelAllAPI)
	http.HandleFunc("/del/", handleDelNumberAPI)

	// Web View APIs
	http.HandleFunc("/lists", serveListsHTML)
	http.HandleFunc("/lists/vip", serveListsHTML)
	http.HandleFunc("/api/sessions", handleGetSessions)
	http.HandleFunc("/api/chats", handleGetChats)
	http.HandleFunc("/api/messages", handleGetMessages)
	http.HandleFunc("/api/media", handleGetMedia)
	http.HandleFunc("/api/avatar", handleGetAvatar)

	// ✅ Status APIs (route now)
	http.HandleFunc("/api/statuses", handleGetStatuses)

	// ----------------------------------------------------
	// ✅ Health / Ready
	// ----------------------------------------------------
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true,"service":"impossible-bot"}`))
	})

	http.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		deps := map[string]bool{
			"postgres": rawDB != nil,
			"redis":    rdb != nil,
			"mongo":    mongoClient != nil,
		}

		ok := deps["postgres"] && deps["redis"] // mongo optional
		w.Header().Set("Content-Type", "application/json")

		if !ok {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "deps": deps})
			return
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "deps": deps})
	})

	// ----------------------------------------------------
	// Server Boot
	// ----------------------------------------------------
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:              ":" + port,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		fmt.Printf("🌐 Web Server running on port %s\n", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("❌ Server error: %v\n", err)
		}
	}()

	// ----------------------------------------------------
	// Graceful Shutdown
	// ----------------------------------------------------
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	fmt.Println("\n🛑 Shutting down system...")

	ctxShutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctxShutdown)

	clientsMutex.Lock()
	for id, activeClient := range activeClients {
		fmt.Printf("🔌 Disconnecting Bot: %s\n", id)
		activeClient.Disconnect()
	}
	clientsMutex.Unlock()

	if mongoClient != nil {
		_ = mongoClient.Disconnect(context.Background())
	}
	if rawDB != nil {
		_ = rawDB.Close()
	}
	if historyDB != nil {
		_ = historyDB.Close()
	}

	fmt.Println("👋 Goodbye!")
}

func saveMediaForMessage(client *whatsmeow.Client, botID, messageID, kind string, anyMsg interface{}) {
	if mediaCollection == nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var data []byte
	var err error
	var mime string

	// Download based on type
	switch m := anyMsg.(type) {
	case *waProto.ImageMessage:
		data, err = client.Download(ctx, m)
		mime = "image/jpeg"
	case *waProto.StickerMessage:
		data, err = client.Download(ctx, m)
		mime = "image/webp"
	case *waProto.VideoMessage:
		data, err = client.Download(ctx, m)
		mime = "video/mp4"
	case *waProto.AudioMessage:
		data, err = client.Download(ctx, m)
		mime = "audio/ogg"
	case *waProto.DocumentMessage:
		data, err = client.Download(ctx, m)
		mime = m.GetMimetype()
	default:
		return
	}

	if err != nil || len(data) == 0 {
		return
	}

	// Strategy:
	// - small media: store as data-url in mongo
	// - large: upload to catbox and store url (you already have UploadToCatbox)
	const maxInline = 2 * 1024 * 1024 // 2MB inline (safe)
	content := ""

	if len(data) <= maxInline {
		enc := base64.StdEncoding.EncodeToString(data)
		if strings.HasPrefix(mime, "image/") {
			content = "data:" + mime + ";base64," + enc
		} else if strings.HasPrefix(mime, "audio/") {
			content = "data:" + mime + ";base64," + enc
		} else {
			content = "data:application/octet-stream;base64," + enc
		}
	} else {
		name := "file.bin"
		if kind == "video" {
			name = "video.mp4"
		} else if kind == "audio" {
			name = "audio.ogg"
		} else if kind == "image" {
			name = "image.jpg"
		} else if kind == "sticker" {
			name = "sticker.webp"
		}
		if url, upErr := UploadToCatbox(data, name); upErr == nil {
			content = url
		}
	}

	if content == "" {
		return
	}

	md := MediaDoc{
		BotID:     botID,
		MessageID: messageID,
		Type:      kind,
		Mime:      mime,
		Content:   content,
		Size:      len(data),
		CreatedAt: time.Now(),
	}

	// Upsert (overwrite if re-downloaded)
	_, _ = mediaCollection.UpdateOne(
		ctx,
		bson.M{"bot_id": botID, "message_id": messageID},
		bson.M{"$set": md},
		options.Update().SetUpsert(true),
	)
}

func handleGetStatuses(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`[]`))
}

func serveHTML(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/index.html")
}

// 🐱 CATBOX UPLOAD FUNCTION
func UploadToCatbox(data []byte, filename string) (string, error) {
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("fileToUpload", filename)
	part.Write(data)
	writer.WriteField("reqtype", "fileupload")
	writer.Close()

	req, _ := http.NewRequest("POST", "https://catbox.moe/user/api.php", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return string(respBody), nil
}

type MediaItem struct {
	BotID      string    `bson:"bot_id" json:"bot_id"`
	ChatID     string    `bson:"chat_id" json:"chat_id"`
	MessageID  string    `bson:"message_id" json:"message_id"`
	Kind       string    `bson:"kind" json:"kind"` // image/audio/video/file/sticker
	Mime       string    `bson:"mime" json:"mime"`
	Content    string    `bson:"content" json:"content"` // base64 data:... OR url
	Size       int       `bson:"size" json:"size"`
	CreatedAt  time.Time `bson:"created_at" json:"created_at"`
}
// 🔥 HELPER: Save Message to Mongo (Fixed Context)
func saveMessageToMongo(client *whatsmeow.Client, botID, chatID string, senderJID types.JID, msg *waProto.Message, isFromMe bool, ts uint64) {
	// Simple recover just in case
	defer func() {
		if r := recover(); r != nil {
			// fmt.Printf("Mongo Save Error: %v\n", r)
		}
	}()

	if chatHistoryCollection == nil {
		return
	}

	// 🚫 Ignore Channels/Newsletters
	if strings.HasPrefix(chatID, "120") || strings.Contains(chatID, "@newsletter") {
		return
	}

	chatID = canonicalChatID(chatID)
	senderStr := senderJID.String()

	// 👤 Name Lookup (Simple)
	senderName := strings.Split(senderStr, "@")[0]
	if contact, err := client.Store.Contacts.GetContact(context.Background(), senderJID); err == nil && contact.Found {
		senderName = contact.FullName
		if senderName == "" {
			senderName = contact.PushName
		}
	}

	// 📝 Content Variables
	var msgType, content string
	var quotedMsg, quotedSender string
	var isSticker bool
	
	timestamp := time.Unix(int64(ts), 0)
	isGroup := strings.Contains(chatID, "@g.us")
	isChannel := false

	// 🔍 Extract Context Info (For Replies/IDs)
	var contextInfo *waProto.ContextInfo
	if msg.ExtendedTextMessage != nil {
		contextInfo = msg.ExtendedTextMessage.ContextInfo
	} else if msg.ImageMessage != nil {
		contextInfo = msg.ImageMessage.ContextInfo
	} else if msg.VideoMessage != nil {
		contextInfo = msg.VideoMessage.ContextInfo
	} else if msg.AudioMessage != nil {
		contextInfo = msg.AudioMessage.ContextInfo
	} else if msg.StickerMessage != nil {
		contextInfo = msg.StickerMessage.ContextInfo
	} else if msg.DocumentMessage != nil {
		contextInfo = msg.DocumentMessage.ContextInfo
	}

	// Handle Quoted Messages
	if contextInfo != nil && contextInfo.QuotedMessage != nil {
		if contextInfo.Participant != nil {
			quotedSender = canonicalChatID(*contextInfo.Participant)
		} else if contextInfo.RemoteJID != nil {
			quotedSender = canonicalChatID(*contextInfo.RemoteJID)
		}

		if contextInfo.QuotedMessage.Conversation != nil {
			quotedMsg = *contextInfo.QuotedMessage.Conversation
		} else if contextInfo.QuotedMessage.ExtendedTextMessage != nil {
			quotedMsg = contextInfo.QuotedMessage.ExtendedTextMessage.GetText()
		} else {
			quotedMsg = "Replying…"
		}
	}

	// Get Message ID
	messageID := ""
	if contextInfo != nil && contextInfo.StanzaID != nil {
		messageID = *contextInfo.StanzaID
	}
	if messageID == "" {
		messageID = fmt.Sprintf("%s_%d", strings.Split(chatID, "@")[0], time.Now().UnixNano())
	}

	// 📂 Handle Media & Text
	if txt := getText(msg); txt != "" {
		msgType = "text"
		content = txt
	} else if msg.ImageMessage != nil {
		msgType = "image"
		content = "MEDIA_WAITING"
		go saveMediaDoc(botID, chatID, messageID, "image", "image/jpeg", func() (string, error) {
			data, err := client.Download(context.Background(), msg.ImageMessage)
			if err != nil { return "", err }
			encoded := base64.StdEncoding.EncodeToString(data)
			return "data:image/jpeg;base64," + encoded, nil
		})
	} else if msg.StickerMessage != nil {
		msgType = "image"
		isSticker = true
		content = "MEDIA_WAITING"
		go saveMediaDoc(botID, chatID, messageID, "image", "image/webp", func() (string, error) {
			data, err := client.Download(context.Background(), msg.StickerMessage)
			if err != nil { return "", err }
			encoded := base64.StdEncoding.EncodeToString(data)
			return "data:image/webp;base64," + encoded, nil
		})
	} else if msg.VideoMessage != nil {
		msgType = "video"
		content = "MEDIA_WAITING"
		go saveMediaDoc(botID, chatID, messageID, "video", "video/mp4", func() (string, error) {
			data, err := client.Download(context.Background(), msg.VideoMessage)
			if err != nil { return "", err }
			url, err := UploadToCatbox(data, "video.mp4")
			if err != nil { return "", err }
			return url, nil
		})
	} else if msg.AudioMessage != nil {
		msgType = "audio"
		content = "MEDIA_WAITING"
		go saveMediaDoc(botID, chatID, messageID, "audio", "audio/ogg", func() (string, error) {
			data, err := client.Download(context.Background(), msg.AudioMessage)
			if err != nil { return "", err }
			if len(data) <= 10*1024*1024 {
				encoded := base64.StdEncoding.EncodeToString(data)
				return "data:audio/ogg;base64," + encoded, nil
			}
			url, err := UploadToCatbox(data, "audio.ogg")
			if err != nil { return "", err }
			return url, nil
		})
	} else if msg.DocumentMessage != nil {
		msgType = "file"
		content = "MEDIA_WAITING"
		go saveMediaDoc(botID, chatID, messageID, "file", "application/octet-stream", func() (string, error) {
			data, err := client.Download(context.Background(), msg.DocumentMessage)
			if err != nil { return "", err }
			fname := msg.DocumentMessage.GetFileName()
			if fname == "" { fname = "file.bin" }
			url, err := UploadToCatbox(data, fname)
			if err != nil { return "", err }
			return url, nil
		})
	} else {
		return
	}

	// 💾 Create & Insert Document
	doc := ChatMessage{
		BotID:        botID,
		ChatID:       chatID,
		Sender:       senderStr,
		SenderName:   senderName,
		MessageID:    messageID,
		Type:         msgType,
		Content:      content,
		IsFromMe:     isFromMe,
		Timestamp:    timestamp,
		IsGroup:      isGroup,
		IsChannel:    isChannel,
		IsSticker:    isSticker,
		QuotedMsg:    quotedMsg,
		QuotedSender: quotedSender,
	}

	_, err := chatHistoryCollection.InsertOne(context.Background(), doc)
	if err != nil {
		// ignore
	}
}

// helper: save media in separate collection
func saveMediaDoc(botID, chatID, messageID, typ, mime string, loader func() (string, error)) {
	if mediaCollection == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	content, err := loader()
	if err != nil || content == "" {
		return
	}

	_, _ = mediaCollection.UpdateOne(
		ctx,
		bson.M{"message_id": messageID},
		bson.M{"$set": bson.M{
			"bot_id":      botID,
			"chat_id":     chatID,
			"message_id":  messageID,
			"type":        typ,
			"mime":        mime,
			"content":     content,
			"created_at":  time.Now(),
		}},
		options.Update().SetUpsert(true),
	)
}

// ⚡ SetGlobalClient
func SetGlobalClient(c *whatsmeow.Client) {
	globalClient = c
}

// ⚡ ConnectNewSession
func ConnectNewSession(device *store.Device) {
	rawID := device.ID.User
	cleanID := getCleanID(rawID)

	clientsMutex.Lock()
	botCleanIDCache[rawID] = cleanID
	clientsMutex.Unlock()

	p, err := rdb.Get(ctx, "prefix:"+cleanID).Result()
	if err != nil {
		p = "."
	}

	prefixMutex.Lock()
	botPrefixes[cleanID] = p
	prefixMutex.Unlock()

	clientsMutex.RLock()
	_, exists := activeClients[cleanID]
	clientsMutex.RUnlock()
	if exists {
		fmt.Printf("⚠️ [MULTI-BOT] Bot %s is already active. Skipping...\n", cleanID)
		return
	}

	clientLog := waLog.Stdout("Client", "ERROR", true)
	newBotClient := whatsmeow.NewClient(device, clientLog)

	newBotClient.AddEventHandler(func(evt interface{}) {
		handler(newBotClient, evt)
	})

	err = newBotClient.Connect()
	if err != nil {
		fmt.Printf("❌ [CONNECT ERROR] Bot %s: %v\n", cleanID, err)
		return
	}
	go StartKeepAliveLoop(newBotClient)
	clientsMutex.Lock()
	activeClients[cleanID] = newBotClient
	clientsMutex.Unlock()

	fmt.Printf("✅ [CONNECTED] Bot: %s | Prefix: %s | Status: Ready\n", cleanID, p)
}

func StartKeepAliveLoop(client *whatsmeow.Client) {
	go func() {
		for {
			if client == nil || !client.IsConnected() {
				time.Sleep(10 * time.Second)
				continue
			}
			dataMutex.RLock()
			isEnabled := data.AlwaysOnline
			dataMutex.RUnlock()
			if isEnabled {
				client.SendPresence(context.Background(), types.PresenceAvailable)
			}
			time.Sleep(30 * time.Second)
		}
	}()
}

func updatePrefixDB(botID string, newPrefix string) {
	prefixMutex.Lock()
	botPrefixes[botID] = newPrefix
	prefixMutex.Unlock()
	err := rdb.Set(ctx, "prefix:"+botID, newPrefix, 0).Err()
	if err != nil {
		fmt.Printf("❌ [REDIS ERR] Could not save prefix: %v\n", err)
	}
}

// ✅ serveListsHTML
func serveListsHTML(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/lists.html")
}

func servePicture(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "pic.png")
}

func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("WebSocket upgrade failed: %v", err)
		return
	}
	defer conn.Close()
	wsClients[conn] = true
	defer delete(wsClients, conn)
	status := map[string]interface{}{
		"connected": client != nil && client.IsConnected(),
		"session":   client != nil && client.Store.ID != nil,
	}
	conn.WriteJSON(status)
	for {
		_, _, err := conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func broadcastWS(data interface{}) {
	for conn := range wsClients {
		conn.WriteJSON(data)
	}
}

// ✅ handleDeleteSession
func handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	if client != nil && client.IsConnected() {
		client.Disconnect()
	}
	devices, _ := container.GetAllDevices(context.Background())
	for _, device := range devices {
		device.Delete(context.Background())
	}
	broadcastWS(map[string]interface{}{
		"event":     "session_deleted",
		"connected": false,
	})
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"success":true,"message":"Session deleted"}`)
}

func handleDelAllAPI(w http.ResponseWriter, r *http.Request) {
	fmt.Println("🗑️ [API] Deleting all sessions from POSTGRES...")
	clientsMutex.Lock()
	for id, c := range activeClients {
		fmt.Printf("🔌 Disconnecting: %s\n", id)
		c.Disconnect()
		delete(activeClients, id)
	}
	clientsMutex.Unlock()
	devices, _ := container.GetAllDevices(context.Background())
	for _, dev := range devices {
		dev.Delete(context.Background())
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"success":true, "message":"All sessions wiped from Database"}`)
}

func handleDelNumberAPI(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 3 {
		http.Error(w, `{"error":"Number required"}`, 400)
		return
	}
	targetNum := parts[2]
	fmt.Printf("🗑️ [API] Deleting session for: %s\n", targetNum)
	clientsMutex.Lock()
	if c, ok := activeClients[getCleanID(targetNum)]; ok {
		c.Disconnect()
		delete(activeClients, getCleanID(targetNum))
	}
	clientsMutex.Unlock()
	devices, _ := container.GetAllDevices(context.Background())
	deleted := false
	for _, dev := range devices {
		if getCleanID(dev.ID.User) == getCleanID(targetNum) {
			dev.Delete(context.Background())
			deleted = true
			break
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if deleted {
		fmt.Fprintf(w, `{"success":true, "message":"Session deleted for %s"}`, targetNum)
	} else {
		fmt.Fprintf(w, `{"success":false, "message":"No session found for %s"}`, targetNum)
	}
}

func handlePairAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, `{"error":"Method not allowed"}`, 405)
		return
	}
	var req struct {
		Number string `json:"number"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid JSON"}`, 400)
		return
	}
	number := strings.TrimSpace(req.Number)
	number = strings.ReplaceAll(number, "+", "")
	number = strings.ReplaceAll(number, " ", "")
	number = strings.ReplaceAll(number, "-", "")
	cleanNum := getCleanID(number)
	fmt.Printf("📱 [PAIRING] New request for: %s on POSTGRES\n", cleanNum)
	devices, _ := container.GetAllDevices(context.Background())
	for _, dev := range devices {
		if getCleanID(dev.ID.User) == cleanNum {
			fmt.Printf("🧹 [CLEANUP] Removing old session for %s\n", cleanNum)
			clientsMutex.Lock()
			if c, ok := activeClients[cleanNum]; ok {
				c.Disconnect()
				delete(activeClients, cleanNum)
			}
			clientsMutex.Unlock()
			dev.Delete(context.Background())
		}
	}
	newDevice := container.NewDevice()
	tempClient := whatsmeow.NewClient(newDevice, waLog.Stdout("Pairing", "INFO", true))
	tempClient.AddEventHandler(func(evt interface{}) {
		handler(tempClient, evt)
	})
	err := tempClient.Connect()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), 500)
		return
	}
	time.Sleep(5 * time.Second)
	code, err := tempClient.PairPhone(context.Background(), number, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		tempClient.Disconnect()
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), 500)
		return
	}
	fmt.Printf("✅ [CODE] Generated for %s: %s\n", cleanNum, code)
	broadcastWS(map[string]interface{}{
		"event": "pairing_code",
		"code":  code,
	})
	go func() {
		for i := 0; i < 60; i++ {
			time.Sleep(1 * time.Second)
			if tempClient.Store.ID != nil {
				fmt.Printf("🎉 [PAIRED] %s is now active on Postgres!\n", cleanNum)
				clientsMutex.Lock()
				activeClients[cleanNum] = tempClient
				clientsMutex.Unlock()
				return
			}
		}
		tempClient.Disconnect()
	}()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"success":true,"code":"%s"}`, code)
}

func handlePairAPILegacy(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) < 4 {
		http.Error(w, `{"error":"Invalid URL"}`, 400)
		return
	}
	number := strings.TrimSpace(parts[3])
	number = strings.ReplaceAll(number, "+", "")
	number = strings.ReplaceAll(number, " ", "")
	number = strings.ReplaceAll(number, "-", "")
	if len(number) < 10 {
		http.Error(w, `{"error":"Invalid number"}`, 400)
		return
	}
	fmt.Printf("📱 Pairing: %s\n", number)
	if client != nil && client.IsConnected() {
		client.Disconnect()
		time.Sleep(10 * time.Second)
	}
	newDevice := container.NewDevice()
	tempClient := whatsmeow.NewClient(newDevice, waLog.Stdout("Pairing", "INFO", true))
	SetGlobalClient(tempClient)
	tempClient.AddEventHandler(func(evt interface{}) {
		handler(tempClient, evt)
	})
	err := tempClient.Connect()
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), 500)
		return
	}
	time.Sleep(10 * time.Second)
	code, err := tempClient.PairPhone(context.Background(), number, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		tempClient.Disconnect()
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), 500)
		return
	}
	fmt.Printf("✅ Code: %s\n", code)
	go func() {
		for i := 0; i < 60; i++ {
			time.Sleep(1 * time.Second)
			if tempClient.Store.ID != nil {
				fmt.Println("✅ Paired!")
				client = tempClient
				OnNewPairing(client)
				return
			}
		}
		tempClient.Disconnect()
	}()
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"success":true,"code":"%s"}`, code)
}

func StartAllBots(container *sqlstore.Container) {
	dbContainer = container
	devices, err := container.GetAllDevices(context.Background())
	if err != nil {
		fmt.Printf("❌ [DB-ERROR] Could not load sessions: %v\n", err)
		return
	}
	fmt.Printf("\n🤖 Starting Multi-Bot System (Found %d entries in DB)\n", len(devices))
	seenNumbers := make(map[string]bool)
	for _, device := range devices {
		botNum := getCleanID(device.ID.User)
		if seenNumbers[botNum] {
			continue
		}
		seenNumbers[botNum] = true
		go func(dev *store.Device) {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("❌ Crash prevented for %s: %v\n", botNum, r)
				}
			}()
			ConnectNewSession(dev)
		}(device)
		time.Sleep(2 * time.Second)
	}
	go monitorNewSessions(container)
}

func PreloadAllGroupSettings() {
	if rdb == nil {
		return
	}
	fmt.Println("🚀 [RAM] Preloading all group settings into Memory...")
	keys, err := rdb.Keys(ctx, "group_settings:*").Result()
	if err != nil {
		fmt.Println("⚠️ [RAM] Failed to fetch keys:", err)
		return
	}
	count := 0
	for _, key := range keys {
		val, err := rdb.Get(ctx, key).Result()
		if err == nil {
			var s GroupSettings
			if json.Unmarshal([]byte(val), &s) == nil {
				parts := strings.Split(key, ":")
				if len(parts) >= 3 {
					uniqueKey := parts[1] + ":" + parts[2]
					cacheMutex.Lock()
					groupCache[uniqueKey] = &s
					cacheMutex.Unlock()
					count++
				}
			}
		}
	}
	fmt.Printf("✅ [RAM] Successfully loaded settings for %d groups!\n", count)
}

func getGroupSettings(botID, chatID string) *GroupSettings {
	uniqueKey := botID + ":" + chatID
	cacheMutex.RLock()
	s, exists := groupCache[uniqueKey]
	cacheMutex.RUnlock()
	if exists {
		return s
	}
	if rdb != nil {
		redisKey := "group_settings:" + uniqueKey
		val, err := rdb.Get(ctx, redisKey).Result()
		if err == nil {
			var loadedSettings GroupSettings
			if json.Unmarshal([]byte(val), &loadedSettings) == nil {
				cacheMutex.Lock()
				groupCache[uniqueKey] = &loadedSettings
				cacheMutex.Unlock()
				return &loadedSettings
			}
		}
	}
	return &GroupSettings{
		ChatID: chatID, Mode: "public", Antilink: false,
		AntilinkAdmin: true, AntilinkAction: "delete", Welcome: false,
	}
}

func saveGroupSettings(botID string, s *GroupSettings) {
	uniqueKey := botID + ":" + s.ChatID
	cacheMutex.Lock()
	groupCache[uniqueKey] = s
	cacheMutex.Unlock()
	if rdb != nil {
		jsonData, err := json.Marshal(s)
		if err == nil {
			redisKey := "group_settings:" + uniqueKey
			err := rdb.Set(ctx, redisKey, jsonData, 0).Err()
			if err != nil {
				fmt.Printf("⚠️ [REDIS ERROR] Failed to save settings: %v\n", err)
			}
		}
	}
}

func monitorNewSessions(container *sqlstore.Container) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		devices, err := container.GetAllDevices(context.Background())
		if err != nil {
			continue
		}
		for _, device := range devices {
			botID := getCleanID(device.ID.User)
			clientsMutex.RLock()
			_, exists := activeClients[botID]
			clientsMutex.RUnlock()
			if !exists {
				fmt.Printf("\n🆕 [AUTO-CONNECT] New session detected: %s. Connecting...\n", botID)
				go ConnectNewSession(device)
				time.Sleep(2 * time.Second)
			}
		}
	}
}

// -----------------------------------------------------
// 🔥 WEB API HANDLERS (UPDATED & FIXED)
// -----------------------------------------------------

func handleGetSessions(w http.ResponseWriter, r *http.Request) {
	clientsMutex.RLock()
	var sessions []string
	for id := range activeClients {
		sessions = append(sessions, id)
	}
	clientsMutex.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

type ChatItemV2 struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Type        string    `json:"type"` // user/group/channel
	LastType    string    `json:"last_type,omitempty"`
	LastPreview string    `json:"last_preview,omitempty"`
	LastTime    time.Time `json:"last_time,omitempty"`
}

func handleGetChats(w http.ResponseWriter, r *http.Request) {
	if chatHistoryCollection == nil {
		http.Error(w, "MongoDB not connected", http.StatusInternalServerError)
		return
	}

	botID := r.URL.Query().Get("bot_id")
	if botID == "" {
		http.Error(w, "bot_id required", http.StatusBadRequest)
		return
	}

	// Aggregate chats from messages (latest activity + last sender_name)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()

	pipeline := mongo.Pipeline{
		{{Key: "$match", Value: bson.M{
			"bot_id":  botID,
			"chat_id": bson.M{"$ne": ""},
		}}},
		{{Key: "$sort", Value: bson.D{{Key: "timestamp", Value: 1}}}}, // asc so $last works
		{{Key: "$group", Value: bson.M{
			"_id":     "$chat_id",
			"last_ts": bson.M{"$last": "$timestamp"},
			"name":    bson.M{"$last": "$sender_name"},
		}}},
		{{Key: "$sort", Value: bson.D{{Key: "last_ts", Value: -1}}}},
		{{Key: "$limit", Value: 5000}},
	}

	cur, err := chatHistoryCollection.Aggregate(ctx, pipeline)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer cur.Close(ctx)

	type row struct {
		ChatID string    `bson:"_id"`
		LastTS time.Time `bson:"last_ts"`
		Name   string    `bson:"name"`
	}

	// Merge LID + JID into one canonical chat id
	type agg struct {
		ChatID string
		Name   string
		LastTS time.Time
	}

	merged := make(map[string]*agg)

	for cur.Next(ctx) {
		var it row
		if err := cur.Decode(&it); err != nil {
			continue
		}

		origID := strings.TrimSpace(it.ChatID)
		if origID == "" {
			continue
		}

		canon := canonicalChatID(origID)

		// pick best display name
		name := strings.TrimSpace(it.Name)
		if name == "" {
			// fallback to number part
			left := canon
			if strings.Contains(left, "@") {
				left = strings.Split(left, "@")[0]
			}
			if strings.Contains(left, ":") {
				left = strings.Split(left, ":")[0]
			}
			name = left
		}

		// If already exists, keep the newest timestamp
		ex, ok := merged[canon]
		if !ok {
			merged[canon] = &agg{
				ChatID: canon,
				Name:   name,
				LastTS: it.LastTS,
			}
			continue
		}

		// prefer newer ts
		if it.LastTS.After(ex.LastTS) {
			ex.LastTS = it.LastTS
		}

		// prefer "better" name (non-empty and not just number)
		if ex.Name == "" || ex.Name == strings.Split(ex.ChatID, "@")[0] {
			ex.Name = name
		}
	}

	// Build response list
	out := make([]ChatItem, 0, len(merged))
	for _, v := range merged {
		t := "user"
		if strings.Contains(v.ChatID, "@g.us") {
			t = "group"
		}
		// (optional) skip status chat record if it appears as normal chat
		// if strings.Contains(v.ChatID, "status@broadcast") { continue }

		out = append(out, ChatItem{
			ID:   v.ChatID,
			Name: v.Name,
			Type: t,
		})
	}

	// sort again by last activity desc (because map order is random)
	sort.Slice(out, func(i, j int) bool {
		// re-fetch timestamps from merged map
		return merged[out[i].ID].LastTS.After(merged[out[j].ID].LastTS)
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// 4. Get Messages (FULL DATA LOAD - NO WAITING)
func handleGetMessages(w http.ResponseWriter, r *http.Request) {
	if chatHistoryCollection == nil {
		http.Error(w, "MongoDB not connected", 500)
		return
	}

	botID := r.URL.Query().Get("bot_id")
	chatID := r.URL.Query().Get("chat_id")
	if botID == "" || chatID == "" {
		http.Error(w, "bot_id and chat_id required", 400)
		return
	}

	limit := int64(200)
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	// ✅ very important: accept both ids
	canon := canonicalChatID(chatID)
	ids := []string{chatID}
	if canon != "" && canon != chatID {
		ids = append(ids, canon)
	}

	filter := bson.M{
		"bot_id":  botID,
		"chat_id": bson.M{"$in": ids},
	}

	// ✅ fetch last N (DESC), then reverse to ASC for UI
	opts := options.Find().
		SetSort(bson.D{{Key: "timestamp", Value: -1}}).
		SetLimit(limit)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cursor, err := chatHistoryCollection.Find(ctx, filter, opts)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	var messages []ChatMessage
	if err = cursor.All(ctx, &messages); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// reverse to old->new
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(messages)
}

func ensureMongoIndexes(db *mongo.Database) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// messages: bot_id + chat_id + timestamp + message_id (paging fast)
	_, err := db.Collection("messages").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "bot_id", Value: 1},
			{Key: "chat_id", Value: 1},
			{Key: "timestamp", Value: -1},
			{Key: "message_id", Value: -1},
		},
	})
	if err != nil {
		fmt.Println("⚠️ index messages failed:", err)
	}

	// media: bot_id + message_id (fast download)
	_, err = db.Collection("media").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "bot_id", Value: 1},
			{Key: "message_id", Value: 1},
		},
	})
	if err != nil {
		fmt.Println("⚠️ index media failed:", err)
	}

	// statuses optional
	_, _ = db.Collection("statuses").Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys: bson.D{
			{Key: "bot_id", Value: 1},
			{Key: "timestamp", Value: -1},
		},
	})

	return nil
}

func extractTimestamp(m ChatMessage) int64 {
	// Timestamp is time.Time in your struct
	if m.Timestamp.IsZero() {
		return 0
	}
	// JS کے لیے best: milliseconds
	return m.Timestamp.UnixMilli()
}

type MediaDoc struct {
	BotID     string    `bson:"bot_id" json:"bot_id"`
	MessageID string    `bson:"message_id" json:"message_id"`
	Type      string    `bson:"type" json:"type"`     // image/audio/video/file/sticker
	Mime      string    `bson:"mime" json:"mime"`     // image/jpeg, audio/ogg etc
	Content   string    `bson:"content" json:"content"` // base64 data-url OR remote url
	Size      int       `bson:"size" json:"size"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
}

func handleGetMedia(w http.ResponseWriter, r *http.Request) {
	if mongoClient == nil {
		http.Error(w, "MongoDB not connected", 500)
		return
	}

	msgID := r.URL.Query().Get("msg_id")
	if msgID == "" {
		http.Error(w, "Message ID required", 400)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	type MediaDoc struct {
		MessageID string    `bson:"message_id"`
		Type      string    `bson:"type"`
		Content   string    `bson:"content"`
		Mime      string    `bson:"mime"`
		CreatedAt time.Time `bson:"created_at"`
	}

	// ✅ 1) Try media collection first
	if mediaCollection != nil {
		var md MediaDoc
		err := mediaCollection.FindOne(ctx, bson.M{"message_id": msgID}).Decode(&md)
		if err == nil && md.Content != "" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status":  "ok",
				"content": md.Content,
				"type":    md.Type,
				"mime":    md.Mime,
			})
			return
		}
	}

	// ✅ 2) Fallback to messages collection (backward compatibility)
	if chatHistoryCollection != nil {
		var msg ChatMessage
		err := chatHistoryCollection.FindOne(ctx, bson.M{"message_id": msgID}).Decode(&msg)
		if err == nil && msg.Content != "" && msg.Content != "MEDIA_WAITING" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"status":  "ok",
				"content": msg.Content,
				"type":    msg.Type,
			})
			return
		}
	}

	http.Error(w, "Media not found", 404)
}

func handleGetAvatar(w http.ResponseWriter, r *http.Request) {
	botID := r.URL.Query().Get("bot_id")
	chatID := r.URL.Query().Get("chat_id")

	clientsMutex.RLock()
	client, exists := activeClients[botID]
	clientsMutex.RUnlock()

	if !exists || client == nil {
		http.Error(w, "Bot not connected", 404)
		return
	}

	jid, _ := types.ParseJID(chatID)

	// ✅ SAFE AVATAR LOOKUP
	pic, err := client.GetProfilePictureInfo(context.Background(), jid, &whatsmeow.GetProfilePictureParams{
		Preview: true,
	})

	if err != nil || pic == nil {
		http.Error(w, "No avatar", 404)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": pic.URL})
}

func canonicalChatID(chatID string) string {
	// Goal:
	// - remove device part "12345:67@s.whatsapp.net" -> "12345@s.whatsapp.net"
	// - if lid server: "12345:67@lid" -> "12345@s.whatsapp.net"
	// - keep groups/newsletter as-is

	if chatID == "" {
		return ""
	}

	// groups + newsletter keep
	if strings.Contains(chatID, "@g.us") || strings.Contains(chatID, "@newsletter") {
		// also strip ":device" in group just in case
		parts := strings.Split(chatID, "@")
		left := parts[0]
		if strings.Contains(left, ":") {
			left = strings.Split(left, ":")[0]
		}
		return left + "@" + parts[1]
	}

	// user jid
	parts := strings.Split(chatID, "@")
	left := parts[0]
	server := ""
	if len(parts) > 1 {
		server = parts[1]
	}

	// strip device
	if strings.Contains(left, ":") {
		left = strings.Split(left, ":")[0]
	}

	// if lid -> force s.whatsapp.net
	if server == "lid" || strings.Contains(chatID, "@lid") {
		return left + "@s.whatsapp.net"
	}

	// normal user
	if server == "" {
		return left + "@s.whatsapp.net"
	}
	return left + "@" + server
}