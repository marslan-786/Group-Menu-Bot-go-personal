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
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
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
	// مینیو سسٹم
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

	// سیٹنگز
	case "alwaysonline": toggleAlwaysOnline(client, v)
	case "autoread": toggleAutoRead(client, v)
	case "autoreact": toggleAutoReact(client, v)
	case "autostatus": toggleAutoStatus(client, v)
	case "statusreact": toggleStatusReact(client, v)
	case "addstatus": handleAddStatus(client, v, args)
	case "delstatus": handleDelStatus(client, v, args)
	case "liststatus": handleListStatus(client, v)
	case "readallstatus": handleReadAllStatus(client, v)
	case "setprefix": handleSetPrefix(client, v, args)
	case "mode": handleMode(client, v, args)

	// سیکورٹی
	case "antilink": startSecuritySetup(client, v, "antilink")
	case "antipic": startSecuritySetup(client, v, "antipic")
	case "antivideo": startSecuritySetup(client, v, "antivideo")
	case "antisticker": startSecuritySetup(client, v, "antisticker")

	// گروپ
	case "kick": handleKick(client, v, args)
	case "add": handleAdd(client, v, args)
	case "promote": handlePromote(client, v, args)
	case "demote": handleDemote(client, v, args)
	case "tagall": handleTagAll(client, v, args)
	case "hidetag": handleHideTag(client, v, args)
	case "group": handleGroup(client, v, args)
	case "del", "delete": handleDelete(client, v)

	// ڈاؤن لوڈرز
	case "tiktok", "tt": handleTikTok(client, v, fullArgs)
	case "fb", "facebook": handleFacebook(client, v, fullArgs)
	case "insta", "ig": handleInstagram(client, v, fullArgs)
	case "pin", "pinterest": handlePinterest(client, v, fullArgs)
	case "ytmp3": handleYouTubeMP3(client, v, fullArgs)
	case "ytmp4": handleYouTubeMP4(client, v, fullArgs)

	// ٹولز
	case "sticker", "s": handleSticker(client, v)
	case "toimg": handleToImg(client, v)
	case "tovideo": handleToVideo(client, v)
	case "removebg": handleRemoveBG(client, v)
	case "remini": handleRemini(client, v)
	case "tourl": handleToURL(client, v)
	case "weather": handleWeather(client, v, fullArgs)
	case "translate", "tr": handleTranslate(client, v, args)
	case "vv": handleVV(client, v)
	}
}

// ==================== مینیو سسٹم ====================
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
	
	menu := fmt.Sprintf(`╭━━━〔 %s 〕━━━┈
┃ 👋 *Assalam-o-Alaikum*
┃ 👑 *Owner:* %s
┃ 🛡️ *Mode:* %s
┃ ⏳ *Uptime:* %s
┃
┃ ╭━━〔 *DOWNLOADERS* 〕━━┈
┃ ┃ 🔸 *%sfb*
┃ ┃ 🔸 *%sig*
┃ ┃ 🔸 *%spin*
┃ ┃ 🔸 *%stiktok*
┃ ┃ 🔸 *%sytmp3*
┃ ┃ 🔸 *%sytmp4*
┃ ╰━━━━━━━━━━━━━━━━━━┈
┃
┃ ╭━━〔 *GROUP* 〕━━┈
┃ ┃ 🔸 *%sadd*
┃ ┃ 🔸 *%sdemote*
┃ ┃ 🔸 *%sgroup*
┃ ┃ 🔸 *%shidetag*
┃ ┃ 🔸 *%skick*
┃ ┃ 🔸 *%spromote*
┃ ┃ 🔸 *%stagall*
┃ ╰━━━━━━━━━━━━━━━━━━┈
┃
┃ ╭━━〔 *SETTINGS* 〕━━┈
┃ ┃ 🔸 *%saddstatus*
┃ ┃ 🔸 *%salwaysonline*
┃ ┃ 🔸 *%santilink*
┃ ┃ 🔸 *%santipic*
┃ ┃ 🔸 *%santisticker*
┃ ┃ 🔸 *%santivideo*
┃ ┃ 🔸 *%sautoreact*
┃ ┃ 🔸 *%sautoread*
┃ ┃ 🔸 *%sautostatus*
┃ ┃ 🔸 *%sdelstatus*
┃ ┃ 🔸 *%sliststatus*
┃ ┃ 🔸 *%smode*
┃ ┃ 🔸 *%sowner*
┃ ┃ 🔸 *%sreadallstatus*
┃ ┃ 🔸 *%sstatusreact*
┃ ╰━━━━━━━━━━━━━━━━━━┈
┃
┃ ╭━━〔 *TOOLS* 〕━━┈
┃ ┃ 🔸 *%sdata*
┃ ┃ 🔸 *%sid*
┃ ┃ 🔸 *%sping*
┃ ┃ 🔸 *%sremini*
┃ ┃ 🔸 *%sremovebg*
┃ ┃ 🔸 *%ssticker*
┃ ┃ 🔸 *%stoimg*
┃ ┃ 🔸 *%stourl*
┃ ┃ 🔸 *%stovideo*
┃ ┃ 🔸 *%stranslate*
┃ ┃ 🔸 *%svv*
┃ ┃ 🔸 *%sweather*
┃ ╰━━━━━━━━━━━━━━━━━━┈
┃
┃ © 2025 Nothing is Impossible
╰━━━━━━━━━━━━━━━━━━┈`, 
		BOT_NAME, OWNER_NAME, currentMode, uptime,
		p, p, p, p, p, p,
		p, p, p, p, p, p, p,
		p, p, p, p, p, p, p, p, p, p, p, p, p, p, p,
		p, p, p, p, p, p, p, p, p, p, p, p)

	imgData, err := ioutil.ReadFile("pic.png")
	if err != nil {
		imgData, err = ioutil.ReadFile("web/pic.png")
	}

	if err == nil {
		resp, err := client.Upload(context.Background(), imgData, whatsmeow.MediaImage)
		if err == nil {
			client.SendMessage(context.Background(), chat, &waProto.Message{
				ImageMessage: &waProto.ImageMessage{
					Caption:       proto.String(menu),
					URL:           proto.String(resp.URL),
					DirectPath:    proto.String(resp.DirectPath),
					MediaKey:      resp.MediaKey,
					Mimetype:      proto.String("image/png"),
					FileEncSHA256: resp.FileEncSHA256,
					FileSHA256:    resp.FileSHA256,
				},
			})
			return
		}
	}
	
	client.SendMessage(context.Background(), chat, &waProto.Message{
		Conversation: proto.String(menu),
	})
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
	if v.Info.IsGroup {
		chatType = "Group"
	}

	msg := fmt.Sprintf(`╭━━━〔 ID INFO 〕━━━┈
┃ 👤 *User:* `+"`%s`"+`
┃ 👥 *Chat:* `+"`%s`"+`
┃ 🏷️ *Type:* %s
╰━━━━━━━━━━━━━━━━━━┈`, user, chat, chatType)

	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		Conversation: proto.String(msg),
	})
}

