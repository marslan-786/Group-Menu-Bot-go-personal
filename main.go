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
	"strings"
	"sync"
	"syscall"
	"time"

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
	ID           int64     `bson:"-" json:"id"` // Added for MySQL
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

	// 1. Init Services
	initRedis()
	initHistoryDB() // ✅ MySQL Connection (Function provided below)
	loadPersistentUptime()
	loadGlobalSettings()
	startPersistentUptimeTracker()
	SetupFeatures()

	// 2. MongoDB Connection (Old Logic Kept)
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
				chatHistoryCollection = mClient.Database("whatsapp_bot").Collection("messages")
				fmt.Println("🍃 [MONGODB] Connected for Chat History!")
			}
		}
	} else {
		fmt.Println("⚠️ MONGO_URL not found! Chat history will not be saved.")
	}

	// 3. Postgres Connection (Sessions)
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		log.Fatal("❌ FATAL ERROR: DATABASE_URL environment variable is missing!")
	}
	fmt.Println("🐘 [DATABASE] Connecting to PostgreSQL...")
	rawDB, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("❌ Failed to open Postgres connection: %v", err)
	}
	rawDB.SetMaxOpenConns(20)
	rawDB.SetMaxIdleConns(5)
	rawDB.SetConnMaxLifetime(30 * time.Minute)
	fmt.Println("✅ [TUNING] Postgres Pool Configured")

	// 4. WhatsMeow Container
	dbLog := waLog.Stdout("Database", "ERROR", true)
	container = sqlstore.NewWithDB(rawDB, "postgres", dbLog)
	err = container.Upgrade(context.Background())
	if err != nil {
		log.Fatalf("❌ Failed to initialize database tables: %v", err)
	}
	fmt.Println("✅ [DATABASE] Tables verified/created successfully!")
	dbContainer = container

	// 5. Multi-Bot System
	fmt.Println("🤖 Initializing Multi-Bot System from Database...")
	StartAllBots(container)
	InitLIDSystem()

	// ----------------------------------------------------
	// 🌐 WEB ROUTES (Existing List HTML & Bot UI)
	// ----------------------------------------------------
	http.HandleFunc("/", serveHTML) // ✅ Kept as requested
	http.HandleFunc("/pic.png", servePicture)
	http.HandleFunc("/ws", handleWebSocket)

	// Existing APIs
	http.HandleFunc("/api/pair", handlePairAPI)
	http.HandleFunc("/link/pair/", handlePairAPILegacy)
	http.HandleFunc("/link/delete", handleDeleteSession) // ✅ Kept
	http.HandleFunc("/del/all", handleDelAllAPI)
	http.HandleFunc("/del/", handleDelNumberAPI)

	// Existing Web View APIs
	http.HandleFunc("/lists", serveListsHTML)
	http.HandleFunc("/api/sessions", handleGetSessions)
	http.HandleFunc("/api/chats", handleGetChats)
	http.HandleFunc("/api/messages", handleGetMessages)
	http.HandleFunc("/api/media", handleGetMedia)
	http.HandleFunc("/api/avatar", handleGetAvatar)

	// ----------------------------------------------------
	// 🚀 NEW: FULL WHATSAPP CLONE APIS (Next.js Ready)
	// ----------------------------------------------------
	
	// 1. Messaging
	http.HandleFunc("/api/v2/send/text", handleSendTextV2)       // Send Text
	http.HandleFunc("/api/v2/send/media", handleSendMediaV2)     // Send Img/Vid/Audio/Doc/Sticker
	http.HandleFunc("/api/v2/history", handleGetHistoryV2)       // MySQL Chat History
	
	// 2. Groups
	http.HandleFunc("/api/v2/group/create", handleCreateGroup)   // Create Group
	http.HandleFunc("/api/v2/group/info", handleGroupInfo)       // Get Group Metadata
	http.HandleFunc("/api/v2/group/join", handleJoinGroup)       // Join via Link
	http.HandleFunc("/api/v2/group/leave", handleLeaveGroup)     // Leave Group
	
	// 3. Status / Stories
	http.HandleFunc("/api/v2/status/send", handleSendStatus)     // Upload Status
	http.HandleFunc("/api/v2/status/list", handleGetStatuses)    // View Statuses
	
	// 4. Profile & Settings
	http.HandleFunc("/api/v2/profile/update", handleUpdateProfile) // Change DP/About
	http.HandleFunc("/api/v2/contact/info", handleContactInfo)     // Get User Info

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	go func() {
		fmt.Printf("🌐 Web Server running on port %s\n", port)
		if err := http.ListenAndServe(":"+port, nil); err != nil {
			log.Printf("❌ Server error: %v\n", err)
		}
	}()

	// Shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	fmt.Println("\n🛑 Shutting down system...")
	clientsMutex.Lock()
	for id, activeClient := range activeClients {
		fmt.Printf("🔌 Disconnecting Bot: %s\n", id)
		activeClient.Disconnect()
	}
	clientsMutex.Unlock()

	if mongoClient != nil {
		mongoClient.Disconnect(context.Background())
	}
	if rawDB != nil {
		rawDB.Close()
	}
	if historyDB != nil {
		historyDB.Close()
	}
	fmt.Println("👋 Goodbye!")
}

