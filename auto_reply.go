package main

import (
	"context"
	"fmt"
	"math"
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

const (
	KeyAutoAITargets = "autoai:targets_set"
	KeyChatHistory   = "chat:history:%s:%s"
	KeyLastMsgTime   = "autoai:last_msg_time:%s"
	KeyLastOwnerMsg  = "autoai:last_owner_msg:%s"
	KeyStickyOnline  = "autoai:sticky_online:%s"
	KeyLastActivity  = "autoai:last_activity:%s"
)

// 🕵️ HELPER: Get Message Type
func GetMessageType(msg *waProto.Message) string {
	if msg == nil { return "unknown" }
	if msg.AudioMessage != nil { return "audio" }
	if msg.ImageMessage != nil { return "media" }
	if msg.VideoMessage != nil { return "media" }
	if msg.StickerMessage != nil { return "media" }
	if msg.DocumentMessage != nil { return "media" }
	if msg.Conversation != nil || msg.ExtendedTextMessage != nil { return "text" }
	return "unknown"
}

// 🕵️ HELPER: Get Exact Audio Duration
func GetAudioDuration(msg *waProto.Message) int {
	if msg == nil { return 0 }
	var audio *waProto.AudioMessage
	
	if msg.AudioMessage != nil {
		audio = msg.AudioMessage
	} else if msg.EphemeralMessage != nil && msg.EphemeralMessage.Message != nil {
		audio = msg.EphemeralMessage.Message.AudioMessage
	} else if msg.ViewOnceMessage != nil && msg.ViewOnceMessage.Message != nil {
		audio = msg.ViewOnceMessage.Message.AudioMessage
	}

	if audio != nil {
		seconds := int(audio.GetSeconds())
		if seconds > 0 { return seconds }
		return 3 // Default fallback
	}
	return 0
}

// 🕵️ HELPER: Get Audio Data
func GetAudioFromMessage(msg *waProto.Message) *waProto.AudioMessage {
	if msg == nil { return nil }
	if msg.AudioMessage != nil { return msg.AudioMessage }
	if msg.EphemeralMessage != nil && msg.EphemeralMessage.Message != nil {
		if msg.EphemeralMessage.Message.AudioMessage != nil { return msg.EphemeralMessage.Message.AudioMessage }
	}
	if msg.ViewOnceMessage != nil && msg.ViewOnceMessage.Message != nil {
		if msg.ViewOnceMessage.Message.AudioMessage != nil { return msg.ViewOnceMessage.Message.AudioMessage }
	}
	return nil
}

// 🕵️ HELPER: Get Sender Name
func GetSenderName(client *whatsmeow.Client, v *events.Message) string {
	if v.Info.PushName != "" { return v.Info.PushName }
	ctx := context.Background()
	if contact, err := client.Store.Contacts.GetContact(ctx, v.Info.Sender); err == nil && contact.Found {
		if contact.FullName != "" { return contact.FullName }
	}
	return v.Info.Sender.User
}

// 🕵️ HELPER: Get Identifiers
func GetAllSenderIdentifiers(client *whatsmeow.Client, v *events.Message) []string {
	identifiers := []string{}
	if v.Info.PushName != "" { identifiers = append(identifiers, v.Info.PushName) }
	ctx := context.Background()
	if contact, err := client.Store.Contacts.GetContact(ctx, v.Info.Sender); err == nil && contact.Found {
		if contact.FullName != "" { identifiers = append(identifiers, contact.FullName) }
	}
	identifiers = append(identifiers, v.Info.Sender.User)
	return identifiers
}

// 📝 1. HISTORY RECORDER
func RecordChatHistory(client *whatsmeow.Client, v *events.Message, botID string) {
	if time.Since(v.Info.Timestamp) > 60*time.Second { return }
	if v.Info.IsGroup || strings.Contains(v.Info.Chat.String(), "@newsletter") { return }

	ctx := context.Background()
	chatID := v.Info.Chat.String()
	
	// IMPORTANT: Do NOT update activity here, it breaks cold start logic
	// Only record the message content
	
	go func() {
		if v.Info.IsFromMe {
			rdb.Set(ctx, fmt.Sprintf(KeyLastOwnerMsg, chatID), time.Now().Unix(), 0)
			// If Owner replies, we DO update activity so bot stays online
			rdb.Set(ctx, fmt.Sprintf(KeyLastActivity, chatID), time.Now().Unix(), 0)
		}

		senderName := "Me"
		if !v.Info.IsFromMe { senderName = GetSenderName(client, v) }

		text := ""
		msgType := GetMessageType(v.Message)

		if msgType == "audio" {
			audioMsg := GetAudioFromMessage(v.Message)
			data, err := client.Download(ctx, audioMsg)
			if err == nil {
				transcribed, _ := TranscribeAudio(data)
				text = "[Voice]: " + transcribed
			}
		} else if msgType == "text" {
			text = v.Message.GetConversation()
			if text == "" { text = v.Message.GetExtendedTextMessage().GetText() }
		} else {
			text = fmt.Sprintf("[%s message]", msgType)
		}

		if text == "" { return }

		entry := fmt.Sprintf("%s: %s", senderName, text)
		key := fmt.Sprintf(KeyChatHistory, botID, chatID)
		rdb.RPush(ctx, key, entry)
		rdb.LTrim(ctx, key, -50, -1)
	}()
}

// 🚀 2. COMMAND HANDLER
func HandleAutoAICmd(client *whatsmeow.Client, v *events.Message, args []string) {
	if len(args) == 0 { return }
	ctx := context.Background()
	switch strings.ToLower(args[0]) {
	case "set":
		if len(args) < 2 { return }
		targetName := strings.Join(args[1:], " ")
		rdb.SAdd(ctx, KeyAutoAITargets, targetName)
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "✅ AI Active for: "+targetName)
	case "off":
		targetName := strings.Join(args[1:], " ")
		if strings.ToLower(targetName) == "all" {
			rdb.Del(ctx, KeyAutoAITargets)
		} else {
			rdb.SRem(ctx, KeyAutoAITargets, targetName)
		}
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "🛑 Stopped.")
	case "list":
		targets, _ := rdb.SMembers(ctx, KeyAutoAITargets).Result()
		sendCleanReply(client, v.Info.Chat, v.Info.ID, fmt.Sprintf("Targets: %v", targets))
	}
}

