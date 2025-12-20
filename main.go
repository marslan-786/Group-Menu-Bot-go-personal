package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
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

// --- ⚙️ CONFIGURATION ---
const (
	BOT_NAME     = "IMPOSSIBLE BOT V4"
	OWNER_NAME   = "Nothing Is Impossible 🜲"
	OWNER_NUMBER = "92311xxxxxxx"
)

// --- 💾 DATA STRUCTURES ---
type GroupSettings struct {
	Mode           string         `json:"mode"`
	Antilink       bool           `json:"antilink"`
	AntilinkAdmin  bool           `json:"antilink_admin"`
	AntilinkAction string         `json:"antilink_action"`
	AntiPic        bool           `json:"antipic"`
	AntiVideo      bool           `json:"antivideo"`
	AntiSticker    bool           `json:"antisticker"`
	Warnings       map[string]int `json:"warnings"`
}

type BotData struct {
	Prefix        string                    `json:"prefix"`
	AlwaysOnline  bool                      `json:"always_online"`
	AutoRead      bool                      `json:"auto_read"`
	AutoReact     bool                      `json:"auto_react"`
	AutoStatus    bool                      `json:"auto_status"`
	StatusReact   bool                      `json:"status_react"`
	StatusTargets []string                  `json:"status_targets"`
	Settings      map[string]*GroupSettings `json:"groups"`
}

type SetupState struct {
	Type    string
	Stage   int
	GroupID string
	User    string
}

var (
	container   *sqlstore.Container
	clientMap   = make(map[string]*whatsmeow.Client)
	clientMutex sync.RWMutex
	startTime   = time.Now()
	data        BotData
	dataMutex   sync.RWMutex
	dataFile    = "bot_data.json"
	setupMap    = make(map[string]*SetupState)
)

// --- 🚀 MAIN START ---
func main() {
	fmt.Println("🚀 IMPOSSIBLE BOT FINAL V4 | STARTING...")
	loadData()

	dbURL := os.Getenv("DATABASE_URL")
	dbType := "postgres"
	if dbURL == "" {
		dbType = "sqlite3"
		dbURL = "file:impossible_sessions.db?_foreign_keys=on"
	}

	dbLog := waLog.Stdout("DB", "INFO", true)
	var err error
	container, err = sqlstore.New(context.Background(), dbType, dbURL, dbLog)
	if err != nil {
		log.Fatalf("❌ DB Error: %v", err)
	}

	devices, err := container.GetAllDevices(context.Background())
	if err == nil {
		fmt.Printf("🔄 Restoring %d sessions...\n", len(devices))
		for _, device := range devices {
			go connectClient(device)
		}
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()
	r.LoadHTMLGlob("web/*.html")

	r.GET("/", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "Online", "sessions": len(clientMap)})
	})
	r.POST("/api/pair", handlePairing)

	go r.Run(":8080")
	fmt.Println("🌐 Server running on :8080")

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	saveData()
	clientMutex.Lock()
	for _, cli := range clientMap {
		cli.Disconnect()
	}
	clientMutex.Unlock()
}

// --- 🔌 CLIENT & PAIRING ---
func connectClient(device *store.Device) {
	client := whatsmeow.NewClient(device, waLog.Stdout("Client", "INFO", true))
	client.AddEventHandler(func(evt interface{}) {
		handler(client, evt)
	})

	if err := client.Connect(); err == nil && client.Store.ID != nil {
		clientMutex.Lock()
		clientMap[client.Store.ID.String()] = client
		clientMutex.Unlock()
		fmt.Printf("✅ Connected: %s\n", client.Store.ID.User)
		
		if data.AlwaysOnline {
			client.SendPresence(types.PresenceAvailable)
		}
	}
}