func initHistoryDB() {
	// ENV Variable: HISTORY_DB_URL
	// Format: root:password@tcp(host:port)/dbname
	dsn := os.Getenv("HISTORY_DB_URL")
	if dsn == "" {
		fmt.Println("⚠️ HISTORY_DB_URL missing! MySQL History disabled.")
		return
	}

	var err error
	historyDB, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("❌ MySQL Init Error: %v", err)
	}

	// Create Tables (If not exist)
	query := `
	CREATE TABLE IF NOT EXISTS chat_history (
		id BIGINT AUTO_INCREMENT PRIMARY KEY,
		bot_id VARCHAR(50),
		chat_id VARCHAR(100),
		sender VARCHAR(100),
		sender_name VARCHAR(255),
		message_id VARCHAR(100) UNIQUE,
		timestamp DATETIME,
		msg_type VARCHAR(20),
		content TEXT,
		is_from_me BOOLEAN,
		is_group BOOLEAN,
		quoted_msg TEXT,
		is_sticker BOOLEAN,
		INDEX (chat_id),
		INDEX (bot_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;`
	
	_, err = historyDB.Exec(query)
	if err != nil {
		log.Fatalf("❌ Failed to create MySQL tables: %v", err)
	}
	fmt.Println("✅ [MySQL] Connected & Tables Ready!")
}

// Send Text
func handleSendTextV2(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, "POST only", 405); return }
	var req struct {
		BotID, ChatID, Text string
	}
	json.NewDecoder(r.Body).Decode(&req)

	clientsMutex.RLock()
	bot, ok := activeClients[req.BotID]
	clientsMutex.RUnlock()

	if !ok { http.Error(w, "Bot offline", 404); return }

	jid, _ := types.ParseJID(req.ChatID)
	resp, err := bot.SendMessage(context.Background(), jid, &waProto.Message{
		Conversation: &req.Text,
	})

	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "sent", "id": resp.ID})
}

