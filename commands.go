package main

import (
	"context"
	"fmt"
	"strings"
	"os"
	"time"
    
    "go.mau.fi/whatsmeow"
	"github.com/showwin/speedtest-go/speedtest"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

// =========================================================
// 🛑 ANTI-SPAM CONFIGURATION (RESTRICTED ZONES)
// =========================================================

// 1. جن گروپس میں آپ چاہتے ہیں کہ صرف "خاص بوٹس" بولیں
var RestrictedGroups = map[string]bool{
    "120363365896020486@g.us": true, // آپ کا مین گروپ 1
}

// 2. وہ بوٹ نمبرز جو ان گروپس میں بولنے کی اجازت رکھتے ہیں (صرف آپ کے نمبر)
var AuthorizedBots = map[string]bool{
    "923017552805": true, // آپ کا مین بوٹ نمبر
    "923116573691": true, // کوئی دوسرا بیک اپ بوٹ
}
// =========================================================

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



// ⚡ PERMISSION CHECK FUNCTION (UPDATED)
func canExecute(client *whatsmeow.Client, v *events.Message, cmd string) bool {
	// 1. Owner Check
	if isOwner(client, v.Info.Sender) { return true }
	
	// 2. Private Chat Check (Always Allowed unless blacklisted)
	if !v.Info.IsGroup { return true }

	// 3. Group Checks (Need Bot ID)
	rawBotID := client.Store.ID.User
	botID := getCleanID(rawBotID)
	
	s := getGroupSettings(botID, v.Info.Chat.String())
	
	if s.Mode == "private" { return false }
	if s.Mode == "admin" { return isAdmin(client, v.Info.Chat, v.Info.Sender) }
	
	return true
}

// ⚡ MAIN MESSAGE PROCESSOR (FULL & OPTIMIZED)
func processMessage(client *whatsmeow.Client, v *events.Message) {
	// ⚡ 1. Panic Recovery
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠️ Critical Panic in ProcessMessage: %v\n", r)
		}
	}()

	// ⚡ 2. Timestamp Check (Relaxed to 5s for slower networks)
	if time.Since(v.Info.Timestamp) > 5*time.Second {
		return
	}

	// ⚡ 3. Basic Text Extraction
	bodyRaw := getText(v.Message)
	if bodyRaw == "" {
		if v.Info.Chat.String() == "status@broadcast" {
			// Status Logic Handled Below...
		} else {
			return
		}
	}
	bodyClean := strings.TrimSpace(bodyRaw)

	// =========================================================
	// 🛡️ 0. IMMEDIATE ANTI-BUG PROTECTION (Private Chats Only)
	// =========================================================
	// اب یہ چیک کرے گا کہ کیا AntiBug آن ہے اور کیا یہ پرسنل چیٹ ہے؟
	// !v.Info.IsGroup کا مطلب ہے "اگر گروپ نہیں ہے"
	if AntiBugEnabled && !v.Info.IsGroup {
		
		// وہ تمام خطرناک کیریکٹرز جو ایپ کریش کرتے ہیں
		badChars := []string{"\u200b", "\u202e", "\u202d", "\u2060", "\u200f"}
		totalJunk := 0
		
		// لوپ لگا کر سب گنیں
		for _, char := range badChars {
			totalJunk += strings.Count(bodyClean, char)
		}

		// اگر کچرا 50 سے زیادہ ہے تو اڑا دیں
		if totalJunk > 50 {
			fmt.Printf("🛡️ MALICIOUS BUG DETECTED in DM! From: %s | Cleaning...\n", v.Info.Sender.User)
			
			// 1. میسج سب کے لیے ڈیلیٹ کریں (Revoke)
			// نوٹ: پرائیویٹ چیٹ میں آپ دوسرے کا میسج Revoke نہیں کر سکتے (یہ واٹس ایپ کی لمیٹیشن ہے)،
			// لیکن آپ "Clear Chat" کمانڈ چلا سکتے ہیں اگر آپ نے خود بنایا ہو، 
			// یا کم از کم بوٹ کو کریش ہونے سے بچانے کے لیے return کروا سکتے ہیں۔
			// ٹیسٹنگ کے لیے ہم یہاں Revoke کی کوشش کریں گے۔
			client.RevokeMessage(context.Background(), v.Info.Chat, v.Info.ID)
			
			// 2. فنکشن یہیں روک دیں (Return)
			return 
		}
	}
	// =========================================================

	// ⚡ 4. Bot ID Handling (No Lock if possible)
	rawBotID := client.Store.ID.User
	// Fast Path: Direct Clean
	botID := strings.TrimSuffix(strings.Split(rawBotID, ":")[0], "@s.whatsapp.net")

	// 🟢 VARIABLES
	chatID := v.Info.Chat.String()
	isGroup := v.Info.IsGroup
	senderID := v.Info.Sender.ToNonAD().String()

	// =========================================================
	// 🛡️ 1. RESTRICTED GROUP FILTER (Anti-Spam)
	// =========================================================
	if RestrictedGroups[chatID] {
		if !AuthorizedBots[botID] {
			return 
		}
	}

	// =========================================================
	// 🛡️ 2. MODE CHECK (Admin / Private / Public)
	// =========================================================
	if isGroup {
		s := getGroupSettings(botID, chatID)
		
		if s.Mode == "private" && !isOwner(client, v.Info.Sender) {
			return
		}

		if s.Mode == "admin" && !isOwner(client, v.Info.Sender) {
			if !isAdmin(client, v.Info.Chat, v.Info.Sender) {
				return
			}
		}
	}

	// ⚡ 5. Prefix Check
	prefix := getPrefix(botID)
	isCommand := strings.HasPrefix(bodyClean, prefix)

	// 🛠️ 7. Context Info
	var qID string
	var isReply bool
	if extMsg := v.Message.GetExtendedTextMessage(); extMsg != nil && extMsg.ContextInfo != nil {
		qID = extMsg.ContextInfo.GetStanzaID()
		isReply = true
	}

	// 🔍 8. Session Checks
	var isSetup, isYTS, isYTSelect, isTT bool
	var session YTSession
	var stateYT YTState

	if !isCommand {
		if isReply && qID != "" {
			_, isSetup = setupMap[qID]
			session, isYTS = ytCache[qID]
			stateYT, isYTSelect = ytDownloadCache[qID]
		}
		_, isTT = ttCache[senderID]
	}

	// 🚀 9. DECISION MATRIX
	isAnySession := isSetup || isYTS || isYTSelect || isTT
	isStatus := v.Info.Chat.String() == "status@broadcast"

	if !isCommand && !isAnySession && !isStatus {
		if v.Info.IsGroup {
			// Security checks in background (Low Priority)
			go func() {
				defer recovery()
				checkSecurity(client, v)
			}()
		}
		return 
	}

	// =========================================================================
	// ⚡ SMART EXECUTION ENGINE (RAM MANAGED)
	// =========================================================================
	
	go func() {
		defer recovery()

		// 📺 A. Status Handling
		if isStatus {
			dataMutex.RLock()
			if data.AutoStatus {
				client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
				if data.StatusReact {
					emojis := []string{"💚", "❤️", "🔥", "😍", "💯", "😎", "✨"}
					react(client, v.Info.Chat, v.Info.ID, emojis[time.Now().UnixNano()%int64(len(emojis))])
				}
			}
			dataMutex.RUnlock()
			return
		}

		// 🔘 B. AUTO READ & RANDOM MULTI-REACTION 🌟
		dataMutex.RLock()
		if data.AutoRead {
			client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
		}
		if data.AutoReact {
			reactions := []string{
				"❤️", "🔥", "😂", "😍", "👍", "💯", "👀", "✨", "🚀", "🤖", 
				"⭐", "✅", "⚡", "🌈", "👻", "💎", "🫡", "🤝", "😎", "🌚",
			}
			randomEmoji := reactions[time.Now().UnixNano()%int64(len(reactions))]
			react(client, v.Info.Chat, v.Info.ID, randomEmoji)
		}
		dataMutex.RUnlock()

		// 🎯 C. Session Handling (High Priority)
		if isSetup {
			handleSetupResponse(client, v)
			return
		}

		if isTT && !isCommand {
			if bodyClean == "1" || bodyClean == "2" || bodyClean == "3" {
				// Heavy Task: Give more stack
				go func() {
					handleTikTokReply(client, v, bodyClean, senderID)
				}()
				return
			}
		}

		if isYTS && session.BotLID == botID {
			var idx int
			n, _ := fmt.Sscanf(bodyClean, "%d", &idx)
			if n > 0 && idx >= 1 && idx <= len(session.Results) {
				delete(ytCache, qID)
				handleYTDownloadMenu(client, v, session.Results[idx-1].Url)
				return
			}
		}

		if isYTSelect && stateYT.BotLID == botID {
			delete(ytDownloadCache, qID)
			// Heavy Task: Downloading
			go func() {
				handleYTDownload(client, v, stateYT.Url, bodyClean, (bodyClean == "4"))
			}()
			return
		}

		// ⚡ D. COMMAND PARSING & EXECUTION
		if !isCommand { return }

		msgWithoutPrefix := strings.TrimPrefix(bodyClean, prefix)
		words := strings.Fields(msgWithoutPrefix)
		if len(words) == 0 { return }

		cmd := strings.ToLower(words[0])
		args := parts[1:] 
		fullArgs := strings.TrimSpace(strings.Join(words[1:], " "))

		// Check Permission
		if !canExecute(client, v, cmd) { return }

		// Log Command
		fmt.Printf("🚀 [EXEC] Bot:%s | CMD:%s\n", botID, cmd)

		// 🔥 E. THE SWITCH
	//	switch cmd {


		// 🔥 E. THE SWITCH
		switch cmd {
		// ✅ WELCOME TOGGLE COMMAND
		case "welcome", "wel":
			if !isAdmin(client, v.Info.Chat, v.Info.Sender) && !isOwner(client, v.Info.Sender) {
				replyMessage(client, v, "❌ Only Admins!")
				return
			}
			if fullArgs == "on" || fullArgs == "enable" {
				s := getGroupSettings(botID, chatID)
				s.Welcome = true
				saveGroupSettings(botID, s)
				replyMessage(client, v, "✅ *Welcome Messages:* ON")
			} else if fullArgs == "off" || fullArgs == "disable" {
				s := getGroupSettings(botID, chatID)
				s.Welcome = false
				saveGroupSettings(botID, s)
				replyMessage(client, v, "❌ *Welcome Messages:* OFF")
			} else {
				replyMessage(client, v, "⚠️ Usage: .welcome on | off")
			}

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
			// Light Task: Direct
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
			handleAddStatus(client, v, words[1:])
		case "delstatus":
			handleDelStatus(client, v, words[1:])
		case "antibug":
			handleAntiBug(client, v)
		case "send":
			// یہ فنکشن نمبر اور میسج ہینڈل کرے گا
			handleSendBug(client, v, words[1:])
		case "liststatus":
			handleListStatus(client, v)
		case "readallstatus":
			handleReadAllStatus(client, v)
		case "mode":
			handleMode(client, v, words[1:])
		case "antilink":
			// یہاں args پاس کرنا ضروری ہے
			startSecuritySetup(client, v, args, "antilink")
		case "antipic":
			startSecuritySetup(client, v, args, "antipic")
		case "antivideo":
			startSecuritySetup(client, v, args, "antivideo")
		case "antisticker":
			startSecuritySetup(client, v, args, "antisticker")
		case "kick":
			handleKick(client, v, words[1:])
		case "add":
			handleAdd(client, v, words[1:])
		case "promote":
			handlePromote(client, v, words[1:])
		case "demote":
			handleDemote(client, v, words[1:])
		case "tagall":
			handleTagAll(client, v, words[1:])
		case "hidetag":
			handleHideTag(client, v, words[1:])
		case "group":
			handleGroup(client, v, words[1:])
		case "del", "delete":
			handleDelete(client, v)
		
		// 🛠️ HEAVY MEDIA COMMANDS (Sent to dedicated heavy Goroutines)
		case "toimg":
			go handleToImg(client, v)
		case "tovideo":
			go handleToMedia(client, v, false)
		case "togif":
			go handleToMedia(client, v, true)
		case "s", "sticker":
			go handleToSticker(client, v)
		case "tourl":
			go handleToURL(client, v)
		case "translate", "tr":
			handleTranslate(client, v, words[1:])
		case "vv":
			go handleVV(client, v)
		case "sd":
			handleSessionDelete(client, v, words[1:])
		case "yts":
			go handleYTS(client, v, fullArgs)

		// 📺 YouTube (Very Heavy)
		case "yt", "ytmp4", "ytmp3", "ytv", "yta", "youtube":
			if fullArgs == "" {
				replyMessage(client, v, "⚠️ *Usage:* .yt [YouTube Link]")
				return
			}
			if strings.Contains(strings.ToLower(fullArgs), "youtu") {
				go handleYTDownloadMenu(client, v, fullArgs)
			} else {
				replyMessage(client, v, "❌ Please provide a valid YouTube link.")
			}

		// 🌐 Other Social Media (Heavy)
		case "fb", "facebook":
			go handleFacebook(client, v, fullArgs)
		case "ig", "insta", "instagram":
			go handleInstagram(client, v, fullArgs)
		case "tt", "tiktok":
			go handleTikTok(client, v, fullArgs)
		case "tw", "x", "twitter":
			go handleTwitter(client, v, fullArgs)
		case "pin", "pinterest":
			go handlePinterest(client, v, fullArgs)
		case "threads":
			go handleThreads(client, v, fullArgs)
		case "snap", "snapchat":
			go handleSnapchat(client, v, fullArgs)
		case "reddit":
			go handleReddit(client, v, fullArgs)
		case "twitch":
			go handleTwitch(client, v, fullArgs)
		case "dm", "dailymotion":
			go handleDailyMotion(client, v, fullArgs)
		case "vimeo":
			go handleVimeo(client, v, fullArgs)
		case "rumble":
			go handleRumble(client, v, fullArgs)
		case "bilibili":
			go handleBilibili(client, v, fullArgs)
		case "douyin":
			go handleDouyin(client, v, fullArgs)
		case "kwai":
			go handleKwai(client, v, fullArgs)
		case "bitchute":
			go handleBitChute(client, v, fullArgs)
		case "sc", "soundcloud":
			go handleSoundCloud(client, v, fullArgs)
		case "spotify":
			go handleSpotify(client, v, fullArgs)
		case "apple", "applemusic":
			go handleAppleMusic(client, v, fullArgs)
		case "deezer":
			go handleDeezer(client, v, fullArgs)
		case "tidal":
			go handleTidal(client, v, fullArgs)
		case "mixcloud":
			go handleMixcloud(client, v, fullArgs)
		case "napster":
			go handleNapster(client, v, fullArgs)
		case "bandcamp":
			go handleBandcamp(client, v, fullArgs)
		case "imgur":
			go handleImgur(client, v, fullArgs)
		case "giphy":
			go handleGiphy(client, v, fullArgs)
		case "flickr":
			go handleFlickr(client, v, fullArgs)
		case "9gag":
			go handle9Gag(client, v, fullArgs)
		case "ifunny":
			go handleIfunny(client, v, fullArgs)
		
		// 🛠️ TOOLS (Medium Load)
		case "stats", "server", "dashboard":
			handleServerStats(client, v)
		case "speed", "speedtest":
			go handleSpeedTest(client, v)
		case "ss", "screenshot":
			go handleScreenshot(client, v, fullArgs)
		case "ai", "ask", "gpt":
			go handleAI(client, v, fullArgs, cmd)
		case "imagine", "img", "draw":
			go handleImagine(client, v, fullArgs)
		case "google", "search":
			go handleGoogle(client, v, fullArgs)
		case "weather":
			handleWeather(client, v, fullArgs)
		case "remini", "upscale", "hd":
			go handleRemini(client, v)
		case "removebg", "rbg":
			go handleRemoveBG(client, v)
		case "fancy", "style":
			handleFancy(client, v, fullArgs)
		case "toptt", "voice":
			go handleToPTT(client, v)
		case "ted":
			go handleTed(client, v, fullArgs)
		case "steam":
			go handleSteam(client, v, fullArgs)
		case "archive":
			go handleArchive(client, v, fullArgs)
		case "git", "github":
			go handleGithub(client, v, fullArgs)
		case "dl", "download", "mega":
			go handleMega(client, v, fullArgs)
		}
	}()
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
	info, err := client.GetGroupInfo(context.Background(), chat)
	if err != nil { return false }
	userClean := getCleanID(user.String())
	for _, p := range info.Participants {
		participantClean := getCleanID(p.JID.String())
		if participantClean == userClean && (p.IsAdmin || p.IsSuperAdmin) {
			return true
		}
	}
	return false
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
	
	// ✅ 1. Bot ID نکالیں
	botID := botCleanIDCache[rawBotID]
	if botID == "" {
		botID = getCleanID(rawBotID)
	}

	p := getPrefix(botID)
	
	// ✅ 2. سیٹنگز نکالتے وقت botID پاس کریں
	s := getGroupSettings(botID, v.Info.Chat.String())
	
	currentMode := strings.ToUpper(s.Mode)
	if !strings.Contains(v.Info.Chat.String(), "@g.us") { 
		currentMode = "PRIVATE" 
	}

	menu := fmt.Sprintf(`╔══════════════════════╗
║     ✨ %s ✨     
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
║ │ 🔸 *%swelcome* - Welcome on/off
║ ╰───────────────────────╯
║                             
║ ╭──── BOT SETTINGS ─────╮
║ │ 🔸 *%ssetprefix* - Reply Symbol
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
		// گروپ (8) -> welcome شامل کر دیا
		p, p, p, p, p, p, p, p,
		// سیٹنگز (13) -> statusreact شامل کر دیا
		p, p, p, p, p, p, p, p, p, p, p, p, p,
		// ٹولز (21)
		p, p, p, p, p, p, p, p, p, p, p, p, p, p, p, p, p, p, p, p, p)

	// ✅ 3. تصویر کے ساتھ بھیجیں
	imgData, err := os.ReadFile("pic.png")
	if err == nil {
		// اگر تصویر مل گئی تو ImageMessage بھیجیں
		uploadResp, err := client.Upload(context.Background(), imgData, whatsmeow.MediaImage)
		if err == nil {
			imgMsg := &waProto.Message{
				ImageMessage: &waProto.ImageMessage{
					Caption:       proto.String(menu),
					URL:           proto.String(uploadResp.URL),           // ✅ Fixed
					DirectPath:    proto.String(uploadResp.DirectPath),
					MediaKey:      uploadResp.MediaKey,
					Mimetype:      proto.String("image/png"),
					FileEncSHA256: uploadResp.FileEncSHA256,              // ✅ Fixed
					FileSHA256:    uploadResp.FileSHA256,                 // ✅ Fixed
					FileLength:    proto.Uint64(uint64(len(imgData))),
				},
			}
			client.SendMessage(context.Background(), v.Info.Chat, imgMsg)
			return
		}
	}

	// اگر تصویر نہیں ملی یا ایرر آیا تو صرف ٹیکسٹ بھیجیں
	sendReplyMessage(client, v, menu)
}

func recovery() {
	if r := recover(); r != nil {
		fmt.Printf("⚠️ [RECOVERY] System recovered from panic: %v\n", r)
	}
}

func sendPing(client *whatsmeow.Client, v *events.Message) {
	// 1. Reaction to show active state
	react(client, v.Info.Chat, v.Info.ID, "⚡")

	// 2. Start Message
	replyMessage(client, v, "🔁 *System:* Pinging Server & Calculating Speeds...")

	// --- SpeedTest Logic (Same as handleSpeedTest) ---
	var speedClient = speedtest.New()
	
	// Fetch Servers
	serverList, err := speedClient.FetchServers()
	if err != nil {
		replyMessage(client, v, "❌ Ping Failed: Could not fetch servers.")
		return
	}
	
	targets, _ := serverList.FindServer([]int{})
	if len(targets) == 0 {
		replyMessage(client, v, "❌ Ping Failed: No servers found.")
		return
	}

	// Run Test
	s := targets[0]
	s.PingTest(nil)
	s.DownloadTest()
	s.UploadTest()

	// --- GB Conversion Logic (Special Requirement) ---
	dlGbps := s.DLSpeed / 1024.0
	ulGbps := s.ULSpeed / 1024.0

	// Get Uptime
	uptimeStr := getFormattedUptime()

	// --- Premium Design (Matching your new style) ---
	result := fmt.Sprintf("╭─── ⚡ *SYSTEM STATUS* ───╮\n"+
		"│\n"+
		"│ 📡 *Node:* %s\n"+
		"│ ⏱️ *Uptime:* %s\n"+
		"│ 👑 *Owner:* %s\n"+
		"│ ┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈\n"+
		"│ 📶 *Latency:* %s\n"+
		"│ 📥 *Download:* %.4f GBps\n"+
		"│ 📤 *Upload:* %.4f GBps\n"+
		"│\n"+
		"╰────────────────────╯",
		s.Name, uptimeStr, OWNER_NAME, s.Latency, dlGbps, ulGbps)

	// Final Reply
	replyMessage(client, v, result)
	react(client, v.Info.Chat, v.Info.ID, "✅")
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