// 🧠 3. MAIN CHECKER
func ProcessAutoAIVoice(client *whatsmeow.Client, v *events.Message) {
	senderName := GetSenderName(client, v)
	go processAIResponse(client, v, senderName)
}

func CheckAndHandleAutoReply(client *whatsmeow.Client, v *events.Message) bool {
	if time.Since(v.Info.Timestamp) > 60*time.Second { return false }
	if v.Info.IsFromMe { return false }

	ctx := context.Background()
	chatID := v.Info.Chat.String()

	// Owner Override Check
	lastOwnerMsgStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastOwnerMsg, chatID)).Result()
	if lastOwnerMsgStr != "" {
		var lastOwnerMsg int64
		fmt.Sscanf(lastOwnerMsgStr, "%d", &lastOwnerMsg)
		if time.Now().Unix()-lastOwnerMsg < 60 {
			fmt.Println("🛑 [ABORT] Owner is active.")
			return false
		}
	}

	targets, err := rdb.SMembers(ctx, KeyAutoAITargets).Result()
	if err != nil || len(targets) == 0 { return false }

	identifiers := GetAllSenderIdentifiers(client, v)
	matchedTarget := ""
	
	for _, id := range identifiers {
		for _, t := range targets {
			if strings.Contains(strings.ToLower(id), strings.ToLower(t)) {
				matchedTarget = t
				break
			}
		}
		if matchedTarget != "" { break }
	}

	if matchedTarget != "" {
		fmt.Printf("\n🔔 [AI MATCH] Target: %s\n", matchedTarget)
		go processAIResponse(client, v, identifiers[0]) 
		return true 
	}
	return false
}

