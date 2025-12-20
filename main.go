package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

const (
	BOT_TAG  = "IMPOSSIBLE_STABLE_V1"
	DEV_NAME = "Nothing Is Impossible"
)

var (
	client    *whatsmeow.Client
	container *sqlstore.Container
	startTime = time.Now()
)

func main() {
	fmt.Println("🚀 IMPOSSIBLE BOT | START")

	// ------------------- DB SETUP -------------------
	dbURL := os.Getenv("DATABASE_URL")
	dbType := "postgres"
	if dbURL == "" {
		dbType = "sqlite3"
		dbURL = "file:impossible.db?_foreign_keys=on"
	}

	var err error
	container, err = sqlstore.New(
		context.Background(),
		dbType,
		dbURL,
		waLog.Stdout("DB", "INFO", true),
	)
	if err != nil {
		log.Fatalf("DB error: %v", err)
	}

	// ------------------- DEVICE SETUP -------------------
	var device *store.Device
	devices, _ := container.GetAllDevices(context.Background())

	// Get the most recent device (last paired)
	if len(devices) > 0 {
		device = devices[len(devices)-1]
		fmt.Printf("📱 Found existing device: %s\n", device.PushName)
	}

	if device == nil {
		device = container.NewDevice()
		device.PushName = BOT_TAG
		fmt.Println("🆕 New session created")
	}

	client = whatsmeow.NewClient(device, waLog.Stdout("Client", "INFO", true))
	client.AddEventHandler(eventHandler)

	// Auto-connect if session exists
	if client.Store.ID != nil {
		fmt.Println("🔄 Restoring previous session...")
		err = client.Connect()
		if err != nil {
			log.Printf("⚠️ Connection error: %v", err)
			fmt.Println("💡 Use website to pair again")
		} else {
			fmt.Println("✅ Session restored and connected!")
		}
	} else {
		fmt.Println("⏳ No active session - Use website to pair")
	}

	// ------------------- WEB SERVER -------------------
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// Check if web folder exists
	if _, err := os.Stat("web"); !os.IsNotExist(err) {
		r.LoadHTMLGlob("web/*.html")
		r.Static("/static", "./web")
	}

	// Home page
	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", gin.H{
			"paired": client != nil && client.Store.ID != nil,
		})
	})

	// API to get pairing code
	r.POST("/api/pair", handlePairAPI)

	go r.Run(":8080")
	fmt.Println("🌐 Web server running on port 8080")

	// ------------------- GRACEFUL SHUTDOWN -------------------
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	<-sigChan
	
	fmt.Println("\n🛑 Shutting down...")
	if client != nil && client.IsConnected() {
		client.Disconnect()
	}
	fmt.Println("👋 Goodbye!")
}

// ================= EVENTS =================

func eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		if v.Info.IsFromMe {
			return
		}

		text := strings.ToLower(strings.TrimSpace(getText(v.Message)))

		fmt.Printf("📩 Msg: %s | From: %s\n", text, v.Info.Sender.User)

		// Handle text commands
		switch text {
		case "#menu", "menu":
			sendMenu(v.Info.Chat)
		case "#ping", "ping":
			sendPing(v.Info.Chat)
		case "#info", "info":
			sendInfo(v.Info.Chat)
		}

	case *events.Connected:
		fmt.Println("🟢 BOT CONNECTED")
	case *events.Disconnected:
		fmt.Println("🔴 BOT DISCONNECTED")
	}
}

func getText(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}
	if msg.Conversation != nil {
		return *msg.Conversation
	}
	if msg.ExtendedTextMessage != nil && msg.ExtendedTextMessage.Text != nil {
		return *msg.ExtendedTextMessage.Text
	}
	return ""
}

// ================= MENU =================

func sendMenu(chat types.JID) {
	menuText := `╔════════════════════╗
║  🚀 IMPOSSIBLE BOT
║  📋 MAIN MENU
╠════════════════════╣
║
║  ⚡ *#ping*
║     Check bot speed
║
║  ℹ️ *#info*
║     Bot information
║
║  📋 *#menu*
║     Show this menu
║
╠════════════════════╣
║  👨‍💻 Developer:
║  Nothing Is Impossible
╚════════════════════╝`

	client.SendMessage(context.Background(), chat, &waProto.Message{
		Conversation: proto.String(menuText),
	})
	fmt.Println("✅ Menu sent")
}

// ================= PING =================