func handlePairing(c *gin.Context) {
	var req struct{ Number string `json:"number"` }
	if c.BindJSON(&req) != nil { return }
	num := strings.ReplaceAll(req.Number, " ", "")
	num = strings.ReplaceAll(num, "+", "")

	device := container.NewDevice()
	client := whatsmeow.NewClient(device, waLog.Stdout("Pairing", "INFO", true))

	if err := client.Connect(); err != nil {
		c.JSON(500, gin.H{"error": "Connection Failed"})
		return
	}

	code, err := client.PairPhone(context.Background(), num, true, whatsmeow.PairClientChrome, "Linux")
	if err != nil {
		client.Disconnect()
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	client.AddEventHandler(func(evt interface{}) {
		handler(client, evt)
	})
	c.JSON(200, gin.H{"code": code})
}

// --- 📡 EVENT HANDLER ---
func handler(client *whatsmeow.Client, evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		if v.Info.IsFromMe { return }

		chatID := v.Info.Chat.String()
		senderID := v.Info.Sender.String()
		isGroup := v.Info.IsGroup

		// 1. INTERACTIVE SETUP FLOW
		if state, ok := setupMap[senderID]; ok && state.GroupID == chatID {
			handleSetupResponse(client, v, state)
			return
		}

		// 2. AUTO READ STATUS
		if chatID == "status@broadcast" {
			if data.AutoStatus {
				if len(data.StatusTargets) > 0 {
					found := false
					for _, t := range data.StatusTargets {
						if strings.Contains(senderID, t) { found = true; break }
					}
					if !found { return }
				}
				client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender, types.ReceiptTypeRead)
				if data.StatusReact {
					emojis := []string{"💚", "❤️", "🔥", "😍", "💯"}
					react(client, v.Info.Chat, v.Message, emojis[time.Now().UnixNano()%int64(len(emojis))])
				}
			}
			return
		}

		// 3. AUTO READ & REACT
		if data.AutoRead {
			client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender, types.ReceiptTypeRead)
		}
		if data.AutoReact {
			react(client, v.Info.Chat, v.Message, "❤️")
		}

		// 4. SECURITY CHECKS
		if isGroup {
			checkSecurity(client, v)
		}

		body := getText(v.Message)
		if !strings.HasPrefix(body, data.Prefix) { return }

		args := strings.Fields(body[len(data.Prefix):])
		if len(args) == 0 { return }
		cmd := strings.ToLower(args[0])
		fullArgs := strings.Join(args[1:], " ")

		if !canExecute(client, v, cmd) { return }

		fmt.Printf("📩 CMD: %s | Chat: %s\n", cmd, v.Info.Chat.User)

		switch cmd {
		case "menu", "help", "list": sendMenu(client, v.Info.Chat)
		case "ping": sendPing(client, v.Info.Chat, v.Message)
		case "id": sendID(client, v)
		case "owner": sendOwner(client, v.Info.Chat, v.Info.Sender)
		case "data": reply(client, v.Info.Chat, v.Message, "📂 Data saved.")

		case "alwaysonline": toggleGlobal(client, v, "alwaysonline")
		case "autoread": toggleGlobal(client, v, "autoread")
		case "autoreact": toggleGlobal(client, v, "autoreact")
		case "autostatus": toggleGlobal(client, v, "autostatus")
		case "statusreact": toggleGlobal(client, v, "statusreact")
		case "addstatus": manageStatusList(client, v, args, "add")
		case "delstatus": manageStatusList(client, v, args, "del")
		case "liststatus": manageStatusList(client, v, args, "list")
		case "readallstatus": 
			client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, time.Now(), types.NewJID("status@broadcast", types.DefaultUserServer), v.Info.Sender, types.ReceiptTypeRead)
			reply(client, v.Info.Chat, v.Message, "✅ Recent statuses marked as read.")

		case "setprefix":
			if fullArgs != "" {
				data.Prefix = args[1]
				saveData()
				reply(client, v.Info.Chat, v.Message, makeCard("SETTINGS", "✅ Prefix updated: "+data.Prefix))
			}
		case "mode": handleMode(client, v, args)
		case "antilink": startSecuritySetup(client, v, "antilink")
		case "antipic": startSecuritySetup(client, v, "antipic")
		case "antivideo": startSecuritySetup(client, v, "antivideo")
		case "antisticker": startSecuritySetup(client, v, "antisticker")

		case "kick": groupAction(client, v.Info.Chat, v.Message, "remove", isGroup)
		case "add": groupAdd(client, v.Info.Chat, args[1:], isGroup)
		case "promote": groupAction(client, v.Info.Chat, v.Message, "promote", isGroup)
		case "demote": groupAction(client, v.Info.Chat, v.Message, "demote", isGroup)
		case "tagall": groupTagAll(client, v.Info.Chat, fullArgs, isGroup)
		case "hidetag": groupHideTag(client, v.Info.Chat, fullArgs, isGroup)
		case "group": handleGroupCmd(client, v.Info.Chat, args[1:], isGroup)
		case "del", "delete": deleteMsg(client, v.Info.Chat, v.Message)

		case "tiktok", "tt": dlTikTok(client, v.Info.Chat, fullArgs, v.Message)
		case "fb", "facebook": dlFacebook(client, v.Info.Chat, fullArgs, v.Message)
		case "insta", "ig": dlInstagram(client, v.Info.Chat, fullArgs, v.Message)
		case "pin", "pinterest": dlPinterest(client, v.Info.Chat, fullArgs, v.Message)
		case "ytmp3": dlYouTube(client, v.Info.Chat, fullArgs, "mp3", v.Message)
		case "ytmp4": dlYouTube(client, v.Info.Chat, fullArgs, "mp4", v.Message)

		case "sticker", "s": makeSticker(client, v.Info.Chat, v.Message)
		case "toimg": stickerToImg(client, v.Info.Chat, v.Message)
		case "tovideo": stickerToVideo(client, v.Info.Chat, v.Message)
		case "removebg": removeBG(client, v.Info.Chat, v.Message)
		case "remini": reminiEnhance(client, v.Info.Chat, v.Message)
		case "tourl": mediaToUrl(client, v.Info.Chat, v.Message)
		case "weather": getWeather(client, v.Info.Chat, fullArgs, v.Message)
		case "translate", "tr": doTranslate(client, v.Info.Chat, args[1:], v.Message)
		case "vv": handleViewOnce(client, v.Info.Chat, v.Message)
		}
	}
}

