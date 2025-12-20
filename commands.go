package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
	
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// --- ⚙️ CONFIGURATION ---
const (
	BOT_NAME     = "IMPOSSIBLE BOT V4"
	OWNER_NAME   = "Nothing Is Impossible"
)

// --- 💾 DATA STRUCTURES ---
type GroupSettings struct {
	ChatID         string         `bson:"chat_id" json:"chat_id"`
	Mode           string         `bson:"mode" json:"mode"`
	Antilink       bool           `bson:"antilink" json:"antilink"`
	AntilinkAdmin  bool           `bson:"antilink_admin" json:"antilink_admin"`
	AntilinkAction string         `bson:"antilink_action" json:"antilink_action"`
	AntiPic        bool           `bson:"antipic" json:"antipic"`
	AntiVideo      bool           `bson:"antivideo" json:"antivideo"`
	AntiSticker    bool           `bson:"antisticker" json:"antisticker"`
	Warnings       map[string]int `bson:"warnings" json:"warnings"`
}

type BotData struct {
	ID            string   `bson:"_id" json:"id"`
	Prefix        string   `bson:"prefix" json:"prefix"`
	AlwaysOnline  bool     `bson:"always_online" json:"always_online"`
	AutoRead      bool     `bson:"auto_read" json:"auto_read"`
	AutoReact     bool     `bson:"auto_react" json:"auto_react"`
	AutoStatus    bool     `bson:"auto_status" json:"auto_status"`
	StatusReact   bool     `bson:"status_react" json:"status_react"`
	StatusTargets []string `bson:"status_targets" json:"status_targets"`
}

type SetupState struct {
	Type    string
	Stage   int
	GroupID string
	User    string
}

// --- 🌍 LOGIC VARIABLES ---
var (
	startTime   = time.Now()
	data        BotData
	dataMutex   sync.RWMutex
	setupMap    = make(map[string]*SetupState)
	groupCache  = make(map[string]*GroupSettings)
	cacheMutex  sync.RWMutex
)

// --- 📡 MAIN EVENT HANDLER ---
func handler(client *whatsmeow.Client, evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		go processMessage(client, v)
	}
}

