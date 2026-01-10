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
	_ "github.com/lib/pq"            // Postgres Driver (For Sessions)
	_ "github.com/go-sql-driver/mysql" // ✅ MySQL Driver (For History)
	"github.com/redis/go-redis/v9"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	waProto "go.mau.fi/whatsmeow/binary/proto"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// 📦 Chat Message Structure (MySQL Compatible)
type ChatMessage struct {
	ID           int64     `json:"id"`
	BotID        string    `json:"bot_id"`
	ChatID       string    `json:"chat_id"`
	Sender       string    `json:"sender"`
	SenderName   string    `json:"sender_name"`
	MessageID    string    `json:"message_id"`
	Timestamp    time.Time `json:"timestamp"`
	Type         string    `json:"type"` 
	Content      string    `json:"content"`
	IsFromMe     bool      `json:"is_from_me"`
	IsGroup      bool      `json:"is_group"`
	IsChannel    bool      `json:"is_channel"`
	QuotedMsg    string    `json:"quoted_msg"`
	QuotedSender string    `json:"quoted_sender"`
	IsSticker    bool      `json:"is_sticker"`
}

// 📦 Chat List Item
type ChatItem struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	LastMsg   string    `json:"last_msg"`
	Timestamp time.Time `json:"timestamp"`
	Unread    int       `json:"unread"`
}

// Global Variables
var (
	client           *whatsmeow.Client
	container        *sqlstore.Container
	rdb              *redis.Client
	historyDB        *sql.DB // 🟢 MySQL Database for History
	ctx              = context.Background()
	persistentUptime int64
	upgrader         = websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	wsClients        = make(map[*websocket.Conn]bool)
	clientsMutex     sync.RWMutex
	activeClients    = make(map[string]*whatsmeow.Client)
	botCleanIDCache  = make(map[string]string)
	
	// MongoDB variables preserved if needed elsewhere, though we switched to MySQL for history
	mongoClient           *mongo.Client
	chatHistoryCollection *mongo.Collection
)

// ---------------------------------------------------------
// 🟢 1. DATABASE & SERVER INITIALIZATION
// ---------------------------------------------------------

func initRedis() {
	redisURL := os.Getenv("REDIS_URL")
	if redisURL == "" {
		redisURL = "redis://localhost:6379"
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		log.Printf("⚠️ Redis URL parsing failed: %v", err)
		return
	}
	rdb = redis.NewClient(opt)
	fmt.Println("✅ Redis Connected")
}

func initHistoryDB() {
	// Get MySQL Connection String from Env
	// Example: root:password@tcp(host:port)/dbname
	dsn := os.Getenv("HISTORY_DB_URL") 
	if dsn == "" {
		log.Println("⚠️ HISTORY_DB_URL not set! Chat history will not be saved.")
		return
	}

	var err error
	historyDB, err = sql.Open("mysql", dsn)
	if err != nil {
		log.Fatalf("❌ MySQL connection failed: %v", err)
	}

	if err = historyDB.Ping(); err != nil {
		log.Fatalf("❌ MySQL Ping failed: %v", err)
	}

	// Create Messages Table if not exists
	query := `
	CREATE TABLE IF NOT EXISTS messages (
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
		is_channel BOOLEAN,
		quoted_msg TEXT,
		quoted_sender VARCHAR(100),
		is_sticker BOOLEAN,
		INDEX idx_chat (chat_id),
		INDEX idx_bot (bot_id)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
	`
	_, err = historyDB.Exec(query)
	if err != nil {
		log.Fatalf("❌ Failed to create MySQL tables: %v", err)
	}
	fmt.Println("✅ [MySQL] Chat History Database Ready!")
}

// ---------------------------------------------------------
// 🟡 2. MESSAGE SAVING LOGIC (MySQL + Latest Whatsmeow)
// ---------------------------------------------------------

