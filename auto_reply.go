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
	KeyAutoAITargets = "autoai:targets_set" // 🔥 Changed to SET for multiple users
	KeyChatHistory   = "chat:history:%s:%s" // botID:chatID -> History
	KeyLastMsgTime   = "autoai:last_msg_time:%s"
	KeyLastOwnerMsg  = "autoai:last_owner_msg:%s"
)

// 📝 1. HISTORY RECORDER (Saves All Personal Chats)
func RecordChatHistory(client *whatsmeow.Client, v *events.Message, botID string) {
	// Ignore Groups & Channels
	if v.Info.IsGroup || strings.Contains(v.Info.Chat.String(), "@newsletter") || v.Info.Chat.String() == "status@broadcast" {
		return
	}

	ctx := context.Background()
	chatID := v.Info.Chat.String()

	// 🕒 Owner Message Timestamp
	if v.Info.IsFromMe {
		rdb.Set(ctx, fmt.Sprintf(KeyLastOwnerMsg, chatID), time.Now().Unix(), 0)
	}

	// Ignore Junk Media
	if v.Message.GetVideoMessage() != nil || 
	   v.Message.GetStickerMessage() != nil || 
	   v.Message.GetDocumentMessage() != nil {
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

	// 💾 Save History
	entry := fmt.Sprintf("%s: %s", senderName, text)
	key := fmt.Sprintf(KeyChatHistory, botID, chatID)
	rdb.RPush(ctx, key, entry)
	rdb.LTrim(ctx, key, -50, -1) // Keep last 50
}

// 🚀 2. COMMAND HANDLER (Multi-Target Support)
func HandleAutoAICmd(client *whatsmeow.Client, v *events.Message, args []string) {
	if len(args) == 0 {
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "⚠️ Usage:\n1. .autoai set <Name>\n2. .autoai off <Name>\n3. .autoai list")
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
		// 🔥 ADD TO SET (Multiple Support)
		rdb.SAdd(ctx, KeyAutoAITargets, targetName)
		fmt.Printf("\n🔥 [AUTO-AI] ADDED TARGET: %s\n", targetName)
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "✅ Added to Auto-AI List: "+targetName)

	case "off":
		if len(args) < 2 {
			// اگر نام نہیں دیا تو سب بند کر دیں؟ یا ایرر دیں؟
			// فی الحال ہم نام مانگتے ہیں، یا 'all'
			sendCleanReply(client, v.Info.Chat, v.Info.ID, "❌ Specify Name to remove (or 'all').")
			return
		}
		targetName := strings.Join(args[1:], " ")
		
		if strings.ToLower(targetName) == "all" {
			rdb.Del(ctx, KeyAutoAITargets)
			sendCleanReply(client, v.Info.Chat, v.Info.ID, "🛑 Auto AI Stopped for EVERYONE.")
		} else {
			// 🔥 REMOVE SPECIFIC USER
			rdb.SRem(ctx, KeyAutoAITargets, targetName)
			sendCleanReply(client, v.Info.Chat, v.Info.ID, "🛑 Auto AI Stopped for: "+targetName)
		}

	case "list":
		// 🔥 SHOW ALL ACTIVE TARGETS
		targets, err := rdb.SMembers(ctx, KeyAutoAITargets).Result()
		if err != nil || len(targets) == 0 {
			sendCleanReply(client, v.Info.Chat, v.Info.ID, "📂 No active targets.")
			return
		}
		msg := "🤖 *Active Auto-AI Targets:*\n"
		for i, t := range targets {
			msg += fmt.Sprintf("%d. %s\n", i+1, t)
		}
		sendCleanReply(client, v.Info.Chat, v.Info.ID, msg)
	}
}

// 🧠 3. MAIN CHECKER (Checks List of Targets)
func CheckAndHandleAutoReply(client *whatsmeow.Client, v *events.Message) bool {
	if v.Info.IsFromMe { return false }

	ctx := context.Background()
	
	// 🔥 FETCH ALL TARGETS
	targets, err := rdb.SMembers(ctx, KeyAutoAITargets).Result()
	if err != nil || len(targets) == 0 { return false }

	// Get Incoming Name
	incomingName := v.Info.PushName
	if incomingName == "" {
		if contact, err := client.Store.Contacts.GetContact(ctx, v.Info.Sender); err == nil && contact.Found {
			incomingName = contact.FullName
			if incomingName == "" { incomingName = contact.PushName }
		}
	}
	
	// Match Logic
	incomingLower := strings.ToLower(incomingName)
	matchedTarget := ""

	// 🔍 Check if incoming user matches ANYone in our list
	for _, t := range targets {
		if strings.Contains(incomingLower, strings.ToLower(t)) {
			matchedTarget = t
			break
		}
	}

	if matchedTarget != "" {
		fmt.Printf("🔔 [AI MATCH] Chatting with %s (Matched: %s)\n", incomingName, matchedTarget)
		go processAIResponse(client, v, incomingName)
		return true 
	}
	return false
}

