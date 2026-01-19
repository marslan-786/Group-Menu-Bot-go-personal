package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
    "log" 
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
	"google.golang.org/genai"
)

// 💾 AI کی یادداشت کا اسٹرکچر
type AISession struct {
	History     string `json:"history"`       // پرانی بات چیت
	LastMsgID   string `json:"last_msg_id"`   // آخری AI میسج کی ID
	LastUpdated int64  `json:"last_updated"`  // کب بات ہوئی تھی
}

// 🧠 1. MAIN AI FUNCTION (Command Handler)
func handleAI(client *whatsmeow.Client, v *events.Message, query string, cmd string) {
	if query == "" {
		replyMessage(client, v, "⚠️ Please provide a prompt.")
		return
	}
	
	// چیٹ شروع کریں (نئی یا پرانی)
	processAIConversation(client, v, query, cmd, false)
}

// 🧠 2. REPLY HANDLER (Process Message میں استعمال ہوگا)
func handleAIReply(client *whatsmeow.Client, v *events.Message) bool {
	// 1. چیک کریں کہ کیا یہ رپلائی ہے؟
	ext := v.Message.GetExtendedTextMessage()
	if ext == nil || ext.ContextInfo == nil || ext.ContextInfo.StanzaID == nil { // Fixed: StanzaID
		return false
	}
	
	replyToID := ext.ContextInfo.GetStanzaID() // Fixed: GetStanzaID
	senderID := v.Info.Sender.ToNonAD().String()

	// 2. Redis سے چیک کریں کہ کیا یہ رپلائی AI کے میسج پر ہے؟
	if rdb != nil {
		key := "ai_session:" + senderID
		val, err := rdb.Get(context.Background(), key).Result()
		if err == nil {
			var session AISession
			json.Unmarshal([]byte(val), &session)

			// 🎯 اگر یوزر نے اسی میسج کو رپلائی کیا جو AI نے بھیجا تھا
			if session.LastMsgID == replyToID {
				// میسج کا ٹیکسٹ نکالیں
				userMsg := v.Message.GetConversation()
				if userMsg == "" {
					userMsg = v.Message.GetExtendedTextMessage().GetText()
				}
				
				// بات چیت آگے بڑھائیں
				processAIConversation(client, v, userMsg, "ai", true)
				return true // بتا دیں کہ یہ ہینڈل ہو گیا ہے
			}
		}
	}
	return false
}

// ⚙️ INTERNAL LOGIC (Common for Command & Reply)
func processAIConversation(client *whatsmeow.Client, v *events.Message, query string, cmd string, isReply bool) {
	// اگر یہ رپلائی نہیں ہے تو ری ایکٹ کریں (Processing...)
	if !isReply {
		react(client, v.Info.Chat, v.Info.ID, "🧠")
	}

	senderID := v.Info.Sender.ToNonAD().String()
	var history string = ""

	// --- REDIS: پرانی چیٹ لوڈ کریں ---
	if rdb != nil {
		key := "ai_session:" + senderID
		val, err := rdb.Get(context.Background(), key).Result()
		if err == nil {
			var session AISession
			_ = json.Unmarshal([]byte(val), &session)

			// اگر سیشن 30 منٹ سے پرانا ہو تو نیا شروع کریں
			if time.Now().Unix()-session.LastUpdated < 1800 {
				history = session.History
			}
		}
	}

	// 🕵️ AI کی شخصیت سیٹ کریں
	aiName := "Impossible AI"
	if strings.ToLower(cmd) == "gpt" {
		aiName = "GPT-4"
	}

	// ہسٹری کو لمٹ کریں
	if len(history) > 1500 {
		history = history[len(history)-1500:]
	}

	// 🔥 [PROMPT]
	fullPrompt := fmt.Sprintf(
		"System: You are %s, a smart and friendly assistant.\n"+
			"🔴 IMPORTANT RULES:\n"+
			"1. **Match User's Language & Script:** If user types in Roman Urdu, reply in Roman Urdu. If Urdu Script, reply in Urdu Script. If English, reply in English.\n"+
			"2. **Detect Topic Change:** Focus 100%% on the new message.\n"+
			"3. **Be Casual:** Talk like a close friend.\n"+
			"----------------\n"+
			"Chat History:\n%s\n"+
			"----------------\n"+
			"User's New Message: %s\n"+
			"AI Response:",
		aiName, history, query)

	// 🚀 GEMINI INTEGRATION
	ctx := context.Background()
	
	// کلائنٹ بنائیں
	genaiClient, err := genai.NewClient(ctx, nil)
	if err != nil {
		log.Println("Error creating Gemini client:", err)
		if !isReply {
			// 🛑 Client Creation Error بھیجیں
			errMsg := fmt.Sprintf("❌ *Client Error:*\n```%v```", err)
			replyMessage(client, v, errMsg)
		}
		return
	}

	// ماڈل کو کال کریں
	// نوٹ: اگر ماڈل کا نام غلط ہوا تو یہیں ایرر آئے گا
	result, err := genaiClient.Models.GenerateContent(
		ctx,
		"gemini-1.5-flash", // میک شور کریں کہ یہ ماڈل آپ کے اکاؤنٹ پر ایکٹو ہے
		genai.Text(fullPrompt),
		nil,
	)

	var finalResponse string
	if err != nil {
		log.Println("Gemini API Error:", err)
		if !isReply {
			// 🛑 🛑 🛑 MAIN FIX IS HERE 🛑 🛑 🛑
			// اصلی ایرر میسج بھیجیں
			actualError := fmt.Sprintf("❌ *Gemini API Error:*\n```%v```", err)
			replyMessage(client, v, actualError)
		}
		return
	} else {
		finalResponse = result.Text()
	}

	// ✅ جواب بھیجیں
	respPtr, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(finalResponse),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(v.Info.ID),
				Participant:   proto.String(v.Info.Sender.String()),
				QuotedMessage: v.Message,
			},
		},
	})

	if err == nil {
		// --- REDIS: نیا ڈیٹا محفوظ کریں ---
		if rdb != nil {
			newHistory := fmt.Sprintf("%s\nUser: %s\nAI: %s", history, query, finalResponse)

			newSession := AISession{
				History:     newHistory,
				LastMsgID:   respPtr.ID,
				LastUpdated: time.Now().Unix(),
			}

			jsonData, _ := json.Marshal(newSession)
			rdb.Set(context.Background(), "ai_session:"+senderID, jsonData, 30*time.Minute)
		}

		if !isReply {
			react(client, v.Info.Chat, v.Info.ID, "✅")
		}
	}
}


