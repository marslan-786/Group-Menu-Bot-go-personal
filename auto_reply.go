package main

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/genai"
	"google.golang.org/protobuf/proto"
)

// 💾 Redis Keys
const (
	KeyAutoAITargets = "autoai:targets_set"
	KeyChatHistory   = "chat:history:%s:%s" // botID:chatID
	KeyLastMsgTime   = "autoai:last_msg_time:%s"
	KeyLastOwnerMsg  = "autoai:last_owner_msg:%s"
)

// 📝 1. HISTORY RECORDER (Saves All Chats)
func RecordChatHistory(client *whatsmeow.Client, v *events.Message, botID string) {
	if v.Info.IsGroup || strings.Contains(v.Info.Chat.String(), "@newsletter") || v.Info.Chat.String() == "status@broadcast" {
		return
	}

	ctx := context.Background()
	chatID := v.Info.Chat.String()

	// 🕒 Owner Timestamp Update
	// اگر آپ (Owner) بولے، تو ٹائم نوٹ کریں تاکہ بوٹ خاموش رہے
	if v.Info.IsFromMe {
		rdb.Set(ctx, fmt.Sprintf(KeyLastOwnerMsg, chatID), time.Now().Unix(), 0)
	}

	if v.Message.GetVideoMessage() != nil || v.Message.GetStickerMessage() != nil || v.Message.GetDocumentMessage() != nil {
		return
	}

	senderName := v.Info.PushName
	if v.Info.IsFromMe {
		senderName = "Me"
	} else if senderName == "" {
		if contact, err := client.Store.Contacts.GetContact(ctx, v.Info.Sender); err == nil && contact.Found {
			senderName = contact.FullName
		}
		if senderName == "" { senderName = "User" }
	}

	text := ""
	if v.Message.GetAudioMessage() != nil {
		text = "[Voice Message]" 
	} else {
		text = v.Message.GetConversation()
		if text == "" {
			text = v.Message.GetExtendedTextMessage().GetText()
		}
	}

	if text == "" { return }

	entry := fmt.Sprintf("%s: %s", senderName, text)
	key := fmt.Sprintf(KeyChatHistory, botID, chatID)
	rdb.RPush(ctx, key, entry)
	rdb.LTrim(ctx, key, -50, -1)
}

// 🚀 2. COMMAND HANDLER
func HandleAutoAICmd(client *whatsmeow.Client, v *events.Message, args []string) {
	if len(args) == 0 {
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "⚠️ Usage:\n.autoai set <Name>\n.autoai off <Name/all>\n.autoai list")
		return
	}

	mode := strings.ToLower(args[0])
	ctx := context.Background()

	switch mode {
	case "set":
		if len(args) < 2 {
			sendCleanReply(client, v.Info.Chat, v.Info.ID, "❌ Name required.")
			return
		}
		targetName := strings.Join(args[1:], " ")
		rdb.SAdd(ctx, KeyAutoAITargets, targetName)
		fmt.Printf("\n🔥 [AUTO-AI] ADDED: %s\n", targetName)
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "✅ AI Active for: "+targetName)

	case "off":
		if len(args) < 2 {
			sendCleanReply(client, v.Info.Chat, v.Info.ID, "❌ Specify Name or 'all'.")
			return
		}
		targetName := strings.Join(args[1:], " ")
		if strings.ToLower(targetName) == "all" {
			rdb.Del(ctx, KeyAutoAITargets)
			sendCleanReply(client, v.Info.Chat, v.Info.ID, "🛑 Stopped for EVERYONE.")
		} else {
			rdb.SRem(ctx, KeyAutoAITargets, targetName)
			sendCleanReply(client, v.Info.Chat, v.Info.ID, "🛑 Stopped for: "+targetName)
		}

	case "list":
		targets, _ := rdb.SMembers(ctx, KeyAutoAITargets).Result()
		msg := "🤖 *Active Targets:*\n"
		for i, t := range targets {
			msg += fmt.Sprintf("%d. %s\n", i+1, t)
		}
		sendCleanReply(client, v.Info.Chat, v.Info.ID, msg)
	}
}

