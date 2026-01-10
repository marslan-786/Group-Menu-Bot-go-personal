package main

import (
	"context"
	"database/sql" 
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
    "go.mau.fi/whatsmeow/types"
	"github.com/gorilla/websocket"
	_ "github.com/lib/pq"
	"github.com/redis/go-redis/v9"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mongodb.org/mongo-driver/mongo"
    "go.mongodb.org/mongo-driver/mongo/options"
    "go.mongodb.org/mongo-driver/bson"
    "mime/multipart" // Catbox k liye
    "bytes"          // Catbox k liye
    "io"   
)

// 📦 STRUCT FOR MESSAGE HISTORY (MISSING THA)
type ChatMessage struct {
	BotID      string    `bson:"bot_id" json:"bot_id"`
	ChatID     string    `bson:"chat_id" json:"chat_id"`
	Sender     string    `bson:"sender" json:"sender"`
	SenderName string    `bson:"sender_name" json:"sender_name"`
	MessageID  string    `bson:"message_id" json:"message_id"`
	Timestamp  time.Time `bson:"timestamp" json:"timestamp"`
	Type       string    `bson:"type" json:"type"` // text, image, video, audio
	Content    string    `bson:"content" json:"content"` // Text or URL
	IsFromMe   bool      `bson:"is_from_me" json:"is_from_me"`
	IsGroup    bool      `bson:"is_group" json:"is_group"`
	IsChannel  bool      `bson:"is_channel" json:"is_channel"`
}

var (
	client           *whatsmeow.Client
	container        *sqlstore.Container
	dbContainer      *sqlstore.Container
	rdb              *redis.Client
	ctx              = context.Background()
	persistentUptime int64
	groupCache       = make(map[string]*GroupSettings)
	cacheMutex       sync.RWMutex
	upgrader         = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	wsClients       = make(map[*websocket.Conn]bool)
	botCleanIDCache = make(map[string]string)
	botPrefixes     = make(map[string]string)
	prefixMutex     sync.RWMutex
	clientsMutex    sync.RWMutex
	activeClients   = make(map[string]*whatsmeow.Client)
	globalClient    *whatsmeow.Client
	ytCache         = make(map[string]YTSession)
	ytDownloadCache = make(map[string]YTState)
	cachedMenuImage *waProto.ImageMessage

	// ✅ MongoDB Globals (UPDATED NAME)
	mongoClient           *mongo.Client
	chatHistoryCollection *mongo.Collection
)

// ✅ 1. ریڈیس کنکشن
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

// ✅ 2. گلوبل سیٹنگز لوڈ کرنا (تاکہ ری اسٹارٹ پر سیٹنگز یاد رہیں)
func loadGlobalSettings() {
	if rdb == nil { return }
	val, err := rdb.Get(ctx, "bot_global_settings").Result()
	if err == nil {
		dataMutex.Lock()
		json.Unmarshal([]byte(val), &data)
		dataMutex.Unlock()
		fmt.Println("✅ [SETTINGS] Bot Settings Restored from Redis")
	}
}

func main() {
	fmt.Println("🚀 IMPOSSIBLE BOT | STARTING (POSTGRES ONLY)")

	// 1. Services Start
	initRedis()
	loadPersistentUptime()
	loadGlobalSettings()
	startPersistentUptimeTracker()
	SetupFeatures()

	// 🔥🔥🔥 [NEW] MONGODB CONNECTION START 🔥🔥🔥
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
				// ✅ FIX: Variable name updated to chatHistoryCollection
				chatHistoryCollection = mClient.Database("whatsapp_bot").Collection("messages")
				fmt.Println("🍃 [MONGODB] Connected for Chat History!")
			}
		}
	} else {
		fmt.Println("⚠️ MONGO_URL not found! Chat history will not be saved.")
	}
	// 🔥🔥🔥 [NEW] MONGODB CONNECTION END 🔥🔥🔥

	// 2. Postgres Connection
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

	// 3. WhatsMeow Container
	dbLog := waLog.Stdout("Database", "ERROR", true)
	container = sqlstore.NewWithDB(rawDB, "postgres", dbLog)
	err = container.Upgrade(context.Background())
	if err != nil {
		log.Fatalf("❌ Failed to initialize database tables: %v", err)
	}
	fmt.Println("✅ [DATABASE] Tables verified/created successfully!")
	dbContainer = container

	// 4. Multi-Bot System
	fmt.Println("🤖 Initializing Multi-Bot System from Database...")
	StartAllBots(container)

	// 5. Systems
	InitLIDSystem()

	// 6. Web Server Routes
	http.HandleFunc("/", serveHTML)
	http.HandleFunc("/pic.png", servePicture)
	http.HandleFunc("/ws", handleWebSocket)

	// Pair APIs
	http.HandleFunc("/api/pair", handlePairAPI)
	http.HandleFunc("/link/pair/", handlePairAPILegacy)

	// Delete APIs
	http.HandleFunc("/link/delete", handleDeleteSession)
	http.HandleFunc("/del/all", handleDelAllAPI)
	http.HandleFunc("/del/", handleDelNumberAPI)

	// 🔥 WEB VIEW & CHAT HISTORY APIS 🔥
	http.HandleFunc("/lists", serveListsHTML)
	http.HandleFunc("/api/sessions", handleGetSessions)
	http.HandleFunc("/api/chats", handleGetChats)       // Updated Function
	http.HandleFunc("/api/messages", handleGetMessages) // Updated Function

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

	// 7. Shutdown Handling
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
		fmt.Println("🍃 MongoDB Disconnected")
	}
	if rawDB != nil {
		rawDB.Close()
	}
	fmt.Println("👋 Goodbye!")
}