func saveMessageToHistory(client *whatsmeow.Client, botID, chatID string, msg *waProto.Message, isFromMe bool, ts uint64) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠️ Recovered from save error: %v\n", r)
		}
	}()

	if historyDB == nil { return }

	var msgType, content, senderName, quotedMsg, quotedSender string
	var isSticker bool
	timestamp := time.Unix(int64(ts), 0)
	isGroup := strings.Contains(chatID, "@g.us")
	isChannel := strings.Contains(chatID, "@newsletter")
	jid, _ := types.ParseJID(chatID)

	// 1. Name Lookup (Safe for Latest Version)
	if isGroup {
		if info, err := client.GetGroupInfo(context.Background(), jid); err == nil {
			senderName = info.Name
		}
	}
	if senderName == "" {
		if contact, err := client.Store.Contacts.GetContact(context.Background(), jid); err == nil {
			if contact.Found {
				senderName = contact.FullName
				if senderName == "" { senderName = contact.PushName }
			} else {
				senderName = contact.PushName
			}
		}
	}
	if senderName == "" { senderName = strings.Split(chatID, "@")[0] }

	// 2. Content & Media
	if txt := getText(msg); txt != "" {
		msgType = "text"
		content = txt
	} else if msg.ImageMessage != nil {
		msgType = "image"
		data, err := client.Download(context.Background(), msg.ImageMessage)
		if err == nil {
			content = "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)
		}
	} else if msg.StickerMessage != nil {
		msgType = "image"
		isSticker = true
		data, err := client.Download(context.Background(), msg.StickerMessage)
		if err == nil {
			content = "data:image/webp;base64," + base64.StdEncoding.EncodeToString(data)
		}
	} else if msg.AudioMessage != nil {
		msgType = "audio"
		data, err := client.Download(context.Background(), msg.AudioMessage)
		if err == nil {
			if len(data) > 5*1024*1024 {
				url, _ := UploadToCatbox(data, "audio.ogg")
				content = url
			} else {
				content = "data:audio/ogg;base64," + base64.StdEncoding.EncodeToString(data)
			}
		}
	} else if msg.VideoMessage != nil {
		msgType = "video"
		data, err := client.Download(context.Background(), msg.VideoMessage)
		if err == nil {
			url, _ := UploadToCatbox(data, "video.mp4")
			content = url
		}
	} else if msg.DocumentMessage != nil {
		msgType = "file"
		data, err := client.Download(context.Background(), msg.DocumentMessage)
		if err == nil {
			fname := msg.DocumentMessage.GetFileName()
			if fname == "" { fname = "file.bin" }
			url, _ := UploadToCatbox(data, fname)
			content = url
		}
	} else {
		return // Unknown
	}

	if content == "" { return }

	// 3. Quoted Message Handling
	var ctxInfo *waProto.ContextInfo
	if msg.ExtendedTextMessage != nil { ctxInfo = msg.ExtendedTextMessage.ContextInfo }
	if msg.ImageMessage != nil { ctxInfo = msg.ImageMessage.ContextInfo }
	if msg.StickerMessage != nil { ctxInfo = msg.StickerMessage.ContextInfo }
	if msg.AudioMessage != nil { ctxInfo = msg.AudioMessage.ContextInfo }
	if msg.VideoMessage != nil { ctxInfo = msg.VideoMessage.ContextInfo }
	
	if ctxInfo != nil && ctxInfo.QuotedMessage != nil {
		if ctxInfo.Participant != nil { quotedSender = *ctxInfo.Participant }
		else if ctxInfo.StanzaID != nil { quotedSender = *ctxInfo.StanzaID }
		
		if ctxInfo.QuotedMessage.Conversation != nil {
			quotedMsg = *ctxInfo.QuotedMessage.Conversation
		} else if ctxInfo.QuotedMessage.ExtendedTextMessage != nil {
			quotedMsg = *ctxInfo.QuotedMessage.ExtendedTextMessage.Text
		} else {
			quotedMsg = "Media Message"
		}
	}

	// 4. Insert into MySQL
	query := `
		INSERT INTO messages(bot_id, chat_id, sender, sender_name, message_id, timestamp, msg_type, content, is_from_me, is_group, is_channel, quoted_msg, quoted_sender, is_sticker)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE content=VALUES(content)
	`
	stmt, err := historyDB.Prepare(query)
	if err != nil {
		fmt.Println("❌ MySQL Prepare Error:", err)
		return
	}
	defer stmt.Close()

	_, err = stmt.Exec(botID, chatID, chatID, senderName, msg.Key.GetId(), timestamp, msgType, content, isFromMe, isGroup, isChannel, quotedMsg, quotedSender, isSticker)
	if err != nil {
		fmt.Printf("⚠️ MySQL Insert Error: %v\n", err)
	}

	// 5. Realtime Notification
	broadcastWS(map[string]interface{}{
		"event": "new_message",
		"data": map[string]interface{}{
			"chat_id": chatID,
			"content": content,
			"type": msgType,
			"is_from_me": isFromMe,
		},
	})
}