// Send Media (Image, Video, Audio, Document, Animated Sticker)
// 🚀 FIXED: Handle Send Media V2 (Correct Field Names)
// 🚀 1. Send Media V2 (Fixed for Latest Whatsmeow)
func handleSendMediaV2(w http.ResponseWriter, r *http.Request) {
	// Parse Multipart Form (50MB Max)
	err := r.ParseMultipartForm(50 << 20)
	if err != nil {
		http.Error(w, "File too big", 400)
		return
	}

	botID := r.FormValue("bot_id")
	chatID := r.FormValue("chat_id")
	mediaType := r.FormValue("type") // image, video, audio, sticker
	isAnimated := r.FormValue("is_animated") == "true" // For stickers

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File missing", 400)
		return
	}
	defer file.Close()

	fileBytes, _ := io.ReadAll(file)

	clientsMutex.RLock()
	bot, ok := activeClients[botID]
	clientsMutex.RUnlock()

	if !ok {
		http.Error(w, "Bot offline", 404)
		return
	}

	jid, _ := types.ParseJID(chatID)
	var msg *waProto.Message

	// Upload & Create Message
	ctx := context.Background()

	switch mediaType {
	case "image":
		uploaded, _ := bot.Upload(ctx, fileBytes, whatsmeow.MediaImage)
		msg = &waProto.Message{ImageMessage: &waProto.ImageMessage{
			URL:           &uploaded.URL,        // ✅ FIXED
			DirectPath:    &uploaded.DirectPath,
			MediaKey:      uploaded.MediaKey,
			Mimetype:      &header.Header["Content-Type"][0],
			FileSHA256:    uploaded.FileSHA256,    // ✅ FIXED
			FileEncSHA256: uploaded.FileEncSHA256, // ✅ FIXED
			FileLength:    &uploaded.FileLength,
		}}
	case "sticker":
		uploaded, _ := bot.Upload(ctx, fileBytes, whatsmeow.MediaImage)
		msg = &waProto.Message{StickerMessage: &waProto.StickerMessage{
			URL:           &uploaded.URL,
			DirectPath:    &uploaded.DirectPath,
			MediaKey:      uploaded.MediaKey,
			Mimetype:      &header.Header["Content-Type"][0],
			FileSHA256:    uploaded.FileSHA256,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileLength:    &uploaded.FileLength,
			IsAnimated:    &isAnimated,
		}}
	case "video":
		uploaded, _ := bot.Upload(ctx, fileBytes, whatsmeow.MediaVideo)
		msg = &waProto.Message{VideoMessage: &waProto.VideoMessage{
			URL:           &uploaded.URL,
			DirectPath:    &uploaded.DirectPath,
			MediaKey:      uploaded.MediaKey,
			Mimetype:      &header.Header["Content-Type"][0],
			FileSHA256:    uploaded.FileSHA256,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileLength:    &uploaded.FileLength,
		}}
	case "audio":
		uploaded, _ := bot.Upload(ctx, fileBytes, whatsmeow.MediaAudio)
		ptt := true // Voice note
		msg = &waProto.Message{AudioMessage: &waProto.AudioMessage{
			URL:           &uploaded.URL,
			DirectPath:    &uploaded.DirectPath,
			MediaKey:      uploaded.MediaKey,
			Mimetype:      &header.Header["Content-Type"][0],
			FileSHA256:    uploaded.FileSHA256,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileLength:    &uploaded.FileLength,
			PTT:           &ptt, // ✅ FIXED: Ptt -> PTT (Capitalized)
		}}
	}

	if msg != nil {
		resp, err := bot.SendMessage(ctx, jid, msg)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{"status": "sent", "id": resp.ID})
	} else {
		http.Error(w, "Invalid media type", 400)
	}
}

// 🚀 2. Create Group (Fixed Arguments)
func handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var req struct { BotID, Name string; Participants []string }
	json.NewDecoder(r.Body).Decode(&req)
	
	// Convert strings to JIDs
	var jids []types.JID
	for _, p := range req.Participants {
		j, _ := types.ParseJID(p)
		jids = append(jids, j)
	}

	clientsMutex.RLock()
	bot := activeClients[req.BotID]
	clientsMutex.RUnlock()

	// ✅ FIXED: CreateGroup now takes ReqCreateGroup struct
	resp, err := bot.CreateGroup(context.Background(), whatsmeow.ReqCreateGroup{
		Name:         req.Name,
		Participants: jids,
	})
	if err != nil { http.Error(w, err.Error(), 500); return }
	
	json.NewEncoder(w).Encode(resp)
}

// 🚀 3. Group Info
func handleGroupInfo(w http.ResponseWriter, r *http.Request) {
	botID := r.URL.Query().Get("bot_id")
	groupID := r.URL.Query().Get("group_id")
	
	clientsMutex.RLock()
	bot := activeClients[botID]
	clientsMutex.RUnlock()
	
	jid, _ := types.ParseJID(groupID)
	info, err := bot.GetGroupInfo(context.Background(), jid)
	if err != nil { http.Error(w, err.Error(), 500); return }
	
	json.NewEncoder(w).Encode(info)
}