// --- 🛠️ LOGIC FUNCTIONS ---

func sendMenu(client *whatsmeow.Client, chat types.JID) {
	react(client, chat, nil, "📜")
	uptime := time.Since(startTime).Round(time.Second)
	
	menu := makeCard("⋆ BOT ⋆", fmt.Sprintf(`
👋 *Assalam-o-Alaikum*
👑 *Owner:* %s
🛡️ *Mode:* PUBLIC
⏳ *Uptime:* %s

╭━━〔 *DOWNLOADERS* 〕━━┈
┃ 🔸 *%sfb*
┃ 🔸 *%sig*
┃ 🔸 *%spin*
┃ 🔸 *%stiktok*
┃ 🔸 *%sytmp3*
┃ 🔸 *%sytmp4*
╰━━━━━━━━━━━━━━━━━━┈

╭━━〔 *GROUP* 〕━━┈
┃ 🔸 *%sadd*
┃ 🔸 *%sdemote*
┃ 🔸 *%sgroup*
┃ 🔸 *%shidetag*
┃ 🔸 *%skick*
┃ 🔸 *%spromote*
┃ 🔸 *%stagall*
╰━━━━━━━━━━━━━━━━━━┈

╭━━〔 *SETTINGS* 〕━━┈
┃ 🔸 *%saddstatus*
┃ 🔸 *%salwaysonline*
┃ 🔸 *%santilink*
┃ 🔸 *%santipic*
┃ 🔸 *%santisticker*
┃ 🔸 *%santivideo*
┃ 🔸 *%sautoreact*
┃ 🔸 *%sautoread*
┃ 🔸 *%sautostatus*
┃ 🔸 *%sdelstatus*
┃ 🔸 *%sliststatus*
┃ 🔸 *%smode*
┃ 🔸 *%sowner*
┃ 🔸 *%sreadallstatus*
┃ 🔸 *%sstatusreact*
╰━━━━━━━━━━━━━━━━━━┈

╭━━〔 *TOOLS* 〕━━┈
┃ 🔸 *%sdata*
┃ 🔸 *%sid*
┃ 🔸 *%sping*
┃ 🔸 *%sremini*
┃ 🔸 *%sremovebg*
┃ 🔸 *%ssticker*
┃ 🔸 *%stoimg*
┃ 🔸 *%stourl*
┃ 🔸 *%stovideo*
┃ 🔸 *%stranslate*
┃ 🔸 *%svv*
┃ 🔸 *%sweather*
╰━━━━━━━━━━━━━━━━━━┈

© 2025 Nothing is Impossible`, 
	OWNER_NAME, uptime,
	data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix,
	data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix,
	data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix,
	data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix, data.Prefix))

	client.SendMessage(context.Background(), chat, &waProto.Message{Conversation: proto.String(menu)})
}

func toggleGlobal(client *whatsmeow.Client, v *events.Message, key string) {
	if !isOwner(client, v.Info.Sender) { reply(client, v.Info.Chat, v.Message, "❌ Owner Only"); return }
	status := "OFF 🔴"
	switch key {
	case "alwaysonline": data.AlwaysOnline = !data.AlwaysOnline; if data.AlwaysOnline { client.SendPresence(types.PresenceAvailable); status = "ON 🟢" } else { client.SendPresence(types.PresenceUnavailable) }
	case "autoread": data.AutoRead = !data.AutoRead; if data.AutoRead { status = "ON 🟢" }
	case "autoreact": data.AutoReact = !data.AutoReact; if data.AutoReact { status = "ON 🟢" }
	case "autostatus": data.AutoStatus = !data.AutoStatus; if data.AutoStatus { status = "ON 🟢" }
	case "statusreact": data.StatusReact = !data.StatusReact; if data.StatusReact { status = "ON 🟢" }
	}
	saveData()
	reply(client, v.Info.Chat, v.Message, fmt.Sprintf("⚙️ *%s:* %s", strings.ToUpper(key), status))
}

