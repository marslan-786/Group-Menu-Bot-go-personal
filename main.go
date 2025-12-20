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

const BOT_TAG = "IMPOSSIBLE_MENU_V2"
const DEVELOPER = "Nothing Is Impossible"

func main() {
	fmt.Printf("🚀 [%s] Starting Ultimate Go Engine...\n", BOT_TAG)

	dbURL := os.Getenv("DATABASE_URL")
	dbType := "postgres"
	if dbURL == "" { dbType = "sqlite3"; dbURL = "file:impossible.db?_foreign_keys=on" }

	var err error
	container, err = sqlstore.New(context.Background(), dbType, dbURL, waLog.Stdout("Database", "INFO", true))
	if err != nil { panic(err) }

	// سیشن آئسولیشن لاجک
	var targetDevice *store.Device
	devices, _ := container.GetAllDevices(context.Background())
	for _, dev := range devices {
		if dev.PushName == BOT_TAG {
			targetDevice = dev
			break
		}
	}

	if targetDevice == nil {
		fmt.Println("ℹ️ [Auth] No session found. Waiting for pairing...")
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

		// ہیش مینیو کمانڈ
		if body == "#menu" {
			_, _ = client.SendMessage(context.Background(), v.Info.Chat, client.BuildReaction(v.Info.Chat, v.Info.Sender, v.Info.ID, "📜"))
			sendInteractiveMenu(v.Info.Chat)
		}

		// ہیش پنگ کمانڈ
		if body == "#ping" {
			start := time.Now()
			_, _ = client.SendMessage(context.Background(), v.Info.Chat, client.BuildReaction(v.Info.Chat, v.Info.Sender, v.Info.ID, "⚡"))
			latency := time.Since(start)
			
			res := fmt.Sprintf("🚀 *IMPOSSIBLE SPEED*\n\nLatency: `%s`\nDeveloper: _%s_", latency.String(), DEVELOPER)
			client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{Conversation: proto.String(res)})
		}
	}
}

// چیٹ جی پی ٹی کے مشورے کے مطابق انٹیریکٹو مینیو بٹن
func sendInteractiveMenu(chat types.JID) {
	fmt.Println("📤 [Action] Sending Interactive OpenMenu Button...")

	// یہ وہ اسٹرکچر ہے جو واٹس ایپ کے نئے ورژن میں "Open Menu" دکھاتا ہے
	interactiveMsg := &waProto.InteractiveMessage{
		Header: &waProto.InteractiveMessage_Header{
			Title: proto.String("IMPOSSIBLE MENU"),
		},
		Body: &waProto.InteractiveMessage_Body{
			Text: proto.String("نیچے دیے گئے بٹن پر کلک کر کے آپشنز دیکھیں 👇"),
		},
		Footer: &waProto.InteractiveMessage_Footer{
			Text: proto.String(DEVELOPER),
		},
		Action: &waProto.InteractiveMessage_Action{
			Button: proto.String("Click to Open Menu"),
			Sections: []*waProto.InteractiveMessage_Section{
				{
					Title: proto.String("MAIN TOOLS"),
					Rows: []*waProto.InteractiveMessage_Row{
						{
							Id:          proto.String("ping_id"),
							Title:       proto.String("Check Ping"),
							Description: proto.String("Get bot response time"),
						},
						{
							Id:          proto.String("id_info"),
							Title:       proto.String("My ID"),
							Description: proto.String("Get your JID details"),
						},
					},
				},
			},
		},
	}

	// میسج سینڈ کرنا
	_, err := client.SendMessage(context.Background(), chat, &waProto.Message{
		InteractiveMessage: interactiveMsg,
	})

	if err != nil {
		fmt.Printf("❌ Interactive Error: %v. Using ListMessage Fallback.\n", err)
		// اگر انٹرایکٹو فیل ہو جائے تو پرانا لسٹ میسج ٹرائی کریں
		fallbackList(chat)
	}
}

func fallbackList(chat types.JID) {
	listMsg := &waProto.ListMessage{
		Title:       proto.String("IMPOSSIBLE MENU"),
		Description: proto.String("Please select an option:"),
		ButtonText:  proto.String("OPEN MENU"),
		ListType:    waProto.ListMessage_SINGLE_SELECT.Enum(),
		Sections: []*waProto.ListMessage_Section{
			{
				Title: proto.String("FEATURES"),
				Rows: []*waProto.ListMessage_Row{
					{Title: proto.String("Ping"), RowId: proto.String("ping")},
				},
			},
		},
	}
	client.SendMessage(context.Background(), chat, &waProto.Message{ListMessage: listMsg})
}

func handlePairAPI(c *gin.Context) {
	var req struct{ Number string `json:"number"` }
	c.BindJSON(&req)
	num := strings.ReplaceAll(req.Number, "+", "")

	// صرف اپنا متعلقہ سیشن صاف کریں
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