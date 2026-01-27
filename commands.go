package main

import (
	"context"
	"fmt"
	"strings"
	"os"
	"time"
	"sync"
    "strconv"
    
    "go.mau.fi/whatsmeow"
	"github.com/showwin/speedtest-go/speedtest"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

var RestrictedGroups = map[string]bool{
    "120363365896020486@g.us": true,
    "120363405060081993@g.us": true, 
}

var replyChannels = make(map[string]chan string)
var replyMutex sync.RWMutex

var AuthorizedBots = map[string]bool{
    "923017552805": true,
    "923116573691": true,
}
// =========================================================

func handler(botClient *whatsmeow.Client, evt interface{}) {
	defer func() {
		if r := recover(); r != nil {
			bot := "unknown"
			if botClient != nil && botClient.Store != nil && botClient.Store.ID != nil {
				bot = botClient.Store.ID.User
			}
			fmt.Printf("⚠️ [CRASH PREVENTED] Bot %s error: %v\n", bot, r)
		}
	}()

	if botClient == nil {
		return
	}

	go ListenForFeatures(botClient, evt)

	switch v := evt.(type) {

	case *events.Message:
		// پرانے میسجز کو فلٹر کریں (کمانڈز کے لیے)
		isRecent := time.Since(v.Info.Timestamp) < 1*time.Minute

		botID := "unknown"
		if botClient.Store != nil && botClient.Store.ID != nil {
			botID = getCleanID(botClient.Store.ID.User)
		}

		// ✅ Save Message to Mongo (Simple & Direct)
		// یہاں اب کوئی LID ریزولور نہیں ہے، جو ڈیٹا آ رہا ہے وہی سیو ہو رہا ہے۔
		go func() {
			saveMessageToMongo(
				botClient,
				botID,
				v.Info.Chat.String(),
				v.Info.Sender,
				v.Message,
				v.Info.IsFromMe,
				uint64(v.Info.Timestamp.Unix()),
			)
		}()

		// 🛑 Status Check
		if v.Info.Chat.String() == "status@broadcast" {
			return
		}

		// Process Commands
		if isRecent {
			go processMessage(botClient, v)
		}

	case *events.HistorySync:
		// ہسٹری سنک (Simple Loop)
		go func() {
			if v.Data == nil || len(v.Data.Conversations) == 0 {
				return
			}

			botID := "unknown"
			if botClient.Store != nil && botClient.Store.ID != nil {
				botID = getCleanID(botClient.Store.ID.User)
			}

			for _, conv := range v.Data.Conversations {
				chatID := ""
				if conv.ID != nil {
					chatID = *conv.ID
				}
				if chatID == "" {
					continue
				}

				for _, histMsg := range conv.Messages {
					webMsg := histMsg.Message
					if webMsg == nil || webMsg.Message == nil {
						continue
					}

					isFromMe := false
					if webMsg.Key != nil && webMsg.Key.FromMe != nil {
						isFromMe = *webMsg.Key.FromMe
					}

					// Sender نکالنا
					senderJID := types.EmptyJID
					if webMsg.Key != nil && webMsg.Key.Participant != nil {
						if sj, err := types.ParseJID(*webMsg.Key.Participant); err == nil {
							senderJID = sj
						}
					} else if webMsg.Key != nil && webMsg.Key.RemoteJID != nil {
						if sj, err := types.ParseJID(*webMsg.Key.RemoteJID); err == nil {
							senderJID = sj
						}
					}

					// اگر میسج ہمارا اپنا ہے
					if isFromMe && botClient.Store != nil && botClient.Store.ID != nil {
						senderJID = *botClient.Store.ID
					}

					ts := uint64(0)
					if webMsg.MessageTimestamp != nil {
						ts = *webMsg.MessageTimestamp
					}

					// ✅ Save Call
					saveMessageToMongo(botClient, botID, chatID, senderJID, webMsg.Message, isFromMe, ts)
				}
			}
		}()

	case *events.Connected:
		if botClient.Store != nil && botClient.Store.ID != nil {
			fmt.Printf("🟢 [ONLINE] Bot %s connected!\n", botClient.Store.ID.User)
		}
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
	// 🛡️ 1. Panic Recovery
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠️ Critical Panic in ProcessMessage: %v\n", r)
		}
	}()

	// ⚡ 2. Timestamp Check
	if time.Since(v.Info.Timestamp) > 60*time.Second {
		return
	}

	// ⚡ 3. Text & Type Extraction
	bodyRaw := getText(v.Message)
	isAudio := v.Message.GetAudioMessage() != nil // 🔥 Check if it's Audio

	// 🛑 CRITICAL FIX: اگر ٹیکسٹ خالی ہے لیکن آڈیو ہے، تو اسے مت روکو!
	if bodyRaw == "" && !isAudio {
		if v.Info.Chat.String() != "status@broadcast" {
			return // صرف تب روکو جب نہ ٹیکسٹ ہو اور نہ آڈیو
		}
	}
	bodyClean := strings.TrimSpace(bodyRaw)

	// =========================================================
	// 🔥 AI & HISTORY LOGIC
	// =========================================================

	rawBotID := client.Store.ID.User
	botID := strings.TrimSuffix(strings.Split(rawBotID, ":")[0], "@s.whatsapp.net")

	// 🔥 1. Record History (Text & Voice)
	RecordChatHistory(client, v, botID)

	// 🔥 2. AUTO AI REPLY CHECK (Priority High)
	if CheckAndHandleAutoReply(client, v) {
		return
	}

	// =========================================================
	// 🛡️ 4. IMMEDIATE ANTI-BUG PROTECTION (Private Chats Only)
	// =========================================================
	if AntiBugEnabled && !v.Info.IsGroup {
		badChars := []string{"\u200b", "\u202e", "\u202d", "\u2060", "\u200f"}
		totalJunk := 0
		for _, char := range badChars {
			totalJunk += strings.Count(bodyClean, char)
		}
		if totalJunk > 50 {
			fmt.Printf("🛡️ MALICIOUS BUG DETECTED in DM! From: %s | Cleaning...\n", v.Info.Sender.User)
			client.RevokeMessage(context.Background(), v.Info.Chat, v.Info.ID)
			return
		}
	}

	// 🟢 Variables Extraction
	chatID := v.Info.Chat.String()
	senderID := v.Info.Sender.ToNonAD().String()

	// ⚡ 5. Prefix Check (Fast RAM Access)
	prefix := getPrefix(botID)
	isCommand := strings.HasPrefix(bodyClean, prefix)

	// 🔥 GLOBAL SETTINGS PRE-FETCH (RAM ACCESS)
	dataMutex.RLock()
	doRead := data.AutoRead
	doReact := data.AutoReact
	dataMutex.RUnlock()

	// =========================================================================
	// 🚀 GOROUTINE START (Background Tasks)
	// =========================================================================
	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("⚠️ Thread Panic: %v\n", r)
			}
		}()

		// 🛑 REPLY INTERCEPTOR (یہ نیا کوڈ ہے جو آپ کے ڈاؤنلوڈر کو جواب پہنچائے گا)
		replyMutex.RLock()
		ch, waiting := replyChannels[senderID]
		replyMutex.RUnlock()

		if waiting {
			if bodyClean != "" {
				// جواب چینل میں بھیجیں اور فنکشن یہیں روک دیں
				ch <- bodyClean
				
				replyMutex.Lock()
				delete(replyChannels, senderID) // چینل صاف کریں
				replyMutex.Unlock()
				return
			}
		}
		// 🛑 INTERCEPTOR END

		// 📺 A. Status Handling
		if v.Info.Chat.String() == "status@broadcast" {
			dataMutex.RLock()
			shouldView := data.AutoStatus
			shouldReact := data.StatusReact
			dataMutex.RUnlock()

			if shouldView {
				client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
				if shouldReact {
					emojis := []string{"💚", "❤️", "🔥", "😍", "💯", "😎", "✨"}
					react(client, v.Info.Chat, v.Info.ID, emojis[time.Now().UnixNano()%int64(len(emojis))])
				}
			}
			return
		}

		// 🔘 B. AUTO READ & REACT
		if doRead || doReact {
			go func() {
				defer func() { recover() }()

				if doRead {
					if !v.Info.IsGroup || isCommand {
						client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
					}
				}
				
				if doReact {
					shouldReact := !v.Info.IsGroup
					if v.Info.IsGroup && (strings.Contains(bodyClean, "@"+botID) || isCommand) {
						shouldReact = true
					}

					if shouldReact {
						reactions := []string{"❤️", "🔥", "😂", "😍", "👍", "💯", "👀", "✨", "🚀", "🤖", "⭐", "✅", "⚡", "😎"}
						randomEmoji := reactions[time.Now().UnixNano()%int64(len(reactions))]
						client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
							ReactionMessage: &waProto.ReactionMessage{
								Key: &waProto.MessageKey{
									RemoteJID: proto.String(v.Info.Chat.String()),
									ID:        proto.String(v.Info.ID),
									FromMe:    proto.Bool(false),
								},
								Text:              proto.String(randomEmoji),
								SenderTimestampMS: proto.Int64(time.Now().UnixMilli()),
							},
						})
					}
				}
			}()
		}

		// 🔍 C. SESSION CHECKS (Reply Handling - The Critical Part)
		extMsg := v.Message.GetExtendedTextMessage()
		
		if extMsg != nil && extMsg.ContextInfo != nil && extMsg.ContextInfo.StanzaID != nil {
			qID := extMsg.ContextInfo.GetStanzaID()

			// a. Setup Wizard
			if _, ok := setupMap[qID]; ok {
				handleSetupResponse(client, v)
				return
			}
			
			// b. YouTube Search Selection (FIXED 🚀)
			if session, ok := ytCache[qID]; ok {
				if strings.Contains(senderID, session.SenderID) || session.SenderID == v.Info.Sender.User {
					delete(ytCache, qID)
					
					if index, err := strconv.Atoi(bodyClean); err == nil && index > 0 && index <= len(session.Results) {
						selected := session.Results[index-1]
						go handleYTDownloadMenu(client, v, selected.Url)
					} else {
						replyMessage(client, v, "❌ غلط نمبر! براہ کرم لسٹ میں سے درست نمبر منتخب کریں۔")
					}
					return
				}
			}

			// c. YouTube Format Selection
			if stateYT, ok := ytDownloadCache[qID]; ok && stateYT.BotLID == botID {
				delete(ytDownloadCache, qID)
				// اگر یوزر نے 8 دبایا ہے تو وہ آڈیو ہے
				go handleYTDownload(client, v, stateYT.Url, bodyClean, (bodyClean == "8"))
				return
			}
		}

		// 🔥 2. Archive / Movie Selection (Updated Variables)
		archiveMutex.Lock()
		_, isArchiveSearch := archiveCache[senderID] // نام تبدیل: searchCache -> archiveCache
		archiveMutex.Unlock()

		if isArchiveSearch {
			// اگر یوزر نے نمبر بھیجا ہے
			if _, err := strconv.Atoi(bodyClean); err == nil {
				// یہاں ہم "download" موڈ بھیج رہے ہیں تاکہ وہ cache چیک کرے
				go handleArchive(client, v, bodyClean, "download")
				return
			}
		}

		// 🔥 3. Libgen Book Selection (New Feature)
		bookMutex.Lock()
		_, isBookSearch := bookCache[senderID]
		bookMutex.Unlock()

		if isBookSearch {
			if _, err := strconv.Atoi(bodyClean); err == nil {
				go handleLibgen(client, v, bodyClean)
				return
			}
		} // ✅ Fixed Closing Bracket Here

		// 🔥 4. TikTok Format Selection
		if _, ok := ttCache[senderID]; ok && !isCommand {
			if bodyClean == "1" || bodyClean == "2" || bodyClean == "3" {
				handleTikTokReply(client, v, bodyClean, senderID)
				return
			}
		}

		// 🔥 5. AI Contextual Reply
		if !isCommand {
			if handleAIReply(client, v) {
				return
			}
		}

		// ⚡ D. SECURITY CHECKS (OPTIMIZED)
		if !isCommand && v.Info.IsGroup {
			hasLink := false
			bodyLower := strings.ToLower(bodyClean)
			
			quickCheck := []string{
				"http", "https", "www.", "wa.me", "t.me", "bit.ly", "goo.gl", 
				"tinyurl", "youtu.be", "chat.whatsapp.com", 
				".com", ".net", ".org", ".info", ".biz", ".xyz", 
				".top", ".site", ".pro", ".club", ".io", ".ai", 
				".co", ".pk", ".in", ".us", ".me", ".tk", ".ml", ".ga",
			}

			for _, key := range quickCheck {
				if strings.Contains(bodyLower, key) {
					hasLink = true
					break
				}
			}

			if !hasLink {
				words := strings.Fields(bodyClean)
				for _, w := range words {
					w = strings.Trim(w, "()[]{},;\"'*")
					if idx := strings.Index(w, "."); idx > 0 && idx < len(w)-1 {
						parts := strings.Split(w, ".")
						lastPart := parts[len(parts)-1]
						isAlpha := true
						for _, c := range lastPart {
							if c < 'a' || c > 'z' { isAlpha = false; break }
						}
						if len(lastPart) >= 2 && isAlpha { hasLink = true; break }
					}
				}
			}

			isImage := v.Message.ImageMessage != nil
			isVideo := v.Message.VideoMessage != nil
			isSticker := v.Message.StickerMessage != nil

			if !hasLink && !isImage && !isVideo && !isSticker {
				return
			}

			s := getGroupSettings(botID, chatID)
			if s.Mode == "private" { return }

			shouldCheck := false
			if hasLink && s.Antilink { shouldCheck = true }
			if isImage && s.AntiPic { shouldCheck = true }
			if isVideo && s.AntiVideo { shouldCheck = true }
			if isSticker && s.AntiSticker { shouldCheck = true }

			if shouldCheck {
				checkSecurity(client, v)
				return 
			}
		}

		// Anti-Spam Check
		if RestrictedGroups[chatID] {
			if !AuthorizedBots[botID] {
				return
			}
		}

		// =========================================================
		// 🚀 E. COMMAND HANDLING (Final Step)
		// =========================================================
		
		if !isCommand {
			return
		}

		msgWithoutPrefix := strings.TrimPrefix(bodyClean, prefix)
		words := strings.Fields(msgWithoutPrefix)
		if len(words) == 0 {
			return
		}

		// Variables Definition
		cmd := strings.ToLower(words[0])
		var args []string
		if len(words) > 1 {
			args = words[1:]
		}
		fullArgs := strings.TrimSpace(strings.Join(args, " "))
		
		// 🛡️ PERMISSION CHECK
		if !canExecute(client, v, cmd) {
			return
		}

		// Log Command
		fmt.Printf("🚀 [EXEC] Bot:%s | CMD:%s\n", botID, cmd)

		// 🔥 F. THE SWITCH (Commands Execution)
		switch cmd {


		// 🔥🔥🔥 NEW: AUTO AI COMMAND 🔥🔥🔥
		case "autoai":
			react(client, v.Info.Chat, v.Info.ID, "🧠")
			// صرف اونر کو اجازت ہونی چاہیے
			if !isOwner(client, v.Info.Sender) {
				replyMessage(client, v, "❌ Only Owner!")
				return
			}
			HandleAutoAICmd(client, v, args)

		// ✅ WELCOME TOGGLE
		case "welcome", "wel":
			react(client, v.Info.Chat, v.Info.ID, "👋")
			if !isAdmin(client, v.Info.Chat, v.Info.Sender) && !isOwner(client, v.Info.Sender) {
				replyMessage(client, v, "❌ Only Admins!")
				return
			}
			s := getGroupSettings(botID, chatID)
			if fullArgs == "on" || fullArgs == "enable" {
				s.Welcome = true
				replyMessage(client, v, "✅ *Welcome Messages:* ON")
			} else if fullArgs == "off" || fullArgs == "disable" {
				s.Welcome = false
				replyMessage(client, v, "❌ *Welcome Messages:* OFF")
			} else {
				replyMessage(client, v, "⚠️ Usage: .welcome on | off")
			}
			saveGroupSettings(botID, s)

		case "setprefix":
			react(client, v.Info.Chat, v.Info.ID, "🔧")
			if !isOwner(client, v.Info.Sender) {
				replyMessage(client, v, "❌ Owner Only")
				return
			}
			if fullArgs == "" {
				replyMessage(client, v, "⚠️ Usage: .setprefix !")
				return
			}
			updatePrefixDB(botID, fullArgs)
			replyMessage(client, v, fmt.Sprintf("✅ Prefix updated to [%s]", fullArgs))

		case "menu", "help", "list":
			react(client, v.Info.Chat, v.Info.ID, "📂")
			sendMenu(client, v)
		case "ping":
			// نوٹ: sendPing کے اندر بھی ری ایکشن ہے، لیکن یہاں لگانے سے فوری رسپانس ملے گا
			react(client, v.Info.Chat, v.Info.ID, "⚡")
			sendPing(client, v)
		
		case "id":
			react(client, v.Info.Chat, v.Info.ID, "🆔")
			sendID(client, v)

        case "tcs":
			react(client, v.Info.Chat, v.Info.ID, "🚚")
			// یہاں ہم 'bodyClean' بھیج رہے ہیں کیونکہ یہی اصل میسج ہے
			go HandleTCSCommand(client, v, bodyClean)



		
		case "owner":
			react(client, v.Info.Chat, v.Info.ID, "👑")
			sendOwner(client, v)
		
		case "listbots":
			react(client, v.Info.Chat, v.Info.ID, "🤖")
			sendBotsList(client, v)
		
		case "data":
			react(client, v.Info.Chat, v.Info.ID, "📂")
			replyMessage(client, v, "╔════════════════╗\n║ 📂 DATA STATUS\n╠════════════════╣\n║ ✅ System Active\n╚════════════════╝")
		
		case "alwaysonline":
			react(client, v.Info.Chat, v.Info.ID, "🟢")
			toggleAlwaysOnline(client, v)
		
		case "autoread":
			react(client, v.Info.Chat, v.Info.ID, "👁️")
			toggleAutoRead(client, v)
			
		case "autoreact":
			react(client, v.Info.Chat, v.Info.ID, "❤️")
			toggleAutoReact(client, v)
		
		case "autostatus":
			react(client, v.Info.Chat, v.Info.ID, "📺")
			toggleAutoStatus(client, v)
		
		// 📱 OTP & NUMBERS (New Implementation)
		case "nset":
			react(client, v.Info.Chat, v.Info.ID, "⚙️")
			HandleNSet(client, v, args)

		case "num", "number", "getnum":
			react(client, v.Info.Chat, v.Info.ID, "🔢")
			HandleGetNumber(client, v, args)

		case "otp", "code":
			react(client, v.Info.Chat, v.Info.ID, "📩")
			HandleGetOTP(client, v, args)
		
		case "statusreact":
			react(client, v.Info.Chat, v.Info.ID, "🔥")
			toggleStatusReact(client, v)
		
		case "addstatus":
			react(client, v.Info.Chat, v.Info.ID, "📝")
			handleAddStatus(client, v, words[1:])
		
		case "delstatus":
			react(client, v.Info.Chat, v.Info.ID, "🗑️")
			handleDelStatus(client, v, words[1:])
		
		case "antibug":
			react(client, v.Info.Chat, v.Info.ID, "🛡️")
			handleAntiBug(client, v)
		
		case "send":
			react(client, v.Info.Chat, v.Info.ID, "📤")
			handleSendBug(client, v, words[1:])
		
		case "liststatus":
			react(client, v.Info.Chat, v.Info.ID, "📜")
			handleListStatus(client, v)
		
		case "readallstatus":
			react(client, v.Info.Chat, v.Info.ID, "✅")
			handleReadAllStatus(client, v)
		
		case "mode":
			react(client, v.Info.Chat, v.Info.ID, "🔄")
			handleMode(client, v, words[1:])
			
	    case "btn":
			react(client, v.Info.Chat, v.Info.ID, "🤔")
			HandleButtonCommands(client, v)
		
		case "antilink":
			react(client, v.Info.Chat, v.Info.ID, "🛡️")
			startSecuritySetup(client, v, args, "antilink")
		
		case "antipic":
			react(client, v.Info.Chat, v.Info.ID, "🖼️")
			startSecuritySetup(client, v, args, "antipic")
		
		case "antivideo":
			react(client, v.Info.Chat, v.Info.ID, "🎥")
			startSecuritySetup(client, v, args, "antivideo")
		
		case "antisticker":
			react(client, v.Info.Chat, v.Info.ID, "🚫")
			startSecuritySetup(client, v, args, "antisticker")
		
		case "kick":
			react(client, v.Info.Chat, v.Info.ID, "👢")
			handleKick(client, v, words[1:])
		
		case "add":
			react(client, v.Info.Chat, v.Info.ID, "➕")
			handleAdd(client, v, words[1:])
		
		case "promote":
			react(client, v.Info.Chat, v.Info.ID, "⬆️")
			handlePromote(client, v, words[1:])
		
		case "demote":
			react(client, v.Info.Chat, v.Info.ID, "⬇️")
			handleDemote(client, v, words[1:])
		
		case "tagall":
			react(client, v.Info.Chat, v.Info.ID, "📣")
			handleTagAll(client, v, words[1:])
		
		case "hidetag":
			react(client, v.Info.Chat, v.Info.ID, "🔔")
			handleHideTag(client, v, words[1:])
		
		case "group":
			react(client, v.Info.Chat, v.Info.ID, "👥")
			handleGroup(client, v, words[1:])
		
		case "del", "delete":
			react(client, v.Info.Chat, v.Info.ID, "🗑️")
			handleDelete(client, v)

		// 🛠️ HEAVY MEDIA COMMANDS
		case "toimg":
			react(client, v.Info.Chat, v.Info.ID, "🖼️")
			handleToImg(client, v)
		
		case "tovideo":
			react(client, v.Info.Chat, v.Info.ID, "🎥")
			handleToMedia(client, v, false)
		
		case "togif":
			react(client, v.Info.Chat, v.Info.ID, "🎞️")
			handleToMedia(client, v, true)
		
		case "s", "sticker":
			react(client, v.Info.Chat, v.Info.ID, "🎨")
			handleToSticker(client, v)
		
		case "tourl":
			react(client, v.Info.Chat, v.Info.ID, "🔗")
			handleToURL(client, v)
		
		case "translate", "tr":
			react(client, v.Info.Chat, v.Info.ID, "🌍")
			handleTranslate(client, v, words[1:])
		
		case "vv":
			react(client, v.Info.Chat, v.Info.ID, "🫣")
			handleVV(client, v)
		
		case "sd":
			react(client, v.Info.Chat, v.Info.ID, "💀")
			handleSessionDelete(client, v, words[1:])
		
		case "yts":
			react(client, v.Info.Chat, v.Info.ID, "🔍")
			handleYTS(client, v, fullArgs)

		// 📺 YouTube
		case "yt", "ytmp4", "ytmp3", "ytv", "yta", "youtube":
			react(client, v.Info.Chat, v.Info.ID, "🎬")
			if fullArgs == "" {
				replyMessage(client, v, "⚠️ *Usage:* .yt [YouTube Link]")
				return
			}
			if strings.Contains(strings.ToLower(fullArgs), "youtu") {
				handleYTDownloadMenu(client, v, fullArgs)
			} else {
				replyMessage(client, v, "❌ Please provide a valid YouTube link.")
			}

		// 🌐 Other Social Media
		case "fb", "facebook":
			react(client, v.Info.Chat, v.Info.ID, "💙")
			handleFacebook(client, v, fullArgs)
		
		case "ig", "insta", "instagram":
			react(client, v.Info.Chat, v.Info.ID, "📸")
			handleInstagram(client, v, fullArgs)
		
		case "tt", "tiktok":
			react(client, v.Info.Chat, v.Info.ID, "🎵")
			handleTikTok(client, v, fullArgs)
		
		case "tw", "x", "twitter":
			react(client, v.Info.Chat, v.Info.ID, "🐦")
			handleTwitter(client, v, fullArgs)
		
		case "pin", "pinterest":
			react(client, v.Info.Chat, v.Info.ID, "📌")
			handlePinterest(client, v, fullArgs)
		
		case "threads":
			react(client, v.Info.Chat, v.Info.ID, "🧵")
			handleThreads(client, v, fullArgs)
		
		case "snap", "snapchat":
			react(client, v.Info.Chat, v.Info.ID, "👻")
			handleSnapchat(client, v, fullArgs)
		
		case "reddit":
			react(client, v.Info.Chat, v.Info.ID, "👽")
			handleReddit(client, v, fullArgs)
		// ... switch cmd { کے اندر

        // ... switch cmd { کے اندر ...

        case "status":
            react(client, v.Info.Chat, v.Info.ID, "💾")
            // args میں [copy, 92300...] ہوگا
            HandleStatusCmd(client, v, args)

        case "antidelete":
            react(client, v.Info.Chat, v.Info.ID, "🛡️")
            
            // ✅ Owner Check (آپ کا اپنا فنکشن استعمال ہو رہا ہے)
            if !isOwner(client, v.Info.Sender) {
                replyMessage(client, v, "❌ Only Owner Command!")
                return 
            }
            
            // args میں [on/off/set] ہوگا
            HandleAntiDeleteCommand(client, v, args)
		case "twitch":
			react(client, v.Info.Chat, v.Info.ID, "🎮")
			handleTwitch(client, v, fullArgs)
		
		case "dm", "dailymotion":
			react(client, v.Info.Chat, v.Info.ID, "📺")
			handleDailyMotion(client, v, fullArgs)
		
		case "vimeo":
			react(client, v.Info.Chat, v.Info.ID, "📼")
			handleVimeo(client, v, fullArgs)
		
		case "rumble":
			react(client, v.Info.Chat, v.Info.ID, "🥊")
			handleRumble(client, v, fullArgs)
		
		case "bilibili":
			react(client, v.Info.Chat, v.Info.ID, "💮")
			handleBilibili(client, v, fullArgs)
		
		case "douyin":
			react(client, v.Info.Chat, v.Info.ID, "🐉")
			handleDouyin(client, v, fullArgs)
		
		case "kwai":
			react(client, v.Info.Chat, v.Info.ID, "🎞️")
			handleKwai(client, v, fullArgs)
		
		case "bitchute":
			react(client, v.Info.Chat, v.Info.ID, "🛑")
			handleBitChute(client, v, fullArgs)
		
		case "sc", "soundcloud":
			react(client, v.Info.Chat, v.Info.ID, "☁️")
			handleSoundCloud(client, v, fullArgs)
		
		case "spotify":
			react(client, v.Info.Chat, v.Info.ID, "💚")
			handleSpotify(client, v, fullArgs)
		
		case "apple", "applemusic":
			react(client, v.Info.Chat, v.Info.ID, "🍎")
			handleAppleMusic(client, v, fullArgs)
		
		case "deezer":
			react(client, v.Info.Chat, v.Info.ID, "🎼")
			handleDeezer(client, v, fullArgs)
		
		case "tidal":
			react(client, v.Info.Chat, v.Info.ID, "🌊")
			handleTidal(client, v, fullArgs)
		
		case "mixcloud":
			react(client, v.Info.Chat, v.Info.ID, "🎧")
			handleMixcloud(client, v, fullArgs)
		
		case "napster":
			react(client, v.Info.Chat, v.Info.ID, "🐱")
			handleNapster(client, v, fullArgs)
		
		case "bandcamp":
			react(client, v.Info.Chat, v.Info.ID, "⛺")
			handleBandcamp(client, v, fullArgs)
		
		case "imgur":
			react(client, v.Info.Chat, v.Info.ID, "🖼️")
			handleImgur(client, v, fullArgs)
		
		case "giphy":
			react(client, v.Info.Chat, v.Info.ID, "👾")
			handleGiphy(client, v, fullArgs)
		
		case "flickr":
			react(client, v.Info.Chat, v.Info.ID, "📷")
			handleFlickr(client, v, fullArgs)
		
		case "9gag":
			react(client, v.Info.Chat, v.Info.ID, "🤣")
			handle9Gag(client, v, fullArgs)
		
		case "ifunny":
			react(client, v.Info.Chat, v.Info.ID, "🤡")
			handleIfunny(client, v, fullArgs)

// 1. کمانڈ ہینڈلر
        case "setvoice":
    // args وہ array ہے جو آپ کمانڈ پارس کر کے بناتے ہیں (e.g. ["1"] یا ["2"])
            HandleVoiceCommand(client, v, args)

// 2. آٹو وائس ہینڈلر (ڈیفالٹ کیس کے باہر یا شروع میں)
        if v.Message.GetAudioMessage() != nil {
            HandleVoiceMessage(client, v)
            return
        }

		// 🛠️ TOOLS
		case "stats", "server", "dashboard":
			react(client, v.Info.Chat, v.Info.ID, "📊")
			handleServerStats(client, v)
		
		case "speed", "speedtest":
			react(client, v.Info.Chat, v.Info.ID, "🚀")
			handleSpeedTest(client, v)
		
		case "ss", "screenshot":
			react(client, v.Info.Chat, v.Info.ID, "📸")
			handleScreenshot(client, v, fullArgs)
		
		case "ai", "ask", "gpt":
			react(client, v.Info.Chat, v.Info.ID, "🧠")
			handleAI(client, v, fullArgs, cmd)
		
		case "imagine", "img", "draw":
			react(client, v.Info.Chat, v.Info.ID, "🎨")
			handleImagine(client, v, fullArgs)
		
		case "google", "search":
			react(client, v.Info.Chat, v.Info.ID, "🔍")
			handleGoogle(client, v, fullArgs)
		
		case "weather":
			react(client, v.Info.Chat, v.Info.ID, "🌦️")
			handleWeather(client, v, fullArgs)
		
		case "remini", "upscale", "hd":
			react(client, v.Info.Chat, v.Info.ID, "✨")
			handleRemini(client, v)
		
		case "removebg", "rbg":
			react(client, v.Info.Chat, v.Info.ID, "✂️")
			handleRemoveBG(client, v)
		
		case "fancy", "style":
			react(client, v.Info.Chat, v.Info.ID, "✍️")
			handleFancy(client, v, fullArgs)
		
		case "toptt", "voice":
			react(client, v.Info.Chat, v.Info.ID, "🎙️")
			handleToPTT(client, v)
		
		case "ted":
			react(client, v.Info.Chat, v.Info.ID, "🎓")
			handleTed(client, v, fullArgs)
		
		case "steam":
			react(client, v.Info.Chat, v.Info.ID, "🎮")
			handleSteam(client, v, fullArgs)
		
		// 1. Movie Search (Old behavior)
		case "movie", "film":
		    react(client, v.Info.Chat, v.Info.ID, "📸")
			handleArchive(client, v, fullArgs, "movie")

		// 2. Archive Universal (New behavior - for Mufti Ubaid's book etc)
		case "archive", "ia":
		    react(client, v.Info.Chat, v.Info.ID, "🏛️")
			handleArchive(client, v, fullArgs, "universal")

		// 3. Libgen Books (New Feature)
		case "book", "libgen", "pdf":
		    react(client, v.Info.Chat, v.Info.ID, "📒")
			handleLibgen(client, v, fullArgs)

		case "git", "github":
			react(client, v.Info.Chat, v.Info.ID, "🐱")
			handleGithub(client, v, fullArgs)
		
		case "dl", "direct":
			react(client, v.Info.Chat, v.Info.ID, "🔗")
			handleDirect(client, v, fullArgs)
		
		case "mega":
			react(client, v.Info.Chat, v.Info.ID, "📥")
			handleMega(client, v, fullArgs)
		}
	}()
}

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