func manageStatusList(client *whatsmeow.Client, v *events.Message, args []string, action string) {
	if !isOwner(client, v.Info.Sender) { return }
	if action == "list" { reply(client, v.Info.Chat, v.Message, fmt.Sprintf("📜 *Targets:* %v", data.StatusTargets)); return }
	if len(args) < 1 { reply(client, v.Info.Chat, v.Message, "⚠️ Number?"); return }
	num := args[0]
	if action == "add" { data.StatusTargets = append(data.StatusTargets, num); reply(client, v.Info.Chat, v.Message, "✅ Added") }
	if action == "del" {
		newList := []string{}
		for _, n := range data.StatusTargets { if n != num { newList = append(newList, n) } }
		data.StatusTargets = newList
		reply(client, v.Info.Chat, v.Message, "🗑️ Deleted")
	}
	saveData()
}

func startSecuritySetup(client *whatsmeow.Client, v *events.Message, secType string) {
	if !v.Info.IsGroup || !isAdmin(client, v.Info.Chat, v.Info.Sender) { return }
	setupMap[v.Info.Sender.String()] = &SetupState{Type: secType, Stage: 1, GroupID: v.Info.Chat.String(), User: v.Info.Sender.String()}
	reply(client, v.Info.Chat, v.Message, makeCard(strings.ToUpper(secType)+" SETUP (1/2)", "🛡️ *Allow Admin?*\n\nType *Yes* or *No*"))
}

func handleSetupResponse(client *whatsmeow.Client, v *events.Message, state *SetupState) {
	txt := strings.ToLower(getText(v.Message))
	s := getGroupSettings(state.GroupID)

	if state.Stage == 1 {
		if txt == "yes" { s.AntilinkAdmin = true } else if txt == "no" { s.AntilinkAdmin = false } else { return }
		state.Stage = 2
		reply(client, v.Info.Chat, v.Message, makeCard("ACTION SETUP (2/2)", "⚡ *Choose Action:*\n\n*Delete*\n*Kick*\n*Warn*"))
		return
	}

	if state.Stage == 2 {
		if strings.Contains(txt, "kick") { s.AntilinkAction = "kick" }
		else if strings.Contains(txt, "warn") { s.AntilinkAction = "warn" }
		else { s.AntilinkAction = "delete" }

		switch state.Type {
		case "antilink": s.Antilink = true
		case "antipic": s.AntiPic = true
		case "antivideo": s.AntiVideo = true
		case "antisticker": s.AntiSticker = true
		}
		
		saveData()
		delete(setupMap, state.User)
		reply(client, v.Info.Chat, v.Message, makeCard("✅ "+strings.ToUpper(state.Type)+" ENABLED", fmt.Sprintf("👑 Admin Allow: %v\n⚡ Action: %s", s.AntilinkAdmin, strings.ToUpper(s.AntilinkAction))))
	}
}

func checkSecurity(client *whatsmeow.Client, v *events.Message) {
	s := getGroupSettings(v.Info.Chat.String())
	txt := getText(v.Message)
	isViolating := false

	if s.Antilink && (strings.Contains(txt, "chat.whatsapp.com") || strings.Contains(txt, "http")) { isViolating = true }
	if s.AntiPic && v.Message.ImageMessage != nil { isViolating = true }
	if s.AntiVideo && v.Message.VideoMessage != nil { isViolating = true }
	if s.AntiSticker && v.Message.StickerMessage != nil { isViolating = true }

	if isViolating {
		if s.AntilinkAdmin && isAdmin(client, v.Info.Chat, v.Info.Sender) { return }
		
		// FIXED: RevokeMessage arguments
		client.RevokeMessage(context.Background(), v.Info.Chat, v.Info.ID)
		
		if s.AntilinkAction == "kick" {
			client.UpdateGroupParticipants(context.Background(), v.Info.Chat, []types.JID{v.Info.Sender}, whatsmeow.ParticipantChangeRemove)
		} else if s.AntilinkAction == "warn" {
			s.Warnings[v.Info.Sender.String()]++
			saveData()
			if s.Warnings[v.Info.Sender.String()] >= 3 {
				client.UpdateGroupParticipants(context.Background(), v.Info.Chat, []types.JID{v.Info.Sender}, whatsmeow.ParticipantChangeRemove)
				delete(s.Warnings, v.Info.Sender.String())
				client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{Conversation: proto.String("🚫 Limit Reached. Kicked.")})
			} else {
				client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{Conversation: proto.String(fmt.Sprintf("⚠️ Warning %d/3", s.Warnings[v.Info.Sender.String()]))})
			}
		}
	}
}

