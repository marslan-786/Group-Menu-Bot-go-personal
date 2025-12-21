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
	dbContainer *sqlstore.Container
)

func handler(botClient *whatsmeow.Client, evt interface{}) {
	// اب ہمیں گلوبل کلائنٹ کی ضرورت نہیں، ہم وہ کلائنٹ استعمال کریں گے جس نے ایونٹ بھیجا ہے
	if botClient == nil {
		return
	}
	
	switch v := evt.(type) {
	case *events.Message:
		// میسج پروسیسنگ کے لیے اسی مخصوص بوٹ کا کلائنٹ بھیجیں
		go processMessage(botClient, v)
	case *events.GroupInfo:
		// گروپ تبدیلیوں کے لیے بھی وہی کلائنٹ
		go handleGroupInfoChange(botClient, v)
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
	chatID := v.Info.Chat.String()
	senderID := v.Info.Sender.String()
	isGroup := v.Info.IsGroup

	if state, ok := setupMap[senderID]; ok && state.GroupID == chatID {
		handleSetupResponse(client, v, state)
		return
	}

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

	dataMutex.RLock()
	if data.AutoRead {
		client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
	}
	if data.AutoReact {
		react(client, v.Info.Chat, v.Info.ID, "❤️")
	}
	dataMutex.RUnlock()

	if isGroup {
		checkSecurity(client, v)
	}

	body := getText(v.Message)
	dataMutex.RLock()
	prefix := data.Prefix
	dataMutex.RUnlock()

	if !strings.HasPrefix(body, prefix) && !isKnownCommand(body) {
		return
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
	fmt.Printf("📩 CMD: %s | User: %s | Chat: %s\n", cmd, v.Info.Sender.User, v.Info.Chat.User)

	switch cmd {
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
	case "listbots":
		react(client, v.Info.Chat, v.Info.ID, "📊")
		sendBotsList(client, v)
	case "data":
		replyMessage(client, v, "╔════════════════╗\n║ 📂 DATA STATUS\n╠════════════════╣\n║ ✅ DB Coming\n╚════════════════╝")
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
		
	}
}

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
		cleanLID := getCleanID(lidStr)
		fmt.Printf("🔍 [DB LID] Raw: %s | Clean: %s\n", lidStr, cleanLID)
		return cleanLID
	}
	
	cleanID := getCleanID(client.Store.ID.User)
	fmt.Printf("🔍 [BOT ID] Raw: %s | Clean: %s\n", client.Store.ID.User, cleanID)
	return cleanID
}

