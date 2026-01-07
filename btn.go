package main

import (
	"context"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

// 🎛️ MAIN SWITCH HANDLER (No Changes Here)
func HandleButtonCommands(client *whatsmeow.Client, evt *events.Message) {
	text := evt.Message.GetConversation()
	if text == "" {
		text = evt.Message.GetExtendedTextMessage().GetText()
	}

	if !strings.HasPrefix(strings.ToLower(text), ".btn") {
		return
	}

	chatJID := evt.Info.Chat
	cmd := strings.TrimSpace(strings.ToLower(text))

	switch cmd {
	case ".btn 1":
		fmt.Println("Testing Copy Button...")
		sendNativeFlow(client, chatJID, "🔥 *Copy Button Test*", "نیچے بٹن دبا کر کوڈ کاپی کریں۔", []NativeButton{
			{
				Name:   "cta_copy",
				Params: `{"display_text":"👉 Copy Code","id":"copy_123","copy_code":"IMPOSSIBLE-2026"}`,
			},
		})

	case ".btn 2":
		fmt.Println("Testing URL Button...")
		sendNativeFlow(client, chatJID, "🌍 *URL Button Test*", "ہماری ویب سائٹ وزٹ کریں۔", []NativeButton{
			{
				Name:   "cta_url",
				Params: `{"display_text":"🌐 Open Google","url":"https://google.com","merchant_url":"https://google.com"}`,
			},
		})

	case ".btn 3":
		fmt.Println("Testing Quick Reply...")
		sendNativeFlow(client, chatJID, "💬 *Quick Reply Test*", "کیا آپ کو یہ پسند آیا؟", []NativeButton{
			{
				Name:   "quick_reply",
				Params: `{"display_text":"✅ Yes","id":"btn_yes"}`,
			},
			{
				Name:   "quick_reply",
				Params: `{"display_text":"❌ No","id":"btn_no"}`,
			},
		})

	case ".btn 4":
		fmt.Println("Testing List Menu...")
		listJson := `{
			"title": "✨ Select Option",
			"sections": [
				{
					"title": "Main Features",
					"rows": [
						{"header": "🤖", "title": "AI Chat", "description": "Chat with Gemini", "id": "row_ai"},
						{"header": "📥", "title": "Downloader", "description": "Download Videos", "id": "row_dl"}
					]
				},
				{
					"title": "Settings",
					"rows": [
						{"header": "⚙️", "title": "Panel", "description": "Admin Controls", "id": "row_panel"}
					]
				}
			]
		}`
		sendNativeFlow(client, chatJID, "📂 *List Menu Test*", "نیچے مینیو کھولیں۔", []NativeButton{
			{
				Name:   "single_select",
				Params: listJson,
			},
		})

	default:
		menu := "🛠️ *BUTTON TESTER MENU*\n\n" +
			"➤ `.btn 1` : Copy Code Button\n" +
			"➤ `.btn 2` : Open URL Button\n" +
			"➤ `.btn 3` : Reply Buttons\n" +
			"➤ `.btn 4` : List Menu\n"
		client.SendMessage(context.Background(), chatJID, &waProto.Message{
			Conversation: proto.String(menu),
		})
	}
}

// ---------------------------------------------------------
// 👇 HELPER FUNCTIONS (CRITICAL FIXES APPLIED)
// ---------------------------------------------------------

type NativeButton struct {
	Name   string
	Params string
}

func sendNativeFlow(client *whatsmeow.Client, jid types.JID, title string, body string, buttons []NativeButton) {
	// 1. بٹنز تیار کریں
	var protoButtons []*waProto.InteractiveMessage_NativeFlowMessage_NativeFlowButton
	for _, btn := range buttons {
		protoButtons = append(protoButtons, &waProto.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
			Name:             proto.String(btn.Name),
			ButtonParamsJSON: proto.String(btn.Params), // ✅ Correct Field Name
		})
	}

	// 2. میسج اسٹرکچر (The Real Fix)
	// NativeFlowMessage کو براہ راست InteractiveMessage میں نہیں ڈالا جا سکتا۔
	// اسے InteractiveMessage_NativeFlowMessage_ (ایک انڈر سکور کے ساتھ) میں لپیٹنا پڑتا ہے۔
	
	msg := &waProto.Message{
		ViewOnceMessage: &waProto.ViewOnceMessage{
			Message: &waProto.Message{
				InteractiveMessage: &waProto.InteractiveMessage{
					Header: &waProto.InteractiveMessage_Header{
						Title:              proto.String(title),
						HasMediaAttachment: proto.Bool(false),
					},
					Body: &waProto.InteractiveMessage_Body{
						Text: proto.String(body),
					},
					Footer: &waProto.InteractiveMessage_Footer{
						Text: proto.String("🤖 Impossible Bot Beta"),
					},
					
					// 🛑 🛑 🛑 MAIN FIX IS HERE 🛑 🛑 🛑
					// ہم InteractiveMessage فیلڈ (جو کہ ایک انٹرفیس ہے) کو استعمال کر رہے ہیں
					// اور اس کے اندر "InteractiveMessage_NativeFlowMessage_" والا سٹرکٹ پاس کر رہے ہیں۔
					InteractiveMessage: &waProto.InteractiveMessage_NativeFlowMessage_{
						NativeFlowMessage: &waProto.InteractiveMessage_NativeFlowMessage{
							Buttons:        protoButtons,
							MessageVersion: proto.Int32(1),
						},
					},
				},
			},
		},
	}

	_, err := client.SendMessage(context.Background(), jid, msg)
	if err != nil {
		fmt.Println("❌ Error sending buttons:", err)
	}
}