// ✅ ⚡ بوٹ کنیکٹ (Same logic, slightly cleaned up)
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

// 🔄 یہ فنکشن ہر بوٹ کے کنیکٹ ہونے پر کال کریں
func StartKeepAliveLoop(client *whatsmeow.Client) {
	go func() {
		for {
			// اگر کلائنٹ کنیکٹ نہیں ہے یا نِل ہے تو لوپ روک دیں
			if client == nil || !client.IsConnected() {
				time.Sleep(10 * time.Second)
				continue
			}

			// ⚡ سیٹنگ چیک کریں
			dataMutex.RLock()
			isEnabled := data.AlwaysOnline
			dataMutex.RUnlock()

			// ✅ اگر آپشن آن ہے تو پریزنس بھیجیں
			if isEnabled {
				err := client.SendPresence(context.Background(), types.PresenceAvailable)
				if err != nil {
					// خاموشی سے اگنور کریں یا لاگ کریں
				}
			}

			// ⏳ 25 سیکنڈ کا وقفہ (تاکہ واٹس ایپ آف لائن نہ کرے)
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

// ... (باقی ویب روٹس سیم ہیں) ...

func serveHTML(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/index.html")
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
	// (یہ فنکشن بھی وہی Postgres logic استعمال کرے گا کیونکہ container اب صرف Postgres ہے)
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

	code, err := tempClient.PairPhone(
		context.Background(),
		number,
		true,
		whatsmeow.PairClientChrome,
		"Chrome (Linux)",
	)

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
		time.Sleep(2 * time.Second) // Postgres تیز ہے، اس لئے وقفہ کم کر دیا
	}
	go monitorNewSessions(container)
}

// ✅ یہ فنکشن مین (main) کے اندر StartAllBots کے بعد کال کریں
func PreloadAllGroupSettings() {
    if rdb == nil { return }
    
    fmt.Println("🚀 [RAM] Preloading all group settings into Memory...")
    
    // Redis سے تمام سیٹنگز کی Keys منگوائیں
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
                // Key سے botID اور chatID الگ کریں
                // Key format: "group_settings:923xx:1203xx@g.us"
                parts := strings.Split(key, ":")
                if len(parts) >= 3 {
                    // uniqueKey = "923xx:1203xx@g.us"
                    uniqueKey := parts[1] + ":" + parts[2]
                    
                    // 💾 سیدھا RAM میں سٹور کریں
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

// ⚡ آپٹییمائزڈ گیٹر (صرف RAM استعمال کرے گا)
func getGroupSettings(botID, chatID string) *GroupSettings {
    uniqueKey := botID + ":" + chatID

    // 1. سب سے پہلے RAM چیک کریں (0ms Latency)
    cacheMutex.RLock()
    s, exists := groupCache[uniqueKey]
    cacheMutex.RUnlock()

    if exists {
        return s
    }

    // 2. اگر RAM میں نہیں ہے (شاید نیا گروپ ہے)، تب Redis چیک کریں
    // (یہ بہت کم ہوگا کیونکہ ہم نے Preload کر لیا ہے)
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

    // 3. ڈیفالٹ
    return &GroupSettings{
        ChatID: chatID, Mode: "public", Antilink: false, 
        AntilinkAdmin: true, AntilinkAction: "delete", Welcome: false,
    }
}

func loadPersistentUptime() {
	if rdb != nil {
		val, err := rdb.Get(ctx, "total_uptime").Int64()
		if err == nil {
			persistentUptime = val
		}
	}
	fmt.Println("⏳ [UPTIME] Persistent uptime loaded from Redis")
}

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

func SetGlobalClient(c *whatsmeow.Client) {
	globalClient = c
}

// ⚡ سیٹنگز حاصل کرنے کا فنکشن (اب بوٹ آئی ڈی بھی مانگے گا)

// ⚡ سیٹنگز محفوظ کرنے کا فنکشن (بوٹ آئی ڈی کے ساتھ)
func saveGroupSettings(botID string, s *GroupSettings) {
	uniqueKey := botID + ":" + s.ChatID

	// 1. میموری (RAM) میں اپڈیٹ کریں
	cacheMutex.Lock()
	groupCache[uniqueKey] = s
	cacheMutex.Unlock()

	// 2. Redis میں محفوظ کریں (الگ کی کے ساتھ)
	if rdb != nil {
		jsonData, err := json.Marshal(s)
		if err == nil {
			redisKey := "group_settings:" + uniqueKey
			
			// Redis میں سیو کریں (No Expiry)
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

// 1. HTML Page Serve
func serveListsHTML(w http.ResponseWriter, r *http.Request) {
    http.ServeFile(w, r, "web/lists.html")
}

// 2. Active Sessions API
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

// 3. Get Chats (Unique ChatIDs from Mongo for a Bot)
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
	// ✅ FIX: Using chatHistoryCollection
	chats, err := chatHistoryCollection.Distinct(context.Background(), "chat_id", filter)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(chats)
}

func handleGetMessages(w http.ResponseWriter, r *http.Request) {
	if chatHistoryCollection == nil {
		http.Error(w, "MongoDB not connected", 500)
		return
	}
	botID := r.URL.Query().Get("bot_id")
	chatID := r.URL.Query().Get("chat_id")

	filter := bson.M{"bot_id": botID, "chat_id": chatID}
	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: 1}})

	// ✅ FIX: Using chatHistoryCollection
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

// 🐱 CATBOX UPLOAD FUNCTION (یہ غائب تھا)
func UploadToCatbox(data []byte, filename string) (string, error) {
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)

	// File Part
	part, _ := writer.CreateFormFile("fileToUpload", filename)
	part.Write(data)

	// Type Part
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

// 🔥 HELPER: Save Message to Mongo (Fixed Download & Variable)
// 📦 STRUCT UPDATE (اسے main.go یا commands.go میں جہاں struct ہے وہاں replace کریں)

// 🔥 HELPER: Save Message to Mongo (Updated with 10MB Logic & Tabs)
func saveMessageToMongo(client *whatsmeow.Client, botID, chatID string, msg *waProto.Message, isFromMe bool, ts uint64) {
	if chatHistoryCollection == nil { return }

	var msgType, content, senderName string
	timestamp := time.Unix(int64(ts), 0)
	
	// 🏷️ Identify Chat Type (Group/Channel/Private)
	isGroup := strings.Contains(chatID, "@g.us")
	isChannel := strings.Contains(chatID, "@newsletter")

	// 🕵️ NAME LOOKUP
	jid, _ := types.ParseJID(chatID)
	if contact, err := client.Store.Contacts.GetContact(jid); err == nil && contact.Found {
		senderName = contact.FullName
		if senderName == "" { senderName = contact.PushName }
	} else if contact, err := client.Store.Contacts.GetContact(jid); err == nil {
		senderName = contact.PushName
	}
	if senderName == "" { senderName = strings.Split(chatID, "@")[0] }

	// 📂 MEDIA HANDLING
	if txt := getText(msg); txt != "" {
		msgType = "text"
		content = txt
	} else if msg.ImageMessage != nil {
		// --- IMAGE (Always Mongo Base64 as per request) ---
		msgType = "image"
		data, err := client.Download(context.Background(), msg.ImageMessage)
		if err == nil {
			encoded := base64.StdEncoding.EncodeToString(data)
			content = "data:image/jpeg;base64," + encoded
		}
	} else if msg.VideoMessage != nil {
		// --- VIDEO (Always Catbox as per request) ---
		msgType = "video"
		data, err := client.Download(context.Background(), msg.VideoMessage)
		if err == nil {
			url, err := UploadToCatbox(data, "video.mp4")
			if err == nil { content = url }
		}
	} else if msg.AudioMessage != nil {
		// --- AUDIO (Logic: <10MB Mongo, >10MB Catbox) ---
		msgType = "audio"
		data, err := client.Download(context.Background(), msg.AudioMessage)
		if err == nil {
			// ⚡ Check Size: 10MB = 10 * 1024 * 1024 bytes
			if len(data) > 10*1024*1024 {
				// Too big -> Upload to Catbox
				url, err := UploadToCatbox(data, "audio.ogg")
				if err == nil { content = url }
			} else {
				// Small enough -> Save in Mongo (Base64)
				encoded := base64.StdEncoding.EncodeToString(data)
				// MIME type audio/ogg or audio/mpeg
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
		BotID:      botID,
		ChatID:     chatID,
		Sender:     chatID,
		SenderName: senderName,
		Type:       msgType,
		Content:    content,
		IsFromMe:   isFromMe,
		Timestamp:  timestamp,
		IsGroup:    isGroup,    // ✅ Saved
		IsChannel:  isChannel,  // ✅ Saved
	}

	_, err := chatHistoryCollection.InsertOne(context.Background(), doc)
	if err != nil {
		fmt.Printf("❌ Mongo Save Error: %v\n", err)
	}
}