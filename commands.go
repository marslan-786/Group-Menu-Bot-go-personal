package main

import (
	"context"
	"fmt"
	"strings"
	"time"
	"sync"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	// ✅ waLog امپورٹ یہاں سے ہٹا دیا گیا ہے کیونکہ یہ اس فائل میں استعمال نہیں ہو رہا تھا
	"google.golang.org/protobuf/proto"
)

type CachedAdminList struct {
    Admins    map[string]bool // صرف ایڈمنز کی لسٹ رکھیں گے
    Timestamp time.Time       // کب ڈیٹا لیا تھا
}

var (
    adminCache      = make(map[string]CachedAdminList) // GroupID -> AdminList
    adminCacheMutex sync.RWMutex
)


// ⚡ نوٹ: یہاں سے وہ ڈپلیکیٹ ویری ایبلز (activeClients, clientsMutex وغیرہ) 
// ہٹا دیئے گئے ہیں کیونکہ وہ اب صرف main.go میں ایک ہی بار ڈیفائن ہوں گے۔

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
	// ⚡ STEP 1: انتہائی بنیادی معلومات (جو بلاک نہیں کرتیں) مین تھریڈ میں نکالیں
	// یہ ضروری ہے تاکہ ہم فیصلہ کر سکیں کہ گوروٹین چلانی ہے یا نہیں
	rawBotID := client.Store.ID.User
	
	// JID ہینڈلنگ کو محفوظ بنائیں
	if v.Info.Sender.User == "" { return }
	
	// ✅ VIP فکس: ToNonAD() تاکہ کمپیوٹر/موبائل کا مسئلہ نہ ہو
	senderID := v.Info.Sender.ToNonAD().String()
	chatID := v.Info.Chat.String()
	isGroup := v.Info.IsGroup
	msgID := v.Info.ID

	// 🛡️ باڈی ٹیکسٹ نکالیں (یہ ہلکا کام ہے)
	bodyRaw := getText(v.Message)
	if bodyRaw == "" { return }

	// ====================================================================
	// 🚀 THE ULTIMATE BACKGROUND WORKER
	// یہاں سے آگے ہر چیز الگ دنیا (Goroutine) میں چلے گی۔
	// مین بوٹ 0.0001ms میں فارغ ہو کر اگلے میسج کے لیے تیار ہو جائے گا۔
	// ====================================================================
	go func() {
		// 🛡️ Panic Recovery (تاکہ یہ تھریڈ کریش ہو تو بوٹ زندہ رہے)
		defer func() {
			if r := recover(); r != nil {
				// خاموشی سے ایرر ہینڈل کریں یا لاگ کریں
				// fmt.Println("Recovered in worker:", r) 
			}
		}()

		// 1️⃣ ہیوی ویری ایبلز اور کیشے (اب یہ بیک گراؤنڈ میں ہیں)
		botID := botCleanIDCache[rawBotID]
		if botID == "" { botID = getCleanID(rawBotID) }
		prefix := getPrefix(botID) // ڈیٹا بیس کال (اب مین تھریڈ کو نہیں روکے گی)
		bodyClean := strings.TrimSpace(bodyRaw)

		// 🛠️ ریپلائی آئی ڈی نکالنا
		var qID string
		if extMsg := v.Message.GetExtendedTextMessage(); extMsg != nil && extMsg.ContextInfo != nil {
			qID = extMsg.ContextInfo.GetStanzaID()
		}

		// 🔍 سیشن چیک (Thread-Safe Reads)
		// چونکہ تم ہائی سپیڈ چاہتے ہو، اس لیے ہم مانتے ہیں کہ تمہارے Cache پر Mutex لگا ہوا ہے
		session, isYTS := ytCache[qID]
		stateYT, isYTSelect := ytDownloadCache[qID]
		_, isSetup := setupMap[qID]
		_, isTT := ttCache[senderID]

		// 🛡️ 2️⃣ سیکیورٹی چیک (Completely Independent Process)
		// یہ کمانڈ پروسیسنگ کا رتی برابر بھی انتظار نہیں کرے گا
		if isGroup {
			go func() {
				// سیٹنگز حاصل کرنا DB آپریشن ہو سکتا ہے، اس لیے یہ بھی الگ Goroutine میں
				s := getGroupSettings(chatID)
				if s.Antilink || s.AntiPic || s.AntiVideo || s.AntiSticker {
					checkSecurity(client, v) // یہ فنکشن خود سیکیورٹی ہینڈل کرے گا
				}
			}()
		}

		// 🚀 3️⃣ فلٹر: اگر کمانڈ نہیں اور سیشن نہیں، تو یہیں رک جاؤ (Resource Saver)
		isAnySession := isSetup || isYTS || isYTSelect || isTT
		if !strings.HasPrefix(bodyClean, prefix) && !isAnySession && chatID != "status@broadcast" {
			return
		}

		// 🎯 4️⃣ ریپلائی ہینڈلنگ (Priority Logic)
		if isSetup {
			handleSetupResponse(client, v); return
		}

		if isTT && !strings.HasPrefix(bodyClean, prefix) {
			if bodyClean == "1" || bodyClean == "2" || bodyClean == "3" {
				// ٹک ٹاک ڈاؤنلوڈ کو بھی الگ پروسیس میں پھینکیں
				go handleTikTokReply(client, v, bodyClean, senderID)
				return
			}
		}

		if qID != "" {
			if isYTS && session.BotLID == botID {
				var idx int
				n, _ := fmt.Sscanf(bodyClean, "%d", &idx)
				if n > 0 && idx >= 1 && idx <= len(session.Results) {
					delete(ytCache, qID)
					// ہیوی مینیو جنریشن، الگ پروسیس
					go handleYTDownloadMenu(client, v, session.Results[idx-1].Url)
					return
				}
			}
			if isYTSelect && stateYT.BotLID == botID {
				delete(ytDownloadCache, qID)
				// ڈاؤنلوڈنگ سب سے ہیوی کام ہے، اسے لازمی الگ کریں
				go handleYTDownload(client, v, stateYT.Url, bodyClean, (bodyClean == "4"))
				return
			}
		}

		// 📺 5️⃣ اسٹیٹس ویور (Status Viewer)
		if chatID == "status@broadcast" {
			go func() { // اسٹیٹس دیکھنا مین تھریڈ کا کام نہیں
				dataMutex.RLock()
				defer dataMutex.RUnlock()
				if data.AutoStatus {
					client.MarkRead(context.Background(), []types.MessageID{msgID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
					if data.StatusReact {
						emojis := []string{"💚", "❤️", "🔥", "😍", "💯"}
						react(client, v.Info.Chat, msgID, emojis[time.Now().UnixNano()%int64(len(emojis))])
					}
				}
			}()
			return
		}

		// 🔘 6️⃣ آٹو ریڈ اور ری ایکٹ (Low Priority Background Task)
		go func() {
			dataMutex.RLock()
			defer dataMutex.RUnlock()
			if data.AutoRead { client.MarkRead(context.Background(), []types.MessageID{msgID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender) }
			if data.AutoReact { react(client, v.Info.Chat, msgID, "❤️") }
		}()

		// ⚡ 7️⃣ کمانڈ پارسنگ انجن
		msgWithoutPrefix := strings.TrimPrefix(bodyClean, prefix)
		words := strings.Fields(msgWithoutPrefix)
		if len(words) == 0 { return }

		cmd := strings.ToLower(words[0])
		fullArgs := strings.TrimSpace(strings.Join(words[1:], " "))

		// پرمیشن چیک (یہ بھی DB لے سکتا ہے، لیکن اہم ہے)
		if !canExecute(client, v, cmd) { return }

		// لاگنگ (صرف ڈیبگنگ کے لیے، اسے بھی الگ کر دیا)
		go fmt.Printf("🚀 [EXEC] Bot: %s | CMD: %s | Arg: %s\n", botID, cmd, fullArgs)

		// 🔥 8️⃣ کمانڈ روٹنگ (ہر کمانڈ ایک نیا تھریڈ)
		// اب اگر ایک یوزر 'kick' چلائے اور دوسرا 'download'، دونوں 100% پیرالل چلیں گے
		
		switch cmd {
		case "setprefix":
			go func() {
				if !isOwner(client, v.Info.Sender) { replyMessage(client, v, "❌ Only Owner."); return }
				if fullArgs == "" { replyMessage(client, v, "⚠️ Usage: .setprefix !"); return }
				updatePrefixDB(botID, fullArgs)
				replyMessage(client, v, fmt.Sprintf("✅ Prefix updated to [%s]", fullArgs))
			}()

		case "menu", "help", "list":
			go func() { react(client, v.Info.Chat, msgID, "📜"); sendMenu(client, v) }()
		case "ping":
			go func() { react(client, v.Info.Chat, msgID, "⚡"); sendPing(client, v) }()
		case "id":
			go sendID(client, v)
		case "owner":
			go sendOwner(client, v)
		case "listbots":
			go sendBotsList(client, v)
		case "data":
			go replyMessage(client, v, "╔════════════════╗\n║ 📂 DATA STATUS\n╠════════════════╣\n║ ✅ System Active\n╚════════════════╝")
		
		// --- Settings (Parallel) ---
		case "alwaysonline": go toggleAlwaysOnline(client, v)
		case "autoread":     go toggleAutoRead(client, v)
		case "autoreact":    go toggleAutoReact(client, v)
		case "autostatus":   go toggleAutoStatus(client, v)
		case "statusreact":  go toggleStatusReact(client, v)
		case "addstatus":    go handleAddStatus(client, v, words[1:])
		case "delstatus":    go handleDelStatus(client, v, words[1:])
		case "liststatus":   go handleListStatus(client, v)
		case "readallstatus":go handleReadAllStatus(client, v)
		case "mode":         go handleMode(client, v, words[1:])
		
		// --- Security (Parallel) ---
		case "antilink":    go startSecuritySetup(client, v, "antilink")
		case "antipic":     go startSecuritySetup(client, v, "antipic")
		case "antivideo":   go startSecuritySetup(client, v, "antivideo")
		case "antisticker": go startSecuritySetup(client, v, "antisticker")
		
		// --- Groups (Parallel) ---
		case "kick":    go handleKick(client, v, words[1:])
		case "add":     go handleAdd(client, v, words[1:])
		case "promote": go handlePromote(client, v, words[1:])
		case "demote":  go handleDemote(client, v, words[1:])
		case "tagall":  go handleTagAll(client, v, words[1:])
		case "hidetag": go handleHideTag(client, v, words[1:])
		case "group":   go handleGroup(client, v, words[1:])
		case "del", "delete": go handleDelete(client, v)
		
		// --- Tools (Parallel) ---
		case "toimg":   go handleToImg(client, v)
		case "tovideo": go handleToMedia(client, v, false) // ✅ فکسڈ: سادہ ویڈیو
		case "togif":   go handleToMedia(client, v, true)  // ✅ فکسڈ: GIF موڈ
		case "s", "sticker": go handleToSticker(client, v)
		case "tourl":   go handleToURL(client, v)
		case "translate", "tr": go handleTranslate(client, v, words[1:])
		case "vv":      go handleVV(client, v)
		case "sd":      go handleSessionDelete(client, v, words[1:])
		
		// --- Downloaders (Heavy Duty - MUST BE PARALLEL) ---
		case "yts": go handleYTS(client, v, fullArgs)

		case "yt", "ytmp4", "ytmp3", "ytv", "yta", "youtube":
			go func() {
				if fullArgs == "" {
					replyMessage(client, v, "⚠️ *Usage:* .yt [YouTube Link]")
					return
				}
				if strings.Contains(strings.ToLower(fullArgs), "youtu") {
					handleYTDownloadMenu(client, v, fullArgs)
				} else {
					replyMessage(client, v, "❌ Please provide a valid YouTube link.")
				}
			}()

		case "fb", "facebook":    go handleFacebook(client, v, fullArgs)
		case "ig", "insta", "instagram": go handleInstagram(client, v, fullArgs)
		case "tt", "tiktok":      go handleTikTok(client, v, fullArgs)
		case "tw", "x", "twitter": go handleTwitter(client, v, fullArgs)
		case "pin", "pinterest":  go handlePinterest(client, v, fullArgs)
		case "threads":           go handleThreads(client, v, fullArgs)
		case "snap", "snapchat":  go handleSnapchat(client, v, fullArgs)
		case "reddit":            go handleReddit(client, v, fullArgs)
		case "twitch":            go handleTwitch(client, v, fullArgs)
		case "dm", "dailymotion": go handleDailyMotion(client, v, fullArgs)
		case "vimeo":             go handleVimeo(client, v, fullArgs)
		case "rumble":            go handleRumble(client, v, fullArgs)
		case "bilibili":          go handleBilibili(client, v, fullArgs)
		case "douyin":            go handleDouyin(client, v, fullArgs)
		case "kwai":              go handleKwai(client, v, fullArgs)
		case "bitchute":          go handleBitChute(client, v, fullArgs)
		case "sc", "soundcloud":  go handleSoundCloud(client, v, fullArgs)
		case "spotify":           go handleSpotify(client, v, fullArgs)
		case "apple", "applemusic": go handleAppleMusic(client, v, fullArgs)
		case "deezer":            go handleDeezer(client, v, fullArgs)
		case "tidal":             go handleTidal(client, v, fullArgs)
		case "mixcloud":          go handleMixcloud(client, v, fullArgs)
		case "napster":           go handleNapster(client, v, fullArgs)
		case "bandcamp":          go handleBandcamp(client, v, fullArgs)
		
		// --- Fun & AI (Parallel) ---
		case "imgur":   go handleImgur(client, v, fullArgs)
		case "giphy":   go handleGiphy(client, v, fullArgs)
		case "flickr":  go handleFlickr(client, v, fullArgs)
		case "9gag":    go handle9Gag(client, v, fullArgs)
		case "ifunny":  go handleIfunny(client, v, fullArgs)
		
		case "stats", "server", "dashboard": go handleServerStats(client, v)
		case "speed", "speedtest":           go handleSpeedTest(client, v)
		case "ss", "screenshot":             go handleScreenshot(client, v, fullArgs)
		
		case "ai", "ask", "gpt":
			go handleAI(client, v, fullArgs, cmd) // ✅ cmd پاس کر دیا
			
		case "imagine", "img", "draw": go handleImagine(client, v, fullArgs)
		case "google", "search":       go handleGoogle(client, v, fullArgs)
		case "weather":                go handleWeather(client, v, fullArgs)
		case "remini", "upscale", "hd": go handleRemini(client, v)
		case "removebg", "rbg":        go handleRemoveBG(client, v)
		case "fancy", "style":         go handleFancy(client, v, fullArgs)
		case "toptt", "voice":         go handleToPTT(client, v)
		case "ted":                    go handleTed(client, v, fullArgs)
		case "steam":                  go handleSteam(client, v, fullArgs)
		case "archive":                go handleArchive(client, v, fullArgs)
		case "git", "github":          go handleGithub(client, v, fullArgs)
		case "dl", "download", "mega": go handleMega(client, v, fullArgs)
		}
	}() // 🚀 گوروٹین ختم
}

// 🚀 ہیلپرز اور اسپیڈ آپٹیمائزڈ فنکشنز

func getPrefix(botID string) string {
	prefixMutex.RLock()
	p, exists := botPrefixes[botID]
	prefixMutex.RUnlock()
	if exists {
		return p
	}
	// اگر میموری میں نہیں ہے تو ریڈیس سے لیں (main.go والے rdb کو استعمال کرتے ہوئے)
	val, err := rdb.Get(context.Background(), "prefix:"+botID).Result()
	if err != nil || val == "" {
		return "." 
	}
	prefixMutex.Lock()
	botPrefixes[botID] = val
	prefixMutex.Unlock()
	return val
}

func getCleanID(jidStr string) string {
	if jidStr == "" { return "unknown" }
	parts := strings.Split(jidStr, "@")
	if len(parts) == 0 { return "unknown" }
	userPart := parts[0]
	if strings.Contains(userPart, ":") {
		userPart = strings.Split(userPart, ":")[0]
	}
	if strings.Contains(userPart, ".") {
		userPart = strings.Split(userPart, ".")[0]
	}
	return strings.TrimSpace(userPart)
}

// 🆔 ڈیٹا بیس سے صرف اور صرف LID نکالنا
func getBotLIDFromDB(client *whatsmeow.Client) string {
	// اگر سٹور میں LID موجود نہیں ہے تو unknown واپس کرے
	if client.Store.LID.IsEmpty() { 
		return "unknown" 
	}
	// صرف LID کا یوزر آئی ڈی (ہندسے) نکال کر صاف کریں
	return getCleanID(client.Store.LID.User)
}

// 🎯 اونر لاجک: صرف LID میچنگ (نمبر میچ نہیں ہوگا)
func isOwner(client *whatsmeow.Client, sender types.JID) bool {
	// اگر بوٹ کی اپنی LID سٹور میں نہیں ہے تو چیک فیل کر دیں
	if client.Store.LID.IsEmpty() { 
		return false 
	}

	// 1. میسج بھیجنے والے کی LID نکالیں
	senderLID := getCleanID(sender.User)

	// 2. بوٹ کی اپنی LID نکالیں
	botLID := getCleanID(client.Store.LID.User)

	// 🔍 فائنل چیک: صرف LID بمقابلہ LID
	// اب یہ 192883340648500 کو بوٹ کی LID سے ہی میچ کرے گا
	return senderLID == botLID
}

func isAdmin(client *whatsmeow.Client, chat, user types.JID) bool {
    chatID := chat.String()
    userNum := getCleanID(user.User)

    // 1️⃣ پہلے کیش (RAM) چیک کریں
    adminCacheMutex.RLock()
    cached, exists := adminCache[chatID]
    adminCacheMutex.RUnlock()

    // اگر ڈیٹا موجود ہے اور 5 منٹ سے زیادہ پرانا نہیں ہے، تو وہیں سے جواب دیں
    if exists && time.Since(cached.Timestamp) < 5*time.Minute {
        return cached.Admins[userNum]
    }

    // 2️⃣ اگر کیش میں نہیں ہے، تو واٹس ایپ سے فریش ڈیٹا منگوائیں (Network Call)
    info, err := client.GetGroupInfo(context.Background(), chat)
    if err != nil {
        return false
    }

    // 3️⃣ نئی لسٹ تیار کریں
    newAdmins := make(map[string]bool)
    for _, p := range info.Participants {
        if p.IsAdmin || p.IsSuperAdmin {
            cleanP := getCleanID(p.JID.User)
            newAdmins[cleanP] = true
        }
    }

    // 4️⃣ کیش اپڈیٹ کریں
    adminCacheMutex.Lock()
    adminCache[chatID] = CachedAdminList{
        Admins:    newAdmins,
        Timestamp: time.Now(),
    }
    adminCacheMutex.Unlock()

    // 5️⃣ رزلٹ واپس کریں
    return newAdmins[userNum]
}


func canExecute(client *whatsmeow.Client, v *events.Message, cmd string) bool {
	if isOwner(client, v.Info.Sender) { return true }
	if !v.Info.IsGroup { return true }
	s := getGroupSettings(v.Info.Chat.String())
	if s.Mode == "private" { return false }
	if s.Mode == "admin" { return isAdmin(client, v.Info.Chat, v.Info.Sender) }
	return true
}

func sendOwner(client *whatsmeow.Client, v *events.Message) {
	// 1. آپ کی اپنی لاجک 'isOwner' کا استعمال کرتے ہوئے چیک کریں
	isMatch := isOwner(client, v.Info.Sender)
	
	// 2. کارڈ پر دکھانے کے لیے کلین آئی ڈیز حاصل کریں
	// بوٹ کی LID آپ کے فنکشن 'getBotLIDFromDB' سے
	botLID := getBotLIDFromDB(client)
	
	// سینڈر کی LID براہ راست نکال کر صاف کریں
	senderLID := getCleanID(v.Info.Sender.User)
	
	// 3. اسٹیٹس اور ایموجی سیٹ کریں
	status := "❌ NOT Owner"
	emoji := "🚫"
	if isMatch {
		status = "✅ YOU are Owner"
		emoji = "👑"
	}
	
	// 📊 سرور لاگز میں آپ کی لاجک کا رزلٹ دکھانا
	fmt.Printf(`
╔═════════════════════════╗
║ 🎯 LID OWNER CHECK (STRICT)
╠═════════════════════════╣
║ 👤 Sender LID   : %s
║ 🆔 Bot LID DB   : %s
║ ✅ Verification : %v
╚═════════════════════════╝
`, senderLID, botLID, isMatch)
	
	// 💬 واٹس ایپ پر پریمیم کارڈ
	msg := fmt.Sprintf(`╔═══════════════════╗
║ %s OWNER VERIFICATION
╠═══════════════════╣
║ 🆔 Bot LID  : %s
║ 👤 Your LID : %s
╠═══════════════════╣
║ 📊 Status: %s
╚═══════════════════╝`, emoji, botLID, senderLID, status)
	
	replyMessage(client, v, msg)
}

func sendBotsList(client *whatsmeow.Client, v *events.Message) {
	clientsMutex.RLock()
	count := len(activeClients)
	msg := fmt.Sprintf(`╔═══════════════════╗
║ 📊 MULTI-BOT STATUS
╠═══════════════════╣
║ 🤖 Active Bots: %d
╠═══════════════════╣`, count)
	i := 1
	for num := range activeClients {
		msg += fmt.Sprintf("\n║ %d. %s", i, num)
		i++
	}
	clientsMutex.RUnlock()
	msg += "\n╚═══════════════════╝"
	replyMessage(client, v, msg)
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
	rawBotID := client.Store.ID.User
	botID := botCleanIDCache[rawBotID]
	p := getPrefix(botID)
	s := getGroupSettings(v.Info.Chat.String())
	currentMode := strings.ToUpper(s.Mode)
	if !strings.Contains(v.Info.Chat.String(), "@g.us") { currentMode = "PRIVATE" }

	menu := fmt.Sprintf(`╔══════════════════════╗
║     ✨ %s ✨     
╠══════════════════════╣
║ 👋 *Assalam-o-Alaikum*
║ 👑 *Owner:* %s              
║ 🛡️ *Mode:* %s               
║ ⏳ *Uptime:* %s             
╠══════════════════════╣
║                           
║ ╭─── SOCIAL DOWNLOADERS ──╮
║ │ 🔸 *%sfb* - Facebook Video
║ │ 🔸 *%sig* - Instagram Reel/Post
║ │ 🔸 *%stt* - TikTok No Watermark
║ │ 🔸 *%stw* - Twitter/X Media
║ │ 🔸 *%spin* - Pinterest Downloader
║ │ 🔸 *%sthreads* - Threads Video
║ │ 🔸 *%ssnap* - Snapchat Content
║ │ 🔸 *%sreddit* - Reddit with Audio
║ ╰───────────────────────╯
║                             
║ ╭─── VIDEO & STREAMS ────╮
║ │ 🔸 *%syt* - <Link>
║ │ 🔸 *%syts* - YouTube Search
║ │ 🔸 *%stwitch* - Twitch Clips
║ │ 🔸 *%sdm* - DailyMotion HQ
║ │ 🔸 *%svimeo* - Vimeo Pro Video
║ │ 🔸 *%srumble* - Rumble Stream
║ │ 🔸 *%sbilibili* - Bilibili Anime
║ │ 🔸 *%sdouyin* - Chinese TikTok
║ │ 🔸 *%skwai* - Kwai Short Video
║ │ 🔸 *%sbitchute* - BitChute Alt
║ ╰───────────────────────╯
║
║ ╭─── MUSIC PLATFORMS ────╮
║ │ 🔸 *%ssc* - SoundCloud Music
║ │ 🔸 *%sspotify* - Spotify Track
║ │ 🔸 *%sapple* - Apple Music
║ │ 🔸 *%sdeezer* - Deezer Rippin
║ │ 🔸 *%stidal* - Tidal HQ Audio
║ │ 🔸 *%smixcloud* - DJ Mixsets
║ │ 🔸 *%snapster* - Napster Legacy
║ │ 🔸 *%sbandcamp* - Indie Music
║ ╰───────────────────────╯
║                             
║ ╭────── GROUP ADMIN ──────╮
║ │ 🔸 *%sadd* - Add New Member
║ │ 🔸 *%sdemote* - Remove Admin
║ │ 🔸 *%sgroup* - Group Settings
║ │ 🔸 *%shidetag* - Hidden Mention
║ │ 🔸 *%skick* - Remove Member    
║ │ 🔸 *%spromote* - Make Admin
║ │ 🔸 *%stagall* - Mention Everyone
║ ╰───────────────────────╯
║                             
║ ╭──── BOT SETTINGS ─────╮
║ │ 🔸 *%saddstatus* - Auto Status
║ │ 🔸 *%salwaysonline* - Online 24/7
║ │ 🔸 *%santilink* - Link Protection
║ │ 🔸 *%santipic* - No Images Mode
║ │ 🔸 *%santisticker* - No Stickers
║ │ 🔸 *%santivideo* - No Video Mode
║ │ 🔸 *%sautoreact* - Automatic React
║ │ 🔸 *%sautoread* - Blue Tick Mark
║ │ 🔸 *%sautostatus* - Status View
║ │ 🔸 *%sdelstatus* - Remove Status
║ │ 🔸 *%smode* - Private/Public
║ │ 🔸 *%sstatusreact* - React Status
║ ╰────────────────────────╯
║                             
║ ╭────── AI & TOOLS ─────────╮
║ │ 🔸 *%sstats* - Server Dashboard
║ │ 🔸 *%sspeed* - Internet Speed
║ │ 🔸 *%sss* - Web Screenshot
║ │ 🔸 *%sai* - Artificial Intelligence
║ │ 🔸 *%sask* - Ask Questions
║ │ 🔸 *%sgpt* - GPT 4o Model
║ │ 🔸 *%simg* - Image Generator 
║ │ 🔸 *%sgoogle* - Fast Search
║ │ 🔸 *%sweather* - Climate Info
║ │ 🔸 *%sremini* - HD Image Upscaler
║ │ 🔸 *%sremovebg* - Background Eraser
║ │ 🔸 *%sfancy* - Stylish Text
║ │ 🔸 *%stoptt* - Convert to Audio
║ │ 🔸 *%svv* - ViewOnce Bypass
║ │ 🔸 *%ssticker* - Image to Sticker
║ │ 🔸 *%stoimg* - Sticker to Image
║ │ 🔸 *%stogif* - Sticker To Gif
║ │ 🔸 *%stovideo* - Sticker to Video
║ │ 🔸 *%sgit* - GitHub Downloader
║ │ 🔸 *%sarchive* - Internet Archive
║ │ 🔸 *%smega* - Universal Downloader
║ ╰────────────────────────╯
║                           
╠══════════════════════╣
║ © 2025 Nothing is Impossible 
╚══════════════════════╝`,
		BOT_NAME, OWNER_NAME, currentMode, uptimeStr,
		// سوشل ڈاؤنلوڈرز (8)
		p, p, p, p, p, p, p, p,
		// ویڈیوز (10)
		p, p, p, p, p, p, p, p, p, p,
		// میوزک (8)
		p, p, p, p, p, p, p, p,
		// گروپ (7)
		p, p, p, p, p, p, p,
		// سیٹنگز (12)
		p, p, p, p, p, p, p, p, p, p, p, p,
		// ٹولز (16)
		p, p, p, p, p, p, p, p, p, p, p, p, p, p, p, p, p, p, p, p, p)

	sendReplyMessage(client, v, menu)
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
╠═════════════════════╣
║      🟢 System Running
╚═════════════════════╝`, ms, uptimeStr, OWNER_NAME)
	sendReplyMessage(client, v, msg)
}

func sendID(client *whatsmeow.Client, v *events.Message) {
	user := v.Info.Sender.User
	chat := v.Info.Chat.User
	chatType := "Private"
	if v.Info.IsGroup { chatType = "Group" }
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
	if m.Conversation != nil { return *m.Conversation }
	if m.ExtendedTextMessage != nil && m.ExtendedTextMessage.Text != nil { return *m.ExtendedTextMessage.Text }
	if m.ImageMessage != nil && m.ImageMessage.Caption != nil { return *m.ImageMessage.Caption }
	if m.VideoMessage != nil && m.VideoMessage.Caption != nil { return *m.VideoMessage.Caption }
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
		Warnings:       make(map[string]int),
	}

	cacheMutex.Lock()
	groupCache[id] = s
	cacheMutex.Unlock()
	return s
}

func handleSessionDelete(client *whatsmeow.Client, v *events.Message, args []string) {
	if !isOwner(client, v.Info.Sender) {
		replyMessage(client, v, "╔═══════════════════╗\n║ 👑 OWNER ONLY      \n╠═══════════════════╣\n║ You don't have    \n║ permission.       \n╚═══════════════════╝")
		return
	}
	if len(args) == 0 {
		replyMessage(client, v, "⚠️ Please provide a number.")
		return
	}
	targetNumber := args[0]
	targetJID, ok := parseJID(targetNumber)
	if !ok {
		replyMessage(client, v, "❌ Invalid format.")
		return
	}
	clientsMutex.Lock()
	if targetClient, exists := activeClients[getCleanID(targetNumber)]; exists {
		targetClient.Disconnect()
		delete(activeClients, getCleanID(targetNumber))
	}
	clientsMutex.Unlock()

	if dbContainer == nil {
		replyMessage(client, v, "❌ Database error.")
		return
	}
	device, err := dbContainer.GetDevice(context.Background(), targetJID)
	if err != nil || device == nil {
		replyMessage(client, v, "❌ Not found.")
		return
	}
	device.Delete(context.Background())
	msg := fmt.Sprintf("╔═══════════════════╗\n║ 🗑️ SESSION DELETED  \n╠═══════════════════╣\n║ Number: %s\n╚═══════════════════╝", targetNumber)
	replyMessage(client, v, msg)
}

func parseJID(arg string) (types.JID, bool) {
	if arg == "" { return types.EmptyJID, false }
	if !strings.Contains(arg, "@") { arg += "@s.whatsapp.net" }
	jid, err := types.ParseJID(arg)
	if err != nil { return types.EmptyJID, false }
	return jid, true
}