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
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

var client *whatsmeow.Client
var container *sqlstore.Container

const BOT_TAG = "IMPOSSIBLE_STABLE_V1"
const DEVELOPER = "Nothing Is Impossible"

func main() {
	fmt.Println("🚀 [System] Impossible Bot: Starting Final Stable Version...")

	dbURL := os.Getenv("DATABASE_URL")
	dbType := "postgres"
	if dbURL == "" { dbType = "sqlite3"; dbURL = "file:impossible.db?_foreign_keys=on" }

	var err error
	container, err = sqlstore.New(context.Background(), dbType, dbURL, waLog.Stdout("Database", "INFO", true))
	if err != nil { panic(err) }

	// سیشن آئسولیشن
	var targetDevice *store.Device
	devices, _ := container.GetAllDevices(context.Background())
	for _, dev := range devices {
		if dev.PushName == BOT_TAG {
			targetDevice = dev
			break
		}
	}

	if targetDevice == nil {
		targetDevice = container.NewDevice()
		targetDevice.PushName = BOT_TAG
	}

	client = whatsmeow.NewClient(targetDevice, waLog.Stdout("Client", "INFO", true))
	client.AddEventHandler(eventHandler)

	if client.Store.ID != nil { client.Connect() }

	port := os.Getenv("PORT")
	if port == "" { port = "8080" }
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	r.StaticFile("/", "./web/index.html")
	r.POST("/api/pair", handlePairAPI)

	go r.Run(":" + port)

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c
	client.Disconnect()
}

func getBody(msg *waProto.Message) string {
	if msg == nil { return "" }
	if msg.Conversation != nil { return msg.GetConversation() }
	if msg.ExtendedTextMessage != nil { return msg.ExtendedTextMessage.GetText() }
	return ""
}

func eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		if v.Info.IsFromMe { return }
		body := strings.TrimSpace(strings.ToLower(getBody(v.Message)))
		
		fmt.Printf("📩 [MSG] From: %s | Text: %s\n", v.Info.Sender.User, body)

		if body == "#menu" {
			_, _ = client.SendMessage(context.Background(), v.Info.Chat, client.BuildReaction(v.Info.Chat, v.Info.Sender, v.Info.ID, "📜"))
			sendOfficialListMenu(v.Info.Chat)
		}

		if body == "#ping" {
			start := time.Now()
			_, _ = client.SendMessage(context.Background(), v.Info.Chat, client.BuildReaction(v.Info.Chat, v.Info.Sender, v.Info.ID, "⚡"))
			latency := time.Since(start)
			res := fmt.Sprintf("🚀 *IMPOSSIBLE PING*\n\nLatency: `%s`\nDev: _%s_", latency.String(), DEVELOPER)
			client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{Conversation: proto.String(res)})
		}
	}
}

func sendOfficialListMenu(chat types.JID) {
	fmt.Println("📤 [Action] Sending Protobuf-Compatible List Menu...")

	// فکسڈ: RowID (بڑے حروف میں) اور لسٹ میسج کا صحیح اسٹرکچر
	listMsg := &waProto.ListMessage{
		Title:       proto.String("IMPOSSIBLE MENU"),
		Description: proto.String("نیچے دیے گئے بٹن پر کلک کر کے آپشنز دیکھیں 👇"),
		ButtonText:  proto.String("Open Menu"),
		ListType:    waProto.ListMessage_SINGLE_SELECT.Enum(),
		Sections: []*waProto.ListMessage_Section{
			{
				Title: proto.String("BOT FEATURES"),
				Rows: []*waProto.ListMessage_Row{
					{
						RowID:       proto.String("ping_row"), // فکسڈ: RowID
						Title:       proto.String("Check Speed"),
						Description: proto.String("Get current server latency"),
					},
					{
						RowID:       proto.String("id_row"),
						Title:       proto.String("User Info"),
						Description: proto.String("Get your JID details"),
					},
				},
			},
		},
	}

	// فکسڈ: SendMessage اب دو ویلیوز ریٹرن کرتا ہے
	_, err := client.SendMessage(context.Background(), chat, &waProto.Message{
		ListMessage: listMsg,
	})

	if err != nil {
		fmt.Printf("❌ [Error] List failed: %v. Sending Text Fallback.\n", err)
		fallback := "*📜 IMPOSSIBLE MENU*\n\n• #ping (Speed)\n• #id (JID Info)\n\n_Account Restricted_"
		client.SendMessage(context.Background(), chat, &waProto.Message{Conversation: proto.String(fallback)})
	}
}

func handlePairAPI(c *gin.Context) {
	var req struct{ Number string `json:"number"` }
	c.BindJSON(&req)
	num := strings.ReplaceAll(req.Number, "+", "")

	devices, _ := container.GetAllDevices(context.Background())
	for _, dev := range devices {
		if dev.PushName == BOT_TAG {
			container.DeleteDevice(context.Background(), dev)
		}
	}

	newStore := container.NewDevice()
	newStore.PushName = BOT_TAG 

	if client.IsConnected() { client.Disconnect() }
	client = whatsmeow.NewClient(newStore, waLog.Stdout("Client", "INFO", true))
	client.AddEventHandler(eventHandler)
	client.Connect()
	
	time.Sleep(10 * time.Second)
	code, err := client.PairPhone(context.Background(), num, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"code": code})
}