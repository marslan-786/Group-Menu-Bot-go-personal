package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"math/rand"
	"net/http"
	"net/url"
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

// ⚙️ SETTINGS
const CustomAPIURL = "https://gemini-api-production-b665.up.railway.app/chat"

// 💾 Redis Keys
const (
	KeyAutoAITargets = "autoai:targets_set"
	KeyChatHistory   = "chat:history:%s:%s" 
	KeyLastMsgTime   = "autoai:last_msg_time:%s"
	KeyLastOwnerMsg  = "autoai:last_owner_msg:%s"
	KeyStickyOnline  = "autoai:sticky_online:%s"
	KeyLastActivity  = "autoai:last_activity:%s"
	KeySelectedModel = "autoai:selected_model" // 1 = Gemini (Default), 2 = Custom API
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
		return 3 
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
	
	go func() {
		if v.Info.IsFromMe {
			rdb.Set(ctx, fmt.Sprintf(KeyLastOwnerMsg, chatID), time.Now().Unix(), 0)
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

// 🚀 2. COMMAND HANDLER (UPDATED FOR MODEL SWITCHING)
func HandleAutoAICmd(client *whatsmeow.Client, v *events.Message, args []string) {
	if len(args) == 0 { return }
	ctx := context.Background()
	
	cmd := strings.ToLower(args[0])

	switch cmd {
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

	// 🔥 MODEL SWITCHING LOGIC 🔥
	case "1":
		rdb.Set(ctx, KeySelectedModel, "1", 0)
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "🤖 Switched to **Gemini (Model 1)**")
	
	case "2":
		rdb.Set(ctx, KeySelectedModel, "2", 0)
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "🤖 Switched to **Custom API (Model 2)**")
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

	lastOwnerMsgStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastOwnerMsg, chatID)).Result()
	if lastOwnerMsgStr != "" {
		var lastOwnerMsg int64
		fmt.Sscanf(lastOwnerMsgStr, "%d", &lastOwnerMsg)
		if time.Now().Unix()-lastOwnerMsg < 60 {
			// Owner is active
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

// 🤖 4. AI ENGINE
func processAIResponse(client *whatsmeow.Client, v *events.Message, senderName string) {
	ctx := context.Background()
	chatID := v.Info.Chat.String()
	
	lastActivityStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastActivity, chatID)).Result()
	var lastActivity int64
	if lastActivityStr != "" { fmt.Sscanf(lastActivityStr, "%d", &lastActivity) }
	
	currentTime := time.Now().Unix()
	isColdStart := (currentTime - lastActivity) > 120

	if isColdStart {
		delay := 10 + rand.Intn(3) 
		fmt.Printf("💤 [COLD START] Waiting %ds...\n", delay)
		if interrupted := waitAndCheckOwner(ctx, chatID, delay); interrupted { return }
	} else {
		fmt.Println("🔥 [ACTIVE MODE] Instant Pickup.")
		time.Sleep(1 * time.Second) 
	}

	go keepOnlineSmart(client, chatID)
	rdb.Set(ctx, fmt.Sprintf(KeyLastActivity, chatID), currentTime, 0)

	client.SendPresence(ctx, types.PresenceAvailable)
	client.MarkRead(ctx, []types.MessageID{v.Info.ID}, time.Now(), v.Info.Chat, v.Info.Sender, types.ReceiptTypeRead)

	msgType := GetMessageType(v.Message)
	userText := ""
	
	if msgType == "media" {
		time.Sleep(3 * time.Second) 
		return 
	} else if msgType == "audio" {
		audioSec := GetAudioDuration(v.Message)
		fmt.Printf("🎤 [VOICE] Duration: %ds. Listening...\n", audioSec)
		
		if interrupted := waitAndCheckOwner(ctx, chatID, audioSec); interrupted { return }
		
		client.MarkRead(ctx, []types.MessageID{v.Info.ID}, time.Now(), v.Info.Chat, v.Info.Sender, types.ReceiptTypePlayed)

		audioMsg := GetAudioFromMessage(v.Message)
		data, err := client.Download(ctx, audioMsg)
		if err == nil {
			userText, _ = TranscribeAudio(data)
			fmt.Printf("📝 [TEXT] \"%s\"\n", userText)
		}
	} else {
		userText = v.Message.GetConversation()
		if userText == "" { userText = v.Message.GetExtendedTextMessage().GetText() }
		
		wordCount := len(strings.Split(userText, " "))
		readDelay := int(math.Ceil(float64(wordCount) / 4.0)) 
		if readDelay < 1 { readDelay = 1 }
		if !isColdStart && readDelay > 3 { readDelay = 3 }

		fmt.Printf("👀 [READING] Delay: %ds\n", readDelay)
		if interrupted := waitAndCheckOwner(ctx, chatID, readDelay); interrupted { return }
	}

	if userText == "" { return }

	rawBotID := client.Store.ID.User
	botID := strings.Split(rawBotID, ":")[0]
	botID = strings.Split(botID, "@")[0]

	aiResponse := generateCloneReply(botID, chatID, userText, senderName, msgType)
	if aiResponse == "" { return }

	client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	
	respLen := len(aiResponse)
	typingSec := respLen / 10
	if !isColdStart { typingSec = respLen / 15 }
	if typingSec < 2 { typingSec = 2 }
	if typingSec > 12 { typingSec = 12 }

	fmt.Printf("✍️ [TYPING] Duration: %ds\n", typingSec)
	if interrupted := waitAndCheckOwner(ctx, chatID, typingSec); interrupted { 
		client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
		return 
	}

	client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
	sendCleanReply(client, v.Info.Chat, v.Info.ID, aiResponse)
	
	key := fmt.Sprintf(KeyChatHistory, botID, chatID)
	rdb.RPush(ctx, key, "Me: "+aiResponse)
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

		if time.Now().Unix() - lastActivity > 120 {
			fmt.Println("💤 [OFFLINE] Session expired.")
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
			if time.Now().Unix() - lastOwnerMsg < 5 { return true }
		}
		time.Sleep(1 * time.Second)
	}
	return false
}