func isOwner(client *whatsmeow.Client, sender types.JID) bool {
	if client.Store.ID == nil {
		return false
	}
	
	senderClean := getCleanID(sender.String())
	botLIDClean := getBotLIDFromDB(client)
	
	// صرف خاموشی سے چیک کریں کہ کیا آئی ڈی میچ ہو رہی ہے
	return (senderClean == botLIDClean)
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
	botLIDClean := getBotLIDFromDB(client)
	
	isMatch := (senderClean == botLIDClean)
	status := "❌ NOT Owner"
	emoji := "🚫"
	matchType := "NONE"
	
	if isMatch {
		status = "✅ YOU are Owner"
		emoji = "👑"
		matchType = "LID_MATCH"
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
`, senderClean, botLIDClean, matchType, isMatch)
	
	msg := fmt.Sprintf(`╔═══════════════════╗
║ %s OWNER VERIFICATION
╠═══════════════════╣
║ 🆔 Bot ID  : %s
║ 👤 Your ID : %s
╠═══════════════════╣
║ 📊 Status: %s
╚═══════════════════╝`, emoji, botLIDClean, senderClean, status)
	
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

	menu := fmt.Sprintf(`╔═════════════════╗
║   %s   
╠═════════════════╣
║ 👋 *Assalam-o-Alaikum*     
║ 👑 *Owner:* %s             
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
		BOT_NAME, OWNER_NAME, currentMode, uptime,
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
	uptime := time.Since(startTime).Round(time.Second)

	msg := fmt.Sprintf(`╔════════════════╗
║ ⚡ PING STATUS
╠════════════════╣
║ 🚀 Speed: %d MS
║ ⏱️ Uptime: %s
║ 👑 Dev: %s
╠════════════════╣
║ 🟢 System Running
╚════════════════╝`, ms, uptime, OWNER_NAME)

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

func ConnectNewSession(device *store.Device) {
	// 1. کلائنٹ لاگ سیٹ اپ
	clientLog := waLog.Stdout("Client", "DEBUG", true)
	client := whatsmeow.NewClient(device, clientLog)
	
	// ❌ SetGlobalClient(client) -- اس لائن کو نکال دیا ہے کیونکہ یہ ملٹی بوٹ کے لیے غلط ہے

	// 2. ہینڈلر رجسٹریشن (Closure استعمال کرتے ہوئے)
	// اس سے ہر بوٹ کو اپنا 'client' ملے گا
	client.AddEventHandler(func(evt interface{}) {
		handler(client, evt)
	})

	botID := getCleanID(device.ID.User)
	
	// 3. کنکشن قائم کرنا
	err := client.Connect()
	if err != nil {
		fmt.Printf("❌ [MULTI-BOT] نمبر %s کنیکٹ نہیں ہو سکا: %v\n", botID, err)
		return
	}

	// 4. ایکٹو کلائنٹس کی لسٹ میں محفوظ کرنا (Safe Concurrency)
	clientsMutex.Lock()
	activeClients[botID] = client
	clientsMutex.Unlock()

	// 5. کامیابی کا میسج
	lidStr := device.LID.String()
	fmt.Printf(`
╔═══════════════════════════════════╗
║ ✅ BOT CONNECTED SUCCESSFULLY!
╠═══════════════════════════════════╣
║ 📱 Number: %s
║ 🆔 LID: %s
║ 🕐 Time: %s
╚═══════════════════════════════════╝
`, botID, getCleanID(lidStr), time.Now().Format("15:04:05"))
}


func StartAllBots(container *sqlstore.Container) {
	ctx := context.Background()
	
	devices, err := container.GetAllDevices(ctx)
	if err != nil {
		fmt.Printf("❌ [MULTI-BOT] ڈیٹا بیس سے سیشن لوڈ کرنے میں غلطی: %v\n", err)
		return
	}

	if len(devices) == 0 {
		fmt.Println("⚠️ [MULTI-BOT] کوئی سیشن نہیں ملا! نیا سیشن بنائیں۔")
		return
	}

	fmt.Printf(`
╔═══════════════════════════════════╗
║ 🚀 MULTI-BOT SYSTEM STARTING
╠═══════════════════════════════════╣
║ 📂 Found: %d session(s) in DB
║ 🔄 Connecting all bots...
╚═══════════════════════════════════╝
`, len(devices))

	var wg sync.WaitGroup
	for i, device := range devices {
		wg.Add(1)
		go func(idx int, dev *store.Device) {
			defer wg.Done()
			
			fmt.Printf("\n[%d/%d] 🔌 کنیکٹ ہو رہا ہے: %s...\n", idx+1, len(devices), getCleanID(dev.ID.User))
			ConnectNewSession(dev)
			
			time.Sleep(2 * time.Second)
		}(i, device)
	}

	wg.Wait()

	clientsMutex.RLock()
	activeCount := len(activeClients)
	clientsMutex.RUnlock()

	fmt.Printf(`
╔═══════════════════════════════════╗
║ ✅ MULTI-BOT SYSTEM READY!
╠═══════════════════════════════════╣
║ 🤖 Active Bots: %d/%d
║ 🔐 LID Security: Enabled
║ 📡 Auto-Connect: Active
║ 💾 Database: PostgreSQL
╠═══════════════════════════════════╣
║ 💡 نئے سیشن خودکار طور پر
║    کنیکٹ ہو جائیں گے!
╚═══════════════════════════════════╝
`, activeCount, len(devices))

	go monitorNewSessions(container)
}

func monitorNewSessions(container *sqlstore.Container) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	fmt.Println("\n🔍 [AUTO-CONNECT] نئے سیشنز کی نگرانی شروع...")

	for range ticker.C {
		ctx := context.Background()
		devices, err := container.GetAllDevices(ctx)
		if err != nil {
			continue
		}

		for _, device := range devices {
			botID := getCleanID(device.ID.User)
			
			clientsMutex.RLock()
			_, exists := activeClients[botID]
			clientsMutex.RUnlock()

			if !exists {
				fmt.Printf("\n🆕 [AUTO-CONNECT] نیا سیشن ملا: %s\n", botID)
				go ConnectNewSession(device)
				time.Sleep(3 * time.Second)
			}
		}
	}
}

func handleSessionDelete(client *whatsmeow.Client, v *events.Message, args []string) {
	// 1. صرف اونر ہی سیشن ڈیلیٹ کر سکتا ہے
	if !isOwner(client, v.Info.Sender) {
		replyMessage(client, v, "╔═══════════════════╗\n║ 👑 OWNER ONLY      \n╠═══════════════════╣\n║ You don't have    \n║ permission.       \n╚═══════════════════╝")
		return
	}

	if len(args) == 0 {
		replyMessage(client, v, "⚠️ Please provide a number. Example: .sd 92301xxxxxx")
		return
	}

	targetNumber := args[0]
	// جے آئی ڈی (JID) تیار کریں
	targetJID, ok := parseJID(targetNumber)
	if !ok {
		replyMessage(client, v, "❌ Invalid number format.")
		return
	}

	fmt.Printf("\n--- [SESSION DELETE START] ---\n")
	fmt.Printf("🗑️ Target: %s\n", targetJID.String())

	// 2. چیک کریں کہ کیا بوٹ ایکٹو لسٹ میں ہے؟
	clientsMutex.Lock()
	targetClient, exists := activeClients[getCleanID(targetNumber)]
	if exists {
		fmt.Println("🔌 Disconnecting active client...")
		targetClient.Disconnect()
		delete(activeClients, getCleanID(targetNumber))
	}
	clientsMutex.Unlock()

	// 3. ڈیٹا بیس سے سیشن ڈیلیٹ کریں
	if dbContainer == nil {
		fmt.Println("❌ Error: DB Container is nil")
		replyMessage(client, v, "❌ Database connection error.")
		return
	}

	// ڈیٹا بیس سے ڈیوائس ڈھونڈ کر ڈیلیٹ کریں
	device, err := dbContainer.GetDevice(targetJID)
	if err != nil || device == nil {
		fmt.Printf("❌ Could not find session in DB: %v\n", err)
		replyMessage(client, v, "❌ Session not found in database.")
		return
	}

	err = device.Delete()
	if err != nil {
		fmt.Printf("❌ DB Delete Error: %v\n", err)
		replyMessage(client, v, "❌ Failed to delete session from DB.")
	} else {
		fmt.Println("✅ Session permanently deleted from DB.")
		msg := fmt.Sprintf("╔═══════════════════╗\n║ 🗑️ SESSION DELETED  \n╠═══════════════════╣\n║ Number: %s\n║ Status: REMOVED   \n║ Action: Rescan QR \n╚═══════════════════╝", targetNumber)
		replyMessage(client, v, msg)
	}
	fmt.Printf("--- [SESSION DELETE END] ---\n")
}

// مددگار فنکشن نمبر کو JID میں بدلنے کے لیے
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
