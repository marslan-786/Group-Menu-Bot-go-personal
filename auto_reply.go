package main

import (
	"context"
//	"encoding/json"
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
	KeyAutoAITarget = "autoai:target_user"  // جس نمبر پر آٹو اے آئی لگا ہے
	KeyAutoAIPrompt = "autoai:custom_prompt" // آپ کی 50 میسجز والی ٹریننگ
	KeyLastMsgTime  = "autoai:last_msg_time" // آخری میسج کب آیا تھا
)

// 🚀 1. COMMAND HANDLER (Clean Case for commands.go)
func HandleAutoAICmd(client *whatsmeow.Client, v *whatsmeow.Message, args []string) {
	if len(args) == 0 {
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "⚠️ Usage:\n1. .autoai set 923001234567\n2. .autoai prompt (Paste Chat)\n3. .autoai off")
		return
	}

	mode := strings.ToLower(args[0])
	ctx := context.Background()

	switch mode {
	case "set":
		if len(args) < 2 {
			sendCleanReply(client, v.Info.Chat, v.Info.ID, "❌ Please provide a number.")
			return
		}
		targetNum := args[1]
		// نمبر کو صاف کریں (JID فارمیٹ)
		if !strings.Contains(targetNum, "@") {
			targetNum += "@s.whatsapp.net"
		}
		rdb.Set(ctx, KeyAutoAITarget, targetNum, 0)
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "✅ Auto AI Target Set: "+targetNum)

	case "prompt":
		// باقی سارا ٹیکسٹ پرامپٹ ہے
		if len(args) < 2 {
			sendCleanReply(client, v.Info.Chat, v.Info.ID, "❌ Please paste chat history after command.")
			return
		}
		promptData := strings.Join(args[1:], " ")
		rdb.Set(ctx, KeyAutoAIPrompt, promptData, 0)
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "✅ Training Data Saved! Bot will now mimic this style.")

	case "off":
		rdb.Del(ctx, KeyAutoAITarget)
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "🛑 Auto AI Stopped.")

	default:
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "❌ Unknown Command.")
	}
}

// 🧠 2. MAIN LOGIC (Checks incoming messages)
// یہ فنکشن processMessage کے اندر سب سے اوپر کال ہوگا
func CheckAndHandleAutoReply(client *whatsmeow.Client, v *whatsmeow.Message) bool {
	ctx := context.Background()
	
	// 1. چیک کریں کہ ٹارگٹ سیٹ ہے یا نہیں
	targetUser, err := rdb.Get(ctx, KeyAutoAITarget).Result()
	if err != nil || targetUser == "" {
		return false
	}

	// 2. چیک کریں کہ میسج اسی بندے کا ہے (Sender Match)
	sender := v.Info.Sender.ToNonAD().String()
	if sender != targetUser {
		return false
	}

	// 3. میسج پروسیسنگ شروع
	go processHumanReply(client, v, sender)
	return true // True کا مطلب ہے باقی بوٹ کمانڈز اس پر نہ چلیں
}

