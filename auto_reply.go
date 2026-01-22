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
)

// 🕵️ HELPER: Get BEST Name
func GetSenderName(client *whatsmeow.Client, v *events.Message) string {
	if v.Info.PushName != "" { return v.Info.PushName }
	ctx := context.Background()
	if contact, err := client.Store.Contacts.GetContact(ctx, v.Info.Sender); err == nil && contact.Found {
		if contact.FullName != "" { return contact.FullName }
		if contact.PushName != "" { return contact.PushName }
	}
	return v.Info.Sender.User 
}

// 🕵️ HELPER: Get ALL Identifiers
func GetAllSenderIdentifiers(client *whatsmeow.Client, v *events.Message) []string {
	identifiers := []string{}
	if v.Info.PushName != "" { identifiers = append(identifiers, v.Info.PushName) }
	ctx := context.Background()
	if contact, err := client.Store.Contacts.GetContact(ctx, v.Info.Sender); err == nil && contact.Found {
		if contact.FullName != "" { identifiers = append(identifiers, contact.FullName) }
		if contact.PushName != "" { identifiers = append(identifiers, contact.PushName) }
	}
	identifiers = append(identifiers, v.Info.Sender.User)
	return identifiers
}

// 🕵️ HELPER: Deep Audio Search
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

// 📝 1. HISTORY RECORDER
func RecordChatHistory(client *whatsmeow.Client, v *events.Message, botID string) {
	if time.Since(v.Info.Timestamp) > 60*time.Second { return }
	if v.Info.IsGroup || strings.Contains(v.Info.Chat.String(), "@newsletter") || v.Info.Chat.String() == "status@broadcast" { return }

	go func() {
		ctx := context.Background()
		chatID := v.Info.Chat.String()

		if v.Info.IsFromMe {
			rdb.Set(ctx, fmt.Sprintf(KeyLastOwnerMsg, chatID), time.Now().Unix(), 0)
		}

		senderName := "Me"
		if !v.Info.IsFromMe { senderName = GetSenderName(client, v) }

		text := ""
		audioMsg := GetAudioFromMessage(v.Message)

		if audioMsg != nil {
			data, err := client.Download(ctx, audioMsg)
			if err == nil {
				transcribed, _ := TranscribeAudio(data)
				text = "[Voice]: " + transcribed
			}
		} else {
			text = v.Message.GetConversation()
			if text == "" { text = v.Message.GetExtendedTextMessage().GetText() }
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
	if len(args) == 0 {
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "⚠️ Usage: .autoai set <Name>")
		return
	}
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

// 🧠 3. MAIN CHECKER & PUBLIC HANDLER
// یہ فنکشن اب براہ راست کال بھی کیا جا سکتا ہے (ListenForFeatures سے)
func ProcessAutoAIVoice(client *whatsmeow.Client, v *events.Message) {
	// Wrapper to call the main logic
	senderName := GetSenderName(client, v)
	fmt.Printf("🎤 [AUTO-AI] External Voice Handoff: %s\n", senderName)
	go processAIResponse(client, v, senderName)
}

func CheckAndHandleAutoReply(client *whatsmeow.Client, v *events.Message) bool {
	if time.Since(v.Info.Timestamp) > 60*time.Second { return false }
	if v.Info.IsFromMe { return false }

	ctx := context.Background()
	chatID := v.Info.Chat.String()

	// Refresh Online Timer
	rdb.Set(ctx, fmt.Sprintf(KeyStickyOnline, chatID), "1", 60*time.Second)

	targets, err := rdb.SMembers(ctx, KeyAutoAITargets).Result()
	if err != nil || len(targets) == 0 { return false }

	identifiers := GetAllSenderIdentifiers(client, v)
	matchedTarget := ""
	
	for _, id := range identifiers {
		idLower := strings.ToLower(strings.TrimSpace(id))
		for _, t := range targets {
			if strings.Contains(idLower, strings.ToLower(strings.TrimSpace(t))) {
				matchedTarget = t
				break
			}
		}
		if matchedTarget != "" { break }
	}

	if matchedTarget != "" {
		fmt.Printf("\n🔔 [AI MATCH] Target: %s | ID: %v\n", matchedTarget, identifiers)
		
		lastOwnerMsgStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastOwnerMsg, chatID)).Result()
		if lastOwnerMsgStr != "" {
			var lastOwnerMsg int64
			fmt.Sscanf(lastOwnerMsgStr, "%d", &lastOwnerMsg)
			if time.Now().Unix() - lastOwnerMsg < 60 {
				fmt.Println("🛑 [ABORT] Owner is active.")
				return false
			}
		}

		go processAIResponse(client, v, identifiers[0]) 
		return true 
	}

	return false
}

