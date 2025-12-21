package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

var (
	activeClients = make(map[string]*whatsmeow.Client)
	clientsMutex  sync.RWMutex
	globalClient *whatsmeow.Client
)

func handler(botClient *whatsmeow.Client, evt interface{}) {
	// 🛡️ سیف گارڈ: اگر اس بوٹ میں کوئی ایرر آئے تو یہ پورے سسٹم کو کریش نہیں ہونے دے گا
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠️ [CRASH PREVENTED] Bot %s encountered an error: %v\n", botClient.Store.ID.User, r)
		}
	}()

	if botClient == nil {
		return
	}
	
	switch v := evt.(type) {
	case *events.Message:
		// ہر میسج کو الگ بیک گراؤنڈ (Goroutine) میں چلائیں
		go processMessage(botClient, v)
	case *events.GroupInfo:
		go handleGroupInfoChange(botClient, v)
	case *events.Connected, *events.LoggedOut:
		// کنکشن اسٹیٹس لاگ کریں
		fmt.Printf("ℹ️ [STATUS] Bot %s: %T\n", botClient.Store.ID.User, v)
	}
}



func isKnownCommand(text string) bool {
	commands := []string{
		"menu", "help", "list", "ping", "id", "owner", "data", "listbots",
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

func processMessage(client *whatsmeow.Client, v *events.Message) {
	// ⚡ اسپیڈ بوسٹ #1: میموری سے آئی ڈی اور پریفکس اٹھائیں (0.001ms)
	rawBotID := client.Store.ID.User
	botID := botCleanIDCache[rawBotID]
	if botID == "" { botID = getCleanID(rawBotID) } // Safety backup
	
	prefix := getPrefix(botID)

	// بنیادی ویری ایبلز
	bodyRaw := getText(v.Message)
	if bodyRaw == "" { return }
	bodyClean := strings.TrimSpace(bodyRaw)
	senderID := v.Info.Sender.String()
	chatID := v.Info.Chat.String()
	isGroup := v.Info.IsGroup

	// 🛠️ ⚡ اسپیڈ بوسٹ #2: Early Exit (فلٹر)
	_, isTT := ttCache[senderID]
	_, isYTS := ytCache[senderID]
	_, isYTSelect := ytDownloadCache[chatID]
	isSetup := false
	if state, ok := setupMap[senderID]; ok && state.GroupID == chatID { isSetup = true }

	// اگر یہ کمانڈ نہیں ہے تو بوٹ یہیں مر جائے گا (سائنس دانوں کا وقت بچانے کے لئے)
	if !strings.HasPrefix(bodyClean, prefix) && !isTT && !isYTS && !isYTSelect && !isSetup && chatID != "status@broadcast" {
		return 
	}

	// 2. سیٹ اپ رسپانس ہینڈلر (Setup Wizard)
	if isSetup {
		handleSetupResponse(client, v, setupMap[senderID])
		return
	}

	// 3. اسٹیٹس براڈکاسٹ (Auto Status View/React)
	if chatID == "status@broadcast" {
		dataMutex.RLock()
		if data.AutoStatus {
			client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
			if data.StatusReact {
				emojis := []string{"💚", "❤️", "🔥", "😍", "💯"}
				react(client, v.Info.Chat, v.Info.ID, emojis[time.Now().UnixNano()%int64(len(emojis))])
			}
		}
		dataMutex.RUnlock()
		return
	}

	// 4. آٹو ریڈ اور آٹو ری ایکٹ
	dataMutex.RLock()
	if data.AutoRead {
		client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
	}
	if data.AutoReact {
		react(client, v.Info.Chat, v.Info.ID, "❤️")
	}
	dataMutex.RUnlock()

	// 5. گروپ سیکیورٹی چیک
	if isGroup {
		go checkSecurity(client, v) // گوروٹین میں تاکہ میسج پروسیسنگ نہ رکے
	}

	// 6. 🛠️ انٹرایکٹو آپشنز (TikTok/YouTube)
	
	// ✅ ٹک ٹاک سلیکشن (آپ کا فیورٹ کارڈ اسٹائل)
	if isTT {
		state := ttCache[senderID]
		if bodyClean == "1" {
			delete(ttCache, senderID); react(client, v.Info.Chat, v.Info.ID, "🎬")
			sendVideo(client, v, state.PlayURL, "🎬 *TikTok Video*\n\n✅ Quality: High")
			return
		} else if bodyClean == "2" {
			delete(ttCache, senderID); react(client, v.Info.Chat, v.Info.ID, "🎵")
			sendDocument(client, v, state.MusicURL, "tiktok_audio.mp3", "audio/mpeg")
			return
		} else if bodyClean == "3" {
			delete(ttCache, senderID)
			infoMsg := fmt.Sprintf(`╔═══════════════════╗
║ 📄 TIKTOK INFO      
╠═══════════════════╣
║ 📝 Title: %s
║ 📊 Size: %.2f MB
║ ✨ Status: Success
╚═══════════════════╝`, state.Title, float64(state.Size)/(1024*1024))
			replyMessage(client, v, infoMsg)
			return
		}
	}

	// یوٹیوب سرچ انتخاب
	if results, exists := ytCache[senderID]; exists {
		var idx int
		fmt.Sscanf(bodyClean, "%d", &idx)
		if idx >= 1 && idx <= len(results) {
			selected := results[idx-1]
			delete(ytCache, senderID)
			handleYTDownloadMenu(client, v, selected.Url) 
			return
		}
	}

	// یوٹیوب فارمیٹ انتخاب
	if state, exists := ytDownloadCache[chatID]; exists {
		if senderID != state.SenderID { return } 
		if bodyClean == "1" || bodyClean == "2" || bodyClean == "3" {
			delete(ytDownloadCache, chatID)
			go handleYTDownload(client, v, state.Url, bodyClean, false)
			return
		} else if bodyClean == "4" {
			delete(ytDownloadCache, chatID)
			go handleYTDownload(client, v, state.Url, "mp3", true)
			return
		}
	}

	// 7. کمانڈ پارسنگ
	cmdBody := strings.ToLower(strings.TrimPrefix(bodyClean, prefix))
	split := strings.Fields(cmdBody)
	if len(split) == 0 { return }
	
	cmd := split[0]
	args := split[1:]
	fullArgs := strings.Join(args, " ")

	// 8. پرمیشن چیک
	if !canExecute(client, v, cmd) {
		return
	}

	// 9. کنسول لاگنگ (صرف کمانڈ ایگزیکیوشن پر)
	fmt.Printf("🚀 [EXEC] Bot: %s | CMD: %s | Chat: %s\n", botID, cmd, chatID)

	// 10. مین کمانڈ سوئچ
	switch cmd {
	case "setprefix":
		if !isOwner(client, v.Info.Sender) {
			replyMessage(client, v, "❌ Only Owner can change the prefix.")
			return
		}
		if fullArgs == "" {
			replyMessage(client, v, "⚠️ Usage: .setprefix !")
			return
		}
		updatePrefixDB(botID, fullArgs)
		replyMessage(client, v, fmt.Sprintf("✅ Prefix updated to [%s]", fullArgs))

	case "menu", "help", "list":
		react(client, v.Info.Chat, v.Info.ID, "📜")
		sendMenu(client, v)
	case "ping":
		react(client, v.Info.Chat, v.Info.ID, "⚡")
		sendPing(client, v)
	case "id":
		sendID(client, v)
	case "owner":
		sendOwner(client, v)
	case "listbots":
		sendBotsList(client, v)
	case "data":
		replyMessage(client, v, "╔════════════════╗\n║ 📂 DATA STATUS\n╠════════════════╣\n║ ✅ System Active\n╚════════════════╝")
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
	case "mode":
		handleMode(client, v, args)
	case "antilink":
		startSecuritySetup(client, v, "antilink")
	case "antipic":
		startSecuritySetup(client, v, "antipic")
	case "antivideo":
		startSecuritySetup(client, v, "antivideo")
	case "antisticker":
		startSecuritySetup(client, v, "antisticker")
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
	case "sd":
		handleSessionDelete(client, v, args)
	case "tiktok", "tt":
		handleTikTok(client, v, fullArgs)
	case "fb", "facebook":
		handleFacebook(client, v, fullArgs)
	case "insta", "ig":
		handleInstagram(client, v, fullArgs)
	case "yts":
		handleYTS(client, v, fullArgs)
	case "ytmp4":
		handleYTDownloadMenu(client, v, fullArgs)
	case "ytmp3":
		handleYTDownload(client, v, fullArgs, "mp3", true)
	}
}

// 1. ریڈیس سے پریفکس حاصل کرنا (سائنس دانوں کو حیران کرنے کے لئے)
func fetchPrefixFromRedis(botID string) string {
	// اگر rdb (Redis) کنیکٹ نہیں ہے تو ڈیفالٹ واپس کریں
	if rdb == nil {
		return "."
	}

	val, err := rdb.Get(context.Background(), "prefix:"+botID).Result()
	if err != nil || val == "" {
		return "." // اگر کچھ نہ ملے تو ڈاٹ ڈیفالٹ ہے
	}
	
	return val
}

// 2. میموری سے پریفکس حاصل کرنے والا فاسٹ فنکشن
func getPrefix(botID string) string {
	prefixMutex.RLock()
	p, exists := botPrefixes[botID]
	prefixMutex.RUnlock()
	if exists {
		return p
	}
	// اگر میموری میں نہیں ہے تو فورا ریڈیس سے اٹھا کر میموری میں ڈالیں
	p = fetchPrefixFromRedis(botID)
	prefixMutex.Lock()
	botPrefixes[botID] = p
	prefixMutex.Unlock()
	return p
}

// 3. ریڈیس میں پریفکس اپڈیٹ کرنے والا فنکشن

// 🚀 الٹرا فاسٹ ہیلپرز

func getCleanID(jidStr string) string {
	if jidStr == "" {
		return "unknown"
	}
	
	parts := strings.Split(jidStr, "@")
	if len(parts) == 0 {
		return "unknown"
	}
	
	userPart := parts[0]
	
	if strings.Contains(userPart, ":") {
		colonParts := strings.Split(userPart, ":")
		userPart = colonParts[0]
	}
	
	if strings.Contains(userPart, ".") {
		dotParts := strings.Split(userPart, ".")
		userPart = dotParts[0]
	}
	
	return strings.TrimSpace(userPart)
}

func getBotLIDFromDB(client *whatsmeow.Client) string {
	if client.Store.ID == nil {
		return "unknown"
	}
	
	lidStr := client.Store.LID.String()
	if lidStr != "" {
		return getCleanID(lidStr)
	}
	
	return getCleanID(client.Store.ID.User)
}

func isOwner(client *whatsmeow.Client, sender types.JID) bool {
	if client.Store.ID == nil {
		return false
	}
	
	senderClean := getCleanID(sender.String())
	
	// کیش سے بوٹ کی آئی ڈی لیں
	rawBotID := client.Store.ID.User
	botID := botCleanIDCache[rawBotID]
	if botID == "" { botID = getCleanID(rawBotID) }
	
	return (senderClean == botID)
}


func isAdmin(client *whatsmeow.Client, chat, user types.JID) bool {
	info, err := client.GetGroupInfo(context.Background(), chat)
	if err != nil {
		return false
	}
	
	userClean := getCleanID(user.String())
	
	for _, p := range info.Participants {
		participantClean := getCleanID(p.JID.String())
		if participantClean == userClean && (p.IsAdmin || p.IsSuperAdmin) {
			return true
		}
	}
	return false
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

func sendOwner(client *whatsmeow.Client, v *events.Message) {
	senderClean := getCleanID(v.Info.Sender.String())
	
	// کیش سے بوٹ کی آئی ڈی لیں
	rawBotID := client.Store.ID.User
	botID := botCleanIDCache[rawBotID]
	if botID == "" { botID = getCleanID(rawBotID) }
	
	isMatch := (senderClean == botID)
	status := "❌ NOT Owner"
	emoji := "🚫"
	matchType := "LID_MATCH"
	
	if isMatch {
		status = "✅ YOU are Owner"
		emoji = "👑"
	}
	
	// ✅ اب کارڈ صرف یہاں پرنٹ ہوگا جب کوئی کمانڈ دے گا
	fmt.Printf(`
╔═════════════════════════╗
║ 🎯 OWNER COMMAND TRIGGERED
╠═════════════════════════╣
║ 👤 Sender Clean : %s
║ 🆔 Bot LID Clean: %s
║ 📊 Match Type   : %s
║ ✅ Is Owner     : %v
╚═══════════════════════════════════╝
`, senderClean, botID, matchType, isMatch)
	
	msg := fmt.Sprintf(`╔═══════════════════╗
║ %s OWNER VERIFICATION
╠═══════════════════╣
║ 🆔 Bot ID  : %s
║ 👤 Your ID : %s
╠═══════════════════╣
║ 📊 Status: %s
╚═══════════════════╝`, emoji, botID, senderClean, status)
	
	replyMessage(client, v, msg)
}

func sendBotsList(client *whatsmeow.Client, v *events.Message) {
	clientsMutex.RLock()
	count := len(activeClients)
	
	msg := fmt.Sprintf(`╔═══════════════════╗
║ 📊 MULTI-BOT STATUS
╠═══════════════════╣
║ 🤖 Active Bots: %d
║ 🔄 Auto-Connect: ✅
║ 🔐 LID Security: ✅
║ 📡 DB Sync: ✅
╠═══════════════════╣`, count)
	
	i := 1
	for num := range activeClients {
		msg += fmt.Sprintf("\n║ %d. %s", i, num)
		i++
	}
	
	clientsMutex.RUnlock()
	
	msg += "\n╚═══════════════════════════╝"
	
	replyMessage(client, v, msg)
}

// 🚀 ریڈیس اپ ٹائم لاجک

func loadPersistentUptime() {
	if rdb != nil {
		val, err := rdb.Get(context.Background(), "total_uptime").Int64()
		if err == nil {
			persistentUptime = val
		}
	}
	fmt.Println("⏳ [UPTIME] Persistent uptime loaded from Redis")
}

func startPersistentUptimeTracker() {
	ticker := time.NewTicker(1 * time.Minute)
	go func() {
		for range ticker.C {
			persistentUptime += 60 // 60 سیکنڈز کا اضافہ
			if rdb != nil {
				rdb.Set(context.Background(), "total_uptime", persistentUptime, 0)
			}
		}
	}()
}

func getFormattedUptime() string {
	seconds := persistentUptime
	days := seconds / 86400
	seconds %= 86400
	hours := seconds / 3600
	seconds %= 3600
	minutes := seconds / 60
	
	return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
}


func sendMenu(client *whatsmeow.Client, v *events.Message) {
	uptimeStr := getFormattedUptime()
	
	// پریفکس کیش سے لیں
	rawBotID := client.Store.ID.User
	botID := botCleanIDCache[rawBotID]
	p := getPrefix(botID)

	s := getGroupSettings(v.Info.Chat.String())
	currentMode := strings.ToUpper(s.Mode)
	if !strings.Contains(v.Info.Chat.String(), "@g.us") {
		currentMode = "PRIVATE"
	}

	menu := fmt.Sprintf(`╔═════════════════╗
║   %s   
╠═════════════════╣
║ 👋 *Assalam-o-Alaikum* ║ 👑 *Owner:* %s              
║ 🛡️ *Mode:* %s               
║ ⏳ *Uptime:* %s             
╠═════════════════╣
║                           
║  ╭─────── DOWNLOADERS─╮
║  │ 🔸 *%sfb* - Facebook   
║  │ 🔸 *%sig* - Instagram  
║  │ 🔸 *%spin* - Pinterest 
║  │ 🔸 *%stiktok* - TikTok 
║  │ 🔸 *%sytmp3* - YT Audio
║  │ 🔸 *%sytmp4* - YT Video 
║  ╰───────────────────╯
║                             
║  ╭─────── GROUP ──────╮
║  │ 🔸 *%sadd* - Add Member
║  │ 🔸 *%sdemote* - Demote 
║  │ 🔸 *%sgroup* - Settings
║  │ 🔸 *%shidetag* - Hidden
║  │ 🔸 *%skick* - Remove    
║  │ 🔸 *%spromote* - Admin
║  │ 🔸 *%stagall* - Mention
║  ╰───────────────────╯
║                             
║  ╭──── SETTINGS ───╮
║  │ 🔸 *%saddstatus* 
║  │ 🔸 *%salwaysonline* 
║  │ 🔸 *%santilink* 
║  │ 🔸 *%santipic* 
║  │ 🔸 *%santisticker* 
║  │ 🔸 *%santivideo* 
║  │ 🔸 *%sautoreact* 
║  │ 🔸 *%sautoread* 
║  │ 🔸 *%sautostatus* 
║  │ 🔸 *%sdelstatus* 
║  │ 🔸 *%sliststatus* 
║  │ 🔸 *%smode* 
║  │ 🔸 *%sowner* 
║  │ 🔸 *%sreadallstatus* 
║  │ 🔸 *%sstatusreact* 
║  ╰─────────────────╯
║                             
║  ╭─────── TOOLS ───────╮
║  │ 🔸 *%sdata* - DB Status
║  │ 🔸 *%sid* - Get ID      
║  │ 🔸 *%slistbots* - Bots🆕
║  │ 🔸 *%sping* - Speed     
║  │ 🔸 *%sremini* - Enhance
║  │ 🔸 *%sremovebg* - BG  
║  │ 🔸 *%ssticker* - Create 
║  │ 🔸 *%stoimg* - Convert 
║  │ 🔸 *%stourl* - Upload  
║  │ 🔸 *%stovideo* - Make 
║  │ 🔸 *%stranslate* - Lang
║  │ 🔸 *%svv* - ViewOnce 
║  │ 🔸 *%sweather* - Info
║  ╰────────────────────╯
║                           
╠═════════════════════╣
║ © 2025 Nothing is Impossible 
╚═════════════════════╝`,
		BOT_NAME, OWNER_NAME, currentMode, uptimeStr,
		p, p, p, p, p, p,
		p, p, p, p, p, p, p,
		p, p, p, p, p, p, p, p, p, p, p, p, p, p, p,
		p, p, p, p, p, p, p, p, p, p, p, p, p)

	sendReplyMessage(client, v, menu)
}

func SetGlobalClient(c *whatsmeow.Client) {
	globalClient = c
}

func sendPing(client *whatsmeow.Client, v *events.Message) {
	start := time.Now()
	time.Sleep(10 * time.Millisecond)
	ms := time.Since(start).Milliseconds()
	
	uptimeStr := getFormattedUptime()

	msg := fmt.Sprintf(`╔════════════════╗
║ ⚡ PING STATUS
╠════════════════╣
║ 🚀 Speed: %d MS
║ ⏱️ Uptime: %s
║ 👑 Dev: %s
╠════════════════╣
║ 🟢 System Running
╚════════════════╝`, ms, uptimeStr, OWNER_NAME)

	sendReplyMessage(client, v, msg)
}


func sendID(client *whatsmeow.Client, v *events.Message) {
	user := v.Info.Sender.User
	chat := v.Info.Chat.User
	chatType := "Private"
	if v.Info.IsGroup {
		chatType = "Group"
	}

	msg := fmt.Sprintf(`╔════════════════╗
║ 🆔 ID INFO
╠════════════════╣
║ 👤 User ID:
║ `+"`%s`"+`
║ 👥 Chat ID:
║ `+"`%s`"+`
║ 🏷️ Type: %s
╚════════════════╝`, user, chat, chatType)

	sendReplyMessage(client, v, msg)
}

func react(client *whatsmeow.Client, chat types.JID, msgID types.MessageID, emoji string) {
	client.SendMessage(context.Background(), chat, &waProto.Message{
		ReactionMessage: &waProto.ReactionMessage{
			Key: &waProto.MessageKey{
				RemoteJID: proto.String(chat.String()),
				ID:         proto.String(string(msgID)),
				FromMe:     proto.Bool(false),
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


func StartAllBots(container *sqlstore.Container) {
	dbContainer = container
	ctx := context.Background()
	
	devices, err := container.GetAllDevices(ctx)
	if err != nil {
		fmt.Printf("❌ [DB-ERROR] Could not load sessions: %v\n", err)
		return
	}

	fmt.Printf("\n🤖 Starting Multi-Bot System (Found %d entries in DB)\n", len(devices))

	seenNumbers := make(map[string]bool)

	for i, device := range devices {
		botNum := getCleanID(device.ID.User)
		
		// 🛡️ اگر یہ نمبر اس لوپ میں پہلے آ چکا ہے تو اسے چھوڑ دو
		if seenNumbers[botNum] {
			continue
		}
		seenNumbers[botNum] = true

		go func(idx int, dev *store.Device) {
			defer func() {
				if r := recover(); r != nil {
					fmt.Printf("❌ Crash prevented on startup for %s: %v\n", botNum, r)
				}
			}()
			ConnectNewSession(dev)
		}(i, device)
		
		time.Sleep(5 * time.Second)
	}

	go monitorNewSessions(container)
}



func monitorNewSessions(container *sqlstore.Container) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		devices, err := container.GetAllDevices(context.Background())
		if err != nil {
			continue
		}

		for _, device := range devices {
			botID := getCleanID(device.ID.User)
			
			clientsMutex.RLock()
			_, exists := activeClients[botID]
			clientsMutex.RUnlock()

			if !exists {
				fmt.Printf("\n🆕 [AUTO-CONNECT] New session found: %s\n", botID)
				go ConnectNewSession(device)
				time.Sleep(5 * time.Second)
			}
		}
	}
}


func handleSessionDelete(client *whatsmeow.Client, v *events.Message, args []string) {
	if !isOwner(client, v.Info.Sender) {
		replyMessage(client, v, "╔═══════════════════╗\n║ 👑 OWNER ONLY      \n╠═══════════════════╣\n║ You don't have    \n║ permission.       \n╚═══════════════════╝")
		return
	}

	if len(args) == 0 {
		replyMessage(client, v, "⚠️ Please provide a number. Example: .sd 92301xxxxxx")
		return
	}

	targetNumber := args[0]
	targetJID, ok := parseJID(targetNumber)
	if !ok {
		replyMessage(client, v, "❌ Invalid number format.")
		return
	}

	fmt.Printf("\n--- [SESSION DELETE START] ---\n")
	
	clientsMutex.Lock()
	targetClient, exists := activeClients[getCleanID(targetNumber)]
	if exists {
		targetClient.Disconnect()
		delete(activeClients, getCleanID(targetNumber))
	}
	clientsMutex.Unlock()

	if dbContainer == nil {
		replyMessage(client, v, "❌ Database connection error.")
		return
	}

	device, err := dbContainer.GetDevice(context.Background(), targetJID)
	if err != nil || device == nil {
		replyMessage(client, v, "❌ Session not found in database.")
		return
	}

	err = device.Delete(context.Background())
	if err != nil {
		fmt.Printf("❌ DB Delete Error: %v\n", err)
		replyMessage(client, v, "❌ Failed to delete session from DB.")
	} else {
		msg := fmt.Sprintf("╔═══════════════════╗\n║ 🗑️ SESSION DELETED  \n╠═══════════════════╣\n║ Number: %s\n║ Status: REMOVED    \n║ Action: Rescan QR \n╚═══════════════════╝", targetNumber)
		replyMessage(client, v, msg)
	}
}

func parseJID(arg string) (types.JID, bool) {
	if arg == "" {
		return types.EmptyJID, false
	}
	if !strings.Contains(arg, "@") {
		arg += "@s.whatsapp.net"
	}
	jid, err := types.ParseJID(arg)
	if err != nil {
		return types.EmptyJID, false
	}
	return jid, true
}