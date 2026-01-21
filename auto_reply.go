package main

import (
	"context"
	"encoding/json"
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

// 💾 Redis Keys (Dynamic based on BotID + ChatID)
const (
	KeyAutoAIEnabled = "autoai:enabled:%s:%s" // botID:chatID -> true/false
	KeyChatHistory   = "chat:history:%s:%s"   // botID:chatID -> List of messages
	KeyLastOwnerMsg  = "chat:last_owner:%s:%s" // botID:chatID -> Timestamp
)

// 📝 1. HISTORY RECORDER (Saves EVERY message to Redis)
// اس فنکشن کو processMessage کے شروع میں کال کرنا ہے (نیچے بتاؤں گا کیسے)
func RecordChatHistory(client *whatsmeow.Client, v *events.Message, botID string) {
	ctx := context.Background()
	chatID := v.Info.Chat.String()
	senderName := v.Info.PushName
	if v.Info.IsFromMe {
		senderName = "Me (Owner)"
	} else if senderName == "" {
		senderName = "User"
	}

	// 🎤 Voice Handling (Convert to Text for History)
	text := ""
	if v.Message.GetAudioMessage() != nil {
		// اگر یہ وائس ہے تو کوشش کریں ٹرانسکرائب کرنے کی
		// نوٹ: اگر وائس پرانی ہے یا ڈاؤنلوڈ نہیں ہو رہی تو ایرر آ سکتا ہے، اسے اگنور کریں
		data, err := client.Download(context.Background(), v.Message.GetAudioMessage())
		if err == nil {
			transcribed, err := TranscribeAudio(data)
			if err == nil && transcribed != "" {
				text = "[Voice]: " + transcribed
			} else {
				text = "[Voice Message - Unclear]"
			}
		} else {
			text = "[Voice Message]"
		}
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
	rdb.LTrim(ctx, key, -50, -1) // صرف آخری 50 میسجز رکھیں

	// 🕒 اگر یہ میرا (Owner) کا میسج ہے، تو ٹائم نوٹ کر لیں
	// تاکہ AI کو پتا چلے کہ مالک جاگ رہا ہے
	if v.Info.IsFromMe {
		rdb.Set(ctx, fmt.Sprintf(KeyLastOwnerMsg, botID, chatID), time.Now().Unix(), 0)
		fmt.Printf("👑 [OWNER ACTIVE] Recorded Owner Reply in %s\n", chatID)
	}
}

// 🚀 2. COMMAND HANDLER
func HandleAutoAICmd(client *whatsmeow.Client, v *events.Message, args []string) {
	botID := client.Store.ID.User // Bot's own ID
	botID = strings.Split(botID, ":")[0]
	chatID := v.Info.Chat.String()

	if len(args) == 0 {
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "⚠️ Usage: .autoai on | off")
		return
	}

	ctx := context.Background()
	key := fmt.Sprintf(KeyAutoAIEnabled, botID, chatID)

	switch strings.ToLower(args[0]) {
	case "on":
		rdb.Set(ctx, key, "true", 0)
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "✅ Auto-AI Active for THIS chat.\n(I will learn from history & wait for you before replying)")
	case "off":
		rdb.Del(ctx, key)
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "🛑 Auto-AI Stopped.")
	}
}

// 🧠 3. MAIN AI LOGIC (Check & Wait)
func CheckAndHandleAutoReply(client *whatsmeow.Client, v *events.Message) bool {
	// اگر میسج میرا اپنا ہے تو اگنور کریں (کیونکہ وہ RecordHistory میں ہینڈل ہو چکا ہے)
	if v.Info.IsFromMe { return false }

	botID := strings.Split(client.Store.ID.User, ":")[0]
	chatID := v.Info.Chat.String()
	ctx := context.Background()

	// 1. کیا اس چیٹ پر AI آن ہے؟
	status, _ := rdb.Get(ctx, fmt.Sprintf(KeyAutoAIEnabled, botID, chatID)).Result()
	if status != "true" {
		return false
	}

	// 2. پروسیس شروع کریں (Goroutine میں)
	go processAIResponse(client, v, botID, chatID)
	return true
}