func sendOwner(client *whatsmeow.Client, chat types.JID, sender types.JID) {
	status := "❌ You are NOT the Owner."
	if isOwner(client, sender) {
		status = "👑 You are the OWNER!"
	}
	
	botNum := cleanNumber(client.Store.ID.User)
	userNum := cleanNumber(sender.User)
	
	reply(client, chat, fmt.Sprintf(`╭━━━〔 OWNER VERIFICATION 〕━━━┈
┃ 🤖 *Bot:* %s
┃ 👤 *You:* %s
┃
┃ %s
╰━━━━━━━━━━━━━━━━━━┈`, botNum, userNum, status))
}

// ==================== سیٹنگز سسٹم ====================
func toggleAlwaysOnline(client *whatsmeow.Client, v *events.Message) {
	if !isOwner(client, v.Info.Sender) { 
		reply(client, v.Info.Chat, "❌ Owner Only")
		return 
	}
	
	status := "OFF 🔴"
	dataMutex.Lock()
	data.AlwaysOnline = !data.AlwaysOnline
	if data.AlwaysOnline { 
		client.SendPresence(context.Background(), types.PresenceAvailable)
		status = "ON 🟢" 
	} else { 
		client.SendPresence(context.Background(), types.PresenceUnavailable)
	}
	dataMutex.Unlock()
	
	reply(client, v.Info.Chat, fmt.Sprintf("⚙️ *ALWAYSONLINE:* %s", status))
}

func toggleAutoRead(client *whatsmeow.Client, v *events.Message) {
	if !isOwner(client, v.Info.Sender) { 
		reply(client, v.Info.Chat, "❌ Owner Only")
		return 
	}
	
	status := "OFF 🔴"
	dataMutex.Lock()
	data.AutoRead = !data.AutoRead
	if data.AutoRead { status = "ON 🟢" }
	dataMutex.Unlock()
	
	reply(client, v.Info.Chat, fmt.Sprintf("⚙️ *AUTOREAD:* %s", status))
}

func toggleAutoReact(client *whatsmeow.Client, v *events.Message) {
	if !isOwner(client, v.Info.Sender) { 
		reply(client, v.Info.Chat, "❌ Owner Only")
		return 
	}
	
	status := "OFF 🔴"
	dataMutex.Lock()
	data.AutoReact = !data.AutoReact
	if data.AutoReact { status = "ON 🟢" }
	dataMutex.Unlock()
	
	reply(client, v.Info.Chat, fmt.Sprintf("⚙️ *AUTOREACT:* %s", status))
}

func toggleAutoStatus(client *whatsmeow.Client, v *events.Message) {
	if !isOwner(client, v.Info.Sender) { 
		reply(client, v.Info.Chat, "❌ Owner Only")
		return 
	}
	
	status := "OFF 🔴"
	dataMutex.Lock()
	data.AutoStatus = !data.AutoStatus
	if data.AutoStatus { status = "ON 🟢" }
	dataMutex.Unlock()
	
	reply(client, v.Info.Chat, fmt.Sprintf("⚙️ *AUTOSTATUS:* %s", status))
}

func toggleStatusReact(client *whatsmeow.Client, v *events.Message) {
	if !isOwner(client, v.Info.Sender) { 
		reply(client, v.Info.Chat, "❌ Owner Only")
		return 
	}
	
	status := "OFF 🔴"
	dataMutex.Lock()
	data.StatusReact = !data.StatusReact
	if data.StatusReact { status = "ON 🟢" }
	dataMutex.Unlock()
	
	reply(client, v.Info.Chat, fmt.Sprintf("⚙️ *STATUSREACT:* %s", status))
}