// --- 👇 FIXED PRANK FUNCTION 👇 ---

func HandleHackingPrank(client *whatsmeow.Client, evt *events.Message) {
	var victims []types.JID

	if evt.Info.IsGroup {
		groupInfo, err := client.GetGroupInfo(context.Background(), evt.Info.Chat)
		if err != nil {
			fmt.Println("Failed to get group info:", err)
			return
		}
		
		for _, p := range groupInfo.Participants {
			victims = append(victims, p.JID)
		}
	} else {
		victims = []types.JID{evt.Info.Sender}
	}

	// 3. Main Loop
	for _, targetJID := range victims {
		if targetJID.User == client.Store.ID.User {
			continue
		}

		// --- Step A: Send Initial Message ---
		initialText := buildPrankText(targetJID.User, 10, "Initializing exploit...")
		
		msg := &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String(initialText),
				ContextInfo: &waProto.ContextInfo{
					MentionedJID: []string{targetJID.String()}, // Fixed: MentionedJID
				},
			},
		}

		resp, err := client.SendMessage(context.Background(), evt.Info.Chat, msg)
		if err != nil {
			fmt.Println("Error sending msg:", err)
			continue
		}

		// --- Step B: Animation Loop ---
		stages := []struct {
			percent int
			status  string
		}{
			{30, "Bypassing Firewall..."},
			{60, "Extracting Chats & Photos..."},
			{85, "Uploading to Server..."},
			{100, "✅ HACKED SUCCESSFULLY"},
		}

		for _, stage := range stages {
			time.Sleep(1500 * time.Millisecond)

			newText := buildPrankText(targetJID.User, stage.percent, stage.status)

			// ✅ FIX: Use ProtocolMessage for Editing
			editMsg := &waProto.Message{
				ProtocolMessage: &waProto.ProtocolMessage{
					Key: &waProto.MessageKey{
						RemoteJID: proto.String(evt.Info.Chat.String()), // Fixed: RemoteJID
						FromMe:    proto.Bool(true),
						ID:        proto.String(resp.ID), // Fixed: ID
					},
					Type: waProto.ProtocolMessage_MESSAGE_EDIT.Enum(),
					EditedMessage: &waProto.Message{
						ExtendedTextMessage: &waProto.ExtendedTextMessage{
							Text: proto.String(newText),
							ContextInfo: &waProto.ContextInfo{
								MentionedJID: []string{targetJID.String()}, // Fixed: MentionedJID
							},
						},
					},
				},
			}

			client.SendMessage(context.Background(), evt.Info.Chat, editMsg)
		}

		// --- Step C: Anti-Ban Delay ---
		if evt.Info.IsGroup {
			time.Sleep(3 * time.Second)
		} else {
			time.Sleep(1 * time.Second)
		}
	}

	// Final Message
	client.SendMessage(context.Background(), evt.Info.Chat, &waProto.Message{
		Conversation: proto.String("✅ Operation Completed Successfully."),
	})
}

// Helper function
func buildPrankText(userNum string, percent int, status string) string {
	barLength := 10
	filled := int(float64(percent) / 100.0 * float64(barLength))
	bar := ""
	for i := 0; i < barLength; i++ {
		if i < filled {
			bar += "█"
		} else {
			bar += "░"
		}
	}

	headerTitle := "⚠️ *SYSTEM ALERT* ⚠️\n║ 💀 Hacking in Progress..."
	if percent >= 100 {
		headerTitle = "✅ *SYSTEM SUCCESS* ✅\n║ 😈 Account Hacked Successfully!"
	}

	return fmt.Sprintf(`╔══════════════════════╗
║ ✨ @%s
╠══════════════════════╣
║ %s
╠══════════════════════╣
║ [%s] %d%% 
║ 📂 %s
╚══════════════════════╝`, userNum, headerTitle, bar, percent, status)
}