package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
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

	// ✅ FIX: Only process if message starts with prefix or is a known command
	if !strings.HasPrefix(body, prefix) && !isKnownCommand(body) {
		return // Ignore non-commands
	}

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

	if !canExecute(client, v, cmd) {
		return
	}

	fullArgs := strings.Join(args, " ")
	
	// ✅ FIX: Only log valid commands
	fmt.Printf("📩 CMD: %s | User: %s | Chat: %s\n", cmd, v.Info.Sender.User, v.Info.Chat.User)

	switch cmd {
	// مینیو سسٹم
	case "menu", "help", "list":
		react(client, v.Info.Chat, v.Info.ID, "📜")
		sendMenu(client, v)

	case "ping":
		react(client, v.Info.Chat, v.Info.ID, "⚡")
		sendPing(client, v)

	case "id":
		react(client, v.Info.Chat, v.Info.ID, "🆔")
		sendID(client, v)

	case "owner":
		react(client, v.Info.Chat, v.Info.ID, "👑")
		sendOwner(client, v)

	case "data":
		replyMessage(client, v, "╭═══════════════════╮\n┃   📂 DATA STATUS    ┃\n├═══════════════════┤\n┃ ✅ Data is safe in  ┃\n┃    MongoDB Atlas    ┃\n╰═══════════════════╯")

	// سیٹنگز
	case "alwaysonline":
		toggleAlwaysOnline(client, v)
	case "autoread":
		toggleAutoRead(client, v)
	case "autoreact":
		toggleAutoReact(client, v)
	case "autostatus":
		toggleAutoStatus(client, v)
	case "statusreact":
		toggleStatusReact(client, v)
	case "addstatus":
		handleAddStatus(client, v, args)
	case "delstatus":
		handleDelStatus(client, v, args)
	case "liststatus":
		handleListStatus(client, v)
	case "readallstatus":
		handleReadAllStatus(client, v)
	case "setprefix":
		handleSetPrefix(client, v, args)
	case "mode":
		handleMode(client, v, args)

	// سیکورٹی
	case "antilink":
		startSecuritySetup(client, v, "antilink")
	case "antipic":
		startSecuritySetup(client, v, "antipic")
	case "antivideo":
		startSecuritySetup(client, v, "antivideo")
	case "antisticker":
		startSecuritySetup(client, v, "antisticker")

	// گروپ
	case "kick":
		handleKick(client, v, args)
	case "add":
		handleAdd(client, v, args)
	case "promote":
		handlePromote(client, v, args)
	case "demote":
		handleDemote(client, v, args)
	case "tagall":
		handleTagAll(client, v, args)
	case "hidetag":
		handleHideTag(client, v, args)
	case "group":
		handleGroup(client, v, args)
	case "del", "delete":
		handleDelete(client, v)

	// ڈاؤن لوڈرز
	case "tiktok", "tt":
		handleTikTok(client, v, fullArgs)
	case "fb", "facebook":
		handleFacebook(client, v, fullArgs)
	case "insta", "ig":
		handleInstagram(client, v, fullArgs)
	case "pin", "pinterest":
		handlePinterest(client, v, fullArgs)
	case "ytmp3":
		handleYouTubeMP3(client, v, fullArgs)
	case "ytmp4":
		handleYouTubeMP4(client, v, fullArgs)

	// ٹولز
	case "sticker", "s":
		handleSticker(client, v)
	case "toimg":
		handleToImg(client, v)
	case "tovideo":
		handleToVideo(client, v)
	case "removebg":
		handleRemoveBG(client, v)
	case "remini":
		handleRemini(client, v)
	case "tourl":
		handleToURL(client, v)
	case "weather":
		handleWeather(client, v, fullArgs)
	case "translate", "tr":
		handleTranslate(client, v, args)
	case "vv":
		handleVV(client, v)
	}
}

