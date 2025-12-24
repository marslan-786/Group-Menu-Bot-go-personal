package main

import (
	"context"
	"fmt"
	"strings"
	"time"
    "net/http"
	"github.com/showwin/speedtest-go/speedtest"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
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
    // ⚡ Panic Recovery: تاکہ بوٹ کریش نہ ہو
    defer func() {
        if r := recover(); r != nil {
            fmt.Printf("⚠️ Critical Error in ProcessMessage: %v\n", r)
        }
    }()

    // 1️⃣ بنیادی معلومات نکالنا (JID ہینڈلنگ - Fast Memory Ops)
    rawBotID := client.Store.ID.User
    botID := botCleanIDCache[rawBotID]
    if botID == "" {
        botID = getCleanID(rawBotID)
    }

    prefix := getPrefix(botID)
    bodyRaw := getText(v.Message)
    if bodyRaw == "" {
        return
    }

    bodyClean := strings.TrimSpace(bodyRaw)
    senderID := v.Info.Sender.ToNonAD().String()
    chatID := v.Info.Chat.String()
    isGroup := v.Info.IsGroup

    // 🛠️ 2️⃣ ریپلائی آئی ڈی (qID) نکالنا
    var qID string
    if extMsg := v.Message.GetExtendedTextMessage(); extMsg != nil && extMsg.ContextInfo != nil {
        qID = extMsg.ContextInfo.GetStanzaID()
    }

    // 🔍 3️⃣ سیشنز اور اسٹیٹ چیک (Fast Map Lookups)
    session, isYTS := ytCache[qID]
    stateYT, isYTSelect := ytDownloadCache[qID]
    _, isSetup := setupMap[qID]
    _, isTT := ttCache[senderID]

    // 🛡️ 4️⃣ سیکیورٹی چیک (Async)
    // ✅ FIX: سیکیورٹی چیک کو Goroutine میں ڈال دیا تاکہ یہ مین تھریڈ کو نہ روکے
    if isGroup {
        go func() {
            defer recovery() // Helper function for recovery (نیچے دیکھیں)
            checkSecurity(client, v)
        }()
    }

    // 🚀 5️⃣ مین فلٹر: اگر کمانڈ نہیں ہے اور کوئی سیشن بھی نہیں، تو ریٹرن (CPU بچائیں)
    isAnySession := isSetup || isYTS || isYTSelect || isTT
    if !strings.HasPrefix(bodyClean, prefix) && !isAnySession && chatID != "status@broadcast" {
        return
    }

    // 📺 7️⃣ اسٹیٹس براڈکاسٹ ہینڈلنگ (Async)
    if chatID == "status@broadcast" {
        go func() {
            defer recovery()
            dataMutex.RLock()
            if data.AutoStatus {
                client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
                if data.StatusReact {
                    emojis := []string{"💚", "❤️", "🔥", "😍", "💯"}
                    react(client, v.Info.Chat, v.Info.ID, emojis[time.Now().UnixNano()%int64(len(emojis))])
                }
            }
            dataMutex.RUnlock()
        }()
        return
    }

    // 🔘 8️⃣ آٹو ریڈ اور ری ایکٹ (Completely Async - No Lag in Groups)
    // ✅ FIX: یہ حصہ گروپس میں لیگ کرتا تھا، اب یہ الگ تھریڈ میں ہے
    go func() {
        defer recovery()
        dataMutex.RLock()
        if data.AutoRead {
            client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
        }
        if data.AutoReact {
            react(client, v.Info.Chat, v.Info.ID, "❤️")
        }
        dataMutex.RUnlock()
    }()

    // =========================================================================
    // ⚡ SUPER FAST EXECUTION ENGINE (Goroutines for Heavy Tasks)
    // =========================================================================
    
    // ہم سارا Logic ایک Goroutine میں ڈالیں گے تاکہ اگلا میسج فوراً پک ہو جائے
    go func() {
        defer recovery() // Safe execution

        // 🎯 6️⃣ ترجیحی ریپلائی ہینڈلنگ (Priority Logic)
        if isSetup {
            handleSetupResponse(client, v)
            return
        }

        if isTT && !strings.HasPrefix(bodyClean, prefix) {
            if bodyClean == "1" || bodyClean == "2" || bodyClean == "3" {
                handleTikTokReply(client, v, bodyClean, senderID)
                return
            }
        }

        if qID != "" {
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
                // یہ پہلے ہی `go` تھا، لیکن اب یہ ڈبل سیف ہے
                handleYTDownload(client, v, stateYT.Url, bodyClean, (bodyClean == "4"))
                return
            }
        }

        // ⚡ 9️⃣ مین کمانڈ پارسنگ
        msgWithoutPrefix := strings.TrimPrefix(bodyClean, prefix)
        words := strings.Fields(msgWithoutPrefix)
        if len(words) == 0 {
            return
        }

        cmd := strings.ToLower(words[0])
        fullArgs := strings.TrimSpace(strings.Join(words[1:], " "))

        if !canExecute(client, v, cmd) {
            return
        }

        fmt.Printf("🚀 [ASYNC EXEC] Bot: %s | CMD: %s | Arg: %s\n", botID, cmd, fullArgs)

        // 🔥 THE SWITCH: ہر کیس اب مین تھریڈ کو بلاک نہیں کرے گا کیونکہ یہ پورا بلاک `go func` میں ہے
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
            handleAddStatus(client, v, words[1:])
        case "delstatus":
            handleDelStatus(client, v, words[1:])
        case "liststatus":
            handleListStatus(client, v)
        case "readallstatus":
            handleReadAllStatus(client, v)
        case "mode":
            handleMode(client, v, words[1:])
        case "antilink":
            startSecuritySetup(client, v, "antilink")
        case "antipic":
            startSecuritySetup(client, v, "antipic")
        case "antivideo":
            startSecuritySetup(client, v, "antivideo")
        case "antisticker":
            startSecuritySetup(client, v, "antisticker")
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
        case "toimg":
            handleToImg(client, v)
        case "tovideo":
            handleToMedia(client, v, false)
        case "togif":
            handleToMedia(client, v, true)
        case "s", "sticker":
            handleToSticker(client, v)
        case "tourl":
            handleToURL(client, v)
        case "translate", "tr":
            handleTranslate(client, v, words[1:])
        case "vv":
            handleVV(client, v)
        case "sd":
            handleSessionDelete(client, v, words[1:])
        case "yts":
            handleYTS(client, v, fullArgs)

        // 📺 YouTube
        case "yt", "ytmp4", "ytmp3", "ytv", "yta", "youtube":
            if fullArgs == "" {
                replyMessage(client, v, "⚠️ *Usage:* .yt [YouTube Link]")
                return
            }
            if strings.Contains(strings.ToLower(fullArgs), "youtu") {
                handleYTDownloadMenu(client, v, fullArgs)
            } else {
                replyMessage(client, v, "❌ Please provide a valid YouTube link.")
            }

        // 🌐 Other Social Media (Heavy Tasks)
        case "fb", "facebook":
            handleFacebook(client, v, fullArgs)
        case "ig", "insta", "instagram":
            handleInstagram(client, v, fullArgs)
        case "tt", "tiktok":
            handleTikTok(client, v, fullArgs)
        case "tw", "x", "twitter":
            handleTwitter(client, v, fullArgs)
        case "pin", "pinterest":
            handlePinterest(client, v, fullArgs)
        case "threads":
            handleThreads(client, v, fullArgs)
        case "snap", "snapchat":
            handleSnapchat(client, v, fullArgs)
        case "reddit":
            handleReddit(client, v, fullArgs)
        case "twitch":
            handleTwitch(client, v, fullArgs)
        case "dm", "dailymotion":
            handleDailyMotion(client, v, fullArgs)
        case "vimeo":
            handleVimeo(client, v, fullArgs)
        case "rumble":
            handleRumble(client, v, fullArgs)
        case "bilibili":
            handleBilibili(client, v, fullArgs)
        case "douyin":
            handleDouyin(client, v, fullArgs)
        case "kwai":
            handleKwai(client, v, fullArgs)
        case "bitchute":
            handleBitChute(client, v, fullArgs)
        case "sc", "soundcloud":
            handleSoundCloud(client, v, fullArgs)
        case "spotify":
            handleSpotify(client, v, fullArgs)
        case "apple", "applemusic":
            handleAppleMusic(client, v, fullArgs)
        case "deezer":
            handleDeezer(client, v, fullArgs)
        case "tidal":
            handleTidal(client, v, fullArgs)
        case "mixcloud":
            handleMixcloud(client, v, fullArgs)
        case "napster":
            handleNapster(client, v, fullArgs)
        case "bandcamp":
            handleBandcamp(client, v, fullArgs)
        case "imgur":
            handleImgur(client, v, fullArgs)
        case "giphy":
            handleGiphy(client, v, fullArgs)
        case "flickr":
            handleFlickr(client, v, fullArgs)
        case "9gag":
            handle9Gag(client, v, fullArgs)
        case "ifunny":
            handleIfunny(client, v, fullArgs)
        case "stats", "server", "dashboard":
            handleServerStats(client, v)
        case "speed", "speedtest":
            handleSpeedTest(client, v)
        case "ss", "screenshot":
            handleScreenshot(client, v, fullArgs)
        case "ai", "ask", "gpt":
            handleAI(client, v, fullArgs, cmd)
        case "imagine", "img", "draw":
            handleImagine(client, v, fullArgs)
        case "google", "search":
            handleGoogle(client, v, fullArgs)
        case "weather":
            handleWeather(client, v, fullArgs)
        case "remini", "upscale", "hd":
            handleRemini(client, v)
        case "removebg", "rbg":
            handleRemoveBG(client, v)
        case "fancy", "style":
            handleFancy(client, v, fullArgs)
        case "toptt", "voice":
            handleToPTT(client, v)
        case "ted":
            handleTed(client, v, fullArgs)
        case "steam":
            handleSteam(client, v, fullArgs)
        case "archive":
            handleArchive(client, v, fullArgs)
        case "git", "github":
            handleGithub(client, v, fullArgs)
        case "dl", "download", "mega":
            handleMega(client, v, fullArgs)
        }
    }() // 🔚 End of Command Execution Goroutine
}