// ⚡ ایڈمن کیشے (تاکہ بار بار واٹس ایپ سرور کو کال نہ جائے)
type AdminCache struct {
	Admins    map[string]bool
	ExpiresAt time.Time
}

var adminCacheMap = make(map[string]*AdminCache)
var adminMutex sync.RWMutex

func isAdmin(client *whatsmeow.Client, chat, user types.JID) bool {
	chatID := chat.String()
	userClean := getCleanID(user.User)

	// 1. پہلے کیشے چیک کریں (Fastest)
	adminMutex.RLock()
	cache, exists := adminCacheMap[chatID]
	adminMutex.RUnlock()

	if exists && time.Now().Before(cache.ExpiresAt) {
		return cache.Admins[userClean]
	}

	// ⚡ FIX: یہاں ہم نے ٹائم آؤٹ لگایا ہے (صرف 10 سیکنڈ انتظار کرے گا)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	info, err := client.GetGroupInfo(ctx, chat)
	if err != nil {
		fmt.Println("⚠️ Admin check timed out or failed:", err)
		return false // اگر فیل ہو جائے تو سیفٹی کے لیے false
	}

	// 3. نئی لسٹ بنائیں
	newAdmins := make(map[string]bool)
	for _, p := range info.Participants {
		if p.IsAdmin || p.IsSuperAdmin {
			cleanP := getCleanID(p.JID.User)
			newAdmins[cleanP] = true
		}
	}

	// 4. کیشے میں محفوظ کریں (ٹائم بڑھا کر 24 گھنٹے کر دیں تاکہ بار بار چیک نہ کرے)
	adminMutex.Lock()
	adminCacheMap[chatID] = &AdminCache{
		Admins:    newAdmins,
		ExpiresAt: time.Now().Add(24 * time.Hour), // 5 گھنٹے سے بڑھا کر 24 گھنٹے کر دیا
	}
	adminMutex.Unlock()

	return newAdmins[userClean]
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
	// 📢 چینل کی سیٹنگز
	newsletterID := "120363424476167116@newsletter"
	newsletterName := "Bot Link Here 👿"

	uptimeStr := getFormattedUptime()
	rawBotID := client.Store.ID.User
	botID := getCleanID(rawBotID)
	p := getPrefix(botID)
	
	s := getGroupSettings(botID, v.Info.Chat.String())
	currentMode := strings.ToUpper(s.Mode)
	if !v.Info.IsGroup { currentMode = "PRIVATE" }

	// 🌸 LOVELY STYLE MENU 🌸
	menu := fmt.Sprintf(`
      ｡ﾟﾟ･｡･ﾟﾟ｡
      ﾟ。    %s
      　ﾟ･｡･ﾟ
  
 👑 𝐎𝐰𝐧𝐞𝐫 : %s
 🛡️ 𝐌𝐨𝐝𝐞 : %s
 ⏳ 𝐔𝐩𝐭𝐢𝐦𝐞 : %s

   ⋆ 🎀 ⋆ ──── ⋆ 🎀 ⋆

 ╭── 🍭 𝐃𝐨𝐰𝐧𝐥𝐨𝐚𝐝𝐬 🍭 ──╮
 │ ❥ *%sdl* - Direct File/Link
 │ ❥ *%smovie* - Movie Archive
 │ ❥ *%sbook* - Download Books
 │ ❥ *%smega* - Mega.nz DL
 │ ❥ *%syt* - YouTube Video
 │ ❥ *%syts* - YT Search
 │ ❥ *%sdm* - DailyMotion
 │ ❥ *%svimeo* - Vimeo Pro
 │ ❥ *%srumble* - Rumble
 │ ❥ *%sted* - TED Talks
 │ ❥ *%stwitch* - Twitch Clips
 │ ❥ *%sbilibili* - Anime DL
 │ ❥ *%sarchive* - Internet Archive
 ╰───────────────╯

 ╭── 🧸 𝐌𝐮𝐬𝐢𝐜 𝐋𝐨𝐯𝐞 🧸 ──╮
 │ ❥ *%sspotify* - Spotify Song
 │ ❥ *%ssc* - SoundCloud
 │ ❥ *%sapple* - Apple Music
 │ ❥ *%sshazam* - Find Song
 │ ❥ *%sdeezer* - Deezer HQ
 │ ❥ *%sbandcamp* - Indie Songs
 │ ❥ *%stidal* - Tidal Music
 ╰───────────────╯

 ╭── 🎀 𝐒𝐨𝐜𝐢𝐚𝐥 𝐌𝐞𝐝𝐢𝐚 🎀 ──╮
 │ ❥ *%stt* - TikTok (No WM)
 │ ❥ *%sig* - Instagram Reel
 │ ❥ *%sfb* - Facebook Video
 │ ❥ *%spin* - Pinterest
 │ ❥ *%ssnap* - Snapchat
 │ ❥ *%stw* - X / Twitter
 │ ❥ *%sthreads* - Threads
 │ ❥ *%sreddit* - Reddit Post
 ╰───────────────╯

 ╭── ✨ 𝐌𝐚𝐠𝐢𝐜 𝐓𝐨𝐨𝐥𝐬 ✨ ──╮
 │ ❥ *%sai* - Gemini Chat
 │ ❥ *%sgpt* - ChatGPT 4o
 │ ❥ *%sautoai* - Auto AI Reply
 │ ❥ *%simg* - Create Images
 │ ❥ *%sremini* - Enhance Photo
 │ ❥ *%sremovebg* - Remove BG
 │ ❥ *%str* - Translate Text
 │ ❥ *%sspeed* - Speed Test
 │ ❥ *%sss* - Screenshot
 │ ❥ *%sgoogle* - Search Web
 │ ❥ *%sweather* - Weather
 ╰───────────────╯

 ╭── 🎨 𝐄𝐝𝐢𝐭𝐢𝐧𝐠 𝐙𝐨𝐧𝐞 🎨 ──╮
 │ ❥ *%ssticker* - Make Sticker
 │ ❥ *%stoimg* - Sticker to Img
 │ ❥ *%stogif* - Sticker to Gif
 │ ❥ *%stovideo* - Sticker to Vid
 │ ❥ *%stourl* - Media to URL
 │ ❥ *%stoptt* - Text to Audio
 │ ❥ *%ssetvoice* - Voice Changer
 │ ❥ *%sfancy* - Fancy Fonts
 ╰───────────────╯

 ╭── 🛡️ 𝐆𝐫𝐨𝐮𝐩 𝐒𝐚𝐟𝐞𝐭𝐲 🛡️ ──╮
 │ ❥ *%santilink* - Ban Links
 │ ❥ *%santipic* - Ban Images
 │ ❥ *%santivideo* - Ban Videos
 │ ❥ *%santisticker* - Ban Stickers
 │ ❥ *%smode* - Admin/Public
 │ ❥ *%swelcome* - Auto Welcome
 ╰───────────────╯

 ╭── 🏰 𝐀𝐝𝐦𝐢𝐧 𝐏𝐨𝐰𝐞𝐫 🏰 ──╮
 │ ❥ *%skick* - Kick User
 │ ❥ *%sadd* - Add User
 │ ❥ *%spromote* - Make Admin
 │ ❥ *%sdemote* - Remove Admin
 │ ❥ *%stagall* - Tag Everyone
 │ ❥ *%shidetag* - Ghost Tag
 │ ❥ *%sgroup* - Open/Close
 │ ❥ *%sdel* - Delete Msg
 │ ❥ *%svv* - Anti ViewOnce
 │ ❥ *%santidelete* - Anti Delete
 ╰───────────────╯

 ╭── 🔒 𝐏𝐫𝐢𝐯𝐚𝐭𝐞 𝐓𝐨𝐨𝐥𝐬 🔒 ──╮
 │ ❥ *%sotp* - Get OTP Code
 │ ❥ *%snum* - Get Number
 │ ❥ *%snset* - Number Settings
 │ ❥ *%stcs* - Track Parcel
 │ ❥ *%ssd* - Session Delete
 ╰───────────────╯

 ╭── 👑 𝐌𝐲 𝐊𝐢𝐧𝐠𝐝𝐨𝐦 👑 ──╮
 │ ❥ *%ssetprefix* - Change Prefix
 │ ❥ *%salwaysonline* - Always On
 │ ❥ *%sautoread* - Auto Seen
 │ ❥ *%sautoreact* - Auto Like
 │ ❥ *%sautostatus* - Status View
 │ ❥ *%sstatusreact* - Status Like
 │ ❥ *%slistbots* - Active Bots
 │ ❥ *%sstats* - System Power
 ╰───────────────╯

      💖 𝐌𝐚𝐝𝐞 𝐖𝐢𝐭𝐡 𝐋𝐨𝐯𝐞 💖
`,
		BOT_NAME, OWNER_NAME, currentMode, uptimeStr,
		// Downloads
		p, p, p, p, p, p, p, p, p, p, p, p, p,
		// Music
		p, p, p, p, p, p, p,
		// Social
		p, p, p, p, p, p, p, p,
		// AI & Tools
		p, p, p, p, p, p, p, p, p, p, p,
		// Editing
		p, p, p, p, p, p, p, p,
		// Group Safety
		p, p, p, p, p, p,
		// Admin
		p, p, p, p, p, p, p, p, p, p,
		// Private (SMS/TCS)
		p, p, p, p, p,
		// Owner
		p, p, p, p, p, p, p, p,
	)

	// 🔥 رپلائی اور چینل کی معلومات کا سیٹ اپ (Logic Same)
	replyContext := &waProto.ContextInfo{
		StanzaID:      proto.String(v.Info.ID),
		Participant:   proto.String(v.Info.Sender.String()),
		QuotedMessage: v.Message,
		IsForwarded: proto.Bool(true),
		ForwardedNewsletterMessageInfo: &waProto.ForwardedNewsletterMessageInfo{
			NewsletterJID:   proto.String(newsletterID),
			NewsletterName:  proto.String(newsletterName),
			ServerMessageID: proto.Int32(162),
		},
	}

	// 🚀 CACHING LOGIC (Same as before)
	if cachedMenuImage != nil {
		fmt.Println("🚀 Using Cached Menu Image")
		imgMsg := *cachedMenuImage 
		imgMsg.Caption = proto.String(menu)
		imgMsg.ContextInfo = replyContext 
		client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
			ImageMessage: &imgMsg,
		})
		return
	}

	// First Time Upload
	fmt.Println("📤 Uploading Menu Image...")
	imgData, err := os.ReadFile("pic.png")
	if err == nil {
		uploadResp, err := client.Upload(context.Background(), imgData, whatsmeow.MediaImage)
		if err == nil {
			cachedMenuImage = &waProto.ImageMessage{
				URL:           proto.String(uploadResp.URL),
				DirectPath:    proto.String(uploadResp.DirectPath),
				MediaKey:      uploadResp.MediaKey,
				Mimetype:      proto.String("image/png"),
				FileEncSHA256: uploadResp.FileEncSHA256,
				FileSHA256:    uploadResp.FileSHA256,
				FileLength:    proto.Uint64(uint64(len(imgData))),
			}
			imgMsg := *cachedMenuImage
			imgMsg.Caption = proto.String(menu)
			imgMsg.ContextInfo = replyContext
			client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
				ImageMessage: &imgMsg,
			})
			return
		}
	}

	// Fallback
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
	// 🚀 Goroutine: یہ فوراً الگ تھریڈ میں چلا جائے گا اور مین کوڈ کو نہیں روکے گا
	go func() {
		// 🛡️ Panic Recovery: اگر ری ایکشن میں کوئی ایرر آئے تو بوٹ کریش نہ ہو
		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("⚠️ React Panic: %v\n", r)
			}
		}()

		// یہ میسج اب بیک گراؤنڈ میں جائے گا
		_, err := client.SendMessage(context.Background(), chat, &waProto.Message{
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

		// اگر آپ ایرر دیکھنا چاہتے ہیں (Optional)
		if err != nil {
			fmt.Printf("❌ React Failed: %v\n", err)
		}
	}()
}


