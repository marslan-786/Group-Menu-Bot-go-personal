package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

var client *whatsmeow.Client
var container *sqlstore.Container

func main() {
	fmt.Println("🚀 [Impossible Bot] Booting with Advanced Session Logic...")

	dbURL := os.Getenv("DATABASE_URL")
	dbType := "postgres"
	if dbURL == "" {
		dbType = "sqlite3"
		dbURL = "file:impossible.db?_foreign_keys=on"
	}

	dbLog := waLog.Stdout("Database", "INFO", true)
	var err error
	container, err = sqlstore.New(context.Background(), dbType, dbURL, dbLog)
	if err != nil {
		fmt.Printf("❌ Database Init Error: %v\n", err)
		panic(err)
	}

	// ڈیٹا بیس سے تمام ڈیوائسز چیک کریں اور اپنا پہلا سیشن لوڈ کریں
	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil { panic(err) }

	client = whatsmeow.NewClient(deviceStore, waLog.Stdout("Client", "INFO", true))
	client.AddEventHandler(eventHandler)

	if client.Store.ID != nil {
		fmt.Printf("✅ [Auth] Logged in as %s. Connecting...\n", client.Store.ID.User)
		client.Connect()
	} else {
		fmt.Println("ℹ️ [Auth] Waiting for web pairing...")
	}

	port := os.Getenv("PORT")
	if port == "" { port = "8080" }
	
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	r.StaticFile("/", "./web/index.html")
	r.StaticFile("/pic.png", "./web/pic.png")
	r.POST("/api/pair", handlePairAPI)

	go func() {
		fmt.Printf("🌐 [Web] Dashboard at port %s\n", port)
		r.Run(":" + port)
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	client.Disconnect()
}

func getBody(msg *waProto.Message) string {
	if msg == nil { return "" }
	if msg.Conversation != nil { return msg.GetConversation() }
	if msg.ExtendedTextMessage != nil { return msg.ExtendedTextMessage.GetText() }
	if msg.ImageMessage != nil { return msg.ImageMessage.GetCaption() }
	return ""
}

func eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		if v.Info.IsFromMe { return }
		body := strings.TrimSpace(getBody(v.Message))
		
		fmt.Printf("📩 [Log] Message from %s: %s\n", v.Info.Sender.User, body)

		if strings.ToLower(body) == "#menu" {
			// ری ایکشن
			_, _ = client.SendMessage(context.Background(), v.Info.Chat, client.BuildReaction(v.Info.Chat, v.Info.Sender, v.Info.ID, "📜"))
			sendMenuWithImageAndButtons(v.Info.Chat)
		}
	}
}

func sendMenuWithImageAndButtons(chat types.JID) {
	imgData, err := os.ReadFile("./web/pic.png")
	if err != nil {
		fmt.Println("❌ pic.png missing in web folder")
		return
	}

	// 1. تصویر اپلوڈ
	uploadResp, err := client.Upload(context.Background(), imgData, whatsmeow.MediaImage)
	if err != nil {
		fmt.Printf("❌ Upload fail: %v\n", err)
		return
	}

	// 2. مینیو بٹن سٹرکچر
	listMsg := &waProto.ListMessage{
		Title:       proto.String("IMPOSSIBLE MENU"),
		Description: proto.String("Advanced Go System\nSelect a command:"),
		ButtonText:  proto.String("MENU"),
		ListType:    waProto.ListMessage_SINGLE_SELECT.Enum(),
		Sections: []*waProto.ListMessage_Section{
			{
				Title: proto.String("TOOLS"),
				Rows: []*waProto.ListMessage_Row{
					{Title: proto.String("Bot Speed"), RowID: proto.String("ping"), Description: proto.String("Check Ping")},
					{Title: proto.String("User Info"), RowID: proto.String("id")},
				},
			},
		},
	}

	// 3. تصویر کے ساتھ مینیو بھیجنا
	imageMsg := &waProto.ImageMessage{
		Mimetype:      proto.String("image/png"),
		Caption:       proto.String("*📜 IMPOSSIBLE MENU*\n\nWelcome! Use the MENU button below."),
		URL:           &uploadResp.URL,
		DirectPath:    &uploadResp.DirectPath,
		MediaKey:      uploadResp.MediaKey,
		FileEncSHA256: uploadResp.FileEncSHA256,
		FileSHA256:    uploadResp.FileSHA256,
		FileLength:    proto.Uint64(uint64(len(imgData))),
	}

	msg := &waProto.Message{
		ImageMessage: imageMsg,
		ListMessage:  listMsg,
	}

	fmt.Println("📤 Sending Full Menu Package...")
	_, err = client.SendMessage(context.Background(), chat, msg)
	if err != nil {
		fmt.Printf("❌ Menu Delivery Error: %v\n", err)
	}
}

func handlePairAPI(c *gin.Context) {
	var req struct{ Number string `json:"number"` }
	c.BindJSON(&req)
	num := strings.ReplaceAll(req.Number, "+", "")

	fmt.Printf("🧹 [Cleanup] Wiping specific session for %s\n", num)
	
	// صرف اسی نمبر کا سیشن ڈیلیٹ کریں
	devices, _ := container.GetAllDevices(context.Background())
	for _, dev := range devices {
		if dev.ID != nil && strings.Contains(dev.ID.User, num) {
			container.DeleteDevice(context.Background(), dev)
			fmt.Println("🗑️ Found and deleted old session.")
		}
	}

	newDevice := container.NewDevice()
	if client.IsConnected() { client.Disconnect() }
	
	client = whatsmeow.NewClient(newDevice, waLog.Stdout("Client", "INFO", true))
	client.AddEventHandler(eventHandler)
	client.Connect()
	
	time.Sleep(10 * time.Second)
	code, err := client.PairPhone(context.Background(), num, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	
	if err != nil {
		fmt.Printf("❌ Pair Error: %v\n", err)
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	
	fmt.Printf("✅ Success! Pairing Code: %s\n", code)
	c.JSON(200, gin.H{"code": code})
}