func processMessage(client *whatsmeow.Client, v *events.Message) {
	chatID := v.Info.Chat.String()
	senderID := v.Info.Sender.String()
	isGroup := v.Info.IsGroup

	// 1. SETUP FLOW
	if state, ok := setupMap[senderID]; ok && state.GroupID == chatID {
		handleSetupResponse(client, v, state)
		return
	}

	// 2. AUTO STATUS
	if chatID == "status@broadcast" {
		dataMutex.RLock()
		if data.AutoStatus {
			client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender, types.ReceiptTypeRead)
			if data.StatusReact {
				emojis := []string{"💚", "❤️", "🔥", "😍", "💯"}
				react(client, v.Info.Chat, v.Info.ID, emojis[time.Now().UnixNano()%int64(len(emojis))])
			}
		}
		dataMutex.RUnlock()
		return
	}

	// 3. AUTO READ
	dataMutex.RLock()
	if data.AutoRead {
		client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender, types.ReceiptTypeRead)
	}
	if data.AutoReact {
		react(client, v.Info.Chat, v.Info.ID, "❤️")
	}
	dataMutex.RUnlock()

	// 4. SECURITY CHECKS
	if isGroup {
		checkSecurity(client, v)
	}

	// 5. COMMAND PROCESSING
	body := getText(v.Message)
	dataMutex.RLock()
	prefix := data.Prefix
	dataMutex.RUnlock()

	cmd := strings.ToLower(body)
	args := []string{}
	
	if strings.HasPrefix(cmd, prefix) {
		split := strings.Fields(cmd[len(prefix):])
		if len(split) > 0 {
			cmd = split[0]
			args = split[1:]
		}
	} else {
		split := strings.Fields(cmd)
		if len(split) > 0 {
			cmd = split[0]
			args = split[1:]
		}
	}

	if !canExecute(client, v, cmd) { return }

	fullArgs := strings.Join(args, " ")
	fmt.Printf("📩 CMD: %s | Chat: %s\n", cmd, v.Info.Chat.User)

	switch cmd {
	case "menu", "help", "list":
		react(client, v.Info.Chat, v.Info.ID, "📜")
		sendMenu(client, v.Info.Chat)
	
	case "ping":
		react(client, v.Info.Chat, v.Info.ID, "⚡")
		sendPing(client, v.Info.Chat)

	case "id":
		react(client, v.Info.Chat, v.Info.ID, "🆔")
		sendID(client, v)

	case "owner":
		react(client, v.Info.Chat, v.Info.ID, "👑")
		sendOwner(client, v.Info.Chat, v.Info.Sender)

	case "data":
		reply(client, v.Info.Chat, "📂 Data is safe in MongoDB.")

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
		reply(client, v.Info.Chat, "✅ Recent statuses marked as read.")

	case "setprefix":
		if len(args) > 0 {
			dataMutex.Lock()
			data.Prefix = args[0]
			dataMutex.Unlock()
			saveDataToMongo()
			reply(client, v.Info.Chat, makeCard("SETTINGS", "✅ Prefix updated: "+args[0]))
		}
	
	case "mode": handleMode(client, v, args)
	case "antilink": startSecuritySetup(client, v, "antilink")
	case "antipic": startSecuritySetup(client, v, "antipic")
	case "antivideo": startSecuritySetup(client, v, "antivideo")
	case "antisticker": startSecuritySetup(client, v, "antisticker")

	case "kick": groupAction(client, v.Info.Chat, v.Message, "remove", isGroup)
	case "add": groupAdd(client, v.Info.Chat, args, isGroup)
	case "promote": groupAction(client, v.Info.Chat, v.Message, "promote", isGroup)
	case "demote": groupAction(client, v.Info.Chat, v.Message, "demote", isGroup)
	case "tagall": groupTagAll(client, v.Info.Chat, fullArgs, isGroup)
	case "hidetag": groupHideTag(client, v.Info.Chat, fullArgs, isGroup)
	case "group": handleGroupCmd(client, v.Info.Chat, args, isGroup)
	case "del", "delete": deleteMsg(client, v.Info.Chat, v.Message)

	case "tiktok", "tt": dlTikTok(client, v.Info.Chat, fullArgs, v.Message, v.Info.ID)
	case "fb", "facebook": dlFacebook(client, v.Info.Chat, fullArgs, v.Message, v.Info.ID)
	case "insta", "ig": dlInstagram(client, v.Info.Chat, fullArgs, v.Message, v.Info.ID)
	case "pin", "pinterest": dlPinterest(client, v.Info.Chat, fullArgs, v.Message, v.Info.ID)
	case "ytmp3": dlYouTube(client, v.Info.Chat, fullArgs, "mp3", v.Message, v.Info.ID)
	case "ytmp4": dlYouTube(client, v.Info.Chat, fullArgs, "mp4", v.Message, v.Info.ID)

	case "sticker", "s": makeSticker(client, v.Info.Chat, v.Message, v.Info.ID)
	case "toimg": stickerToImg(client, v.Info.Chat, v.Message, v.Info.ID)
	case "tovideo": stickerToVideo(client, v.Info.Chat, v.Message, v.Info.ID)
	case "removebg": removeBG(client, v.Info.Chat, v.Message, v.Info.ID)
	case "remini": reminiEnhance(client, v.Info.Chat, v.Message, v.Info.ID)
	case "tourl": mediaToUrl(client, v.Info.Chat, v.Message)
	case "weather": getWeather(client, v.Info.Chat, fullArgs, v.Info.ID)
	case "translate", "tr": doTranslate(client, v.Info.Chat, args, v.Message, v.Info.ID)
	case "vv": handleViewOnce(client, v.Info.Chat, v.Message, v.Info.ID)
	}
}

// --- 👑 OWNER LOGIC (DEBUGGED) ---
func isOwner(client *whatsmeow.Client, sender types.JID) bool {
	if client.Store.ID == nil { return false }

	// 1. Get Bot's Own Number (Cleaned)
	botNum := strings.ReplaceAll(client.Store.ID.User, "+", "")
	botNum = strings.Split(botNum, ":")[0] // Handle user:id format
	botNum = strings.Split(botNum, "@")[0] 

	// 2. Get Sender's Number (Cleaned)
	senderNum := strings.ReplaceAll(sender.User, "+", "")
	senderNum = strings.Split(senderNum, ":")[0]
	senderNum = strings.Split(senderNum, "@")[0]

	// 3. Strict Match
	isMatch := botNum == senderNum

	// ⚠️ DEBUG PRINTING
	fmt.Printf("\n🔍 [OWNER CHECK]\n")
	fmt.Printf("🤖 Bot ID: %s\n", botNum)
	fmt.Printf("👤 User ID: %s\n", senderNum)
	fmt.Printf("✅ Match: %v\n\n", isMatch)

	return isMatch
}

