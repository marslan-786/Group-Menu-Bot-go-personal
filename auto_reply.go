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

const (
	KeyAutoAITargets = "autoai:targets_set"
	KeyChatHistory   = "chat:history:%s:%s" 
	KeyLastMsgTime   = "autoai:last_msg_time:%s"
	KeyLastOwnerMsg  = "autoai:last_owner_msg:%s"
	KeyStickyOnline  = "autoai:sticky_online:%s"
)

// ... (GetSenderName and GetAllSenderIdentifiers functions remain same) ...
func GetSenderName(client *whatsmeow.Client, v *events.Message) string {
	if v.Info.PushName != "" { return v.Info.PushName }
	ctx := context.Background()
	if contact, err := client.Store.Contacts.GetContact(ctx, v.Info.Sender); err == nil && contact.Found {
		if contact.FullName != "" { return contact.FullName }
		if contact.PushName != "" { return contact.PushName }
	}
	return v.Info.Sender.User 
}

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
		if !v.Info.IsFromMe {
			senderName = GetSenderName(client, v)
		}

		text := ""
		audioMsg := GetAudioFromMessage(v.Message)

		if audioMsg != nil {
			data, err := client.Download(ctx, audioMsg)
			if err == nil {
				transcribed, _ := TranscribeAudio(data)
				if transcribed != "" {
					text = "[Voice]: " + transcribed
				} else {
					text = "[Voice Message]"
				}
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

// ... (HandleAutoAICmd remains same) ...
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

// 🧠 3. MAIN CHECKER
func CheckAndHandleAutoReply(client *whatsmeow.Client, v *events.Message) bool {
	if time.Since(v.Info.Timestamp) > 60*time.Second { return false }
	if v.Info.IsFromMe { return false }

	ctx := context.Background()
	chatID := v.Info.Chat.String()

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

// 🤖 4. AI ENGINE
func processAIResponse(client *whatsmeow.Client, v *events.Message, senderName string) {
	ctx := context.Background()
	chatID := v.Info.Chat.String()
	
	// Timing Logic
	lastTimeStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastMsgTime, chatID)).Result()
	var lastTime int64
	if lastTimeStr != "" { fmt.Sscanf(lastTimeStr, "%d", &lastTime) }
	
	currentTime := time.Now().Unix()
	rdb.Set(ctx, fmt.Sprintf(KeyLastMsgTime, chatID), fmt.Sprintf("%d", currentTime), 0)

	timeDiff := currentTime - lastTime
	isActiveChat := timeDiff < 60 

	// Online Handling
	if !isActiveChat {
		waitTime := 8 + rand.Intn(5)
		fmt.Printf("🐢 Cold Start. Waiting %ds...\n", waitTime)
		if interrupted := waitAndCheckOwner(ctx, chatID, waitTime); interrupted { return }
		client.SendPresence(ctx, types.PresenceAvailable)
	} else {
		fmt.Println("⚡ Active Chat. Instant Online.")
		client.SendPresence(ctx, types.PresenceAvailable)
	}

	go keepOnlineSmart(client, v.Info.Chat, chatID)

	userText := ""
	audioMsg := GetAudioFromMessage(v.Message)
	
	// 🎤 Voice Processing
	if audioMsg != nil {
		duration := int(audioMsg.GetSeconds())
		if duration == 0 { duration = 5 }

		fmt.Printf("🎤 Processing Voice (%ds)...\n", duration)
		
		// 1. Mark Read (Blue Tick & Played)
		// MarkRead سے عام بلیو ٹک ہوتا ہے، لیکن وائس کے لیے یہ کبھی کبھی مائیک بلیو کر دیتا ہے
		client.MarkRead(ctx, []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
		
		// 2. Listen Delay
		if interrupted := waitAndCheckOwner(ctx, chatID, duration); interrupted { return }

		// 3. Transcribe
		fmt.Println("🔄 Transcribing...")
		data, err := client.Download(ctx, audioMsg)
		if err == nil {
			transcribed, _ := TranscribeAudio(data)
			userText = transcribed
			fmt.Printf("📝 Transcript: \"%s\"\n", userText)
		} else {
			userText = "[Unclear Voice Message]"
		}
	} else {
		// 📝 Text Processing
		userText = v.Message.GetConversation()
		if userText == "" { userText = v.Message.GetExtendedTextMessage().GetText() }

		if userText != "" {
			client.MarkRead(ctx, []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
			
			readDelay := len(userText) / 10
			if isActiveChat { readDelay = 1 } 
			if readDelay < 1 { readDelay = 1 }
			
			if interrupted := waitAndCheckOwner(ctx, chatID, readDelay); interrupted { return }
		}
	}

	if userText == "" { return }

	rawBotID := client.Store.ID.User
	botID := strings.Split(rawBotID, ":")[0]
	botID = strings.Split(botID, "@")[0]

	inputType := "text"
	if audioMsg != nil { inputType = "voice" }

	aiResponse := generateCloneReply(botID, chatID, userText, senderName, inputType)
	if aiResponse == "" { return }

	client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	
	typeSpeed := len(aiResponse) / 7
	if isActiveChat { typeSpeed = len(aiResponse) / 12 }
	if typeSpeed < 2 { typeSpeed = 2 }

	if interrupted := waitAndCheckOwner(ctx, chatID, typeSpeed); interrupted { 
		client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
		return 
	}

	client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
	sendCleanReply(client, v.Info.Chat, v.Info.ID, aiResponse)
	
	key := fmt.Sprintf(KeyChatHistory, botID, chatID)
	rdb.RPush(ctx, key, "Me: "+aiResponse)
	
	fmt.Printf("🚀 Sent: %s\n", aiResponse)
	rdb.Set(ctx, fmt.Sprintf(KeyStickyOnline, chatID), "1", 60*time.Second)
}

// 🛡️ Keep Online
func keepOnlineSmart(client *whatsmeow.Client, jid types.JID, chatID string) {
	ctx := context.Background()
	for {
		val, _ := rdb.Get(ctx, fmt.Sprintf(KeyStickyOnline, chatID)).Result()
		if val == "" {
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

// 🛡️ Owner Check
func waitAndCheckOwner(ctx context.Context, chatID string, seconds int) bool {
	for i := 0; i < seconds; i++ {
		lastOwnerMsgStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastOwnerMsg, chatID)).Result()
		if lastOwnerMsgStr != "" {
			var lastOwnerMsg int64
			fmt.Sscanf(lastOwnerMsgStr, "%d", &lastOwnerMsg)
			if time.Now().Unix() - lastOwnerMsg < 5 { return true }
		}
		time.Sleep(1 * time.Second)
	}
	return false
}

// 🧬 Clone Engine
func generateCloneReply(botID, chatID, currentMsg, senderName, inputType string) string {
	ctx := context.Background()
	historyList, _ := rdb.LRange(ctx, fmt.Sprintf(KeyChatHistory, botID, chatID), 0, -1).Result()
	history := strings.Join(historyList, "\n")

	voiceInstruction := ""
	if inputType == "voice" {
		voiceInstruction = "⚠️ NOTE: User sent a VOICE MESSAGE. Text above is transcription."
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