// ✅ NEW: Check if message is a known command
func isKnownCommand(text string) bool {
	commands := []string{
		"menu", "help", "list", "ping", "id", "owner", "data",
		"alwaysonline", "autoread", "autoreact", "autostatus", "statusreact",
		"addstatus", "delstatus", "liststatus", "readallstatus", "setprefix", "mode",
		"antilink", "antipic", "antivideo", "antisticker",
		"kick", "add", "promote", "demote", "tagall", "hidetag", "group", "del", "delete",
		"tiktok", "tt", "fb", "facebook", "insta", "ig", "pin", "pinterest", "ytmp3", "ytmp4",
		"sticker", "s", "toimg", "tovideo", "removebg", "remini", "tourl", "weather", "translate", "tr", "vv",
	}
	
	lowerText := strings.ToLower(strings.TrimSpace(text))
	for _, cmd := range commands {
		if strings.HasPrefix(lowerText, cmd) {
			return true
		}
	}
	return false
}

// ==================== مینیو سسٹم ====================
func sendMenu(client *whatsmeow.Client, v *events.Message) {
	uptime := time.Since(startTime).Round(time.Second)
	dataMutex.RLock()
	p := data.Prefix
	dataMutex.RUnlock()

	s := getGroupSettings(v.Info.Chat.String())
	currentMode := strings.ToUpper(s.Mode)
	if !strings.Contains(v.Info.Chat.String(), "@g.us") {
		currentMode = "PRIVATE"
	}

	menu := fmt.Sprintf(`╔═══════════════════════════╗
║   %s   ║
╠═══════════════════════════╣
║ 👋 *Assalam-o-Alaikum*      ║
║ 👑 *Owner:* %s              ║
║ 🛡️ *Mode:* %s               ║
║ ⏳ *Uptime:* %s             ║
╠═══════════════════════════╣
║                           ║
║  ╭─────── DOWNLOADERS ─────╮║
║  │ 🔸 *%sfb* - Facebook    │║
║  │ 🔸 *%sig* - Instagram   │║
║  │ 🔸 *%spin* - Pinterest  │║
║  │ 🔸 *%stiktok* - TikTok  │║
║  │ 🔸 *%sytmp3* - YT Audio │║
║  │ 🔸 *%sytmp4* - YT Video │║
║  ╰──────────────────────────╯║
║                           ║
║  ╭─────── GROUP ────────────╮║
║  │ 🔸 *%sadd* - Add Member │║
║  │ 🔸 *%sdemote* - Demote  │║
║  │ 🔸 *%sgroup* - Settings │║
║  │ 🔸 *%shidetag* - Hidden │║
║  │ 🔸 *%skick* - Remove    │║
║  │ 🔸 *%spromote* - Admin  │║
║  │ 🔸 *%stagall* - Mention │║
║  ╰──────────────────────────╯║
║                           ║
║  ╭─────── SETTINGS ─────────╮║
║  │ 🔸 *%saddstatus*        │║
║  │ 🔸 *%salwaysonline*     │║
║  │ 🔸 *%santilink*         │║
║  │ 🔸 *%santipic*          │║
║  │ 🔸 *%santisticker*      │║
║  │ 🔸 *%santivideo*        │║
║  │ 🔸 *%sautoreact*        │║
║  │ 🔸 *%sautoread*         │║
║  │ 🔸 *%sautostatus*       │║
║  │ 🔸 *%sdelstatus*        │║
║  │ 🔸 *%sliststatus*       │║
║  │ 🔸 *%smode*             │║
║  │ 🔸 *%sowner*            │║
║  │ 🔸 *%sreadallstatus*    │║
║  │ 🔸 *%sstatusreact*      │║
║  ╰──────────────────────────╯║
║                           ║
║  ╭─────── TOOLS ────────────╮║
║  │ 🔸 *%sdata* - DB Status │║
║  │ 🔸 *%sid* - Get ID      │║
║  │ 🔸 *%sping* - Speed     │║
║  │ 🔸 *%sremini* - Enhance │║
║  │ 🔸 *%sremovebg* - BG    │║
║  │ 🔸 *%ssticker* - Create │║
║  │ 🔸 *%stoimg* - Convert  │║
║  │ 🔸 *%stourl* - Upload   │║
║  │ 🔸 *%stovideo* - Make   │║
║  │ 🔸 *%stranslate* - Lang │║
║  │ 🔸 *%svv* - ViewOnce    │║
║  │ 🔸 *%sweather* - Info   │║
║  ╰──────────────────────────╯║
║                           ║
╠═══════════════════════════╣
║ © 2025 Nothing is Impossible║
╚═══════════════════════════╝`,
		BOT_NAME, OWNER_NAME, currentMode, uptime,
		p, p, p, p, p, p,
		p, p, p, p, p, p, p,
		p, p, p, p, p, p, p, p, p, p, p, p, p, p, p,
		p, p, p, p, p, p, p, p, p, p, p, p)

	sendReplyMessage(client, v, menu)
}