func handleAddStatus(client *whatsmeow.Client, v *events.Message, args []string) {
	if !isOwner(client, v.Info.Sender) { 
		reply(client, v.Info.Chat, "❌ Owner Only")
		return 
	}
	
	if len(args) < 1 { 
		reply(client, v.Info.Chat, "⚠️ Number?")
		return 
	}
	
	num := args[0]
	dataMutex.Lock()
	data.StatusTargets = append(data.StatusTargets, num)
	dataMutex.Unlock()
	
	reply(client, v.Info.Chat, "✅ Added to status targets")
}

func handleDelStatus(client *whatsmeow.Client, v *events.Message, args []string) {
	if !isOwner(client, v.Info.Sender) { 
		reply(client, v.Info.Chat, "❌ Owner Only")
		return 
	}
	
	if len(args) < 1 { 
		reply(client, v.Info.Chat, "⚠️ Number?")
		return 
	}
	
	num := args[0]
	dataMutex.Lock()
	newList := []string{}
	for _, n := range data.StatusTargets { 
		if n != num { 
			newList = append(newList, n) 
		} 
	}
	data.StatusTargets = newList
	dataMutex.Unlock()
	
	reply(client, v.Info.Chat, "🗑️ Removed from status targets")
}

func handleListStatus(client *whatsmeow.Client, v *events.Message) {
	if !isOwner(client, v.Info.Sender) { 
		return 
	}
	
	dataMutex.RLock()
	targets := data.StatusTargets
	dataMutex.RUnlock()
	
	if len(targets) == 0 {
		reply(client, v.Info.Chat, "📭 No status targets")
		return
	}
	
	msg := "📜 *Status Targets:*\n"
	for i, t := range targets {
		msg += fmt.Sprintf("%d. %s\n", i+1, t)
	}
	
	reply(client, v.Info.Chat, msg)
}

func handleSetPrefix(client *whatsmeow.Client, v *events.Message, args []string) {
	if !isOwner(client, v.Info.Sender) { 
		reply(client, v.Info.Chat, "❌ Owner Only")
		return 
	}
	
	if len(args) < 1 { 
		reply(client, v.Info.Chat, "⚠️ Prefix?")
		return 
	}
	
	newPrefix := args[0]
	dataMutex.Lock()
	data.Prefix = newPrefix
	dataMutex.Unlock()
	
	reply(client, v.Info.Chat, fmt.Sprintf("╭━━━〔 SETTINGS 〕━━━┈\n┃ ✅ Prefix updated: %s\n╰━━━━━━━━━━━━━━━━━━┈", newPrefix))
}

func handleMode(client *whatsmeow.Client, v *events.Message, args []string) {
	if !v.Info.IsGroup {
		reply(client, v.Info.Chat, "❌ Group only command")
		return
	}
	
	if !isAdmin(client, v.Info.Chat, v.Info.Sender) && !isOwner(client, v.Info.Sender) {
		reply(client, v.Info.Chat, "❌ Admin only")
		return
	}
	
	if len(args) < 1 {
		reply(client, v.Info.Chat, "⚠️ Mode? (public/private/admin)")
		return
	}
	
	mode := strings.ToLower(args[0])
	if mode != "public" && mode != "private" && mode != "admin" {
		reply(client, v.Info.Chat, "❌ Invalid mode. Use: public/private/admin")
		return
	}
	
	s := getGroupSettings(v.Info.Chat.String())
	s.Mode = mode
	saveGroupSettings(s)
	
	reply(client, v.Info.Chat, fmt.Sprintf("╭━━━〔 MODE CHANGED 〕━━━┈\n┃ 🔒 Mode: %s\n╰━━━━━━━━━━━━━━━━━━┈", strings.ToUpper(mode)))
}

func handleReadAllStatus(client *whatsmeow.Client, v *events.Message) {
	if !isOwner(client, v.Info.Sender) {
		return
	}
	
	client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, time.Now(), types.NewJID("status@broadcast", types.DefaultUserServer), v.Info.Sender, types.ReceiptTypeRead)
	reply(client, v.Info.Chat, "✅ Recent statuses marked as read")
}

// ==================== سیکورٹی سسٹم ====================
func checkSecurity(client *whatsmeow.Client, v *events.Message) {
	if !v.Info.IsGroup {
		return
	}
	
	s := getGroupSettings(v.Info.Chat.String())
	if s.Mode == "private" {
		return
	}
	
	// Anti-link check
	if s.Antilink && containsLink(getText(v.Message)) {
		if s.AntilinkAdmin && isAdmin(client, v.Info.Chat, v.Info.Sender) {
			return
		}
		takeSecurityAction(client, v, s.AntilinkAction, "Link detected!")
		return
	}
	
	// Anti-picture check
	if s.AntiPic && v.Message.ImageMessage != nil {
		if s.AntilinkAdmin && isAdmin(client, v.Info.Chat, v.Info.Sender) {
			return
		}
		takeSecurityAction(client, v, "delete", "Image not allowed!")
		return
	}
	
	// Anti-video check
	if s.AntiVideo && v.Message.VideoMessage != nil {
		if s.AntilinkAdmin && isAdmin(client, v.Info.Chat, v.Info.Sender) {
			return
		}
		takeSecurityAction(client, v, "delete", "Video not allowed!")
		return
	}
	
	// Anti-sticker check
	if s.AntiSticker && v.Message.StickerMessage != nil {
		if s.AntilinkAdmin && isAdmin(client, v.Info.Chat, v.Info.Sender) {
			return
		}
		takeSecurityAction(client, v, "delete", "Sticker not allowed!")
		return
	}
}