// ---------------------------------------------------------
// 🔴 3. MAIN FUNCTION & HANDLERS
// ---------------------------------------------------------

func main() {
	fmt.Println("🚀 WHATSAPP CLONE BACKEND | STARTING...")

	initRedis()
	initHistoryDB()

	// Connect Postgres for Sessions (Railway Variable: DATABASE_URL)
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" { log.Fatal("DATABASE_URL missing") }
	rawDB, err := sql.Open("postgres", dbURL)
	if err != nil { log.Fatal(err) }
	
	dbLog := waLog.Stdout("Database", "ERROR", true)
	container = sqlstore.NewWithDB(rawDB, "postgres", dbLog)
	container.Upgrade(context.Background())

	// Start Bots
	StartAllBots(container)

	// --- Routes ---
	http.HandleFunc("/", serveHTML) // Keep Index.html for pairing
	http.HandleFunc("/pic.png", servePicture)
	http.HandleFunc("/ws", handleWebSocket)

	// --- NEW API ENDPOINTS (For Next.js Clone) ---
	http.HandleFunc("/api/sessions", handleGetSessions)
	http.HandleFunc("/api/chats", handleGetChatsList) // List all chats
	http.HandleFunc("/api/messages", handleGetChatMessages) // Get chat history
	http.HandleFunc("/api/send", handleSendMessage) // Send text
	http.HandleFunc("/api/send/media", handleSendMedia) // Send media (todo)
	
	// Groups & Profile
	http.HandleFunc("/api/group/create", handleCreateGroup)
	http.HandleFunc("/api/group/info", handleGetGroupInfo)
	http.HandleFunc("/api/profile/update", handleUpdateProfile)
	
	// Status
	http.HandleFunc("/api/status/send", handleSendStatus)
	http.HandleFunc("/api/status/list", handleGetStatuses)
	
	// Pairing & Management
	http.HandleFunc("/api/pair", handlePairAPI)
	http.HandleFunc("/link/delete", handleDeleteSession)
	http.HandleFunc("/del/all", handleDelAllAPI)

	port := os.Getenv("PORT")
	if port == "" { port = "8080" }
	fmt.Printf("🌐 API Server running on port %s\n", port)
	http.ListenAndServe(":"+port, nil)
}

// ---------------------------------------------------------
// 🔵 4. API HANDLERS IMPLEMENTATION
// ---------------------------------------------------------