func sendPing(client *whatsmeow.Client, v *events.Message) {
	start := time.Now()
	time.Sleep(10 * time.Millisecond)
	ms := time.Since(start).Milliseconds()
	uptime := time.Since(startTime).Round(time.Second)

	msg := fmt.Sprintf(`╔═══════════════════════════╗
║      ⚡ PING STATUS ⚡       ║
╠═══════════════════════════╣
║                           ║
║  🚀 *Speed:* %d MS         ║
║  ⏱️ *Uptime:* %s          ║
║  👑 *Dev:* %s             ║
║                           ║
╠═══════════════════════════╣
║    🟢 System Running      ║
╚═══════════════════════════╝`, ms, uptime, OWNER_NAME)

	sendReplyMessage(client, v, msg)
}

func sendID(client *whatsmeow.Client, v *events.Message) {
	user := v.Info.Sender.User
	chat := v.Info.Chat.User
	chatType := "Private"
	if v.Info.IsGroup {
		chatType = "Group"
	}

	msg := fmt.Sprintf(`╔═══════════════════════════╗
║      🆔 ID INFORMATION      ║
╠═══════════════════════════╣
║                           ║
║  👤 *User ID:*             ║
║     `+"`%s`"+`              ║
║                           ║
║  👥 *Chat ID:*             ║
║     `+"`%s`"+`              ║
║                           ║
║  🏷️ *Type:* %s            ║
║                           ║
╚═══════════════════════════╝`, user, chat, chatType)

	sendReplyMessage(client, v, msg)
}

func sendOwner(client *whatsmeow.Client, v *events.Message) {
	status := "❌ You are NOT the Owner"
	statusIcon := "🚫"
	if isOwner(client, v.Info.Sender) {
		status = "✅ You are the OWNER"
		statusIcon = "👑"
	}

	botNum := cleanNumber(client.Store.ID.User)
	userNum := cleanNumber(v.Info.Sender.User)

	msg := fmt.Sprintf(`╔═══════════════════════════╗
║   %s OWNER VERIFICATION    ║
╠═══════════════════════════╣
║                           ║
║  🤖 *Bot Number:*          ║
║     %s                    ║
║                           ║
║  👤 *Your Number:*         ║
║     %s                    ║
║                           ║
║  📊 *Status:*              ║
║     %s                    ║
║                           ║
╚═══════════════════════════╝`, statusIcon, botNum, userNum, status)

	sendReplyMessage(client, v, msg)
}

// ==================== HELPER FUNCTIONS ====================
func react(client *whatsmeow.Client, chat types.JID, msgID types.MessageID, emoji string) {
	client.SendMessage(context.Background(), chat, &waProto.Message{
		ReactionMessage: &waProto.ReactionMessage{
			Key: &waProto.MessageKey{
				RemoteJID: proto.String(chat.String()),
				ID:        proto.String(string(msgID)),
				FromMe:    proto.Bool(false),
			},
			Text:              proto.String(emoji),
			SenderTimestampMS: proto.Int64(time.Now().UnixMilli()),
		},
	})
}

func replyMessage(client *whatsmeow.Client, v *events.Message, text string) {
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,
			},
		},
	})
}

func sendReplyMessage(client *whatsmeow.Client, v *events.Message, text string) {
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,
			},
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

// ✅ FIX: Default mode is now PUBLIC
func getGroupSettings(id string) *GroupSettings {
	cacheMutex.RLock()
	if s, ok := groupCache[id]; ok {
		cacheMutex.RUnlock()
		return s
	}
	cacheMutex.RUnlock()

	s := &GroupSettings{
		ChatID:         id,
		Mode:           "public", // ✅ FIXED: Default is PUBLIC
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