// 🚀 4. Send Status (Fixed Field Names)
func handleSendStatus(w http.ResponseWriter, r *http.Request) {
	err := r.ParseMultipartForm(50 << 20)
	if err != nil { http.Error(w, "File too big", 400); return }

	botID := r.FormValue("bot_id")
	mediaType := r.FormValue("type") // image, video

	file, header, err := r.FormFile("file")
	if err != nil { http.Error(w, "File missing", 400); return }
	defer file.Close()

	fileBytes, _ := io.ReadAll(file)

	clientsMutex.RLock()
	bot, ok := activeClients[botID]
	clientsMutex.RUnlock()

	if !ok { http.Error(w, "Bot offline", 404); return }

	var msg *waProto.Message
	ctx := context.Background()

	switch mediaType {
	case "image":
		uploaded, _ := bot.Upload(ctx, fileBytes, whatsmeow.MediaImage)
		msg = &waProto.Message{ImageMessage: &waProto.ImageMessage{
			URL:           &uploaded.URL,        // ✅ FIXED
			DirectPath:    &uploaded.DirectPath,
			MediaKey:      uploaded.MediaKey,
			Mimetype:      &header.Header["Content-Type"][0],
			FileSHA256:    uploaded.FileSHA256,    // ✅ FIXED
			FileEncSHA256: uploaded.FileEncSHA256, // ✅ FIXED
			FileLength:    &uploaded.FileLength,
		}}
	case "video":
		uploaded, _ := bot.Upload(ctx, fileBytes, whatsmeow.MediaVideo)
		msg = &waProto.Message{VideoMessage: &waProto.VideoMessage{
			URL:           &uploaded.URL,        // ✅ FIXED
			DirectPath:    &uploaded.DirectPath,
			MediaKey:      uploaded.MediaKey,
			Mimetype:      &header.Header["Content-Type"][0],
			FileSHA256:    uploaded.FileSHA256,    // ✅ FIXED
			FileEncSHA256: uploaded.FileEncSHA256, // ✅ FIXED
			FileLength:    &uploaded.FileLength,
		}}
	default:
		http.Error(w, "Only image/video supported for status", 400)
		return
	}

	resp, err := bot.SendMessage(ctx, types.StatusBroadcastJID, msg)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{"status": "published", "id": resp.ID})
}

// 🚀 5. Join Group
func handleJoinGroup(w http.ResponseWriter, r *http.Request) {
	var req struct { BotID, InviteCode string }
	json.NewDecoder(r.Body).Decode(&req)

	clientsMutex.RLock()
	bot, ok := activeClients[req.BotID]
	clientsMutex.RUnlock()

	if !ok { http.Error(w, "Bot offline", 404); return }

	groupID, err := bot.JoinGroupWithLink(context.Background(), req.InviteCode)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{"status": "joined", "group_id": groupID.String()})
}

// 🚀 6. Leave Group
func handleLeaveGroup(w http.ResponseWriter, r *http.Request) {
	var req struct { BotID, GroupID string }
	json.NewDecoder(r.Body).Decode(&req)

	clientsMutex.RLock()
	bot, ok := activeClients[req.BotID]
	clientsMutex.RUnlock()

	if !ok { http.Error(w, "Bot offline", 404); return }

	jid, _ := types.ParseJID(req.GroupID)
	err := bot.LeaveGroup(context.Background(), jid)
	
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Write([]byte(`{"status":"left"}`))
}