func containsLink(text string) bool {
	if text == "" {
		return false
	}
	
	text = strings.ToLower(text)
	linkPatterns := []string{
		"http://", "https://", "www.",
		"chat.whatsapp.com/", "t.me/", "youtube.com/",
		"youtu.be/", "instagram.com/", "fb.com/",
		"facebook.com/", "twitter.com/", "x.com/",
	}
	
	for _, pattern := range linkPatterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	
	return false
}

func takeSecurityAction(client *whatsmeow.Client, v *events.Message, action, reason string) {
	switch action {
	case "delete":
		client.DeleteMessage(context.Background(), v.Info.Chat, v.Info.ID)
		reply(client, v.Info.Chat, fmt.Sprintf("🚫 %s (Message deleted)", reason))
		
	case "kick":
		client.UpdateGroupParticipants(context.Background(), v.Info.Chat, 
			[]types.JID{v.Info.Sender}, whatsmeow.ParticipantChangeRemove)
		reply(client, v.Info.Chat, fmt.Sprintf("👢 %s (User kicked)", reason))
		
	case "warn":
		s := getGroupSettings(v.Info.Chat.String())
		senderKey := v.Info.Sender.String()
		
		s.Warnings[senderKey]++
		warnCount := s.Warnings[senderKey]
		
		if warnCount >= 3 {
			client.UpdateGroupParticipants(context.Background(), v.Info.Chat, 
				[]types.JID{v.Info.Sender}, whatsmeow.ParticipantChangeRemove)
			delete(s.Warnings, senderKey)
			reply(client, v.Info.Chat, "🚫 User kicked after 3 warnings!")
		} else {
			reply(client, v.Info.Chat, fmt.Sprintf("⚠️ Warning %d/3: %s", warnCount, reason))
		}
		
		saveGroupSettings(s)
	}
}

func startSecuritySetup(client *whatsmeow.Client, v *events.Message, secType string) {
	if !v.Info.IsGroup || !isAdmin(client, v.Info.Chat, v.Info.Sender) { 
		return 
	}
	setupMap[v.Info.Sender.String()] = &SetupState{
		Type: secType, 
		Stage: 1, 
		GroupID: v.Info.Chat.String(), 
		User: v.Info.Sender.String(),
	}
	reply(client, v.Info.Chat, fmt.Sprintf("╭━━━〔 %s SETUP (1/2) 〕━━━┈\n┃ 🛡️ *Allow Admin?*\n┃\n┃ Type *Yes* or *No*\n╰━━━━━━━━━━━━━━━━━━┈", strings.ToUpper(secType)))
}

func handleSetupResponse(client *whatsmeow.Client, v *events.Message, state *SetupState) {
	txt := strings.ToLower(getText(v.Message))
	s := getGroupSettings(state.GroupID)

	if state.Stage == 1 {
		if txt == "yes" { 
			s.AntilinkAdmin = true 
		} else if txt == "no" { 
			s.AntilinkAdmin = false 
		} else { 
			return 
		}
		state.Stage = 2
		reply(client, v.Info.Chat, "╭━━━〔 ACTION SETUP (2/2) 〕━━━┈\n┃ ⚡ *Choose Action:*\n┃\n┃ *Delete*\n┃ *Kick*\n┃ *Warn*\n╰━━━━━━━━━━━━━━━━━━┈")
		return
	}

	if state.Stage == 2 {
		if strings.Contains(txt, "kick") { 
			s.AntilinkAction = "kick" 
		} else if strings.Contains(txt, "warn") { 
			s.AntilinkAction = "warn" 
		} else { 
			s.AntilinkAction = "delete" 
		}
		switch state.Type {
		case "antilink": s.Antilink = true
		case "antipic": s.AntiPic = true
		case "antivideo": s.AntiVideo = true
		case "antisticker": s.AntiSticker = true
		}
		saveGroupSettings(s)
		delete(setupMap, state.User)
		reply(client, v.Info.Chat, fmt.Sprintf("╭━━━〔 ✅ %s ENABLED 〕━━━┈\n┃ 👑 Admin Allow: %v\n┃ ⚡ Action: %s\n╰━━━━━━━━━━━━━━━━━━┈", 
			strings.ToUpper(state.Type), s.AntilinkAdmin, strings.ToUpper(s.AntilinkAction)))
	}
}

// ==================== گروپ سسٹم ====================
func handleKick(client *whatsmeow.Client, v *events.Message, args []string) {
	groupAction(client, v, args, "remove")
}

func handleAdd(client *whatsmeow.Client, v *events.Message, args []string) {
	if !v.Info.IsGroup || len(args) == 0 { 
		return 
	}
	jid, _ := types.ParseJID(args[0] + "@s.whatsapp.net")
	client.UpdateGroupParticipants(context.Background(), v.Info.Chat, []types.JID{jid}, whatsmeow.ParticipantChangeAdd)
	reply(client, v.Info.Chat, fmt.Sprintf("✅ Added: %s", args[0]))
}

func handlePromote(client *whatsmeow.Client, v *events.Message, args []string) {
	groupAction(client, v, args, "promote")
}

func handleDemote(client *whatsmeow.Client, v *events.Message, args []string) {
	groupAction(client, v, args, "demote")
}