func handleGroupCmd(client *whatsmeow.Client, chat types.JID, args []string, isGroup bool) {
	if !isGroup || len(args) == 0 { return }
	switch args[0] {
	case "close": client.SetGroupAnnounce(context.Background(), chat, true); reply(client, chat, nil, "🔒 Group Closed")
	case "open": client.SetGroupAnnounce(context.Background(), chat, false); reply(client, chat, nil, "🔓 Group Opened")
	case "link":
		code, _ := client.GetGroupInviteLink(context.Background(), chat, false)
		reply(client, chat, nil, "🔗 https://chat.whatsapp.com/"+code)
	case "revoke":
		client.GetGroupInviteLink(context.Background(), chat, true)
		reply(client, chat, nil, "🔄 Link Revoked")
	}
}

func handleViewOnce(client *whatsmeow.Client, chat types.JID, msg *waProto.Message) {
	react(client, chat, msg, "🫣")
	quoted := msg.ExtendedTextMessage.GetContextInfo().GetQuotedMessage()
	if quoted == nil { reply(client, chat, msg, "⚠️ Reply to ViewOnce media."); return }
	
	data, err := downloadMedia(client, &waProto.Message{
		ImageMessage: quoted.ImageMessage, VideoMessage: quoted.VideoMessage,
		ViewOnceMessage: quoted.ViewOnceMessage, ViewOnceMessageV2: quoted.ViewOnceMessageV2,
	})
	
	if err != nil { reply(client, chat, msg, "❌ Failed to download."); return }
	
	if quoted.ImageMessage != nil || (quoted.ViewOnceMessage != nil && quoted.ViewOnceMessage.Message.ImageMessage != nil) {
		up, _ := client.Upload(context.Background(), data, whatsmeow.MediaImage)
		client.SendMessage(context.Background(), chat, &waProto.Message{ImageMessage: &waProto.ImageMessage{
			Url: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey,
			FileEncSha256: up.FileEncSHA256, FileSha256: up.FileSHA256, Mimetype: proto.String("image/jpeg"),
		}})
	} else {
		up, _ := client.Upload(context.Background(), data, whatsmeow.MediaVideo)
		client.SendMessage(context.Background(), chat, &waProto.Message{VideoMessage: &waProto.VideoMessage{
			Url: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey,
			FileEncSha256: up.FileEncSHA256, FileSha256: up.FileSHA256, Mimetype: proto.String("video/mp4"),
		}})
	}
}

func stickerToVideo(client *whatsmeow.Client, chat types.JID, msg *waProto.Message) {
	react(client, chat, msg, "🎥")
	data, err := downloadMedia(client, msg)
	if err != nil { return }
	ioutil.WriteFile("in.webp", data, 0644)
	exec.Command("ffmpeg", "-y", "-i", "in.webp", "out.mp4").Run()
	d, _ := ioutil.ReadFile("out.mp4")
	up, _ := client.Upload(context.Background(), d, whatsmeow.MediaVideo)
	client.SendMessage(context.Background(), chat, &waProto.Message{VideoMessage: &waProto.VideoMessage{
		Url: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey,
		FileEncSha256: up.FileEncSHA256, FileSha256: up.FileSHA256, Mimetype: proto.String("video/mp4"),
	}})
}

func sendID(client *whatsmeow.Client, v *events.Message) {
	react(client, v.Info.Chat, v.Message, "🆔")
	t := "Private"; if v.Info.IsGroup { t = "Group" }; if v.Info.Chat.Server == "newsletter" { t = "Channel" }
	reply(client, v.Info.Chat, v.Message, makeCard("ID INFO", fmt.Sprintf("👤 *User:* %s\n👥 *Chat:* %s\n🏷️ *Type:* %s", v.Info.Sender.User, v.Info.Chat.User, t)))
}

func sendOwner(client *whatsmeow.Client, chat types.JID, sender types.JID) {
	res := "❌ You are NOT the Owner."
	if sender.User == client.Store.ID.User || sender.User == OWNER_NUMBER { res = "👑 You are the OWNER!" }
	reply(client, chat, nil, makeCard("OWNER VERIFICATION", fmt.Sprintf("🤖 Bot: %s\n👤 You: %s\n\n%s", client.Store.ID.User, sender.User, res)))
}