// 🤖 4. INTELLIGENT PROCESSING (Wait, Check Owner, Reply)
func processAIResponse(client *whatsmeow.Client, v *events.Message, botID, chatID string) {
	ctx := context.Background()
	
	fmt.Printf("🤖 [AI] New msg in %s. Starting 'Wait & Watch'...\n", chatID)

	// 📥 A. سب سے پہلے ان پٹ (Text/Voice) سمجھیں
	userText := ""
	isVoice := false
	if v.Message.GetAudioMessage() != nil {
		isVoice = true
		fmt.Println("🎤 [AI] Processing Voice...")
		data, err := client.Download(context.Background(), v.Message.GetAudioMessage())
		if err == nil {
			userText, err = TranscribeAudio(data)
			if err != nil || userText == "" {
				userText = "" // نشان کہ وائس سمجھ نہیں آئی
			}
		}
	} else {
		userText = v.Message.GetConversation()
		if userText == "" {
			userText = v.Message.GetExtendedTextMessage().GetText()
		}
	}

	// اگر ٹیکسٹ خالی ہے اور وائس بھی فیل ہو گئی
	if userText == "" && isVoice {
		// وائس تھی مگر سمجھ نہیں آئی
		// ہم یہاں فوراً جواب نہیں دیں گے، "Wait" لوپ کے بعد دیں گے
		userText = "[Unclear Voice Message]"
	} else if userText == "" {
		return // کچھ نہیں ہے
	}

	// 🕒 B. THE WAITING GAME (Fake Typing)
	// ہم 30 سے 45 سیکنڈ کا وقفہ لیں گے
	waitTime := 30 + rand.Intn(15) 
	fmt.Printf("⏳ [AI] Waiting %d seconds for Owner...\n", waitTime)

	// وقفے وقفے سے "Typing" شو کرائیں
	for i := 0; i < waitTime; i += 5 {
		// چیک کریں کہ کیا مالک نے جواب دے دیا؟
		lastOwnerTimeStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastOwnerMsg, botID, chatID)).Result()
		var lastOwnerTime int64
		if lastOwnerTimeStr != "" {
			fmt.Sscanf(lastOwnerTimeStr, "%d", &lastOwnerTime)
		}

		// اگر مالک کا میسج، یوزر کے میسج کے *بعد* آیا ہے
		if lastOwnerTime > v.Info.Timestamp.Unix() {
			fmt.Println("🛑 [AI ABORT] Owner replied! I am shutting up.")
			client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
			return // فنکشن ختم
		}

		// ٹائپنگ دکھائیں
		client.SendChatPresence(ctx, v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
		time.Sleep(5 * time.Second)
	}

	// 🛑 FINAL CHECK BEFORE SENDING
	lastOwnerTimeStr, _ := rdb.Get(ctx, fmt.Sprintf(KeyLastOwnerMsg, botID, chatID)).Result()
	var lastOwnerTime int64
	fmt.Sscanf(lastOwnerTimeStr, "%d", &lastOwnerTime)
	if lastOwnerTime > v.Info.Timestamp.Unix() {
		fmt.Println("🛑 [AI ABORT] Owner replied at the last second!")
		return
	}

	// 🧠 C. GENERATE REPLY (اگر وائس سمجھ نہیں آئی تو وہ بتائیں)
	aiResponse := ""
	if userText == "[Unclear Voice Message]" {
		aiResponse = "Yar awaz kat rahi hai, samajh ni ayi. Dubara bhejo ya likh do."
	} else {
		aiResponse = generateCloneReply(botID, chatID, userText)
	}

	// 📤 D. SEND
	sendCleanReply(client, v.Info.Chat, v.Info.ID, aiResponse)
	
	// AI کا اپنا جواب بھی ہسٹری میں ڈالیں
	rdb.RPush(ctx, fmt.Sprintf(KeyChatHistory, botID, chatID), "Me (AI): "+aiResponse)
	fmt.Printf("✅ [AI SENT] %s\n", aiResponse)
}

// 🧬 5. CLONE ENGINE (Reads History & Mimics Style)
func generateCloneReply(botID, chatID, currentMsg string) string {
	ctx := context.Background()
	
	// ہسٹری نکالیں
	historyList, _ := rdb.LRange(ctx, fmt.Sprintf(KeyChatHistory, botID, chatID), 0, -1).Result()
	history := strings.Join(historyList, "\n")

	// 🔥 DYNAMIC PROMPT 🔥
	fullPrompt := fmt.Sprintf(`
You are the user "Me". You are chatting on WhatsApp.
Your goal is to CLONE the speaking style, tone, and emoji usage of "Me" from the history below.

🔍 ANALYSIS RULES:
1. **Tone Check:** Does "Me" joke around? Is "Me" serious? Or flirty? -> MATCH IT.
2. **Emoji Check:** Does "Me" use 😂, 🙃, or no emojis? -> COPY THE FREQUENCY.
3. **Length:** Does "Me" write short answers ("Ok", "Han") or long paragraphs? -> MATCH LENGTH.
4. **Relationship:** Treat the other person exactly how "Me" treats them in the history.

---
CHAT HISTORY:
%s
---
THEIR NEW MESSAGE: %s
YOUR REPLY (as Me):`, history, currentMsg)

	// API Keys
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
	return "..."
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
