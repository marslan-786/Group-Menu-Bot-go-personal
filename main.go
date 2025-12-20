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
	for _, d := range devices {
		if d.PushName == BOT_TAG {
			device = d
			break
		}
	}
	if device == nil {
		device = container.NewDevice()
		device.PushName = BOT_TAG
		fmt.Println("🆕 New session created")
	}

	client = whatsmeow.NewClient(device, waLog.Stdout("Client", "INFO", true))
	client.AddEventHandler(eventHandler)

	// Only connect if already paired
	if client.Store.ID != nil {
		err = client.Connect()
		if err != nil {
			log.Printf("⚠️ Connection error: %v", err)
		} else {
			fmt.Println("✅ Session restored and connected")
		}
	} else {
		fmt.Println("⏳ Waiting for pairing via website...")
	}

	// ------------------- WEB SERVER -------------------
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	r.LoadHTMLGlob("web/*.html")

	// Home page
	r.GET("/", func(c *gin.Context) {
		c.HTML(http.StatusOK, "index.html", gin.H{
			"paired": client.Store.ID != nil,
		})
	})

	// API to get pairing code
	r.POST("/api/pair", handlePairAPI)

	go r.Run(":8080")
	fmt.Println("🌐 Web server running at http://localhost:8080")

	// ------------------- GRACEFUL SHUTDOWN -------------------
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	client.Disconnect()
}

// ================= EVENTS =================

func eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		if v.Info.IsFromMe {
			fmt.Println("⏭️ Skipping own message")
			return
		}

		text := strings.ToLower(strings.TrimSpace(getText(v.Message)))
		
		// Log incoming message for debugging
		fmt.Printf("\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Printf("📩 NEW MESSAGE\n")
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n")
		fmt.Printf("👤 From: %s\n", v.Info.Sender.User)
		fmt.Printf("💬 Text: '%s'\n", text)
		fmt.Printf("📱 Chat: %s\n", v.Info.Chat.String())
		
		// Check message type
		if v.Message.GetListResponseMessage() != nil {
			fmt.Println("📋 Message Type: LIST RESPONSE")
		} else if v.Message.GetConversation() != "" {
			fmt.Println("💭 Message Type: TEXT")
		} else if v.Message.GetExtendedTextMessage() != nil {
			fmt.Println("📝 Message Type: EXTENDED TEXT")
		} else {
			fmt.Println("❓ Message Type: UNKNOWN")
		}
		fmt.Printf("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n")

		// Handle list response
		if v.Message.GetListResponseMessage() != nil {
			fmt.Println("🔄 Processing list response...")
			handleListResponse(v)
			return
		}

		// Handle text commands
		fmt.Printf("🔍 Checking command: '%s'\n", text)
		switch text {
		case "#menu", "menu", "/menu":
			fmt.Println("✅ Command matched: MENU")
			fmt.Println("📋 Sending menu...")
			sendMenu(v.Info.Chat)
		case "#ping", "ping", "/ping":
			fmt.Println("✅ Command matched: PING")
			fmt.Println("⚡ Sending ping...")
			sendPing(v.Info.Chat)
		case "#info", "info", "/info":
			fmt.Println("✅ Command matched: INFO")
			fmt.Println("ℹ️ Sending info...")
			sendInfo(v.Info.Chat)
		default:
			// Log unhandled messages
			if text != "" {
				fmt.Printf("❌ No command matched for: '%s'\n", text)
			} else {
				fmt.Println("⚠️ Empty text - might be media/other message type")
			}
		}
	case *events.Connected:
		fmt.Println("\n🟢 ════════════════════════════════")
		fmt.Println("   BOT CONNECTED TO WHATSAPP")
		fmt.Println("   ════════════════════════════════\n")
	case *events.Disconnected:
		fmt.Println("\n🔴 ════════════════════════════════")
		fmt.Println("   BOT DISCONNECTED FROM WHATSAPP")
		fmt.Println("   ════════════════════════════════\n")
	}
}

func handleListResponse(v *events.Message) {
	listResponse := v.Message.GetListResponseMessage()
	if listResponse == nil {
		fmt.Println("⚠️ List response is nil")
		return
	}

	selectedID := listResponse.GetSingleSelectReply().GetSelectedRowID()
	fmt.Printf("📌 List response received - Selected ID: %s\n", selectedID)

	switch selectedID {
	case "cmd_ping":
		fmt.Println("✅ User selected: Ping")
		sendPing(v.Info.Chat)
	case "cmd_info":
		fmt.Println("✅ User selected: Info")
		sendInfo(v.Info.Chat)
	default:
		fmt.Printf("❓ Unknown list option: %s\n", selectedID)
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
	menu := &waProto.ListMessage{
		Title:       proto.String("🚀 IMPOSSIBLE MENU"),
		Description: proto.String("براہ کرم کوئی آپشن منتخب کریں"),
		ButtonText:  proto.String("📋 مینو کھولیں"),
		ListType:    waProto.ListMessage_SINGLE_SELECT.Enum(),
		Sections: []*waProto.ListMessage_Section{
			{
				Title: proto.String("⚡ COMMANDS"),
				Rows: []*waProto.ListMessage_Row{
					{
						RowID:       proto.String("cmd_ping"),
						Title:       proto.String("⚡ Ping"),
						Description: proto.String("Bot کی رفتار چیک کریں"),
					},
					{
						RowID:       proto.String("cmd_info"),
						Title:       proto.String("ℹ️ Info"),
						Description: proto.String("Bot کی معلومات"),
					},
				},
			},
		},
	}

	client.SendMessage(context.Background(), chat, &waProto.Message{
		ListMessage: menu,
	})
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

	// Clean phone number - remove all non-digits
	number := strings.ReplaceAll(req.Number, "+", "")
	number = strings.ReplaceAll(number, " ", "")
	number = strings.ReplaceAll(number, "-", "")
	number = strings.TrimSpace(number)

	// Validate number
	if len(number) < 10 || len(number) > 15 {
		c.JSON(400, gin.H{"error": "Invalid phone number length"})
		return
	}

	fmt.Printf("📱 Processing number: %s\n", number)

	// Disconnect if already connected to start fresh
	if client.IsConnected() {
		client.Disconnect()
		time.Sleep(1 * time.Second)
	}

	// Connect to WhatsApp
	fmt.Println("🔌 Connecting to WhatsApp...")
	err := client.Connect()
	if err != nil {
		fmt.Printf("❌ Connection error: %v\n", err)
		c.JSON(500, gin.H{"error": "Failed to connect: " + err.Error()})
		return
	}

	// Wait for connection to stabilize
	time.Sleep(5 * time.Second)

	fmt.Printf("📱 Generating pairing code for: %s\n", number)
	code, err := client.PairPhone(
		context.Background(),
		number,
		true,
		whatsmeow.PairClientChrome,
		"Chrome (Linux)",
	)
	if err != nil {
		fmt.Printf("❌ Pairing error: %v\n", err)
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	fmt.Printf("✅ Pairing code generated: %s\n", code)
	c.JSON(200, gin.H{"code": code})
}