// 1. Get All Chats
func handleGetChatsList(w http.ResponseWriter, r *http.Request) {
	botID := r.URL.Query().Get("bot_id")
	if botID == "" || historyDB == nil {
		http.Error(w, "Bot ID or DB missing", 400)
		return
	}

	// Query: Get latest message for each chat (Group by logic for MySQL)
	// Simplified query for performance: Get unique chats
	rows, err := historyDB.Query("SELECT DISTINCT chat_id, sender_name, is_group, is_channel FROM messages WHERE bot_id = ? ORDER BY timestamp DESC", botID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	var chats []ChatItem
	for rows.Next() {
		var c ChatItem
		var isGroup, isChannel bool
		rows.Scan(&c.ID, &c.Name, &isGroup, &isChannel)
		
		if isGroup { c.Type = "group" } else if isChannel { c.Type = "channel" } else { c.Type = "user" }
		chats = append(chats, c)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(chats)
}

// 2. Get Messages
func handleGetChatMessages(w http.ResponseWriter, r *http.Request) {
	botID := r.URL.Query().Get("bot_id")
	chatID := r.URL.Query().Get("chat_id")

	// Fixed MySQL Query order
	rows, err := historyDB.Query("SELECT id, bot_id, chat_id, sender, sender_name, message_id, timestamp, msg_type, content, is_from_me, is_group, is_channel, quoted_msg, quoted_sender, is_sticker FROM messages WHERE bot_id = ? AND chat_id = ? ORDER BY timestamp ASC", botID, chatID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	defer rows.Close()

	var msgs []ChatMessage
	for rows.Next() {
		var m ChatMessage
		var ts []uint8 // MySQL timestamp handling
		err := rows.Scan(&m.ID, &m.BotID, &m.ChatID, &m.Sender, &m.SenderName, &m.MessageID, &ts, &m.Type, &m.Content, &m.IsFromMe, &m.IsGroup, &m.IsChannel, &m.QuotedMsg, &m.QuotedSender, &m.IsSticker)
		if err == nil {
			// Parse timestamp if needed, usually string from driver
			msgs = append(msgs, m)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(msgs)
}

// 3. Send Message
func handleSendMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, "POST required", 405); return }

	var req struct {
		BotID   string `json:"bot_id"`
		ChatID  string `json:"chat_id"`
		Content string `json:"content"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	clientsMutex.RLock()
	client, ok := activeClients[req.BotID]
	clientsMutex.RUnlock()

	if !ok { http.Error(w, "Bot not connected", 404); return }

	jid, _ := types.ParseJID(req.ChatID)
	
	// Send Text
	resp, err := client.SendMessage(context.Background(), jid, &waProto.Message{
		Conversation: &req.Content,
	})
	
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{"status": "sent", "id": resp.ID})
}

// 4. Send Media (Stub)
func handleSendMedia(w http.ResponseWriter, r *http.Request) {
	w.Write([]byte(`{"status":"implemented_soon"}`))
}

// 5. Group & Profile Stubs
func handleCreateGroup(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"ok"}`)) }
func handleGetGroupInfo(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"ok"}`)) }
func handleUpdateProfile(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"ok"}`)) }
func handleSendStatus(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"ok"}`)) }
func handleGetStatuses(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"status":"ok"}`)) }

// ---------------------------------------------------------
// 🟣 5. CORE HELPER FUNCTIONS & BOILERPLATE
// ---------------------------------------------------------

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
	if err != nil { return "", err }
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return string(respBody), nil
}

func getText(msg *waProto.Message) string {
	if msg.Conversation != nil { return *msg.Conversation }
	if msg.ExtendedTextMessage != nil { return *msg.ExtendedTextMessage.Text }
	return ""
}

func getCleanID(id string) string {
	if idx := strings.Index(id, ":"); idx != -1 {
		return id[:idx]
	}
	return strings.Split(id, "@")[0]
}

// --- Handler (Event Listener) ---
func handler(botClient *whatsmeow.Client, evt interface{}) {
	defer func() {
		if r := recover(); r != nil { fmt.Printf("⚠️ Handler Panic: %v\n", r) }
	}()

	switch v := evt.(type) {
	case *events.Message:
		if v.Info.Chat.String() == "status@broadcast" { return }
		
		go func() {
			botID := getCleanID(botClient.Store.ID.User)
			saveMessageToHistory(botClient, botID, v.Info.Chat.String(), v.Message, v.Info.IsFromMe, uint64(v.Info.Timestamp.Unix()))
		}()
	
	case *events.HistorySync:
		go func() {
			botID := getCleanID(botClient.Store.ID.User)
			for _, conv := range v.Data.Conversations {
				chatID := conv.ID // String in latest version
				for _, histMsg := range conv.Messages {
					if histMsg.Message == nil || histMsg.Message.Message == nil { continue }
					
					ts := uint64(0)
					if histMsg.Message.MessageTimestamp != nil { ts = *histMsg.Message.MessageTimestamp }
					
					isFromMe := false
					if histMsg.Message.Key != nil && histMsg.Message.Key.FromMe != nil {
						isFromMe = *histMsg.Message.Key.FromMe
					}

					saveMessageToHistory(botClient, botID, chatID, histMsg.Message.Message, isFromMe, ts)
				}
			}
		}()

	case *events.Connected:
		if botClient.Store.ID != nil {
			fmt.Printf("🟢 [ONLINE] Bot %s connected!\n", botClient.Store.ID.User)
		}
	}
}

