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
	"google.golang.org/protobuf/proto" // ✅ proto.String کے لیے یہ import ضروری ہے
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

	// Connect if device ID exists
	if client.Store.ID != nil {
		err = client.Connect()
		if err != nil {
			log.Fatalf("Failed to connect: %v", err)
		}
		fmt.Println("✅ Session restored")
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
			return
		}

		text := strings.ToLower(strings.TrimSpace(getText(v.Message)))

		switch text {
		case "#menu":
			sendMenu(v.Info.Chat)
		case "#ping":
			sendPing(v.Info.Chat)
		}
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
		return *msg.ExtendedTextMessage.Text // ✅ pointer dereference کیا
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
						RowId:       proto.String("cmd_ping"),
						Title:       proto.String("⚡ Ping"),
						Description: proto.String("Bot کی رفتار چیک کریں"),
					},
					{
						RowId:       proto.String("cmd_info"),
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
		Conversation: proto.String(msg), // ✅ proto.String استعمال کیا
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

	number := strings.ReplaceAll(req.Number, "+", "")

	code, err := client.PairPhone(
		context.Background(),
		number,
		true,
		whatsmeow.PairClientChrome,
		"Chrome Linux",
	)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"code": code})
}