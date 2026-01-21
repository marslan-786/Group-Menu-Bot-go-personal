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
)

// 🚀 1. COMMAND HANDLER (NAME BASED)
func HandleAutoAICmd(client *whatsmeow.Client, v *events.Message, args []string) {
	if len(args) == 0 {
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "⚠️ Usage:\n1. .autoai set Muhammad Arslan\n2. .autoai prompt (Text)\n3. .autoai off")
		return
	}

	mode := strings.ToLower(args[0])
	ctx := context.Background()

	switch mode {
	case "set":
		if len(args) < 2 {
			sendCleanReply(client, v.Info.Chat, v.Info.ID, "❌ Please write the EXACT Name.")
			return
		}
		
		// 🔥 پورا نام اٹھائیں (spaces کے ساتھ)
		targetName := strings.Join(args[1:], " ")
		targetName = strings.TrimSpace(targetName)
		
		rdb.Set(ctx, KeyAutoAITarget, targetName, 0)
		fmt.Printf("✅ [AUTO-AI] Target Name Set: %s\n", targetName)
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "✅ Target Locked by Name: "+targetName)

	case "prompt":
		if len(args) < 2 {
			sendCleanReply(client, v.Info.Chat, v.Info.ID, "❌ Please write prompt text.")
			return
		}
		promptData := strings.Join(args[1:], " ")
		rdb.Set(ctx, KeyAutoAIPrompt, promptData, 0)
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "✅ Persona Saved!")

	case "off":
		rdb.Del(ctx, KeyAutoAITarget)
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "🛑 Auto AI Stopped.")

	default:
		sendCleanReply(client, v.Info.Chat, v.Info.ID, "❌ Unknown Command.")
	}
}

// 🧠 2. MAIN LOGIC (NAME MATCHING 🔥)
func CheckAndHandleAutoReply(client *whatsmeow.Client, v *events.Message) bool {
	ctx := context.Background()
	
	// 1. ٹارگٹ نام اٹھائیں
	targetName, err := rdb.Get(ctx, KeyAutoAITarget).Result()
	if err != nil || targetName == "" {
		return false 
	}

	// 2. میسج بھیجنے والے کا نام (PushName) نکالیں
	incomingName := v.Info.PushName
	senderID := v.Info.Sender.ToNonAD().String() // صرف لاگنگ کے لیے

	// 🔍 DEBUG: کنسول میں دیکھیں کہ کیا نام آ رہا ہے
	// fmt.Printf("🕵️ [CHECK] Incoming Name: '%s' | Target: '%s'\n", incomingName, targetName)

	// 3. NAME MATCHING (Case Insensitive)
	// دونوں کو چھوٹا (Lowercase) کر کے میچ کریں تاکہ spelling mistake نہ ہو
	if strings.EqualFold(strings.TrimSpace(incomingName), strings.TrimSpace(targetName)) {
		
		fmt.Printf("\n🔔 [AUTO-AI] NAME MATCHED! (%s)\n", incomingName)
		
		// پروسیسنگ شروع
		go processHumanReply(client, v, senderID)
		return true 
	}

	return false
}

// 🤖 3. HUMAN BEHAVIOR ENGINE
func processHumanReply(client *whatsmeow.Client, v *events.Message, senderID string) {
	ctx := context.Background()

	// 📥 A. میسج نکالیں
	userText := ""
	if v.Message.GetAudioMessage() != nil {
		fmt.Println("🎤 [AUTO-AI] Voice detected!")
		data, err := client.Download(context.Background(), v.Message.GetAudioMessage())
		if err == nil {
			userText, _ = TranscribeAudio(data)
			userText = "[Voice Message]: " + userText
		}
	} else {
		userText = v.Message.GetConversation()
		if userText == "" {
			userText = v.Message.GetExtendedTextMessage().GetText()
		}
	}

	if userText == "" { return }
	fmt.Printf("📩 User (%s): \"%s\"\n", v.Info.PushName, userText)

	// ⏳ B. ٹائمنگ (Online & Wait)
	waitSec := 2 + rand.Intn(4)
	fmt.Printf("⏳ Waiting %d seconds...\n", waitSec)
	time.Sleep(time.Duration(waitSec) * time.Second)

	// Online Show & Read
	client.SendPresence(context.Background(), types.PresenceAvailable)
	client.MarkRead(context.Background(), []types.MessageID{v.Info.ID}, v.Info.Timestamp, v.Info.Chat, v.Info.Sender)
	
	// Thinking Time
	time.Sleep(1 * time.Second)

	// 🧠 C. جواب (Multi-Key)
	customPrompt, _ := rdb.Get(ctx, KeyAutoAIPrompt).Result()
	if customPrompt == "" { customPrompt = "Reply casually." }

	aiResponse := generateGeminiReplyMultiKey(customPrompt, userText, senderID)
	
	// ✍️ D. ٹائپنگ
	client.SendChatPresence(context.Background(), v.Info.Chat, types.ChatPresenceComposing, types.ChatPresenceMediaText)
	typingDelay := len(aiResponse) / 12
	if typingDelay < 2 { typingDelay = 2 }
	time.Sleep(time.Duration(typingDelay) * time.Second)

	// 📤 E. بھیجیں
	client.SendChatPresence(context.Background(), v.Info.Chat, types.ChatPresencePaused, types.ChatPresenceMediaText)
	sendCleanReply(client, v.Info.Chat, v.Info.ID, aiResponse)
	
	fmt.Printf("✅ Sent Reply: \"%s\"\n", aiResponse)
	SaveAIHistory(senderID, userText, aiResponse, "") 
}

// 🔑 Helper: Multi-Key Switcher
func generateGeminiReplyMultiKey(systemPrompt, userQuery, senderID string) string {
	ctx := context.Background()
	history := GetAIHistory(senderID)

	fullPrompt := fmt.Sprintf("%s\n---\nCONTEXT:\n%s\n---\nUSER: %s\nREPLY:", systemPrompt, history, userQuery)

	var keys []string
	if k := os.Getenv("GOOGLE_API_KEY"); k != "" { keys = append(keys, k) }
	for i := 1; i <= 50; i++ {
		if k := os.Getenv(fmt.Sprintf("GOOGLE_API_KEY_%d", i)); k != "" { keys = append(keys, k) }
	}

	if len(keys) == 0 { return "No API Keys found." }

	for _, key := range keys {
		client, err := genai.NewClient(ctx, &genai.ClientConfig{APIKey: key})
		if err != nil { continue }
		resp, err := client.Models.GenerateContent(ctx, "gemini-2.5-flash", genai.Text(fullPrompt), nil)
		if err == nil { return resp.Text() }
	}
	return "Sorry, connection issue."
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