func sendPing(chat types.JID) {
	start := time.Now()
	time.Sleep(20 * time.Millisecond)
	ms := time.Since(start).Milliseconds()
	uptime := time.Since(startTime).Round(time.Second)

	msg := fmt.Sprintf(
		"╔══════════════════╗\n"+
			"║ 🚀 IMPOSSIBLE BOT\n"+
			"╠══════════════════╣\n"+
			"║ 👨‍💻 %s\n"+
			"╠══════════════════╣\n"+
			"║ ⚡ PING: %d ms\n"+
			"╠══════════════════╣\n"+
			"║ ⏱ UPTIME: %s\n"+
			"╚══════════════════╝",
		DEV_NAME,
		ms,
		uptime,
	)

	client.SendMessage(context.Background(), chat, &waProto.Message{
		Conversation: proto.String(msg),
	})
	fmt.Println("✅ Ping sent")
}

// ================= INFO =================

func sendInfo(chat types.JID) {
	uptime := time.Since(startTime).Round(time.Second)

	msg := fmt.Sprintf(
		"╔══════════════════╗\n"+
			"║ 🤖 BOT INFO\n"+
			"╠══════════════════╣\n"+
			"║ 📛 IMPOSSIBLE BOT\n"+
			"║ 👨‍💻 %s\n"+
			"║ ⏱ UPTIME: %s\n"+
			"║ 🏷 VERSION: 1.0\n"+
			"╚══════════════════╝",
		DEV_NAME,
		uptime,
	)

	client.SendMessage(context.Background(), chat, &waProto.Message{
		Conversation: proto.String(msg),
	})
	fmt.Println("✅ Info sent")
}

// ================= PAIR API =================

func handlePairAPI(c *gin.Context) {
	var req struct {
		Number string `json:"number"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": "invalid request"})
		return
	}

	// Clean phone number
	number := strings.ReplaceAll(req.Number, "+", "")
	number = strings.ReplaceAll(number, " ", "")
	number = strings.ReplaceAll(number, "-", "")
	number = strings.TrimSpace(number)

	// Validate number
	if len(number) < 10 || len(number) > 15 {
		c.JSON(400, gin.H{"error": "Invalid phone number length"})
		return
	}

	fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("📱 NEW PAIRING REQUEST\n")
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
	fmt.Printf("📞 Number: %s\n", number)

	// Create NEW device for this pairing
	newDevice := container.NewDevice()
	newDevice.PushName = BOT_TAG

	// Create temporary client for pairing
	tempClient := whatsmeow.NewClient(newDevice, waLog.Stdout("Pairing", "INFO", true))

	fmt.Println("🔌 Connecting to WhatsApp...")
	err := tempClient.Connect()
	if err != nil {
		fmt.Printf("❌ Connection failed: %v\n", err)
		c.JSON(500, gin.H{"error": "Failed to connect: " + err.Error()})
		return
	}

	// CRITICAL: Wait for connection to stabilize
	fmt.Println("⏳ Waiting for stable connection...")
	time.Sleep(3 * time.Second)

	// Check if still connected
	if !tempClient.IsConnected() {
		fmt.Println("❌ Connection lost before pairing")
		c.JSON(500, gin.H{"error": "Connection unstable"})
		return
	}

	fmt.Printf("📱 Generating pairing code for %s...\n", number)
	code, err := tempClient.PairPhone(
		context.Background(),
		number,
		true,
		whatsmeow.PairClientChrome,
		"Chrome (Linux)",
	)

	if err != nil {
		fmt.Printf("❌ Pairing failed: %v\n", err)
		tempClient.Disconnect()
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	fmt.Printf("✅ Code generated: %s\n", code)
	fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

	// Keep temp client alive and watch for successful pairing
	go func() {
		fmt.Println("⏳ Waiting for user to enter pairing code...")
		
		// Check every second for 60 seconds
		for i := 0; i < 60; i++ {
			time.Sleep(1 * time.Second)
			
			// Check if pairing completed
			if tempClient.Store.ID != nil {
				fmt.Println("✅ Pairing successful!")
				fmt.Printf("📱 Device ID: %s\n", tempClient.Store.ID)
				
				// Disconnect old client if exists
				if client != nil && client.IsConnected() {
					fmt.Println("🔄 Disconnecting old session...")
					client.Disconnect()
					time.Sleep(1 * time.Second)
				}

				// Replace with new client
				client = tempClient
				client.AddEventHandler(eventHandler)
				
				fmt.Println("🎉 New session is now active!")
				return
			}
		}
		
		// Timeout after 60 seconds
		fmt.Println("❌ Pairing timed out (user didn't enter code)")
		tempClient.Disconnect()
	}()

	c.JSON(200, gin.H{"code": code})
}