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
	KeyAutoAITarget = "autoai:target_user"  
	KeyAutoAIPrompt = "autoai:custom_prompt" 
	KeyLastMsgTime  = "autoai:last_msg_time" 
	KeyChatHistory  = "chat:history:%s:%s" // botID:chatID -> History
)

// 📝 1. HISTORY RECORDER
func RecordChatHistory(client *whatsmeow.Client, v *events.Message, botID string) {
	ctx := context.Background()
	chatID := v.Info.Chat.String()
	
	// نام نکالنے کی کوشش (تاکہ ہسٹری میں نام آئے)
	senderName := v.Info.PushName
	if v.Info.IsFromMe {
		senderName = "Me (Owner)"
	} else if senderName == "" {
		// اگر پش نیم نہیں ہے تو کانٹیکٹ لسٹ سے نکالیں
		if contact, err := client.Store.Contacts.GetContact(v.Info.Sender); err == nil && contact.Found {
			senderName = contact.FullName
		}
		if senderName == "" { senderName = "User" }
	}

	// میسج کا ٹیکسٹ نکالیں
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

	// 💾 Save to Redis (Last 50 Messages)
	entry := fmt.Sprintf("%s: %s", senderName, text)
	key := fmt.Sprintf(KeyChatHistory, botID, chatID)
	
	rdb.RPush(ctx, key, entry)
	rdb.LTrim(ctx, key, -50, -1) // صرف آخری 50 رکھیں

	// لاگ (تاکہ پتا چلے ہسٹری سیو ہو رہی ہے)
	// fmt.Printf("💾 [HISTORY] Saved for %s: %s\n", senderName, text)
}

// 🚀 2. COMMAND HANDLER (With Debug Prints)
func HandleAutoAICmd(client *whatsmeow.Client, v *events.Message, args []string) {
	if len(args) == 0 {
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "⚠️ Usage:\n1. .autoai set <Exact Name>\n2. .autoai off")
		return
	}

	mode := strings.ToLower(args[0])
	ctx := context.Background()

	switch mode {
	case "set":
		if len(args) < 2 {
			sendCleanReply(client, v.Info.Chat, v.Info.ID, "❌ Please write the name.\nExample: .autoai set Ali")
			return
		}
		
		targetName := strings.Join(args[1:], " ")
		targetName = strings.TrimSpace(targetName)
		
		rdb.Set(ctx, KeyAutoAITarget, targetName, 0)
		
		// 🔥 HARD LOG
		fmt.Printf("\n🔥🔥🔥 [CMD] AUTO AI TARGET SET TO: '%s' 🔥🔥🔥\n", targetName)
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "✅ Target Locked: "+targetName+"\n(Now checking every message...)")

	case "off":
		rdb.Del(ctx, KeyAutoAITarget)
		fmt.Println("🛑 [CMD] Auto AI Disabled.")
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "🛑 Auto AI Stopped.")

	case "status":
		val, _ := rdb.Get(ctx, KeyAutoAITarget).Result()
		if val == "" { val = "None" }
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "🕵️ Current Target: "+val)

	default:
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "❌ Unknown Command.")
	}
}

// 🧠 3. MAIN LOGIC (HARD DEBUGGING 🕵️‍♂️)
func CheckAndHandleAutoReply(client *whatsmeow.Client, v *events.Message) bool {
	// اگر اپنا میسج ہے تو چھوڑ دو
	if v.Info.IsFromMe { return false }

	ctx := context.Background()
	
	// 1. ریڈیس سے ٹارگٹ نکالیں
	targetName, err := rdb.Get(ctx, KeyAutoAITarget).Result()
	
	// 🔥 DEBUG 1: کیا ٹارگٹ سیٹ ہے؟
	if err != nil || targetName == "" {
		// fmt.Println("🕵️ [DEBUG] AutoAI: No Target Set (Skipping)")
		return false 
	}

	// 2. آنے والے کا نام نکالیں
	incomingName := v.Info.PushName
	
	// اگر پش نیم خالی ہے تو کانٹیکٹ سے ٹرائی کریں
	if incomingName == "" {
		if contact, err := client.Store.Contacts.GetContact(v.Info.Sender); err == nil && contact.Found {
			incomingName = contact.FullName
			if incomingName == "" { incomingName = contact.PushName }
		}
	}
	
	senderID := v.Info.Sender.ToNonAD().String()

	// 🔥 DEBUG 2: ناموں کا موازنہ (Comparison)
	fmt.Printf("\n🔎 [CHECK] Target: '%s' | Incoming: '%s' (ID: %s)\n", targetName, incomingName, senderID)

	// 3. میچنگ (Case Insensitive)
	// دونوں کو چھوٹا کر کے اور اسپیس ختم کر کے چیک کریں
	cleanTarget := strings.ToLower(strings.TrimSpace(targetName))
	cleanIncoming := strings.ToLower(strings.TrimSpace(incomingName))

	// "Contains" استعمال کر رہے ہیں تاکہ اگر نام "Ali Khan" ہو اور آپ "Ali" لکھیں تو بھی چل جائے
	if cleanIncoming != "" && strings.Contains(cleanIncoming, cleanTarget) {
		
		fmt.Printf("✅✅✅ [MATCH FOUND] STARTING AI ENGINE FOR: %s\n", incomingName)
		
		// پروسیسنگ شروع
		go processAIResponse(client, v, senderID, incomingName)
		return true 
	} else {
		fmt.Println("❌ [NO MATCH] Skipping...")
	}

	return false
}