func canExecute(client *whatsmeow.Client, v *events.Message, cmd string) bool {
	if isOwner(client, v.Info.Sender) { return true }
	
	s := getGroupSettings(v.Info.Chat.String())
	if s.Mode == "private" { return false }
	if s.Mode == "admin" { return isAdmin(client, v.Info.Chat, v.Info.Sender) }
	return true
}

func sendOwner(client *whatsmeow.Client, chat types.JID, sender types.JID) {
	status := "❌ You are NOT the Owner."
	if isOwner(client, sender) {
		status = "👑 You are the OWNER!"
	}
	
	botNum := strings.Split(client.Store.ID.User, "@")[0]
	userNum := strings.Split(sender.User, "@")[0]
	
	reply(client, chat, makeCard("OWNER VERIFICATION", fmt.Sprintf("🤖 Bot: %s\n👤 You: %s\n\n%s", botNum, userNum, status)))
}

// --- 🛠️ HELPER FUNCTIONS ---

func react(client *whatsmeow.Client, chat types.JID, msgID types.MessageID, emoji string) {
	client.SendMessage(context.Background(), chat, &waProto.Message{
		ReactionMessage: &waProto.ReactionMessage{
			Key: &waProto.MessageKey{
				RemoteJid: proto.String(chat.String()),
				Id:        proto.String(msgID),
				FromMe:    proto.Bool(true),
			},
			Text: proto.String(emoji),
			SenderTimestampMS: proto.Int64(time.Now().UnixMilli()),
		},
	})
}