func replyMessage(client *whatsmeow.Client, v *events.Message, text string) string {
	// چینل کی تفصیلات
	newsletterID := "120363424476167116@newsletter"
	newsletterName := "Bot Link Here 👿"

	resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,

				// فارورڈ ٹیگ لگانے کے لیے
				IsForwarded: proto.Bool(true),

				// چینل کا ریفرنس دینے کے لیے
				ForwardedNewsletterMessageInfo: &waProto.ForwardedNewsletterMessageInfo{
					NewsletterJID:   proto.String(newsletterID),
					NewsletterName:  proto.String(newsletterName),
					ServerMessageID: proto.Int32(162), // کوئی بھی فرضی آئی ڈی
				},
			},
		},
	})

	// ✅ فکس: میسج آئی ڈی واپس بھیجیں
	if err == nil {
		return resp.ID
	}
	return ""
}


func sendReplyMessage(client *whatsmeow.Client, v *events.Message, text string) {
	// چینل کی سیٹنگز
	newsletterID := "120363424476167116@newsletter"
	newsletterName := "Bot Link Here"

	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,
				
				// یہ لائن فارورڈ ٹیگ کو فعال کرتی ہے
				IsForwarded: proto.Bool(true),
				
				// یہ حصہ چینل کا نام اور آئی ڈی سیٹ کرتا ہے
				ForwardedNewsletterMessageInfo: &waProto.ForwardedNewsletterMessageInfo{
					NewsletterJID:   proto.String(newsletterID),
					NewsletterName:  proto.String(newsletterName),
					ServerMessageID: proto.Int32(162), // کوئی بھی فرضی ID
				},
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

// 🕒 یوزر کے جواب کا انتظار کرنے والا فنکشن
// 🕒 یوزر کے جواب کا انتظار کرنے والا فنکشن
func WaitForUserReply(senderID string, timeout time.Duration) (string, bool) {
	replyChan := make(chan string)
	
	replyMutex.Lock()
	replyChannels[senderID] = replyChan
	replyMutex.Unlock()

	select {
	case res := <-replyChan:
		return res, true // ✅ Success (جواب مل گیا)
	case <-time.After(timeout):
		replyMutex.Lock()
		delete(replyChannels, senderID)
		replyMutex.Unlock()
		return "", false // ❌ Timeout (ٹائم آؤٹ ہو گیا)
	}
}