// 🤖 4. AI ENGINE (CORRECTED HUMAN LOGIC)
func processAIResponse(client *whatsmeow.Client, v *events.Message, senderName string) {
	ctx := context.Background()
	chatID := v.Info.Chat.String()
	
	// --- A. ANALYZE STATE (FIXED) ---
	// 1. Get OLD time first
	lastActivityStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastActivity, chatID)).Result()
	var lastActivity int64
	if lastActivityStr != "" { fmt.Sscanf(lastActivityStr, "%d", &lastActivity) }
	
	currentTime := time.Now().Unix()
	
	// 2. Check if Cold Start (Before updating time)
	isColdStart := (currentTime - lastActivity) > 120 // 2 Minutes Silence

	// --- B. COLD START DELAY ---
	if isColdStart {
		// DO NOT COME ONLINE YET
		delay := 10 + rand.Intn(3) // 10-12 Seconds
		fmt.Printf("💤 [COLD START] Waiting %ds before touching phone...\n", delay)
		
		// Wait offline
		if interrupted := waitAndCheckOwner(ctx, chatID, delay); interrupted { return }
	} else {
		fmt.Println("🔥 [ACTIVE MODE] Instant Pickup.")
		time.Sleep(1 * time.Second) // Tiny human jitter
	}

	// --- C. NOW COME ONLINE & READ ---
	// Start sticky online background task
	go keepOnlineSmart(client, chatID)
	// Update activity time NOW (after delay logic is done)
	rdb.Set(ctx, fmt.Sprintf(KeyLastActivity, chatID), currentTime, 0)

	client.SendPresence(ctx, types.PresenceAvailable)
	
	// Mark EVERYTHING as Read (Blue Ticks) - First Step
	client.MarkRead(ctx, []types.MessageID{v.Info.ID}, time.Now(), v.Info.Chat, v.Info.Sender, types.ReceiptTypeRead)
	fmt.Println("👀 [SEEN] Sent Blue Ticks.")

	// --- D. PROCESSING (LISTENING/READING) ---
	msgType := GetMessageType(v.Message)
	userText := ""
	
	if msgType == "media" {
		fmt.Println("🖼️ [MEDIA] Viewed media.")
		time.Sleep(3 * time.Second) 
		return 
	} else if msgType == "audio" {
		// 🎤 AUDIO LOGIC (FIXED)
		audioSec := GetAudioDuration(v.Message)
		fmt.Printf("🎤 [VOICE] Duration: %ds. Listening...\n", audioSec)
		
		// 1. Wait while "Listening" (Blue Ticks are already sent)
		if interrupted := waitAndCheckOwner(ctx, chatID, audioSec); interrupted { return }
		
		// 2. SEND BLUE MIC (Played) - AFTER Listening
		client.MarkRead(ctx, []types.MessageID{v.Info.ID}, time.Now(), v.Info.Chat, v.Info.Sender, types.ReceiptTypePlayed)
		fmt.Println("🔵 [PLAYED] Sent Blue Mic.")

		// 3. Transcribe
		audioMsg := GetAudioFromMessage(v.Message)
		data, err := client.Download(ctx, audioMsg)
		if err == nil {
			userText, _ = TranscribeAudio(data)
			fmt.Printf("📝 [TEXT] \"%s\"\n", userText)
		}
	} else {
		// 📝 TEXT LOGIC
		userText = v.Message.GetConversation()
		if userText == "" { userText = v.Message.GetExtendedTextMessage().GetText() }
		
		wordCount := len(strings.Split(userText, " "))
		readDelay := int(math.Ceil(float64(wordCount) / 4.0)) 
		if readDelay < 1 { readDelay = 1 }
		
		// Make reading faster if active chat
		if !isColdStart && readDelay > 3 { readDelay = 3 }

		fmt.Printf("👀 [READING] Delay: %ds\n", readDelay)
		if interrupted := waitAndCheckOwner(ctx, chatID, readDelay); interrupted { return }
	}

	if userText == "" { return }

	// --- E. GENERATE REPLY ---
	rawBotID := client.Store.ID.User
	botID := strings.Split(rawBotID, ":")[0]
	botID = strings.Split(botID, "@")[0]

	aiResponse := generateCloneReply(botID, chatID, userText, senderName, msgType)
	if aiResponse == "" { return }

	// --- F. TYPING SIMULATION (FIXED SPEED) ---
	client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	
	// Speed Calculation
	respLen := len(aiResponse)
	typingSec := respLen / 10 // Normal speed
	if !isColdStart { typingSec = respLen / 15 } // Faster
	
	// Safety Caps (Fixing the 1 minute delay issue)
	if typingSec < 2 { typingSec = 2 }
	if typingSec > 12 { typingSec = 12 } // NEVER wait more than 12 seconds to type

	fmt.Printf("✍️ [TYPING] Duration: %ds\n", typingSec)
	if interrupted := waitAndCheckOwner(ctx, chatID, typingSec); interrupted { 
		client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
		return 
	}

	// --- G. SEND ---
	client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
	sendCleanReply(client, v.Info.Chat, v.Info.ID, aiResponse)
	
	key := fmt.Sprintf(KeyChatHistory, botID, chatID)
	rdb.RPush(ctx, key, "Me: "+aiResponse)
	
	// Reset Activity to keep session alive
	rdb.Set(ctx, fmt.Sprintf(KeyLastActivity, chatID), time.Now().Unix(), 0)
}