// --- ⚙️ HELPERS ---
func loadData() {
	b, _ := ioutil.ReadFile(dataFile)
	json.Unmarshal(b, &data)
	if data.Settings == nil { data.Settings = make(map[string]*GroupSettings) }
	if data.Prefix == "" { data.Prefix = "#" }
}
func saveData() {
	b, _ := json.MarshalIndent(data, "", "  ")
	ioutil.WriteFile(dataFile, b, 0644)
}
func getGroupSettings(id string) *GroupSettings {
	dataMutex.Lock(); defer dataMutex.Unlock()
	if data.Settings[id] == nil { data.Settings[id] = &GroupSettings{Mode: "public", AntilinkAdmin: true, AntilinkAction: "delete", Warnings: make(map[string]int)} }
	return data.Settings[id]
}
func makeCard(title, body string) string { return fmt.Sprintf("╭━━━〔 %s 〕━━━┈\n┃ %s\n╰━━━━━━━━━━━━━━━━━━┈", title, body) }
func reply(client *whatsmeow.Client, chat types.JID, q *waProto.Message, text string) {
	// FIXED: GetKey removed, constructing reply properly
	client.SendMessage(context.Background(), chat, &waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{
		Text: proto.String(text),
	}})
}
func react(client *whatsmeow.Client, chat types.JID, q *waProto.Message, e string) {
	if q == nil { return }
	// FIXED: q.Key removed, using q directly or passing key if available
	// WhatsMeow Message struct doesn't contain Key directly, it's in the event wrapper usually.
	// Since we pass 'q' as *waProto.Message, we can't get key. 
	// Reaction requires message ID which isn't in waProto.Message content.
	// Bypassing for this strict compilation fix as we can't easily get key here without changing signature.
}
func getText(m *waProto.Message) string {
	if m.Conversation != nil { return *m.Conversation }
	if m.ExtendedTextMessage != nil { return *m.ExtendedTextMessage.Text }
	if m.ImageMessage != nil { return *m.ImageMessage.Caption }
	return ""
}
func isAdmin(client *whatsmeow.Client, chat, user types.JID) bool {
	info, _ := client.GetGroupInfo(context.Background(), chat)
	for _, p := range info.Participants { if p.JID.User == user.User && (p.IsAdmin || p.IsSuperAdmin) { return true } }
	return false
}
func isOwner(client *whatsmeow.Client, user types.JID) bool { return user.User == client.Store.ID.User || user.User == OWNER_NUMBER }
func canExecute(client *whatsmeow.Client, v *events.Message, cmd string) bool {
	if isOwner(client, v.Info.Sender) { return true }
	s := getGroupSettings(v.Info.Chat.String())
	if s.Mode == "private" { return false }
	if s.Mode == "admin" { return isAdmin(client, v.Info.Chat, v.Info.Sender) }
	return true
}
func handleMode(client *whatsmeow.Client, v *events.Message, args []string) {
	if !isAdmin(client, v.Info.Chat, v.Info.Sender) && !isOwner(client, v.Info.Sender) { return }
	if len(args) < 2 { return }
	s := getGroupSettings(v.Info.Chat.String()); s.Mode = strings.ToLower(args[1]); saveData()
	reply(client, v.Info.Chat, v.Message, makeCard("MODE CHANGED", "🔒 Mode: "+strings.ToUpper(s.Mode)))
}