// 🧠 3. MAIN CHECKER
func CheckAndHandleAutoReply(client *whatsmeow.Client, v *events.Message) bool {
	if v.Info.IsFromMe { return false }

	ctx := context.Background()
	chatID := v.Info.Chat.String()

	// 🛑 OWNER INTERRUPT CHECK
	// اگر اونر نے پچھلے 60 سیکنڈ (1 منٹ) کے اندر کوئی میسج کیا ہے، تو بوٹ خاموش رہے
	lastOwnerMsgStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastOwnerMsg, chatID)).Result()
	if lastOwnerMsgStr != "" {
		var lastOwnerMsg int64
		fmt.Sscanf(lastOwnerMsgStr, "%d", &lastOwnerMsg)
		if time.Now().Unix() - lastOwnerMsg < 60 {
			// fmt.Println("🛑 Owner is active. AI Paused.")
			return false
		}
	}

	// 🔍 Check Targets
	targets, err := rdb.SMembers(ctx, KeyAutoAITargets).Result()
	if err != nil || len(targets) == 0 { return false }

	incomingName := v.Info.PushName
	if incomingName == "" {
		if contact, err := client.Store.Contacts.GetContact(ctx, v.Info.Sender); err == nil && contact.Found {
			incomingName = contact.FullName
			if incomingName == "" { incomingName = contact.PushName }
		}
	}
	
	incomingLower := strings.ToLower(incomingName)
	for _, t := range targets {
		if strings.Contains(incomingLower, strings.ToLower(t)) {
			fmt.Printf("🔔 [AI MATCH] %s detected!\n", incomingName)
			go processAIResponse(client, v, incomingName)
			return true 
		}
	}
	return false
}

// 🤖 4. AI BEHAVIOR ENGINE (Correct Human Flow)
func processAIResponse(client *whatsmeow.Client, v *events.Message, senderName string) {
	ctx := context.Background()
	chatID := v.Info.Chat.String()
	
	// ⏳ A. CHECK TIMING (Active vs Cold)
	lastTimeStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastMsgTime, chatID)).Result()
	var lastTime int64
	if lastTimeStr != "" {
		fmt.Sscanf(lastTimeStr, "%d", &lastTime)
	}
	
	currentTime := time.Now().Unix()
	rdb.Set(ctx, fmt.Sprintf(KeyLastMsgTime, chatID), fmt.Sprintf("%d", currentTime), 0)

	timeDiff := currentTime - lastTime
	isActiveChat := timeDiff < 60 

	// =================================================
	// 🎭 STEP 1: PHONE PICKUP & ONLINE STATUS
	// =================================================
	
	if !isActiveChat {
		// COLD START: 10-15s wait to "pick up phone"
		waitTime := 10 + rand.Intn(6)
		fmt.Printf("🐢 [MODE] Cold Start. Waiting %ds to pick up phone...\n", waitTime)
		
		// Wait Loop with Interrupt Check
		if interrupted := waitAndCheckOwner(ctx, chatID, waitTime); interrupted { return }
		
		// 1. ONLINE (Phone Unlocked)
		fmt.Println("📱 [STATUS] Online")
		client.SendPresence(ctx, types.PresenceAvailable)
		time.Sleep(1 * time.Second) // 1 sec delay before reading
		
	} else {
		// ACTIVE CHAT: Instant Online
		fmt.Println("⚡ [MODE] Active Chat. Instant Online.")
		client.SendPresence(ctx, types.PresenceAvailable)
	}

	// =================================================
	// 👁️ STEP 2: READING / LISTENING (The Blue Tick)
	// =================================================

	userText := ""
	
	// 🎤 Voice Handling
	if v.Message.GetAudioMessage() != nil {
		duration := int(v.Message.GetAudioMessage().GetSeconds())
		if duration == 0 { duration = 5 }

		fmt.Printf("🎤 [VOICE] Received. Listening for %ds...\n", duration)
		
		// 1. Mark Read (Blue Tick) -> Show I have seen it
		client.MarkRead(ctx, []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
		
		// 2. "Listening" Delay (Stay Online)
		// جتنی لمبی وائس ہے، اتنا ٹائم ویٹ کریں (جیسے سن رہے ہوں)
		if interrupted := waitAndCheckOwner(ctx, chatID, duration); interrupted { return }

		// 3. Convert to Text
		data, err := client.Download(ctx, v.Message.GetAudioMessage())
		if err == nil {
			userText, _ = TranscribeAudio(data)
			userText = "[Voice Message]: " + userText
			fmt.Printf("📝 [TRANSCRIPTION] %s\n", userText)
		} else {
			userText = "[Unclear Voice Message]"
		}

	} else {
		// 📝 Text Handling
		userText = v.Message.GetConversation()
		if userText == "" { userText = v.Message.GetExtendedTextMessage().GetText() }

		if userText != "" {
			// 1. Mark Read (Blue Tick)
			fmt.Println("👀 [READ] Marked as Read")
			client.MarkRead(ctx, []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)

			// 2. Reading Delay (Based on Length)
			readDelay := len(userText) / 10
			if isActiveChat { readDelay = 1 } // If active, read fast
			if readDelay < 1 { readDelay = 1 }
			
			// پڑھنے کا ٹائم
			if interrupted := waitAndCheckOwner(ctx, chatID, readDelay); interrupted { return }
		}
	}

	if userText == "" { return }

	// =================================================
	// 🧠 STEP 3: THINK & GENERATE
	// =================================================
	
	rawBotID := client.Store.ID.User
	botID := strings.Split(rawBotID, ":")[0]
	botID = strings.Split(botID, "@")[0]

	aiResponse := generateCloneReply(botID, chatID, userText, senderName)
	if aiResponse == "" { return }

	// =================================================
	// ✍️ STEP 4: TYPING & SENDING
	// =================================================

	fmt.Println("✍️ [TYPING] Sending Composing status...")
	client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	
	// Typing Speed
	typeSpeed := len(aiResponse) / 7 // Normal speed
	if isActiveChat { typeSpeed = len(aiResponse) / 12 } // Fast speed in active chat
	if typeSpeed < 2 { typeSpeed = 2 }

	// Wait while typing
	if interrupted := waitAndCheckOwner(ctx, chatID, typeSpeed); interrupted { 
		client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
		return 
	}

	// 🚀 SEND
	client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
	sendCleanReply(client, v.Info.Chat, v.Info.ID, aiResponse)
	
	// Save AI Reply
	key := fmt.Sprintf(KeyChatHistory, botID, chatID)
	rdb.RPush(ctx, key, "Me: "+aiResponse)
	
	fmt.Printf("🚀 [SENT] %s\n", aiResponse)
	
	// 👋 GO OFFLINE (After 15s)
	go func() {
		time.Sleep(15 * time.Second)
		client.SendPresence(context.Background(), types.PresenceUnavailable)
	}()
}