func handleTagAll(client *whatsmeow.Client, v *events.Message, args []string) {
	if !v.Info.IsGroup { 
		return 
	}
	info, _ := client.GetGroupInfo(context.Background(), v.Info.Chat)
	mentions := []string{}
	out := "📣 *TAG ALL*\n"
	
	if len(args) > 0 {
		out += strings.Join(args, " ") + "\n\n"
	}
	
	for _, p := range info.Participants {
		mentions = append(mentions, p.JID.String())
		out += "@" + p.JID.User + "\n"
	}
	
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(out),
			ContextInfo: &waProto.ContextInfo{
				MentionedJID: mentions,
			},
		},
	})
}

func handleHideTag(client *whatsmeow.Client, v *events.Message, args []string) {
	if !v.Info.IsGroup { 
		return 
	}
	info, _ := client.GetGroupInfo(context.Background(), v.Info.Chat)
	mentions := []string{}
	text := strings.Join(args, " ")
	
	for _, p := range info.Participants {
		mentions = append(mentions, p.JID.String())
	}
	
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waProto.ContextInfo{
				MentionedJID: mentions,
			},
		},
	})
}

func handleGroup(client *whatsmeow.Client, v *events.Message, args []string) {
	if !v.Info.IsGroup || len(args) == 0 { 
		return 
	}
	switch args[0] {
	case "close": 
		client.SetGroupAnnounce(context.Background(), v.Info.Chat, true)
		reply(client, v.Info.Chat, "🔒 Group Closed")
	case "open": 
		client.SetGroupAnnounce(context.Background(), v.Info.Chat, false)
		reply(client, v.Info.Chat, "🔓 Group Opened")
	case "link":
		code, _ := client.GetGroupInviteLink(context.Background(), v.Info.Chat, false)
		reply(client, v.Info.Chat, "🔗 https://chat.whatsapp.com/"+code)
	case "revoke":
		client.GetGroupInviteLink(context.Background(), v.Info.Chat, true)
		reply(client, v.Info.Chat, "🔄 Link Revoked")
	}
}

func handleDelete(client *whatsmeow.Client, v *events.Message) {
	if v.Message.ExtendedTextMessage == nil { 
		return 
	}
	ctx := v.Message.ExtendedTextMessage.ContextInfo
	if ctx == nil { 
		return 
	}
	client.RevokeMessage(context.Background(), v.Info.Chat, *ctx.StanzaID)
}

func groupAction(client *whatsmeow.Client, v *events.Message, args []string, action string) {
	if !v.Info.IsGroup { 
		return 
	}
	
	var targetJID types.JID
	if len(args) > 0 {
		num := strings.TrimSpace(args[0])
		if !strings.Contains(num, "@") {
			num = num + "@s.whatsapp.net"
		}
		jid, err := types.ParseJID(num)
		if err != nil {
			reply(client, v.Info.Chat, "❌ Invalid number")
			return
		}
		targetJID = jid
	} else if v.Message.ExtendedTextMessage != nil && v.Message.ExtendedTextMessage.ContextInfo != nil {
		ctx := v.Message.ExtendedTextMessage.ContextInfo
		if ctx.Participant != nil {
			jid, _ := types.ParseJID(*ctx.Participant)
			targetJID = jid
		} else if len(ctx.MentionedJID) > 0 {
			jid, _ := types.ParseJID(ctx.MentionedJID[0])
			targetJID = jid
		}
	}
	
	if targetJID.User == "" {
		reply(client, v.Info.Chat, "⚠️ Mention or reply to user")
		return
	}
	
	// خود کو نہ نکالے
	if targetJID.User == v.Info.Sender.User && action == "remove" {
		reply(client, v.Info.Chat, "❌ Can't kick yourself")
		return
	}
	
	var actionText string
	switch action {
	case "remove":
		client.UpdateGroupParticipants(context.Background(), v.Info.Chat, []types.JID{targetJID}, whatsmeow.ParticipantChangeRemove)
		actionText = "Kicked"
	case "promote":
		client.UpdateGroupParticipants(context.Background(), v.Info.Chat, []types.JID{targetJID}, whatsmeow.ParticipantChangePromote)
		actionText = "Promoted"
	case "demote":
		client.UpdateGroupParticipants(context.Background(), v.Info.Chat, []types.JID{targetJID}, whatsmeow.ParticipantChangeDemote)
		actionText = "Demoted"
	}
	
	reply(client, v.Info.Chat, fmt.Sprintf("✅ %s: %s", actionText, targetJID.User))
}

// ==================== ڈاؤن لوڈر سسٹم ====================
func handleTikTok(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "🎵")
	type R struct { Data struct { Play string `json:"play"` } `json:"data"` }
	var r R
	getJson("https://www.tikwm.com/api/?url="+url, &r)
	if r.Data.Play != "" { 
		sendVideo(client, v.Info.Chat, r.Data.Play, "🎵 TikTok Video") 
	} else {
		reply(client, v.Info.Chat, "❌ Failed to download TikTok")
	}
}

func handleFacebook(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "📘")
	type R struct { BK9 struct { HD string `json:"HD"` } `json:"BK9"`; Status bool `json:"status"` }
	var r R
	getJson("https://bk9.fun/downloader/facebook?url="+url, &r)
	if r.Status { 
		sendVideo(client, v.Info.Chat, r.BK9.HD, "📘 Facebook Video") 
	} else {
		reply(client, v.Info.Chat, "❌ Failed to download Facebook video")
	}
}