// 🔥 NEW: Call Custom API (Model 2)
func CallCustomAPI(prompt string) string {
	// Encode the prompt safely for URL query
	safePrompt := url.QueryEscape(prompt)
	fullURL := fmt.Sprintf("%s?message=%s", CustomAPIURL, safePrompt)

	// Create Request
	resp, err := http.Get(fullURL)
	if err != nil {
		fmt.Println("❌ [API ERROR] Request failed:", err)
		return ""
	}
	defer resp.Body.Close()

	// Read Body
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("❌ [API ERROR] Read failed:", err)
		return ""
	}

	// Parse JSON
	var result struct {
		Response string `json:"response"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Println("❌ [API ERROR] JSON Parse failed:", err)
		return ""
	}

	return result.Response
}

// 🧬 CLONE ENGINE (UPDATED WITH SWITCH)
func generateCloneReply(botID, chatID, currentMsg, senderName, inputType string) string {
	ctx := context.Background()
	
	// 🔥 Check which model is selected (1 or 2)
	selectedModel, err := rdb.Get(ctx, KeySelectedModel).Result()
	if err != nil || selectedModel == "" {
		selectedModel = "1" // Default to Gemini
	}

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

	// 🔥 MODE 2: CUSTOM API
	if selectedModel == "2" {
		fmt.Println("🤖 [AI ENGINE] Using Custom API (Model 2)...")
		response := CallCustomAPI(fullPrompt)
		if response != "" {
			return response
		}
		fmt.Println("⚠️ [AI ENGINE] Custom API Failed! Fallback to Gemini.")
	}

	// 🔥 MODE 1: GOOGLE GEMINI (Default)
	fmt.Println("🤖 [AI ENGINE] Using Gemini (Model 1)...")
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