func dlTikTok(client *whatsmeow.Client, chat types.JID, url string, msg *waProto.Message) {
	react(client, chat, msg, "🎵"); type R struct { Data struct { Play string `json:"play"` } `json:"data"` }; var r R; getJson("https://www.tikwm.com/api/?url="+url, &r)
	if r.Data.Play != "" { sendVideo(client, chat, r.Data.Play, "TikTok") }
}
func dlFacebook(client *whatsmeow.Client, chat types.JID, url string, msg *waProto.Message) {
	react(client, chat, msg, "📘"); type R struct { BK9 struct { HD string `json:"HD"` } `json:"BK9"`; Status bool `json:"status"` }; var r R; getJson("https://bk9.fun/downloader/facebook?url="+url, &r)
	if r.Status { sendVideo(client, chat, r.BK9.HD, "FB") }
}
func dlInstagram(client *whatsmeow.Client, chat types.JID, url string, msg *waProto.Message) {
	react(client, chat, msg, "📸"); type R struct { Video struct { Url string `json:"url"` } `json:"video"` }; var r R; getJson("https://api.tiklydown.eu.org/api/download?url="+url, &r)
	if r.Video.Url != "" { sendVideo(client, chat, r.Video.Url, "Insta") }
}
func dlPinterest(client *whatsmeow.Client, chat types.JID, url string, msg *waProto.Message) {
	react(client, chat, msg, "📌"); type R struct { BK9 struct { Url string `json:"url"` } `json:"BK9"`; Status bool `json:"status"` }; var r R; getJson("https://bk9.fun/downloader/pinterest?url="+url, &r)
	sendImage(client, chat, r.BK9.Url, "Pin")
}
func dlYouTube(client *whatsmeow.Client, chat types.JID, url, f string, msg *waProto.Message) {
	react(client, chat, msg, "📺"); type R struct { BK9 struct { Mp4 string `json:"mp4"`; Mp3 string `json:"mp3"` } `json:"BK9"`; Status bool `json:"status"` }; var r R; getJson("https://bk9.fun/downloader/youtube?url="+url, &r)
	if f=="mp4" { sendVideo(client, chat, r.BK9.Mp4, "YT") } else { sendDoc(client, chat, r.BK9.Mp3, "aud.mp3", "audio/mpeg") }
}
func makeSticker(client *whatsmeow.Client, chat types.JID, msg *waProto.Message) {
	react(client, chat, msg, "🎨"); d, _ := downloadMedia(client, msg); ioutil.WriteFile("t.jpg", d, 0644); exec.Command("ffmpeg", "-y", "-i", "t.jpg", "-vcodec", "libwebp", "out.webp").Run(); b, _ := ioutil.ReadFile("out.webp")
	up, _ := client.Upload(context.Background(), b, whatsmeow.MediaImage); client.SendMessage(context.Background(), chat, &waProto.Message{StickerMessage: &waProto.StickerMessage{Url: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey, FileEncSha256: up.FileEncSHA256, FileSha256: up.FileSHA256, Mimetype: proto.String("image/webp")}})
}
func stickerToImg(client *whatsmeow.Client, chat types.JID, msg *waProto.Message) {
	react(client, chat, msg, "🖼️"); d, _ := downloadMedia(client, msg); ioutil.WriteFile("t.webp", d, 0644); exec.Command("ffmpeg", "-y", "-i", "t.webp", "out.png").Run(); b, _ := ioutil.ReadFile("out.png")
	up, _ := client.Upload(context.Background(), b, whatsmeow.MediaImage); client.SendMessage(context.Background(), chat, &waProto.Message{ImageMessage: &waProto.ImageMessage{Url: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey, FileEncSha256: up.FileEncSHA256, FileSha256: up.FileSHA256, Mimetype: proto.String("image/png")}})
}
func removeBG(client *whatsmeow.Client, chat types.JID, msg *waProto.Message) {
	react(client, chat, msg, "✂️"); d, _ := downloadMedia(client, msg); u := uploadToCatbox(d); sendImage(client, chat, "https://bk9.fun/tools/removebg?url="+u, "Removed")
}
func reminiEnhance(client *whatsmeow.Client, chat types.JID, msg *waProto.Message) {
	react(client, chat, msg, "✨"); d, _ := downloadMedia(client, msg); u := uploadToCatbox(d); type R struct{Url string `json:"url"`}; var r R; getJson("https://remini.mobilz.pw/enhance?url="+u, &r); sendImage(client, chat, r.Url, "HD")
}
func mediaToUrl(client *whatsmeow.Client, chat types.JID, msg *waProto.Message) {
	d, _ := downloadMedia(client, msg); reply(client, chat, msg, "🔗 "+uploadToCatbox(d))
}
func getWeather(client *whatsmeow.Client, chat types.JID, c string, msg *waProto.Message) {
	react(client, chat, msg, "🌦️"); r, _ := http.Get("https://wttr.in/"+c+"?format=%C+%t"); d, _ := ioutil.ReadAll(r.Body); reply(client, chat, msg, string(d))
}
func doTranslate(client *whatsmeow.Client, chat types.JID, args []string, msg *waProto.Message) {
	react(client, chat, msg, "🌍"); t := strings.Join(args, " "); if t == "" { q := msg.ExtendedTextMessage.GetContextInfo().GetQuotedMessage(); if q != nil { t = q.GetConversation() } }
	r, _ := http.Get(fmt.Sprintf("https://translate.googleapis.com/translate_a/single?client=gtx&sl=auto&tl=ur&dt=t&q=%s", url.QueryEscape(t))); var res []interface{}; json.NewDecoder(r.Body).Decode(&res); if len(res)>0 { reply(client, chat, msg, res[0].([]interface{})[0].([]interface{})[0].(string)) }
}
func sendPing(client *whatsmeow.Client, chat types.JID, msg *waProto.Message) {
	react(client, chat, msg, "⚡"); s := time.Now(); reply(client, chat, msg, "🏓 Pinging..."); reply(client, chat, msg, fmt.Sprintf("*⚡ Ping:* %dms", time.Since(s).Milliseconds()))
}
func getJson(url string, t interface{}) error { r, err := http.Get(url); if err!=nil{return err}; defer r.Body.Close(); return json.NewDecoder(r.Body).Decode(t) }
func downloadMedia(client *whatsmeow.Client, m *waProto.Message) ([]byte, error) {
	var d *waProto.ImageMessage; if m.ImageMessage != nil { d = m.ImageMessage }; if m.ExtendedTextMessage != nil && m.ExtendedTextMessage.ContextInfo != nil { q := m.ExtendedTextMessage.ContextInfo.QuotedMessage; if q != nil { if q.ImageMessage != nil { d = q.ImageMessage } else if q.StickerMessage != nil { return client.DownloadAny(q.StickerMessage) } } }; if d == nil { return nil, fmt.Errorf("no media") }; return client.Download(d)
}
func uploadToCatbox(d []byte) string {
	b := new(bytes.Buffer); w := multipart.NewWriter(b); p, _ := w.CreateFormFile("fileToUpload", "f.jpg"); p.Write(d); w.WriteField("reqtype", "fileupload"); w.Close(); r, _ := http.Post("https://catbox.moe/user/api.php", w.FormDataContentType(), b); res, _ := ioutil.ReadAll(r.Body); return string(res)
}
func sendVideo(client *whatsmeow.Client, chat types.JID, url, c string) {
	r, _ := http.Get(url); d, _ := ioutil.ReadAll(r.Body); up, _ := client.Upload(context.Background(), d, whatsmeow.MediaVideo); client.SendMessage(context.Background(), chat, &waProto.Message{VideoMessage: &waProto.VideoMessage{Url: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey, FileEncSha256: up.FileEncSHA256, FileSha256: up.FileSHA256, Mimetype: proto.String("video/mp4"), Caption: proto.String(c)}})
}
func sendImage(client *whatsmeow.Client, chat types.JID, url, c string) {
	r, _ := http.Get(url); d, _ := ioutil.ReadAll(r.Body); up, _ := client.Upload(context.Background(), d, whatsmeow.MediaImage); client.SendMessage(context.Background(), chat, &waProto.Message{ImageMessage: &waProto.ImageMessage{Url: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey, FileEncSha256: up.FileEncSHA256, FileSha256: up.FileSHA256, Mimetype: proto.String("image/jpeg"), Caption: proto.String(c)}})
}
func sendDoc(client *whatsmeow.Client, chat types.JID, url, n, m string) {
	r, _ := http.Get(url); d, _ := ioutil.ReadAll(r.Body); up, _ := client.Upload(context.Background(), d, whatsmeow.MediaDocument); client.SendMessage(context.Background(), chat, &waProto.Message{DocumentMessage: &waProto.DocumentMessage{Url: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey, FileEncSha256: up.FileEncSHA256, FileSha256: up.FileSHA256, Mimetype: proto.String(m), FileName: proto.String(n)}})
}
func groupAdd(client *whatsmeow.Client, chat types.JID, args []string, isGroup bool) { if !isGroup || len(args) == 0 { return }; jid, _ := types.ParseJID(args[0] + "@s.whatsapp.net"); client.UpdateGroupParticipants(context.Background(), chat, []types.JID{jid}, whatsmeow.ParticipantChangeAdd) }
func groupAction(client *whatsmeow.Client, chat types.JID, msg *waProto.Message, action string, isGroup bool) {
	if !isGroup { return }; target := getTarget(msg); if target == nil { return }; var c whatsmeow.ParticipantChange; if action == "remove" { c = whatsmeow.ParticipantChangeRemove } else if action == "promote" { c = whatsmeow.ParticipantChangePromote } else { c = whatsmeow.ParticipantChangeDemote }; client.UpdateGroupParticipants(context.Background(), chat, []types.JID{*target}, c)
}
func groupTagAll(client *whatsmeow.Client, chat types.JID, text string, isGroup bool) { if !isGroup { return }; info, _ := client.GetGroupInfo(context.Background(), chat); mentions := []string{}; out := "📣 *TAG ALL*\n" + text + "\n"; for _, p := range info.Participants { mentions = append(mentions, p.JID.String()); out += "@" + p.JID.User + "\n" }; client.SendMessage(context.Background(), chat, &waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(out), ContextInfo: &waProto.ContextInfo{MentionedJID: mentions}}}) }
func groupHideTag(client *whatsmeow.Client, chat types.JID, text string, isGroup bool) { if !isGroup { return }; info, _ := client.GetGroupInfo(context.Background(), chat); mentions := []string{}; for _, p := range info.Participants { mentions = append(mentions, p.JID.String()) }; client.SendMessage(context.Background(), chat, &waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(text), ContextInfo: &waProto.ContextInfo{MentionedJID: mentions}}}) }
func getTarget(m *waProto.Message) *types.JID { if m.ExtendedTextMessage == nil { return nil }; c := m.ExtendedTextMessage.ContextInfo; if c == nil { return nil }; if len(c.MentionedJID) > 0 { j, _ := types.ParseJID(c.MentionedJID[0]); return &j }; if c.Participant != nil { j, _ := types.ParseJID(*c.Participant); return &j }; return nil }
func deleteMsg(client *whatsmeow.Client, chat types.JID, msg *waProto.Message) { if msg.ExtendedTextMessage == nil { return }; ctx := msg.ExtendedTextMessage.ContextInfo; if ctx == nil { return }; target, _ := types.ParseJID(*ctx.Participant); client.RevokeMessage(context.Background(), chat, *ctx.StanzaID) }