func handleInstagram(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "📸")
	type R struct { Video struct { Url string `json:"url"` } `json:"video"` }
	var r R
	getJson("https://api.tiklydown.eu.org/api/download?url="+url, &r)
	if r.Video.Url != "" { 
		sendVideo(client, v.Info.Chat, r.Video.Url, "📸 Instagram Video") 
	} else {
		reply(client, v.Info.Chat, "❌ Failed to download Instagram video")
	}
}

func handlePinterest(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "📌")
	type R struct { BK9 struct { Url string `json:"url"` } `json:"BK9"`; Status bool `json:"status"` }
	var r R
	getJson("https://bk9.fun/downloader/pinterest?url="+url, &r)
	if r.Status {
		sendImage(client, v.Info.Chat, r.BK9.Url, "📌 Pinterest Image")
	} else {
		reply(client, v.Info.Chat, "❌ Failed to download Pinterest image")
	}
}

func handleYouTubeMP3(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "📺")
	type R struct { BK9 struct { Mp3 string `json:"mp3"` } `json:"BK9"`; Status bool `json:"status"` }
	var r R
	getJson("https://bk9.fun/downloader/youtube?url="+url, &r)
	if r.Status {
		sendDocument(client, v.Info.Chat, r.BK9.Mp3, "audio.mp3", "audio/mpeg")
	} else {
		reply(client, v.Info.Chat, "❌ Failed to download YouTube audio")
	}
}

func handleYouTubeMP4(client *whatsmeow.Client, v *events.Message, url string) {
	react(client, v.Info.Chat, v.Info.ID, "📺")
	type R struct { BK9 struct { Mp4 string `json:"mp4"` } `json:"BK9"`; Status bool `json:"status"` }
	var r R
	getJson("https://bk9.fun/downloader/youtube?url="+url, &r)
	if r.Status {
		sendVideo(client, v.Info.Chat, r.BK9.Mp4, "📺 YouTube Video")
	} else {
		reply(client, v.Info.Chat, "❌ Failed to download YouTube video")
	}
}

// ==================== ٹولز سسٹم ====================
func handleSticker(client *whatsmeow.Client, v *events.Message) {
	react(client, v.Info.Chat, v.Info.ID, "🎨")
	data, err := downloadMedia(client, v.Message)
	if err != nil { 
		reply(client, v.Info.Chat, "❌ No media found")
		return 
	}
	ioutil.WriteFile("temp.jpg", data, 0644)
	exec.Command("ffmpeg", "-y", "-i", "temp.jpg", "-vcodec", "libwebp", "temp.webp").Run()
	b, _ := ioutil.ReadFile("temp.webp")
	up, _ := client.Upload(context.Background(), b, whatsmeow.MediaImage)
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		StickerMessage: &waProto.StickerMessage{
			URL: proto.String(up.URL), 
			DirectPath: proto.String(up.DirectPath), 
			MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, 
			FileSHA256: up.FileSHA256, 
			Mimetype: proto.String("image/webp"),
		}})
	os.Remove("temp.jpg")
	os.Remove("temp.webp")
}

func handleToImg(client *whatsmeow.Client, v *events.Message) {
	react(client, v.Info.Chat, v.Info.ID, "🖼️")
	data, err := downloadMedia(client, v.Message)
	if err != nil { 
		return 
	}
	ioutil.WriteFile("temp.webp", data, 0644)
	exec.Command("ffmpeg", "-y", "-i", "temp.webp", "temp.png").Run()
	b, _ := ioutil.ReadFile("temp.png")
	up, _ := client.Upload(context.Background(), b, whatsmeow.MediaImage)
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ImageMessage: &waProto.ImageMessage{
			URL: proto.String(up.URL), 
			DirectPath: proto.String(up.DirectPath), 
			MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, 
			FileSHA256: up.FileSHA256, 
			Mimetype: proto.String("image/png"),
		}})
	os.Remove("temp.webp")
	os.Remove("temp.png")
}

func handleToVideo(client *whatsmeow.Client, v *events.Message) {
	react(client, v.Info.Chat, v.Info.ID, "🎥")
	data, err := downloadMedia(client, v.Message)
	if err != nil { 
		return 
	}
	ioutil.WriteFile("temp.webp", data, 0644)
	exec.Command("ffmpeg", "-y", "-i", "temp.webp", "temp.mp4").Run()
	d, _ := ioutil.ReadFile("temp.mp4")
	up, _ := client.Upload(context.Background(), d, whatsmeow.MediaVideo)
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		VideoMessage: &waProto.VideoMessage{
			URL: proto.String(up.URL), 
			DirectPath: proto.String(up.DirectPath), 
			MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, 
			FileSHA256: up.FileSHA256, 
			Mimetype: proto.String("video/mp4"),
		}})
	os.Remove("temp.webp")
	os.Remove("temp.mp4")
}

func handleRemoveBG(client *whatsmeow.Client, v *events.Message) {
	react(client, v.Info.Chat, v.Info.ID, "✂️")
	d, _ := downloadMedia(client, v.Message)
	u := uploadToCatbox(d)
	sendImage(client, v.Info.Chat, "https://bk9.fun/tools/removebg?url="+u, "✂️ Background Removed")
}