// 🛡️ SMART ONLINE KEEPER
func keepOnlineSmart(client *whatsmeow.Client, chatID string) {
	ctx := context.Background()
	for {
		lastActivityStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastActivity, chatID)).Result()
		if lastActivityStr == "" {
			client.SendPresence(ctx, types.PresenceUnavailable)
			return
		}

		var lastActivity int64
		fmt.Sscanf(lastActivityStr, "%d", &lastActivity)

		// 2 Minutes Timeout
		if time.Now().Unix() - lastActivity > 120 {
			fmt.Println("💤 [OFFLINE] Session expired (2 mins).")
			client.SendPresence(ctx, types.PresenceUnavailable)
			return
		}

		lastOwnerMsgStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastOwnerMsg, chatID)).Result()
		if lastOwnerMsgStr != "" {
			var lastOwnerMsg int64
			fmt.Sscanf(lastOwnerMsgStr, "%d", &lastOwnerMsg)
			if time.Now().Unix() - lastOwnerMsg < 10 { return }
		}

		client.SendPresence(ctx, types.PresenceAvailable)
		time.Sleep(5 * time.Second)
	}
}

// 🛡️ OWNER WATCHDOG
func waitAndCheckOwner(ctx context.Context, chatID string, seconds int) bool {
	for i := 0; i < seconds; i++ {
		lastOwnerMsgStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastOwnerMsg, chatID)).Result()
		if lastOwnerMsgStr != "" {
			var lastOwnerMsg int64
			fmt.Sscanf(lastOwnerMsgStr, "%d", &lastOwnerMsg)
			if time.Now().Unix() - lastOwnerMsg < 5 {
				fmt.Println("🛑 Owner Active! Aborting.")
				return true 
			}
		}
		time.Sleep(1 * time.Second)
	}
	return false
}

// 🧬 CLONE ENGINE
func generateCloneReply(botID, chatID, currentMsg, senderName, inputType string) string {
	ctx := context.Background()
	historyList, _ := rdb.LRange(ctx, fmt.Sprintf(KeyChatHistory, botID, chatID), 0, -1).Result()
	history := strings.Join(historyList, "\n")

	voiceInstruction := ""
	if inputType == "audio" {
		voiceInstruction = "⚠️ NOTE: User sent a VOICE MESSAGE. Text is transcription. Reply naturally."
	}

	fullPrompt := fmt.Sprintf(`
You are "Me" (The Owner). You are chatting with "%s".
Reply EXACTLY like "Me".

INSTRUCTIONS:
1. **Mimic Tone:** Copy my style from history.
2. **Context:** %s
3. **Short & Real:** Behave like a human.
4. **Closing:** End chat if natural.

HISTORY:
%s
---
USER (%s): %s
ME:`, senderName, voiceInstruction, history, inputType, currentMsg)

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