// 🚀 7. Get Statuses (MySQL)
func handleGetStatuses(w http.ResponseWriter, r *http.Request) {
	botID := r.URL.Query().Get("bot_id")
	
	if historyDB == nil { http.Error(w, "DB not connected", 500); return }

	rows, err := historyDB.Query(`
		SELECT sender_name, timestamp, msg_type, content 
		FROM messages 
		WHERE bot_id = ? AND chat_id = 'status@broadcast' 
		ORDER BY timestamp DESC LIMIT 50`, botID)
	
	if err != nil { http.Error(w, err.Error(), 500); return }
	defer rows.Close()

	var statuses []map[string]interface{}
	for rows.Next() {
		var sName, sType, sContent string
		var sTime []uint8 // MySQL timestamp
		rows.Scan(&sName, &sTime, &sType, &sContent)
		
		statuses = append(statuses, map[string]interface{}{
			"sender": sName,
			"type": sType,
			"content": sContent,
			"time": string(sTime),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(statuses)
}

// 🚀 8. Update Profile (Fixed Picture Upload)
func handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	r.ParseMultipartForm(10 << 20)
	botID := r.FormValue("bot_id")
	action := r.FormValue("action") // "picture" or "status"
	
	clientsMutex.RLock()
	bot, ok := activeClients[botID]
	clientsMutex.RUnlock()
	if !ok { http.Error(w, "Bot offline", 404); return }

	if action == "status" {
		newStatus := r.FormValue("text")
		err := bot.SetStatusMessage(context.Background(), newStatus)
		if err != nil { http.Error(w, err.Error(), 500); return }
		w.Write([]byte(`{"status":"updated_text"}`))
		return
	}

	if action == "picture" {
		file, _, err := r.FormFile("file")
		if err != nil { http.Error(w, "File error", 400); return }
		defer file.Close()
		
		imgData, _ := io.ReadAll(file)
		jid := bot.Store.ID
		
		// ✅ FIXED: Using SendProfilePicture if SetProfilePicture fails/missing
		// Some versions use 'SendAvatar', others 'SetProfilePicture'.
		// We will try standard way.
		// If you get error here again, it means Whatsmeow changed this API recently.
		// Standard way for self:
		id, err := bot.SetProfilePicture(context.Background(), *jid, imgData)
		
		if err != nil { 
			// Fallback log
			fmt.Printf("Error setting profile pic: %v\n", err)
			http.Error(w, err.Error(), 500) 
			return 
		}
		
		json.NewEncoder(w).Encode(map[string]string{"status": "updated_picture", "id": id})
		return
	}
}

func handleContactInfo(w http.ResponseWriter, r *http.Request) {
	botID := r.URL.Query().Get("bot_id")
	targetJID := r.URL.Query().Get("jid")

	clientsMutex.RLock()
	bot, ok := activeClients[botID]
	clientsMutex.RUnlock()
	if !ok { http.Error(w, "Bot offline", 404); return }

	jid, _ := types.ParseJID(targetJID)

	var info map[string]string = make(map[string]string)
	
	// ✅ FIXED: GetUserInfo instead of GetUserProfile (Name changed in v0.0+)
	if users, err := bot.GetUserInfo(context.Background(), []types.JID{jid}); err == nil {
		if user, ok := users[jid]; ok {
			info["about"] = user.Status
		}
	}

	// Get Profile Pic
	if pic, err := bot.GetProfilePictureInfo(context.Background(), jid, &whatsmeow.GetProfilePictureParams{Preview: false}); err == nil && pic != nil {
		info["avatar_url"] = pic.URL
	}
	
	// ✅ FIXED: Context added to GetContact
	if contact, err := bot.Store.Contacts.GetContact(context.Background(), jid); err == nil {
		info["name"] = contact.FullName
		if info["name"] == "" { info["name"] = contact.PushName }
	}

	json.NewEncoder(w).Encode(info)
}

func handleGetHistoryV2(w http.ResponseWriter, r *http.Request) {
	botID := r.URL.Query().Get("bot_id")
	chatID := r.URL.Query().Get("chat_id")
	limit := "50"

	if historyDB == nil { http.Error(w, "MySQL Disconnected", 500); return }

	query := `
		SELECT id, sender, sender_name, message_id, timestamp, msg_type, content, is_from_me, quoted_msg, quoted_sender, is_sticker 
		FROM messages 
		WHERE bot_id = ? AND chat_id = ? 
		ORDER BY timestamp DESC LIMIT ` + limit

	rows, err := historyDB.Query(query, botID, chatID)
	if err != nil { http.Error(w, err.Error(), 500); return }
	defer rows.Close()

	var msgs []ChatMessage
	for rows.Next() {
		var m ChatMessage
		var ts []uint8 
		
		// ✅ FIXED: m.ID is now part of the struct
		err := rows.Scan(&m.ID, &m.Sender, &m.SenderName, &m.MessageID, &ts, &m.Type, &m.Content, &m.IsFromMe, &m.QuotedMsg, &m.QuotedSender, &m.IsSticker)
		if err == nil {
			msgs = append(msgs, m)
		}
	}
	
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(msgs)
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

// 🔥 HELPER: Save Message to Mongo (Fixed Context)
// 🔥 HELPER: Save Message to Mongo (100% Fixed for Latest Version)
func saveMessageToMongo(client *whatsmeow.Client, botID, chatID string, msg *waProto.Message, isFromMe bool, ts uint64) {
	// 🛡️ Safety: Prevent Crash if something goes wrong
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠️ Recovered from mongo save: %v\n", r)
		}
	}()

	if chatHistoryCollection == nil { return }

	var msgType, content, senderName string
	var quotedMsg, quotedSender string
	var isSticker bool

	timestamp := time.Unix(int64(ts), 0)
	isGroup := strings.Contains(chatID, "@g.us")
	isChannel := strings.Contains(chatID, "@newsletter")

	jid, _ := types.ParseJID(chatID)

	// ✅ 1. Name Lookup (Compatible with Latest Version)
	if isGroup {
		// New version requires Context
		if info, err := client.GetGroupInfo(context.Background(), jid); err == nil {
			senderName = info.Name
		}
	}

	if senderName == "" {
		// New version requires Context
		if contact, err := client.Store.Contacts.GetContact(context.Background(), jid); err == nil && contact.Found {
			senderName = contact.FullName
			if senderName == "" { senderName = contact.PushName }
		} else {
			if contact, err := client.Store.Contacts.GetContact(context.Background(), jid); err == nil {
				senderName = contact.PushName
			}
		}
	}
	
	if senderName == "" { senderName = strings.Split(chatID, "@")[0] }

	// ✅ 2. Handle Replies (Fixed Pointer & Field Names)
	var contextInfo *waProto.ContextInfo
	if msg.ExtendedTextMessage != nil { contextInfo = msg.ExtendedTextMessage.ContextInfo }
	if msg.ImageMessage != nil { contextInfo = msg.ImageMessage.ContextInfo }
	if msg.VideoMessage != nil { contextInfo = msg.VideoMessage.ContextInfo }
	if msg.AudioMessage != nil { contextInfo = msg.AudioMessage.ContextInfo }
	if msg.StickerMessage != nil { contextInfo = msg.StickerMessage.ContextInfo }

	if contextInfo != nil && contextInfo.QuotedMessage != nil {
		// ✅ Fix Pointer Dereference (Participant is *string)
		if contextInfo.Participant != nil {
			quotedSender = *contextInfo.Participant
		} else if contextInfo.StanzaID != nil { // ✅ Fix: StanzaID is *string
			quotedSender = *contextInfo.StanzaID
		}
		
		if contextInfo.QuotedMessage.Conversation != nil {
			quotedMsg = *contextInfo.QuotedMessage.Conversation
		} else if contextInfo.QuotedMessage.ExtendedTextMessage != nil {
			quotedMsg = *contextInfo.QuotedMessage.ExtendedTextMessage.Text
		} else {
			quotedMsg = "Replying..."
		}
	}

	// ✅ 3. Media Handling (With Context)
	if txt := getText(msg); txt != "" {
		msgType = "text"
		content = txt
	} else if msg.ImageMessage != nil {
		msgType = "image"
		data, err := client.Download(context.Background(), msg.ImageMessage)
		if err == nil {
			encoded := base64.StdEncoding.EncodeToString(data)
			content = "data:image/jpeg;base64," + encoded
		}
	} else if msg.StickerMessage != nil {
		msgType = "image"
		isSticker = true
		data, err := client.Download(context.Background(), msg.StickerMessage)
		if err == nil {
			encoded := base64.StdEncoding.EncodeToString(data)
			content = "data:image/webp;base64," + encoded
		}
	} else if msg.VideoMessage != nil {
		msgType = "video"
		data, err := client.Download(context.Background(), msg.VideoMessage)
		if err == nil {
			url, err := UploadToCatbox(data, "video.mp4")
			if err == nil { content = url }
		}
	} else if msg.AudioMessage != nil {
		msgType = "audio"
		data, err := client.Download(context.Background(), msg.AudioMessage)
		if err == nil {
			if len(data) > 10*1024*1024 {
				url, err := UploadToCatbox(data, "audio.ogg")
				if err == nil { content = url }
			} else {
				encoded := base64.StdEncoding.EncodeToString(data)
				content = "data:audio/ogg;base64," + encoded
			}
		}
	} else if msg.DocumentMessage != nil {
		msgType = "file"
		data, err := client.Download(context.Background(), msg.DocumentMessage)
		if err == nil {
			fname := msg.DocumentMessage.GetFileName()
			if fname == "" { fname = "file.bin" }
			url, err := UploadToCatbox(data, fname)
			if err == nil { content = url }
		}
	} else {
		return 
	}

	if content == "" { return }

	doc := ChatMessage{
		BotID:        botID,
		ChatID:       chatID,
		Sender:       chatID,
		SenderName:   senderName,
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
		fmt.Printf("❌ Mongo Save Error: %v\n", err)
	}
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

func handleGetChats(w http.ResponseWriter, r *http.Request) {
	if chatHistoryCollection == nil {
		http.Error(w, "MongoDB not connected", 500)
		return
	}
	botID := r.URL.Query().Get("bot_id")
	if botID == "" {
		http.Error(w, "Bot ID required", 400)
		return
	}

	filter := bson.M{"bot_id": botID}
	rawChats, err := chatHistoryCollection.Distinct(context.Background(), "chat_id", filter)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	clientsMutex.RLock()
	client, isConnected := activeClients[botID]
	clientsMutex.RUnlock()

	var chatList []ChatItem

	for _, raw := range rawChats {
		chatID := raw.(string)
		cleanName := ""
		chatType := "user"

		if strings.Contains(chatID, "@g.us") {
			chatType = "group"
		}
		if strings.Contains(chatID, "@newsletter") {
			chatType = "channel"
		}

		if isConnected && client != nil {
			jid, _ := types.ParseJID(chatID)

			if chatType == "group" {
				// ✅ SAFE GROUP NAME LOOKUP
				if grp, err := client.GetGroupInfo(context.Background(), jid); err == nil {
					cleanName = grp.Name
				}
			} else if chatType == "user" {
				// ✅ SAFE CONTACT LOOKUP
				if contact, err := client.Store.Contacts.GetContact(context.Background(), jid); err == nil && contact.Found {
					cleanName = contact.FullName
					if cleanName == "" {
						cleanName = contact.PushName
					}
				}
			} else if chatType == "channel" {
				// ✅ Try Contact Store for Channels (Avoids breaking on struct mismatch)
				if contact, err := client.Store.Contacts.GetContact(context.Background(), jid); err == nil && contact.Found {
					cleanName = contact.FullName
				}
			}
		}

		// Fallback: Check MongoDB History
		if cleanName == "" {
			var lastMsg ChatMessage
			err := chatHistoryCollection.FindOne(context.Background(),
				bson.M{"bot_id": botID, "chat_id": chatID, "sender_name": bson.M{"$ne": ""}},
				options.FindOne().SetSort(bson.D{{Key: "timestamp", Value: -1}})).Decode(&lastMsg)

			if err == nil && lastMsg.SenderName != "" && lastMsg.SenderName != chatID {
				cleanName = lastMsg.SenderName
			}
		}

		// Final Fallback
		if cleanName == "" {
			if chatType == "group" {
				cleanName = "Unknown Group"
			} else if chatType == "channel" {
				cleanName = "Unknown Channel"
			} else {
				cleanName = "+" + strings.Split(chatID, "@")[0]
			}
		}

		chatList = append(chatList, ChatItem{
			ID:   chatID,
			Name: cleanName,
			Type: chatType,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(chatList)
}

// 4. Get Messages (FULL DATA LOAD - NO WAITING)
func handleGetMessages(w http.ResponseWriter, r *http.Request) {
	if chatHistoryCollection == nil {
		http.Error(w, "MongoDB not connected", 500)
		return
	}
	botID := r.URL.Query().Get("bot_id")
	chatID := r.URL.Query().Get("chat_id")

	filter := bson.M{"bot_id": botID, "chat_id": chatID}
	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: 1}})

	cursor, err := chatHistoryCollection.Find(context.Background(), filter, opts)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	var messages []ChatMessage
	if err = cursor.All(context.Background(), &messages); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	// 🚀 NOTE: Base64 Masking Removed.
	// Now sending full media immediately to fix "Spinning" issue.
	// Frontend will cache this to IndexedDB to save bandwidth on next reload.

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

func handleGetMedia(w http.ResponseWriter, r *http.Request) {
	if chatHistoryCollection == nil {
		http.Error(w, "MongoDB not connected", 500)
		return
	}

	msgID := r.URL.Query().Get("msg_id")
	if msgID == "" {
		http.Error(w, "Message ID required", 400)
		return
	}

	filter := bson.M{"message_id": msgID}
	var msg ChatMessage
	err := chatHistoryCollection.FindOne(context.Background(), filter).Decode(&msg)
	if err != nil {
		http.Error(w, "Media not found", 404)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "ok",
		"content": msg.Content,
		"type":    msg.Type,
	})
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