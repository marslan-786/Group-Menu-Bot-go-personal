package main

import (
	"context"
	"database/sql" // ✅ SQL پیکیج لازمی ہے
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

	"github.com/gorilla/websocket"
	_ "github.com/lib/pq" // ✅ صرف Postgres ڈرائیور رکھا ہے
	// SQLite ڈرائیور یہاں سے مکمل ہٹا دیا گیا ہے 🗑️
	"github.com/redis/go-redis/v9"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"
)

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

	// 1. سروسز اسٹارٹ کریں
	initRedis()
	loadPersistentUptime()
	loadGlobalSettings() // ✅ سیٹنگز لوڈ کریں
	startPersistentUptimeTracker()

	// 2. ڈیٹا بیس کنکشن (صرف Postgres)
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		// اگر URL نہیں ہے تو کریش کر جاؤ (کیونکہ SQLite کا آپشن ختم کر دیا ہے)
		log.Fatal("❌ FATAL ERROR: DATABASE_URL environment variable is missing! This bot requires PostgreSQL.")
	}

	fmt.Println("🐘 [DATABASE] Connecting to PostgreSQL...")

	// ⚡ Raw DB کنکشن کھولیں
	rawDB, err := sql.Open("postgres", dbURL)
	if err != nil {
		log.Fatalf("❌ Failed to open Postgres connection: %v", err)
	}

	// ⚡ Connection Pooling (تیز رفتاری کے لیے)
	rawDB.SetMaxOpenConns(20) // 14+ بوٹس کے لیے بہترین
	rawDB.SetMaxIdleConns(5)
	rawDB.SetConnMaxLifetime(30 * time.Minute)
	fmt.Println("✅ [TUNING] Postgres Pool Configured (Max: 20 Connections)")

	// 3. WhatsMeow کنٹینر بنائیں
	dbLog := waLog.Stdout("Database", "ERROR", true)
	container = sqlstore.NewWithDB(rawDB, "postgres", dbLog)

	// 🔥🔥🔥 [FIXED] اب Context پاس کر دیا ہے 🔥🔥🔥
	err = container.Upgrade(context.Background()) 
	if err != nil {
		log.Fatalf("❌ Failed to initialize database tables: %v", err)
	}
	fmt.Println("✅ [DATABASE] Tables verified/created successfully!")
	// 🔥🔥🔥 [FIX END] 🔥🔥🔥

	dbContainer = container

	// 4. ملٹی بوٹ سسٹم شروع کریں
	fmt.Println("🤖 Initializing Multi-Bot System from Database...")
	StartAllBots(container)

	// 5. باقی سسٹمز
	InitLIDSystem()

	// 6. ویب سرور روٹس
	http.HandleFunc("/", serveHTML)
	http.HandleFunc("/pic.png", servePicture)
	http.HandleFunc("/ws", handleWebSocket)
	http.HandleFunc("/api/pair", handlePairAPI)
	http.HandleFunc("/link/pair/", handlePairAPILegacy)
	http.HandleFunc("/link/delete", handleDeleteSession)
	http.HandleFunc("/del/all", handleDelAllAPI)
	http.HandleFunc("/del/", handleDelNumberAPI)

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

	// 7. شٹ ڈاؤن ہینڈلنگ
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	fmt.Println("\n🛑 Shutting down system...")

	// بوٹس کو صاف طریقے سے بند کریں
	clientsMutex.Lock()
	for id, activeClient := range activeClients {
		fmt.Printf("🔌 Disconnecting Bot: %s\n", id)
		activeClient.Disconnect()
	}
	clientsMutex.Unlock()

	// ڈیٹا بیس بند کریں
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

	clientsMutex.Lock()
	activeClients[cleanID] = newBotClient
	clientsMutex.Unlock()

	fmt.Printf("✅ [CONNECTED] Bot: %s | Prefix: %s | Status: Ready\n", cleanID, p)
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
func getGroupSettings(botID, chatID string) *GroupSettings {
	// یونیک کی بنائیں (تاکہ ہر بوٹ کا ڈیٹا الگ رہے)
	// Key Format: "923001234567:1203630...@g.us"
	uniqueKey := botID + ":" + chatID

	// 1. پہلے میموری (RAM) چیک کریں
	cacheMutex.RLock()
	s, exists := groupCache[uniqueKey]
	cacheMutex.RUnlock()

	if exists {
		return s
	}

	// 2. اگر میموری میں نہیں ہے، تو Redis چیک کریں
	if rdb != nil {
		// Redis Key: "group_settings:92300...:12036..."
		redisKey := "group_settings:" + uniqueKey
		val, err := rdb.Get(ctx, redisKey).Result()
		
		if err == nil {
			var loadedSettings GroupSettings
			err := json.Unmarshal([]byte(val), &loadedSettings)
			if err == nil {
				// میموری میں اپڈیٹ کریں (Composite Key کے ساتھ)
				cacheMutex.Lock()
				groupCache[uniqueKey] = &loadedSettings
				cacheMutex.Unlock()
				
				return &loadedSettings
			}
		}
	}

	// 3. اگر کہیں نہیں ہے تو ڈیفالٹ بنائیں
	newSettings := &GroupSettings{
		ChatID:         chatID,
		Mode:           "public", 
		Antilink:       false,
		AntilinkAdmin:  true,     
		AntilinkAction: "delete", 
		Welcome:        false,
		Warnings:       make(map[string]int),
	}

	return newSettings
}

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