// --- Standard Functions ---
func serveHTML(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "web/index.html")
}
func servePicture(w http.ResponseWriter, r *http.Request) {
	http.ServeFile(w, r, "pic.png")
}
func handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, _ := upgrader.Upgrade(w, r, nil)
	defer conn.Close()
	wsClients[conn] = true
	defer delete(wsClients, conn)
	for {
		if _, _, err := conn.ReadMessage(); err != nil { break }
	}
}
func broadcastWS(data interface{}) {
	for conn := range wsClients {
		conn.WriteJSON(data)
	}
}
func handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	clientsMutex.Lock()
	for id, c := range activeClients { c.Disconnect(); delete(activeClients, id) }
	clientsMutex.Unlock()
	
	devices, _ := container.GetAllDevices(context.Background())
	for _, d := range devices { d.Delete(context.Background()) }
	
	w.Write([]byte(`{"status":"deleted"}`))
}
func handleDelAllAPI(w http.ResponseWriter, r *http.Request) {
	handleDeleteSession(w, r) // Reuse same logic
}

func handlePairAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" { http.Error(w, "Method not allowed", 405); return }
	var req struct { Number string `json:"number"` }
	json.NewDecoder(r.Body).Decode(&req)
	
	number := strings.ReplaceAll(req.Number, "+", "")
	cleanNum := getCleanID(number)
	
	// Delete existing
	devices, _ := container.GetAllDevices(context.Background())
	for _, d := range devices {
		if getCleanID(d.ID.User) == cleanNum { d.Delete(context.Background()) }
	}

	newDevice := container.NewDevice()
	tempClient := whatsmeow.NewClient(newDevice, waLog.Stdout("Pairing", "INFO", true))
	tempClient.AddEventHandler(func(evt interface{}) { handler(tempClient, evt) })
	
	if err := tempClient.Connect(); err != nil { http.Error(w, err.Error(), 500); return }
	
	code, err := tempClient.PairPhone(context.Background(), number, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil { http.Error(w, err.Error(), 500); return }

	go func() {
		for i := 0; i < 60; i++ {
			time.Sleep(1 * time.Second)
			if tempClient.Store.ID != nil {
				clientsMutex.Lock()
				activeClients[cleanNum] = tempClient
				clientsMutex.Unlock()
				return
			}
		}
	}()

	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"success":true,"code":"%s"}`, code)
}

func handleGetSessions(w http.ResponseWriter, r *http.Request) {
	clientsMutex.RLock()
	var sessions []string
	for id := range activeClients { sessions = append(sessions, id) }
	clientsMutex.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(sessions)
}

func StartAllBots(c *sqlstore.Container) {
	devices, _ := c.GetAllDevices(context.Background())
	for _, device := range devices {
		botNum := getCleanID(device.ID.User)
		go func(d *store.Device) {
			ConnectNewSession(d)
		}(device)
		fmt.Printf("🤖 Starting Bot: %s\n", botNum)
	}
}

func ConnectNewSession(device *store.Device) {
	cleanID := getCleanID(device.ID.User)
	newBotClient := whatsmeow.NewClient(device, waLog.Stdout("Client", "ERROR", true))
	newBotClient.AddEventHandler(func(evt interface{}) { handler(newBotClient, evt) })
	
	if err := newBotClient.Connect(); err == nil {
		clientsMutex.Lock()
		activeClients[cleanID] = newBotClient
		clientsMutex.Unlock()
	}
}

// Structs needed for compilation
type GroupSettings struct { ChatID string }
type YTSession struct{}
type YTState struct{}