func handleRemini(client *whatsmeow.Client, v *events.Message) {
	react(client, v.Info.Chat, v.Info.ID, "✨")
	d, _ := downloadMedia(client, v.Message)
	u := uploadToCatbox(d)
	type R struct{Url string `json:"url"`}
	var r R
	getJson("https://remini.mobilz.pw/enhance?url="+u, &r)
	sendImage(client, v.Info.Chat, r.Url, "✨ Enhanced Image")
}

func handleToURL(client *whatsmeow.Client, v *events.Message) {
	d, _ := downloadMedia(client, v.Message)
	reply(client, v.Info.Chat, "🔗 "+uploadToCatbox(d))
}

func handleWeather(client *whatsmeow.Client, v *events.Message, city string) {
	react(client, v.Info.Chat, v.Info.ID, "🌦️")
	r, _ := http.Get("https://wttr.in/"+city+"?format=%C+%t")
	d, _ := ioutil.ReadAll(r.Body)
	reply(client, v.Info.Chat, fmt.Sprintf("🌤️ Weather in %s:\n%s", city, string(d)))
}

func handleTranslate(client *whatsmeow.Client, v *events.Message, args []string) {
	react(client, v.Info.Chat, v.Info.ID, "🌍")
	t := strings.Join(args, " ")
	if t == "" { 
		q := v.Message.ExtendedTextMessage.GetContextInfo().GetQuotedMessage()
		if q != nil { 
			t = q.GetConversation() 
		}
	}
	r, _ := http.Get(fmt.Sprintf("https://translate.googleapis.com/translate_a/single?client=gtx&sl=auto&tl=ur&dt=t&q=%s", url.QueryEscape(t)))
	var res []interface{}
	json.NewDecoder(r.Body).Decode(&res)
	if len(res)>0 { 
		reply(client, v.Info.Chat, res[0].([]interface{})[0].([]interface{})[0].(string)) 
	}
}

func handleVV(client *whatsmeow.Client, v *events.Message) {
	react(client, v.Info.Chat, v.Info.ID, "🫣")
	quoted := v.Message.ExtendedTextMessage.GetContextInfo().GetQuotedMessage()
	if quoted == nil { 
		reply(client, v.Info.Chat, "⚠️ Reply to ViewOnce media.")
		return 
	}
	data, err := downloadMedia(client, &waProto.Message{
		ImageMessage: quoted.ImageMessage, 
		VideoMessage: quoted.VideoMessage, 
		ViewOnceMessage: quoted.ViewOnceMessage, 
		ViewOnceMessageV2: quoted.ViewOnceMessageV2,
	})
	if err != nil { 
		reply(client, v.Info.Chat, "❌ Failed to download.")
		return 
	}
	if quoted.ImageMessage != nil || (quoted.ViewOnceMessage != nil && quoted.ViewOnceMessage.Message.ImageMessage != nil) {
		up, _ := client.Upload(context.Background(), data, whatsmeow.MediaImage)
		client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
			ImageMessage: &waProto.ImageMessage{
				URL: proto.String(up.URL), 
				DirectPath: proto.String(up.DirectPath), 
				MediaKey: up.MediaKey,
				FileEncSHA256: up.FileEncSHA256, 
				FileSHA256: up.FileSHA256, 
				Mimetype: proto.String("image/jpeg"),
			}})
	} else {
		up, _ := client.Upload(context.Background(), data, whatsmeow.MediaVideo)
		client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
			VideoMessage: &waProto.VideoMessage{
				URL: proto.String(up.URL), 
				DirectPath: proto.String(up.DirectPath), 
				MediaKey: up.MediaKey,
				FileEncSHA256: up.FileEncSHA256, 
				FileSHA256: up.FileSHA256, 
				Mimetype: proto.String("video/mp4"),
			}})
	}
}

// ==================== ہیلپر فنکشنز ====================
func react(client *whatsmeow.Client, chat types.JID, msgID types.MessageID, emoji string) {
	client.SendMessage(context.Background(), chat, &waProto.Message{
		ReactionMessage: &waProto.ReactionMessage{
			Key: &waProto.MessageKey{
				RemoteJID: proto.String(chat.String()),
				ID:        proto.String(string(msgID)),
				FromMe:    proto.Bool(true),
			},
			Text:              proto.String(emoji),
			SenderTimestampMS: proto.Int64(time.Now().UnixMilli()),
		},
	})
}

func reply(client *whatsmeow.Client, chat types.JID, text string) {
	client.SendMessage(context.Background(), chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(text),
		},
	})
}

func getText(m *waProto.Message) string {
	if m.Conversation != nil {
		return *m.Conversation
	}
	if m.ExtendedTextMessage != nil && m.ExtendedTextMessage.Text != nil {
		return *m.ExtendedTextMessage.Text
	}
	if m.ImageMessage != nil && m.ImageMessage.Caption != nil {
		return *m.ImageMessage.Caption
	}
	if m.VideoMessage != nil && m.VideoMessage.Caption != nil {
		return *m.VideoMessage.Caption
	}
	return ""
}

func isOwner(client *whatsmeow.Client, sender types.JID) bool {
	if client.Store.ID == nil {
		return false
	}

	botNum := cleanNumber(client.Store.ID.User)
	senderNum := cleanNumber(sender.User)
	
	return botNum == senderNum
}

func cleanNumber(num string) string {
	num = strings.ReplaceAll(num, "+", "")
	if strings.Contains(num, ":") {
		num = strings.Split(num, ":")[0]
	}
	if strings.Contains(num, "@") {
		num = strings.Split(num, "@")[0]
	}
	return num
}