func sendMenu(client *whatsmeow.Client, chat types.JID) {
	uptime := time.Since(startTime).Round(time.Second)
	dataMutex.RLock()
	p := data.Prefix
	dataMutex.RUnlock()
	
	s := getGroupSettings(chat.String())
	currentMode := strings.ToUpper(s.Mode)
	if !strings.Contains(chat.String(), "@g.us") {
		currentMode = "PRIVATE"
	}
	
	menu := makeCard("⋆ BOT ⋆", fmt.Sprintf(`
👋 *Assalam-o-Alaikum*
👑 *Owner:* %s
🛡️ *Mode:* %s
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
	OWNER_NAME, currentMode, uptime,
	p, p, p, p, p, p,
	p, p, p, p, p, p, p,
	p, p, p, p, p, p, p, p, p, p, p, p, p, p, p,
	p, p, p, p, p, p, p, p, p, p, p, p))

	imgData, err := ioutil.ReadFile("pic.png")
	if err != nil {
		imgData, err = ioutil.ReadFile("web/pic.png")
	}

	if err == nil {
		resp, err := client.Upload(context.Background(), imgData, whatsmeow.MediaImage)
		if err == nil {
			client.SendMessage(context.Background(), chat, &waProto.Message{
				ImageMessage: &waProto.ImageMessage{
					Caption: proto.String(menu),
					URL: proto.String(resp.URL),
					DirectPath: proto.String(resp.DirectPath),
					MediaKey: resp.MediaKey,
					Mimetype: proto.String("image/png"),
					FileEncSHA256: resp.FileEncSHA256,
					FileSHA256: resp.FileSHA256,
				},
			})
			return
		}
	}
	client.SendMessage(context.Background(), chat, &waProto.Message{Conversation: proto.String(menu)})
}

func sendPing(client *whatsmeow.Client, chat types.JID) {
	start := time.Now()
	time.Sleep(10 * time.Millisecond)
	ms := time.Since(start).Milliseconds()
	uptime := time.Since(startTime).Round(time.Second)

	msg := fmt.Sprintf(`
╔════════════════════════╗
║        Dev    ║    %s      ║
╚══════════════╩═════════╝
               ┌────────────┐                
               │        ✨ PING          │              
               │           %d MS            │                
               └────────────┘                
╔════════════════════════╗
║    ⏱ UPTIME                      %s       ║
╚══════════════╩═════════╝`,
	OWNER_NAME, ms, uptime)

	client.SendMessage(context.Background(), chat, &waProto.Message{
		Conversation: proto.String(msg),
	})
}

func sendID(client *whatsmeow.Client, v *events.Message) {
	user := v.Info.Sender.User
	chat := v.Info.Chat.User
	chatType := "Private"
	if v.Info.IsGroup { chatType = "Group" }

	msg := fmt.Sprintf(`╭━━━〔 ID INFO 〕━━━┈
┃ 👤 *User:* `+"`%s`"+`
┃  👥 *Chat:* `+"`%s`"+`
┃  🏷️ *Type:* %s
╰━━━━━━━━━━━━━━━━━━┈`, user, chat, chatType)

	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		Conversation: proto.String(msg),
	})
}

func toggleGlobal(client *whatsmeow.Client, v *events.Message, key string) {
	if !isOwner(client, v.Info.Sender) { reply(client, v.Info.Chat, "❌ Owner Only"); return }
	status := "OFF 🔴"
	dataMutex.Lock()
	switch key {
	case "alwaysonline": 
		data.AlwaysOnline = !data.AlwaysOnline
		if data.AlwaysOnline { 
			client.SendPresence(context.Background(), types.PresenceAvailable)
			status = "ON 🟢" 
		} else { 
			client.SendPresence(context.Background(), types.PresenceUnavailable)
		}
	case "autoread": data.AutoRead = !data.AutoRead; if data.AutoRead { status = "ON 🟢" }
	case "autoreact": data.AutoReact = !data.AutoReact; if data.AutoReact { status = "ON 🟢" }
	case "autostatus": data.AutoStatus = !data.AutoStatus; if data.AutoStatus { status = "ON 🟢" }
	case "statusreact": data.StatusReact = !data.StatusReact; if data.StatusReact { status = "ON 🟢" }
	}
	dataMutex.Unlock()
	saveDataToMongo()
	reply(client, v.Info.Chat, fmt.Sprintf("⚙️ *%s:* %s", strings.ToUpper(key), status))
}

func manageStatusList(client *whatsmeow.Client, v *events.Message, args []string, action string) {
	if !isOwner(client, v.Info.Sender) { return }
	dataMutex.Lock()
	defer dataMutex.Unlock()
	if action == "list" { reply(client, v.Info.Chat, fmt.Sprintf("📜 *Targets:* %v", data.StatusTargets)); return }
	if len(args) < 1 { reply(client, v.Info.Chat, "⚠️ Number?"); return }
	num := args[0]
	if action == "add" { data.StatusTargets = append(data.StatusTargets, num); reply(client, v.Info.Chat, "✅ Added") }
	if action == "del" {
		newList := []string{}
		for _, n := range data.StatusTargets { if n != num { newList = append(newList, n) } }
		data.StatusTargets = newList
		reply(client, v.Info.Chat, "🗑️ Deleted")
	}
	saveDataToMongo()
}

func startSecuritySetup(client *whatsmeow.Client, v *events.Message, secType string) {
	if !v.Info.IsGroup || !isAdmin(client, v.Info.Chat, v.Info.Sender) { return }
	setupMap[v.Info.Sender.String()] = &SetupState{Type: secType, Stage: 1, GroupID: v.Info.Chat.String(), User: v.Info.Sender.String()}
	reply(client, v.Info.Chat, makeCard(strings.ToUpper(secType)+" SETUP (1/2)", "🛡️ *Allow Admin?*\n\nType *Yes* or *No*"))
}

func handleSetupResponse(client *whatsmeow.Client, v *events.Message, state *SetupState) {
	txt := strings.ToLower(getText(v.Message))
	s := getGroupSettings(state.GroupID)

	if state.Stage == 1 {
		if txt == "yes" { s.AntilinkAdmin = true } else if txt == "no" { s.AntilinkAdmin = false } else { return }
		state.Stage = 2
		reply(client, v.Info.Chat, makeCard("ACTION SETUP (2/2)", "⚡ *Choose Action:*\n\n*Delete*\n*Kick*\n*Warn*"))
		return
	}

	if state.Stage == 2 {
		if strings.Contains(txt, "kick") { s.AntilinkAction = "kick" } else if strings.Contains(txt, "warn") { s.AntilinkAction = "warn" } else { s.AntilinkAction = "delete" }
		switch state.Type {
		case "antilink": s.Antilink = true
		case "antipic": s.AntiPic = true
		case "antivideo": s.AntiVideo = true
		case "antisticker": s.AntiSticker = true
		}
		saveGroupSettings(s)
		delete(setupMap, state.User)
		reply(client, v.Info.Chat, makeCard("✅ "+strings.ToUpper(state.Type)+" ENABLED", fmt.Sprintf("👑 Admin Allow: %v\n⚡ Action: %s", s.AntilinkAdmin, strings.ToUpper(s.AntilinkAction))))
	}
}

func handleGroupCmd(client *whatsmeow.Client, chat types.JID, args []string, isGroup bool) {
	if !isGroup || len(args) == 0 { return }
	switch args[0] {
	case "close": client.SetGroupAnnounce(context.Background(), chat, true); reply(client, chat, "🔒 Group Closed")
	case "open": client.SetGroupAnnounce(context.Background(), chat, false); reply(client, chat, "🔓 Group Opened")
	case "link":
		code, _ := client.GetGroupInviteLink(context.Background(), chat, false)
		reply(client, chat, "🔗 https://chat.whatsapp.com/"+code)
	case "revoke":
		client.GetGroupInviteLink(context.Background(), chat, true)
		reply(client, chat, "🔄 Link Revoked")
	}
}

func stickerToVideo(client *whatsmeow.Client, chat types.JID, msg *waProto.Message, id types.MessageID) {
	react(client, chat, id, "🎥")
	data, err := downloadMedia(client, msg)
	if err != nil { return }
	ioutil.WriteFile("in.webp", data, 0644)
	exec.Command("ffmpeg", "-y", "-i", "in.webp", "out.mp4").Run()
	d, _ := ioutil.ReadFile("out.mp4")
	up, _ := client.Upload(context.Background(), d, whatsmeow.MediaVideo)
	client.SendMessage(context.Background(), chat, &waProto.Message{VideoMessage: &waProto.VideoMessage{
		URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey,
		FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, Mimetype: proto.String("video/mp4"),
	}})
}

func dlTikTok(client *whatsmeow.Client, chat types.JID, url string, msg *waProto.Message, id types.MessageID) {
	react(client, chat, id, "🎵")
	type R struct { Data struct { Play string `json:"play"` } `json:"data"` }
	var r R
	getJson("https://www.tikwm.com/api/?url="+url, &r)
	if r.Data.Play != "" { sendVideo(client, chat, r.Data.Play, "TikTok") }
}

func dlFacebook(client *whatsmeow.Client, chat types.JID, url string, msg *waProto.Message, id types.MessageID) {
	react(client, chat, id, "📘"); type R struct { BK9 struct { HD string `json:"HD"` } `json:"BK9"`; Status bool `json:"status"` }; var r R; getJson("https://bk9.fun/downloader/facebook?url="+url, &r)
	if r.Status { sendVideo(client, chat, r.BK9.HD, "FB") }
}
func dlInstagram(client *whatsmeow.Client, chat types.JID, url string, msg *waProto.Message, id types.MessageID) {
	react(client, chat, id, "📸"); type R struct { Video struct { Url string `json:"url"` } `json:"video"` }; var r R; getJson("https://api.tiklydown.eu.org/api/download?url="+url, &r)
	if r.Video.Url != "" { sendVideo(client, chat, r.Video.Url, "Insta") }
}
func dlPinterest(client *whatsmeow.Client, chat types.JID, url string, msg *waProto.Message, id types.MessageID) {
	react(client, chat, id, "📌"); type R struct { BK9 struct { Url string `json:"url"` } `json:"BK9"`; Status bool `json:"status"` }; var r R; getJson("https://bk9.fun/downloader/pinterest?url="+url, &r)
	sendImage(client, chat, r.BK9.Url, "Pin")
}
func dlYouTube(client *whatsmeow.Client, chat types.JID, url, f string, msg *waProto.Message, id types.MessageID) {
	react(client, chat, id, "📺"); type R struct { BK9 struct { Mp4 string `json:"mp4"`; Mp3 string `json:"mp3"` } `json:"BK9"`; Status bool `json:"status"` }; var r R; getJson("https://bk9.fun/downloader/youtube?url="+url, &r)
	if f=="mp4" { sendVideo(client, chat, r.BK9.Mp4, "YT") } else { sendDoc(client, chat, r.BK9.Mp3, "aud.mp3", "audio/mpeg") }
}
func makeSticker(client *whatsmeow.Client, chat types.JID, msg *waProto.Message, id types.MessageID) {
	react(client, chat, id, "🎨"); d, _ := downloadMedia(client, msg); ioutil.WriteFile("t.jpg", d, 0644); exec.Command("ffmpeg", "-y", "-i", "t.jpg", "-vcodec", "libwebp", "out.webp").Run(); b, _ := ioutil.ReadFile("out.webp")
	up, _ := client.Upload(context.Background(), b, whatsmeow.MediaImage); client.SendMessage(context.Background(), chat, &waProto.Message{StickerMessage: &waProto.StickerMessage{URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, Mimetype: proto.String("image/webp")}})
}
func stickerToImg(client *whatsmeow.Client, chat types.JID, msg *waProto.Message, id types.MessageID) {
	react(client, chat, id, "🖼️"); d, _ := downloadMedia(client, msg); ioutil.WriteFile("t.webp", d, 0644); exec.Command("ffmpeg", "-y", "-i", "t.webp", "out.png").Run(); b, _ := ioutil.ReadFile("out.png")
	up, _ := client.Upload(context.Background(), b, whatsmeow.MediaImage); client.SendMessage(context.Background(), chat, &waProto.Message{ImageMessage: &waProto.ImageMessage{URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, Mimetype: proto.String("image/png")}})
}
func removeBG(client *whatsmeow.Client, chat types.JID, msg *waProto.Message, id types.MessageID) {
	react(client, chat, id, "✂️"); d, _ := downloadMedia(client, msg); u := uploadToCatbox(d); sendImage(client, chat, "https://bk9.fun/tools/removebg?url="+u, "Removed")
}
func reminiEnhance(client *whatsmeow.Client, chat types.JID, msg *waProto.Message, id types.MessageID) {
	react(client, chat, id, "✨"); d, _ := downloadMedia(client, msg); u := uploadToCatbox(d); type R struct{Url string `json:"url"`}; var r R; getJson("https://remini.mobilz.pw/enhance?url="+u, &r); sendImage(client, chat, r.Url, "HD")
}
func mediaToUrl(client *whatsmeow.Client, chat types.JID, msg *waProto.Message) {
	d, _ := downloadMedia(client, msg); reply(client, chat, "🔗 "+uploadToCatbox(d))
}
func getWeather(client *whatsmeow.Client, chat types.JID, c string, id types.MessageID) {
	react(client, chat, id, "🌦️"); r, _ := http.Get("https://wttr.in/"+c+"?format=%C+%t"); d, _ := ioutil.ReadAll(r.Body); reply(client, chat, string(d))
}
func doTranslate(client *whatsmeow.Client, chat types.JID, args []string, msg *waProto.Message, id types.MessageID) {
	react(client, chat, id, "🌍"); t := strings.Join(args, " "); if t == "" { q := msg.ExtendedTextMessage.GetContextInfo().GetQuotedMessage(); if q != nil { t = q.GetConversation() } }
	r, _ := http.Get(fmt.Sprintf("https://translate.googleapis.com/translate_a/single?client=gtx&sl=auto&tl=ur&dt=t&q=%s", url.QueryEscape(t))); var res []interface{}; json.NewDecoder(r.Body).Decode(&res); if len(res)>0 { reply(client, chat, res[0].([]interface{})[0].([]interface{})[0].(string)) }
}
func handleViewOnce(client *whatsmeow.Client, chat types.JID, msg *waProto.Message, id types.MessageID) {
	react(client, chat, id, "🫣"); quoted := msg.ExtendedTextMessage.GetContextInfo().GetQuotedMessage(); if quoted == nil { reply(client, chat, "⚠️ Reply to ViewOnce media."); return }; data, err := downloadMedia(client, &waProto.Message{ImageMessage: quoted.ImageMessage, VideoMessage: quoted.VideoMessage, ViewOnceMessage: quoted.ViewOnceMessage, ViewOnceMessageV2: quoted.ViewOnceMessageV2}); if err != nil { reply(client, chat, "❌ Failed to download."); return }; if quoted.ImageMessage != nil || (quoted.ViewOnceMessage != nil && quoted.ViewOnceMessage.Message.ImageMessage != nil) { up, _ := client.Upload(context.Background(), data, whatsmeow.MediaImage); client.SendMessage(context.Background(), chat, &waProto.Message{ImageMessage: &waProto.ImageMessage{URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, Mimetype: proto.String("image/jpeg")}}) } else { up, _ := client.Upload(context.Background(), data, whatsmeow.MediaVideo); client.SendMessage(context.Background(), chat, &waProto.Message{VideoMessage: &waProto.VideoMessage{URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, Mimetype: proto.String("video/mp4")}}) }
}
func getJson(url string, target interface{}) error { r, err := http.Get(url); if err != nil { return err }; defer r.Body.Close(); return json.NewDecoder(r.Body).Decode(target) }
func downloadMedia(client *whatsmeow.Client, m *waProto.Message) ([]byte, error) { var d whatsmeow.DownloadableMessage; if m.ImageMessage != nil { d = m.ImageMessage } else if m.VideoMessage != nil { d = m.VideoMessage } else if m.DocumentMessage != nil { d = m.DocumentMessage } else if m.StickerMessage != nil { d = m.StickerMessage } else if m.ExtendedTextMessage != nil && m.ExtendedTextMessage.ContextInfo != nil { q := m.ExtendedTextMessage.ContextInfo.QuotedMessage; if q != nil { if q.ImageMessage != nil { d = q.ImageMessage } else if q.VideoMessage != nil { d = q.VideoMessage } else if q.StickerMessage != nil { d = q.StickerMessage } } }; if d == nil { return nil, fmt.Errorf("no media") }; return client.Download(context.Background(), d) }
func uploadToCatbox(d []byte) string { b := new(bytes.Buffer); w := multipart.NewWriter(b); p, _ := w.CreateFormFile("fileToUpload", "f.jpg"); p.Write(d); w.WriteField("reqtype", "fileupload"); w.Close(); r, _ := http.Post("https://catbox.moe/user/api.php", w.FormDataContentType(), b); res, _ := ioutil.ReadAll(r.Body); return string(res) }
func sendVideo(client *whatsmeow.Client, chat types.JID, url, c string) { r, _ := http.Get(url); d, _ := ioutil.ReadAll(r.Body); up, _ := client.Upload(context.Background(), d, whatsmeow.MediaVideo); client.SendMessage(context.Background(), chat, &waProto.Message{VideoMessage: &waProto.VideoMessage{URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, Mimetype: proto.String("video/mp4"), Caption: proto.String(c)}}) }
func sendImage(client *whatsmeow.Client, chat types.JID, url, c string) { r, _ := http.Get(url); d, _ := ioutil.ReadAll(r.Body); up, _ := client.Upload(context.Background(), d, whatsmeow.MediaImage); client.SendMessage(context.Background(), chat, &waProto.Message{ImageMessage: &waProto.ImageMessage{URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, Mimetype: proto.String("image/jpeg"), Caption: proto.String(c)}}) }
func sendDoc(client *whatsmeow.Client, chat types.JID, url, n, m string) { r, _ := http.Get(url); d, _ := ioutil.ReadAll(r.Body); up, _ := client.Upload(context.Background(), d, whatsmeow.MediaDocument); client.SendMessage(context.Background(), chat, &waProto.Message{DocumentMessage: &waProto.DocumentMessage{URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, Mimetype: proto.String(m), FileName: proto.String(n)}}) }
func groupAdd(client *whatsmeow.Client, chat types.JID, args []string, isGroup bool) { if !isGroup || len(args) == 0 { return }; jid, _ := types.ParseJID(args[0] + "@s.whatsapp.net"); client.UpdateGroupParticipants(context.Background(), chat, []types.JID{jid}, whatsmeow.ParticipantChangeAdd) }
func groupAction(client *whatsmeow.Client, chat types.JID, msg *waProto.Message, action string, isGroup bool) { if !isGroup { return }; target := getTarget(msg); if target == nil { return }; var c whatsmeow.ParticipantChange; if action == "remove" { c = whatsmeow.ParticipantChangeRemove } else if action == "promote" { c = whatsmeow.ParticipantChangePromote } else { c = whatsmeow.ParticipantChangeDemote }; client.UpdateGroupParticipants(context.Background(), chat, []types.JID{*target}, c) }
func groupTagAll(client *whatsmeow.Client, chat types.JID, text string, isGroup bool) { if !isGroup { return }; info, _ := client.GetGroupInfo(context.Background(), chat); mentions := []string{}; out := "📣 *TAG ALL*\n" + text + "\n"; for _, p := range info.Participants { mentions = append(mentions, p.JID.String()); out += "@" + p.JID.User + "\n" }; client.SendMessage(context.Background(), chat, &waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(out), ContextInfo: &waProto.ContextInfo{MentionedJID: mentions}}}) }
func groupHideTag(client *whatsmeow.Client, chat types.JID, text string, isGroup bool) { if !isGroup { return }; info, _ := client.GetGroupInfo(context.Background(), chat); mentions := []string{}; for _, p := range info.Participants { mentions = append(mentions, p.JID.String()) }; client.SendMessage(context.Background(), chat, &waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(text), ContextInfo: &waProto.ContextInfo{MentionedJID: mentions}}}) }
func deleteMsg(client *whatsmeow.Client, chat types.JID, msg *waProto.Message) { if msg.ExtendedTextMessage == nil { return }; ctx := msg.ExtendedTextMessage.ContextInfo; if ctx == nil { return }; client.RevokeMessage(context.Background(), chat, *ctx.StanzaID) }
func getTarget(m *waProto.Message) *types.JID { if m.ExtendedTextMessage == nil { return nil }; c := m.ExtendedTextMessage.ContextInfo; if c == nil { return nil }; if len(c.MentionedJID) > 0 { j, _ := types.ParseJID(c.MentionedJID[0]); return &j }; if c.Participant != nil { j, _ := types.ParseJID(*c.Participant); return &j }; return nil }
func loadDataFromMongo() { if mongoColl == nil { return }; res := mongoColl.FindOne(context.Background(), bson.M{"_id": "global"}); if res.Err() == nil { dataMutex.Lock(); res.Decode(&data); dataMutex.Unlock() } else { dataMutex.Lock(); data.ID = "global"; data.Prefix = "#"; dataMutex.Unlock(); saveDataToMongo() } }
func saveDataToMongo() { if mongoColl == nil { return }; dataMutex.RLock(); defer dataMutex.RUnlock(); opts := options.Update().SetUpsert(true); mongoColl.UpdateOne(context.Background(), bson.M{"_id": "global"}, bson.M{"$set": data}, opts) }
func getGroupSettings(id string) *GroupSettings { cacheMutex.RLock(); if s, ok := groupCache[id]; ok { cacheMutex.RUnlock(); return s }; cacheMutex.RUnlock(); s := &GroupSettings{ChatID: id, Mode: "public", AntilinkAdmin: true, AntilinkAction: "delete", Warnings: make(map[string]int)}; if mongoColl != nil { res := mongoColl.FindOne(context.Background(), bson.M{"chat_id": id}); if res.Err() == nil { res.Decode(s) } }; cacheMutex.Lock(); groupCache[id] = s; cacheMutex.Unlock(); return s }
func saveGroupSettings(s *GroupSettings) { cacheMutex.Lock(); groupCache[s.ChatID] = s; cacheMutex.Unlock(); if mongoColl != nil { opts := options.Update().SetUpsert(true); mongoColl.UpdateOne(context.Background(), bson.M{"chat_id": s.ChatID}, bson.M{"$set": s}, opts) } }
func makeCard(title, body string) string { return fmt.Sprintf("╭━━━〔 %s 〕━━━┈\n┃ %s\n╰━━━━━━━━━━━━━━━━━━┈", title, body) }
func reply(client *whatsmeow.Client, chat types.JID, text string) { client.SendMessage(context.Background(), chat, &waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(text)}}) }
func getText(m *waProto.Message) string { if m.Conversation != nil { return *m.Conversation }; if m.ExtendedTextMessage != nil { return *m.ExtendedTextMessage.Text }; if m.ImageMessage != nil { return *m.ImageMessage.Caption }; return "" }
func isAdmin(client *whatsmeow.Client, chat, user types.JID) bool { info, _ := client.GetGroupInfo(context.Background(), chat); for _, p := range info.Participants { if p.JID.User == user.User && (p.IsAdmin || p.IsSuperAdmin) { return true } }; return false }
func handleMode(client *whatsmeow.Client, v *events.Message, args []string) { if !isAdmin(client, v.Info.Chat, v.Info.Sender) && !isOwner(client, v.Info.Sender) { return }; if len(args) < 1 { return }; s := getGroupSettings(v.Info.Chat.String()); s.Mode = strings.ToLower(args[0]); saveGroupSettings(s); reply(client, v.Info.Chat, makeCard("MODE CHANGED", "🔒 Mode: "+strings.ToUpper(s.Mode))) }