// 🤖 4. AI ENGINE (With Logs)
func processAIResponse(client *whatsmeow.Client, v *events.Message, senderID, senderName string) {
	ctx := context.Background()
	
	// 📥 Input Processing
	userText := ""
	if v.Message.GetAudioMessage() != nil {
		fmt.Println("🎤 [AI] Voice Message Detected! Trying to transcribe...")
		data, err := client.Download(context.Background(), v.Message.GetAudioMessage())
		if err == nil {
			userText, _ = TranscribeAudio(data)
			if userText != "" {
				userText = "[Voice]: " + userText
			} else {
				userText = "[Unclear Voice Message]"
			}
		}
	} else {
		userText = v.Message.GetConversation()
		if userText == "" {
			userText = v.Message.GetExtendedTextMessage().GetText()
		}
	}

	if userText == "" { return }
	fmt.Printf("📩 [AI INPUT] User said: %s\n", userText)

	// 🛑 OWNER INTERRUPTION CHECK
	// 40 سیکنڈ تک انتظار کریں اور دیکھیں کہ مالک جواب دیتا ہے یا نہیں
	// (ٹیسٹنگ کے لیے فی الحال 5 سیکنڈ رکھا ہے، آپ اسے بڑھا سکتے ہیں)
	waitTime := 5 
	fmt.Printf("⏳ [AI] Waiting %d seconds for Owner...\n", waitTime)
	
	// Fake Typing
	client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	
	for i := 0; i < waitTime; i++ {
		time.Sleep(1 * time.Second)
		// یہاں آپ مزید چیک لگا سکتے ہیں کہ مالک نے میسج تو نہیں کر دیا
	}

	// 🧠 GENERATE REPLY
	fmt.Println("🤔 [AI] Generating Response...")
	
	botID := strings.Split(client.Store.ID.User, ":")[0]
	chatID := v.Info.Chat.String()
	aiResponse := generateCloneReply(botID, chatID, userText, senderName)
	
	if aiResponse == "" {
		fmt.Println("❌ [AI ERROR] Empty response from Gemini")
		return
	}

	// 📤 SEND
	client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
	sendCleanReply(client, v.Info.Chat, v.Info.ID, aiResponse)
	
	// Save to History
	RecordChatHistory(client, v, botID) // User Msg Recorded above? No, re-record AI response
	
	key := fmt.Sprintf(KeyChatHistory, botID, chatID)
	rdb.RPush(ctx, key, "Me (AI): "+aiResponse)
	
	fmt.Printf("🚀 [AI SENT] %s\n", aiResponse)
}

// 🧬 5. CLONE ENGINE
func generateCloneReply(botID, chatID, currentMsg, senderName string) string {
	ctx := context.Background()
	
	// History
	historyList, _ := rdb.LRange(ctx, fmt.Sprintf(KeyChatHistory, botID, chatID), 0, -1).Result()
	history := strings.Join(historyList, "\n")

	// Prompt
	fullPrompt := fmt.Sprintf(`
You are "Me" (The Owner). You are chatting with "%s".
CLONE my style from the history below.

RULES:
1. Use Roman Urdu / English mix (Pakistani style).
2. If the user is funny, be funny. If sad, be supportive.
3. Keep it natural. Don't sound like a robot.
4. If it's a voice message text, reply naturally to the content.

HISTORY:
%s
---
USER: %s
ME:`, senderName, history, currentMsg)

	// Keys
	var keys []string
	if k := os.Getenv("GOOGLE_API_KEY"); k != "" { keys = append(keys, k) }
	for i := 1; i <= 50; i++ {
		if k := os.Getenv(fmt.Sprintf("GOOGLE_API_KEY_%d", i)); k != "" { keys = append(keys, k) }
	}

	if len(keys) == 0 { return "System Error (No Keys)" }

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