// 🤖 3. HUMAN BEHAVIOR ENGINE
func processHumanReply(client *whatsmeow.Client, v *whatsmeow.Message, senderID string) {
	ctx := context.Background()

	// ⏳ Step A: ٹائمنگ کیلکولیشن (Human Delay)
	lastTimeStr, _ := rdb.Get(ctx, KeyLastMsgTime).Result()
	var lastTime int64
	if lastTimeStr != "" {
		fmt.Sscanf(lastTimeStr, "%d", &lastTime)
	}

	currentTime := time.Now().Unix()
	timeDiff := currentTime - lastTime

	// ریڈیس میں نیا ٹائم اپڈیٹ کریں
	rdb.Set(ctx, KeyLastMsgTime, fmt.Sprintf("%d", currentTime), 0)

	// 🛑 اگر 10 منٹ (600 سیکنڈ) سے زیادہ گیپ ہے تو "Cold Start"
	if timeDiff > 600 {
		// 3 سے 6 سیکنڈ ویٹ کریں جیسے بندہ موبائل اٹھا رہا ہو
		sleepRandom(3, 6)
	} else {
		// اگر چیٹ چل رہی ہے تو 1 سے 2 سیکنڈ کا نیچرل پاز
		sleepRandom(1, 2)
	}

	// 👀 Step B: Mark as Read (Blue Ticks)
	client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)

	// 📥 Step C: Get User Input (Text or Voice)
	userText := ""
	if v.Message.GetAudioMessage() != nil {
		// وائس ہے تو ڈاؤنلوڈ اور ٹرانسکرائب کریں
		data, err := client.Download(context.Background(), v.Message.GetAudioMessage())
		if err == nil {
			userText, _ = TranscribeAudio(data) // ai_voice.go والا فنکشن
			userText = "[Voice Message]: " + userText
		}
	} else {
		userText = v.Message.GetConversation()
		if userText == "" {
			userText = v.Message.GetExtendedTextMessage().GetText()
		}
	}

	if userText == "" {
		return // کچھ نہیں ملا
	}

	// 🤔 Step D: Thinking Delay (میسج پڑھنے کا ٹائم)
	readTime := len(userText) / 10 // ہر 10 حروف پر 1 سیکنڈ (تقریبا)
	if readTime > 5 { readTime = 5 } // زیادہ سے زیادہ 5 سیکنڈ
	if readTime < 1 { readTime = 1 }
	time.Sleep(time.Duration(readTime) * time.Second)

	// 🧠 Step E: Generate AI Response
	customPrompt, _ := rdb.Get(ctx, KeyAutoAIPrompt).Result()
	if customPrompt == "" {
		customPrompt = "You are a casual friend. Reply briefly in Roman Urdu."
	}

	aiResponse := generateGeminiReply(customPrompt, userText, senderID)

	// ✍️ Step F: Typing Simulation (Composing...)
	client.SendChatPresence(context.Background(), v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	
	// ٹائپنگ کا ٹائم: جواب کی لمبائی کے حساب سے
	typingTime := len(aiResponse) / 15 // ٹائپنگ اسپیڈ
	if typingTime < 2 { typingTime = 2 }
	if typingTime > 10 { typingTime = 10 }
	time.Sleep(time.Duration(typingTime) * time.Second)

	// 📤 Step G: Send Reply (Clean)
	// Composing روک دیں
	client.SendChatPresence(context.Background(), v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
	sendCleanReply(client, v.Info.Chat, v.Info.ID, aiResponse)
	
	// ہسٹری سیو کریں تاکہ کنٹیکسٹ یاد رہے (Optional - ai_manager.go والا فنکشن)
	SaveAIHistory(senderID, userText, aiResponse, "") 
}

// 🔧 Helper: Gemini Call
func generateGeminiReply(systemPrompt, userQuery, senderID string) string {
	ctx := context.Background()
	
	// پچھلی چیٹ ہسٹری بھی اٹھا لیں (ai_manager.go سے)
	history := GetAIHistory(senderID)

	fullPrompt := fmt.Sprintf(`
System Instructions:
%s

CONTEXT (Past Conversation):
%s

CURRENT MESSAGE from User:
%s

TASK: Reply to the User's current message exactly as 'Me' would based on the System Instructions and Context. Keep it natural.
`, systemPrompt, history, userQuery)

	// API Keys Rotation Logic (Same as ai.go)
	key := os.Getenv("GOOGLE_API_KEY") // Simple version for now
	client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: key})
	if err != nil {
		return "Hmm..."
	}
	resp, err := client.Models.GenerateContent(ctx, "gemini-2.5-flash", genai.Text(fullPrompt), nil)
	if err != nil {
		return "Achcha..."
	}
	return resp.Text()
}

// 🧼 Helper: Clean Reply (No Channels, No Tags)
func sendCleanReply(client *whatsmeow.Client, chat types.JID, replyToID string, text string) {
	msg := &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(replyToID),
				Participant:   proto.String(chat.String()), // یا سینڈر
				QuotedMessage: &waProto.Message{Conversation: proto.String("...")}, // Minimal quote
			},
		},
	}
	client.SendMessage(context.Background(), chat, msg)
}

// 🎲 Helper: Random Sleep
func sleepRandom(min, max int) {
	rand.Seed(time.Now().UnixNano())
	duration := rand.Intn(max-min+1) + min
	time.Sleep(time.Duration(duration) * time.Second)
}