// 🛡️ HELPER: Wait while checking for Owner Interruption
func waitAndCheckOwner(ctx context.Context, chatID string, seconds int) bool {
	for i := 0; i < seconds; i++ {
		// Check Redis for recent owner message
		lastOwnerMsgStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastOwnerMsg, chatID)).Result()
		if lastOwnerMsgStr != "" {
			var lastOwnerMsg int64
			fmt.Sscanf(lastOwnerMsgStr, "%d", &lastOwnerMsg)
			// اگر ابھی (پچھلے 5 سیکنڈ میں) اونر نے میسج کیا ہے
			if time.Now().Unix() - lastOwnerMsg < 5 {
				fmt.Println("🛑 [INTERRUPT] Owner is typing/replying! AI Aborting.")
				return true // Stop everything
			}
		}
		time.Sleep(1 * time.Second)
	}
	return false
}

// 🧬 CLONE ENGINE
func generateCloneReply(botID, chatID, currentMsg, senderName string) string {
	ctx := context.Background()
	historyList, _ := rdb.LRange(ctx, fmt.Sprintf(KeyChatHistory, botID, chatID), 0, -1).Result()
	history := strings.Join(historyList, "\n")

	fullPrompt := fmt.Sprintf(`
You are "Me" (The Owner). You are chatting with "%s".
Your task is to reply EXACTLY like I would.

GUIDELINES:
1. **Mimic Tone:** Look at the history. Am I funny? Rude? Short? Copy that.
2. **Context:** If the user sent a voice message (marked as [Voice Message]), assume context from previous texts or reply generally like "Han sahi keh rahy ho" or "Samajh ni ayi".
3. **Short & Real:** Don't write essays. Use Roman Urdu/English mix.
4. **Closing:** Don't drag the chat. If it's ending, let it end (e.g., "Ok", "Han").

HISTORY:
%s
---
USER: %s
ME:`, senderName, history, currentMsg)

	var keys []string
	if k := os.Getenv("GOOGLE_API_KEY"); k != "" { keys = append(keys, k) }
	for i := 1; i <= 50; i++ {
		if k := os.Getenv(fmt.Sprintf("GOOGLE_API_KEY_%d", i)); k != "" { keys = append(keys, k) }
	}

	for _, key := range keys {
		client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: key})
		if err != nil { continue }
		resp, err := client.Models.GenerateContent(ctx, "gemini-2.5-flash", genai.Text(fullPrompt), nil)
		if err == nil { return resp.Text() }
	}
	return ""
}

func sendCleanReply(client *whatsmeow.Client, chat types.JID, replyToID string, text string) {
	msg := &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waProto.ContextInfo{StanzaID: proto.String(replyToID), Participant: proto.String(chat.String())},
		},
	}
	client.SendMessage(context.Background(), chat, msg)
}