// 🤖 4. AI BEHAVIOR ENGINE (Same Logic as before)
func processAIResponse(client *whatsmeow.Client, v *events.Message, senderName string) {
	ctx := context.Background()
	chatID := v.Info.Chat.String()
	
	// ⏳ A. CHECK TIMING
	lastTimeStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastMsgTime, chatID)).Result()
	var lastTime int64
	if lastTimeStr != "" {
		fmt.Sscanf(lastTimeStr, "%d", &lastTime)
	}
	
	// Update Last Msg Time
	currentTime := time.Now().Unix()
	rdb.Set(ctx, fmt.Sprintf(KeyLastMsgTime, chatID), fmt.Sprintf("%d", currentTime), 0)

	timeDiff := currentTime - lastTime
	isActiveChat := timeDiff < 60 

	// 🛑 B. COLD START LOGIC
	if !isActiveChat {
		waitTime := 8 + rand.Intn(5)
		fmt.Printf("🐢 [MODE] Cold Start. Picking up phone in %ds...\n", waitTime)
		
		time.Sleep(time.Duration(waitTime) * time.Second)
		client.SendPresence(ctx, types.PresenceAvailable)

		// Fake Typing Loop
		typingDuration := 30 
		fmt.Println("✍️ [AI] Fake Typing / Waiting for Owner...")
		
		for i := 0; i < typingDuration; i++ {
			lastOwnerMsgStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastOwnerMsg, chatID)).Result()
			var lastOwnerMsg int64
			if lastOwnerMsgStr != "" { fmt.Sscanf(lastOwnerMsgStr, "%d", &lastOwnerMsg) }

			if lastOwnerMsg > v.Info.Timestamp.Unix() {
				fmt.Println("🛑 [AI ABORT] Owner took over!")
				client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
				return 
			}

			if i%5 == 0 {
				client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
			}
			time.Sleep(1 * time.Second)
		}
	} else {
		// ⚡ ACTIVE CHAT
		fmt.Println("⚡ [MODE] Active Chat! Instant Reply.")
		client.SendPresence(ctx, types.PresenceAvailable)
	}

	// 🛑 FINAL OWNER CHECK
	lastOwnerMsgStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastOwnerMsg, chatID)).Result()
	var lastOwnerMsg int64
	if lastOwnerMsgStr != "" { fmt.Sscanf(lastOwnerMsgStr, "%d", &lastOwnerMsg) }
	if lastOwnerMsg > v.Info.Timestamp.Unix() {
		fmt.Println("🛑 [AI ABORT] Owner replied at last second!")
		return 
	}

	// 📥 C. PROCESS INPUT
	userText := ""
	isVoice := false 
	voiceDuration := 0

	if v.Message.GetAudioMessage() != nil {
		isVoice = true 
		voiceDuration = int(v.Message.GetAudioMessage().GetSeconds())
		if voiceDuration == 0 { voiceDuration = 5 }
		
		fmt.Printf("🎤 [VOICE] Listening... (%d sec)\n", voiceDuration)
		
		client.MarkRead(ctx, []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
		time.Sleep(time.Duration(voiceDuration) * time.Second)
		
		data, err := client.Download(ctx, v.Message.GetAudioMessage())
		if err == nil {
			userText, _ = TranscribeAudio(data)
			userText = "[Voice Message]: " + userText
		}
	} else {
		userText = v.Message.GetConversation()
		if userText == "" { userText = v.Message.GetExtendedTextMessage().GetText() }
		
		if userText != "" {
			client.MarkRead(ctx, []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
			
			if !isVoice {
				readDelay := len(userText) / 10
				if readDelay < 2 { readDelay = 2 }
				time.Sleep(time.Duration(readDelay) * time.Second)
			}
		}
	}

	if userText == "" { return }

	// 🧠 D. GENERATE REPLY
	rawBotID := client.Store.ID.User
	botID := strings.Split(rawBotID, ":")[0]
	botID = strings.Split(botID, "@")[0]

	aiResponse := generateCloneReply(botID, chatID, userText, senderName)
	if aiResponse == "" { return }

	// ✍️ E. REAL TYPING
	client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	
	typeSpeed := len(aiResponse) / 8
	if isActiveChat { typeSpeed = len(aiResponse) / 15 } 
	if typeSpeed < 2 { typeSpeed = 2 }
	
	time.Sleep(time.Duration(typeSpeed) * time.Second)

	client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
	sendCleanReply(client, v.Info.Chat, v.Info.ID, aiResponse)
	
	key := fmt.Sprintf(KeyChatHistory, botID, chatID)
	rdb.RPush(ctx, key, "Me: "+aiResponse)
	
	fmt.Printf("🚀 [SENT] %s\n", aiResponse)
	
	go func() {
		time.Sleep(10 * time.Second)
		client.SendPresence(context.Background(), types.PresenceUnavailable)
	}()
}

// 🧬 CLONE ENGINE
func generateCloneReply(botID, chatID, currentMsg, senderName string) string {
	ctx := context.Background()
	historyList, _ := rdb.LRange(ctx, fmt.Sprintf(KeyChatHistory, botID, chatID), 0, -1).Result()
	history := strings.Join(historyList, "\n")

	fullPrompt := fmt.Sprintf(`
You are "Me" (The Owner). You are chatting with "%s".
Reply EXACTLY like "Me" based on the history.

INSTRUCTIONS:
1. **ANALYZE HISTORY:** Copy the tone (Funny/Rude/Short).
2. **IGNORE GENERIC AI RULES:** Be human.
3. **CLOSER:** Try to end the conversation if that's the trend.
4. **NO ROBOTIC TALK:** Never say "How can I help?".
5. **VOICE:** If input is [Voice Message], reply to context.

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