package main

import (
	"context"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types" // ✅ یہ لائن اب شامل ہے (Fix 1)
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

// 🎛️ MAIN SWITCH HANDLER
func HandleButtonCommands(client *whatsmeow.Client, evt *events.Message) {
	// میسج کا ٹیکسٹ حاصل کریں
	text := evt.Message.GetConversation()
	if text == "" {
		text = evt.Message.GetExtendedTextMessage().GetText()
	}

	// کمانڈ چیک کریں (Case Insensitive)
	if !strings.HasPrefix(strings.ToLower(text), ".btn") {
		return
	}

	chatJID := evt.Info.Chat
	cmd := strings.TrimSpace(strings.ToLower(text))

	switch cmd {
	case ".btn 1":
		// 📋 TEST 1: COPY CODE BUTTON
		fmt.Println("Testing Copy Button...")
		sendNativeFlow(client, chatJID, "🔥 *Copy Button Test*", "نیچے بٹن دبا کر کوڈ کاپی کریں۔", []NativeButton{
			{
				Name: "cta_copy",
				Params: `{"display_text":"👉 Copy Code","id":"copy_123","copy_code":"IMPOSSIBLE-2026"}`,
			},
		})

	case ".btn 2":
		// 🔗 TEST 2: URL BUTTON
		fmt.Println("Testing URL Button...")
		sendNativeFlow(client, chatJID, "🌍 *URL Button Test*", "ہماری ویب سائٹ وزٹ کریں۔", []NativeButton{
			{
				Name: "cta_url",
				Params: `{"display_text":"🌐 Open Google","url":"https://google.com","merchant_url":"https://google.com"}`,
			},
		})

	case ".btn 3":
		// ↩️ TEST 3: SIMPLE REPLY BUTTONS (Quick Reply)
		fmt.Println("Testing Quick Reply...")
		sendNativeFlow(client, chatJID, "💬 *Quick Reply Test*", "کیا آپ کو یہ پسند آیا؟", []NativeButton{
			{
				Name: "quick_reply",
				Params: `{"display_text":"✅ Yes","id":"btn_yes"}`,
			},
			{
				Name: "quick_reply",
				Params: `{"display_text":"❌ No","id":"btn_no"}`,
			},
		})

	case ".btn 4":
		// 📜 TEST 4: LIST MENU (Single Select)
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
				Name: "single_select",
				Params: listJson,
			},
		})

	case ".btn 5":
		// 🚀 TEST 5: HYBRID (Copy + URL + Reply)
		fmt.Println("Testing Hybrid Buttons...")
		sendNativeFlow(client, chatJID, "💎 *Hybrid Power Test*", "سارے بٹن ایک ساتھ!", []NativeButton{
			{
				Name: "cta_copy",
				Params: `{"display_text":"📋 Copy ID","id":"copy_id","copy_code":"USER_786"}`,
			},
			{
				Name: "cta_url",
				Params: `{"display_text":"▶️ Watch Video","url":"https://youtube.com","merchant_url":"https://youtube.com"}`,
			},
			{
				Name: "quick_reply",
				Params: `{"display_text":"🔙 Back","id":"btn_back"}`,
			},
		})

	default:
		// ❓ HELP MESSAGE
		menu := "🛠️ *BUTTON TESTER MENU*\n\n" +
			"➤ `.btn 1` : Copy Code Button\n" +
			"➤ `.btn 2` : Open URL Button\n" +
			"➤ `.btn 3` : Reply Buttons (Yes/No)\n" +
			"➤ `.btn 4` : List Menu (Drawer)\n" +
			"➤ `.btn 5` : Mix Buttons\n"
		
		client.SendMessage(context.Background(), chatJID, &waProto.Message{
			Conversation: proto.String(menu),
		})
	}
}

// ---------------------------------------------------------
// 👇 HELPER FUNCTIONS (FIXED FOR LATEST WHATSMEOW)
// ---------------------------------------------------------

type NativeButton struct {
	Name   string
	Params string
}

func sendNativeFlow(client *whatsmeow.Client, jid types.JID, title string, body string, buttons []NativeButton) {
	// بٹنز کو Proto فارمیٹ میں کنورٹ کریں
	var protoButtons []*waProto.InteractiveMessage_NativeFlowMessage_NativeFlowButton
	for _, btn := range buttons {
		protoButtons = append(protoButtons, &waProto.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
			Name:             proto.String(btn.Name),
			ButtonParamsJSON: proto.String(btn.Params), // ✅ FIX 2: Json -> JSON
		})
	}

	// میسج کا اسٹرکچر (ViewOnce کو ہٹا دیا ہے تاکہ ایرر نہ آئے)
	msg := &waProto.Message{
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
			// ✅ FIX 3: فیلڈ کا نام درست کیا
			NativeFlowMessage: &waProto.InteractiveMessage_NativeFlowMessage{
				Buttons:        protoButtons,
				MessageVersion: proto.Int32(1),
			},
		},
	}

	// سینڈ کریں
	_, err := client.SendMessage(context.Background(), jid, msg)
	if err != nil {
		fmt.Println("❌ Error sending buttons:", err)
	}
}