// 🤖 4. AI ENGINE (SMART TIMING)
func processAIResponse(client *whatsmeow.Client, v *events.Message, senderName string) {
	ctx := context.Background()
	chatID := v.Info.Chat.String()
	
	// ⚡ 1. INSTANT ONLINE & READ (First Impression)
	client.SendPresence(ctx, types.PresenceAvailable)
	client.MarkRead(ctx, []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
	
	// Start Sticky Online (Keeps bot online during wait)
	go keepOnlineSmart(client, v.Info.Chat, chatID)

	// Timing Setup
	lastTimeStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastMsgTime, chatID)).Result()
	var lastTime int64
	if lastTimeStr != "" { fmt.Sscanf(lastTimeStr, "%d", &lastTime) }
	
	currentTime := time.Now().Unix()
	rdb.Set(ctx, fmt.Sprintf(KeyLastMsgTime, chatID), fmt.Sprintf("%d", currentTime), 0)
	isActiveChat := (currentTime - lastTime) < 60 

	// 🕵️ PROCESSING INPUT
	userText := ""
	audioMsg := GetAudioFromMessage(v.Message)
	
	if audioMsg != nil {
		// 🎤 VOICE LOGIC
		audioSec := int(audioMsg.GetSeconds())
		if audioSec == 0 { audioSec = 3 } // Minimum 3 sec fallback

		fmt.Printf("🎤 [VOICE] Duration: %ds. Listening...\n", audioSec)
		
		// Wait EXACTLY audio length (Simulation of listening)
		if interrupted := waitAndCheckOwner(ctx, chatID, audioSec); interrupted { return }

		// 🎯 SEND PLAYED RECEIPT (Try to trigger Blue Mic)
		// We call MarkRead again after listening delay
		client.MarkRead(ctx, []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)

		fmt.Println("🔄 Transcribing...")
		data, err := client.Download(ctx, audioMsg)
		if err == nil {
			transcribed, _ := TranscribeAudio(data)
			userText = transcribed
			fmt.Printf("📝 [TEXT] \"%s\"\n", userText)
		} else {
			userText = "[Unclear Voice]"
		}

	} else {
		// 📝 TEXT LOGIC
		userText = v.Message.GetConversation()
		if userText == "" { userText = v.Message.GetExtendedTextMessage().GetText() }

		if userText != "" {
			// Calculate Reading Time (Avg 5 chars per word, 4 words per sec)
			wordCount := len(strings.Split(userText, " "))
			readDelay := int(math.Ceil(float64(wordCount) / 4.0)) 
			
			if !isActiveChat {
				// If cold start, add "Pickup phone" delay
				readDelay += (5 + rand.Intn(4)) 
			} else {
				// Active chat: Minimal delay (1-2s)
				if readDelay < 2 { readDelay = 1 }
			}

			fmt.Printf("👀 [READING] Delay: %ds\n", readDelay)
			if interrupted := waitAndCheckOwner(ctx, chatID, readDelay); interrupted { return }
		}
	}

	if userText == "" { return }

	// 🧠 GENERATE REPLY
	rawBotID := client.Store.ID.User
	botID := strings.Split(rawBotID, ":")[0]
	botID = strings.Split(botID, "@")[0]

	inputType := "text"
	if audioMsg != nil { inputType = "voice" }

	aiResponse := generateCloneReply(botID, chatID, userText, senderName, inputType)
	if aiResponse == "" { return }

	// ✍️ TYPING LOGIC (SMART TIMING)
	client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	
	// Calculate typing speed (Chars / Speed)
	// Active chat = fast typing, Cold chat = normal typing
	respLen := len(aiResponse)
	typingSec := 0
	
	if isActiveChat {
		typingSec = respLen / 15 // Fast: 15 chars per sec
	} else {
		typingSec = respLen / 8  // Normal: 8 chars per sec
	}

	if typingSec < 2 { typingSec = 2 } // Min 2 sec
	if typingSec > 15 { typingSec = 15 } // Max cap to avoid boring wait

	fmt.Printf("✍️ [TYPING] Duration: %ds\n", typingSec)
	if interrupted := waitAndCheckOwner(ctx, chatID, typingSec); interrupted { 
		client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
		return 
	}

	// 🚀 SEND
	client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
	sendCleanReply(client, v.Info.Chat, v.Info.ID, aiResponse)
	
	key := fmt.Sprintf(KeyChatHistory, botID, chatID)
	rdb.RPush(ctx, key, "Me: "+aiResponse)
	
	fmt.Printf("🚀 Sent: %s\n", aiResponse)
	
	// Keep Online for a bit after sending
	rdb.Set(ctx, fmt.Sprintf(KeyStickyOnline, chatID), "1", 60*time.Second)
}

// 🛡️ SMART ONLINE KEEPER
func keepOnlineSmart(client *whatsmeow.Client, jid types.JID, chatID string) {
	ctx := context.Background()
	// Loop runs until Redis key expires
	for {
		val, _ := rdb.Get(ctx, fmt.Sprintf(KeyStickyOnline, chatID)).Result()
		if val == "" {
			client.SendPresence(ctx, types.PresenceUnavailable)
			return
		}
		
		// Check Owner
		lastOwnerMsgStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastOwnerMsg, chatID)).Result()
		if lastOwnerMsgStr != "" {
			var lastOwnerMsg int64
			fmt.Sscanf(lastOwnerMsgStr, "%d", &lastOwnerMsg)
			if time.Now().Unix() - lastOwnerMsg < 10 { return }
		}

		client.SendPresence(ctx, types.PresenceAvailable)
		time.Sleep(5 * time.Second) // Refresh online status every 5s
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
	if inputType == "voice" {
		voiceInstruction = "⚠️ NOTE: User sent a VOICE MESSAGE. Text above is transcription. Reply naturally."
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