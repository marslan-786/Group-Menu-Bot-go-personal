package main

import (
	"compress/flate"
	"compress/gzip"
	"context"
	"fmt" // اب یہ لاگنگ کے لیے استعمال ہو رہا ہے
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/gin-gonic/gin"
	_ "github.com/lib/pq"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

var client *whatsmeow.Client

// ریلوے پورٹ اٹھانا
func getPort() string {
	port := os.Getenv("PORT")
	if port == "" {
		return "8080"
	}
	return port
}

// ڈیٹا فیچنگ فیچر
func handleFetch(c *gin.Context) {
	dataType := c.Query("type")
	var targetURL string
	var referer string

	if dataType == "numbers" {
		targetURL = "http://217.182.195.194/ints/agent/res/data_smsnumbers.php?frange=&fclient=&sEcho=2&iColumns=8&sColumns=%2C%2C%2C%2C%2C%2C%2C&iDisplayStart=0&iDisplayLength=-1&mDataProp_0=0&sSearch_0=&bRegex_0=false&bSearchable_0=true&bSortable_0=false&mDataProp_1=1&sSearch_1=&bRegex_1=false&bSearchable_1=true&bSortable_1=true&mDataProp_2=2&sSearch_2=&bRegex_2=false&bSearchable_2=true&bSortable_2=true&mDataProp_3=3&sSearch_3=&bRegex_3=false&bSearchable_3=true&bSortable_3=true&mDataProp_4=4&sSearch_4=&bRegex_4=false&bSearchable_4=true&bSortable_4=true&mDataProp_5=5&sSearch_5=&bRegex_5=false&bSearchable_5=true&bSortable_5=true&mDataProp_6=6&sSearch_6=&bRegex_6=false&bSearchable_6=true&bSortable_6=true&mDataProp_7=7&sSearch_7=&bRegex_7=false&bSearchable_7=true&bSortable_7=false&sSearch=&bRegex=false&iSortCol_0=0&sSortDir_0=asc&iSortingCols=1&_=1766171476582"
		referer = "http://217.182.195.194/ints/agent/MySMSNumbers"
	} else if dataType == "sms" {
		targetURL = "http://217.182.195.194/ints/agent/res/data_smscdr.php?fdate1=2025-12-19%2000:00:00&fdate2=2085-12-19%2023:59:59&frange=&fclient=&fnum=&fcli=&fgdate=&fgmonth=&fgrange=&fgclient=&fgnumber=&fgcli=&fg=0&csstr=9df7a3e50debcd51cca20329b34c1bfe&sEcho=2&iColumns=9&sColumns=%2C%2C%2C%2C%2C%2C%2C%2C&iDisplayStart=0&iDisplayLength=-1&mDataProp_0=0&sSearch_0=&bRegex_0=false&bSearchable_0=true&bSortable_0=true&mDataProp_1=1&sSearch_1=&bRegex_1=false&bSearchable_1=true&bSortable_1=true&mDataProp_2=2&sSearch_2=&bRegex_2=false&bSearchable_2=true&bSortable_2=true&mDataProp_3=3&sSearch_3=&bRegex_3=false&bSearchable_3=true&bSortable_3=true&mDataProp_4=4&sSearch_4=&bRegex_4=false&bSearchable_4=true&bSortable_4=true&mDataProp_5=5&sSearch_5=&bRegex_5=false&bSearchable_5=true&bSortable_5=true&mDataProp_6=6&sSearch_6=&bRegex_6=false&bSearchable_6=true&bSortable_6=true&mDataProp_7=7&sSearch_7=&bRegex_7=false&bSearchable_7=true&bSortable_7=true&mDataProp_8=8&sSearch_8=&bRegex_8=false&bSearchable_8=true&bSortable_8=false&sSearch=&bRegex=false&iSortCol_0=0&sSortDir_0=desc&iSortingCols=1&_=1766171360378"
		referer = "http://217.182.195.194/ints/agent/SMSCDRStats"
	} else {
		c.JSON(400, gin.H{"error": "Invalid type"})
		return
	}

	req, _ := http.NewRequest("GET", targetURL, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 13; V2040 Build/TP1A.220624.014) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.7499.34 Mobile Safari/537.36")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	req.Header.Set("Referer", referer)
	req.Header.Set("Cookie", "PHPSESSID=pb3620rtcrklvvrmndf8kmt93n")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.JSON(500, gin.H{"error": "Fetch failed"})
		return
	}
	defer resp.Body.Close()

	var reader io.ReadCloser
	encoding := resp.Header.Get("Content-Encoding")
	if strings.Contains(encoding, "gzip") {
		reader, _ = gzip.NewReader(resp.Body)
	} else if strings.Contains(encoding, "deflate") {
		reader = flate.NewReader(resp.Body)
	} else {
		reader = resp.Body
	}
	defer reader.Close()

	body, _ := io.ReadAll(reader)
	c.Data(200, "application/json; charset=utf-8", body)
}

func main() {
	fmt.Println("🚀 Initializing Impossible Bot...") // fmt کا استعمال

	dbLog := waLog.Stdout("Database", "INFO", true)
	container, err := sqlstore.New(context.Background(), "postgres", os.Getenv("DATABASE_URL"), dbLog)
	if err != nil {
		fmt.Printf("❌ Database error: %v\n", err)
		panic(err)
	}

	deviceStore, err := container.GetFirstDevice(context.Background())
	if err != nil {
		panic(err)
	}

	client = whatsmeow.NewClient(deviceStore, waLog.Stdout("Client", "INFO", true))
	client.AddEventHandler(eventHandler)

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	r.StaticFile("/", "./web/index.html")
	r.StaticFile("/pic.png", "./web/pic.png")
	r.GET("/api/fetch", handleFetch)

	r.POST("/api/pair", func(c *gin.Context) {
		var req struct{ Number string `json:"number"` }
		if err := c.BindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "Invalid input"})
			return
		}
		client.Connect()
		code, err := client.PairPhone(context.Background(), req.Number, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{"code": code})
	})

	go func() {
		port := getPort()
		fmt.Printf("🌐 Web Panel active on port %s\n", port)
		r.Run(":" + port)
	}()

	if client.Store.ID != nil {
		client.Connect()
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	client.Disconnect()
}

func eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		body := v.Message.GetConversation()
		if body == "" {
			body = v.Message.GetExtendedTextMessage().GetText()
		}
		if body == "#menu" {
			sendOfficialMenu(v.Info.Chat)
		}
	}
}

func sendOfficialMenu(chat types.JID) {
	listMsg := &waProto.ListMessage{
		Title:       proto.String("IMPOSSIBLE MENU"),
		Description: proto.String("Select category from the list"),
		ButtonText:  proto.String("MENU"),
		ListType:    waProto.ListMessage_SINGLE_SELECT.Enum(),
		Sections: []*waProto.ListMessage_Section{
			{
				Title: proto.String("TOOLS"),
				Rows: []*waProto.ListMessage_Row{
					{Title: proto.String("Ping"), RowID: proto.String("ping")},
					{Title: proto.String("ID"), RowID: proto.String("id")},
				},
			},
		},
	}
	client.SendMessage(context.Background(), chat, &waProto.Message{ListMessage: listMsg})
}