func canExecute(client *whatsmeow.Client, v *events.Message, cmd string) bool {
	if isOwner(client, v.Info.Sender) {
		return true
	}
	
	if !v.Info.IsGroup {
		return true
	}
	
	s := getGroupSettings(v.Info.Chat.String())
	if s.Mode == "private" {
		return false
	}
	if s.Mode == "admin" {
		return isAdmin(client, v.Info.Chat, v.Info.Sender)
	}
	return true
}

func isAdmin(client *whatsmeow.Client, chat, user types.JID) bool {
	info, err := client.GetGroupInfo(context.Background(), chat)
	if err != nil {
		return false
	}
	
	for _, p := range info.Participants {
		if p.JID.User == user.User && (p.IsAdmin || p.IsSuperAdmin) {
			return true
		}
	}
	return false
}

func getGroupSettings(id string) *GroupSettings {
	cacheMutex.RLock()
	if s, ok := groupCache[id]; ok {
		cacheMutex.RUnlock()
		return s
	}
	cacheMutex.RUnlock()
	
	s := &GroupSettings{
		ChatID:         id,
		Mode:           "public",
		Antilink:       false,
		AntilinkAdmin:  true,
		AntilinkAction: "delete",
		AntiPic:        false,
		AntiVideo:      false,
		AntiSticker:    false,
		Warnings:       make(map[string]int),
	}
	
	cacheMutex.Lock()
	groupCache[id] = s
	cacheMutex.Unlock()
	
	return s
}

func saveGroupSettings(s *GroupSettings) {
	cacheMutex.Lock()
	groupCache[s.ChatID] = s
	cacheMutex.Unlock()
}

// ==================== میڈیا ہیلپرز ====================
func getJson(url string, target interface{}) error { 
	r, err := http.Get(url)
	if err != nil { 
		return err 
	}
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target) 
}

func downloadMedia(client *whatsmeow.Client, m *waProto.Message) ([]byte, error) { 
	var d whatsmeow.DownloadableMessage
	if m.ImageMessage != nil { 
		d = m.ImageMessage 
	} else if m.VideoMessage != nil { 
		d = m.VideoMessage 
	} else if m.DocumentMessage != nil { 
		d = m.DocumentMessage 
	} else if m.StickerMessage != nil { 
		d = m.StickerMessage 
	} else if m.ExtendedTextMessage != nil && m.ExtendedTextMessage.ContextInfo != nil { 
		q := m.ExtendedTextMessage.ContextInfo.QuotedMessage
		if q != nil { 
			if q.ImageMessage != nil { 
				d = q.ImageMessage 
			} else if q.VideoMessage != nil { 
				d = q.VideoMessage 
			} else if q.StickerMessage != nil { 
				d = q.StickerMessage 
			} 
		} 
	}
	if d == nil { 
		return nil, fmt.Errorf("no media") 
	}
	return client.Download(context.Background(), d) 
}

func uploadToCatbox(d []byte) string { 
	b := new(bytes.Buffer)
	w := multipart.NewWriter(b)
	p, _ := w.CreateFormFile("fileToUpload", "f.jpg")
	p.Write(d)
	w.WriteField("reqtype", "fileupload")
	w.Close()
	r, _ := http.Post("https://catbox.moe/user/api.php", w.FormDataContentType(), b)
	res, _ := ioutil.ReadAll(r.Body)
	return string(res) 
}

func sendVideo(client *whatsmeow.Client, chat types.JID, url, c string) { 
	r, _ := http.Get(url)
	d, _ := ioutil.ReadAll(r.Body)
	up, _ := client.Upload(context.Background(), d, whatsmeow.MediaVideo)
	client.SendMessage(context.Background(), chat, &waProto.Message{
		VideoMessage: &waProto.VideoMessage{
			URL: proto.String(up.URL), 
			DirectPath: proto.String(up.DirectPath), 
			MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, 
			FileSHA256: up.FileSHA256, 
			Mimetype: proto.String("video/mp4"), 
			Caption: proto.String(c),
		}}) 
}

func sendImage(client *whatsmeow.Client, chat types.JID, url, c string) { 
	r, _ := http.Get(url)
	d, _ := ioutil.ReadAll(r.Body)
	up, _ := client.Upload(context.Background(), d, whatsmeow.MediaImage)
	client.SendMessage(context.Background(), chat, &waProto.Message{
		ImageMessage: &waProto.ImageMessage{
			URL: proto.String(up.URL), 
			DirectPath: proto.String(up.DirectPath), 
			MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, 
			FileSHA256: up.FileSHA256, 
			Mimetype: proto.String("image/jpeg"), 
			Caption: proto.String(c),
		}}) 
}

func sendDocument(client *whatsmeow.Client, chat types.JID, url, n, m string) { 
	r, _ := http.Get(url)
	d, _ := ioutil.ReadAll(r.Body)
	up, _ := client.Upload(context.Background(), d, whatsmeow.MediaDocument)
	client.SendMessage(context.Background(), chat, &waProto.Message{
		DocumentMessage: &waProto.DocumentMessage{
			URL: proto.String(up.URL), 
			DirectPath: proto.String(up.DirectPath), 
			MediaKey: up.MediaKey,
			FileEncSHA256: up.FileEncSHA256, 
			FileSHA256: up.FileSHA256, 
			Mimetype: proto.String(m), 
			FileName: proto.String(n),
		}}) 
}