// ✅ Helper function to recover from panics inside goroutines
func recovery() {
    if r := recover(); r != nil {
        fmt.Println("⚠️ Recovered from background panic:", r)
    }
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
	// --- 1. Real Latency Test (Ping) ---
	start := time.Now()
	_, err := http.Get("https://www.google.com")
	if err != nil {
		fmt.Println("Ping Error:", err)
	}
	latency := time.Since(start).Milliseconds()

	// --- 2. Real Speed Test (GB Mode) ---
	var dlSpeedGbps, ulSpeedGbps float64
	
	user, _ := speedtest.FetchUserInfo()
	serverList, _ := speedtest.FetchServerList(user)
	targets, _ := serverList.FindServer([]int{})

	if len(targets) > 0 {
		s := targets[0]
		_ = s.PingTest(nil)
		_ = s.DownloadTest()
		_ = s.UploadTest()

		// Convert Mbps to Gbps (Division by 1024)
		dlSpeedGbps = s.DLSpeed / 1024.0
		ulSpeedGbps = s.ULSpeed / 1024.0
	}

	uptimeStr := getFormattedUptime() 

	// --- 3. Heavy Style Message Formatting (GB Version) ---
    // %.4f ka matlab hai ashariyah (.) ke baad 4 hindsay dikhaye ga (e.g 1.0245 GB)
	msg := fmt.Sprintf(`╔════════════════════════╗
║    ⚡ 𝐒𝐘𝐒𝐓𝐄𝐌 𝐒𝐓𝐀𝐓𝐔𝐒 ⚡
╠════════════════════════╣
║ 📶 𝐋𝐚𝐭𝐞𝐧𝐜𝐲   : %d ms
║ ⬇️ 𝐃𝐨𝐰𝐧𝐥𝐨𝐚𝐝  : %.4f GBps
║ ⬆️ 𝐔𝐩𝐥𝐨𝐚𝐝    : %.4f GBps
╠════════════════════════╣
║ ⏱️ 𝐔𝐩𝐭𝐢𝐦𝐞    : %s
║ 👑 𝐎𝐰𝐧𝐞𝐫     : %s
╠════════════════════════╣
║   🟢 𝐒𝐞𝐫𝐯𝐞𝐫 𝐢𝐬 𝐎𝐧𝐥𝐢𝐧𝐞
╚════════════════════════╝`, latency, dlSpeedGbps, ulSpeedGbps, uptimeStr, OWNER_NAME)

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