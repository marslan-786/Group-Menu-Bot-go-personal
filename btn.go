package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// 🎛️ MAIN SWITCH HANDLER
func HandleButtonCommands(client *whatsmeow.Client, evt *events.Message) {
	text := evt.Message.GetConversation()
	if text == "" {
		text = evt.Message.GetExtendedTextMessage().GetText()
	}

	if !strings.HasPrefix(strings.ToLower(text), ".btn") {
		return
	}

	cmd := strings.TrimSpace(strings.ToLower(text))

	// 🛠️ SCENARIO 1: COPY CODE
	if cmd == ".btn 1" {
		fmt.Println("Sending Copy Button...")
		
		// ✅ Correct Map Syntax (Key: Value)
		params := map[string]string{
			"display_text": "👉 Copy OTP",
			"copy_code":    "IMPOSSIBLE-2026",
			"id":           "btn_copy_123",
		}
		
		sendNativeFlow(client, evt, "🔥 *Copy Code*", "نیچے بٹن دبا کر کوڈ کاپی کریں۔", "cta_copy", params)
	}

	// 🛠️ SCENARIO 2: OPEN URL
	if cmd == ".btn 2" {
		fmt.Println("Sending URL Button...")
		
		params := map[string]string{
			"display_text": "🌐 Open Google",
			"url":          "https://google.com",
			"merchant_url": "https://google.com",
			"id":           "btn_url_456",
		}
		
		sendNativeFlow(client, evt, "🌍 *URL Access*", "ہماری ویب سائٹ وزٹ کریں۔", "cta_url", params)
	}

	// 🛠️ SCENARIO 3: LIST MENU
	if cmd == ".btn 3" {
		fmt.Println("Sending List Menu...")
		
		// ✅ Complex Nested Map Syntax Fixed
		listParams := map[string]interface{}{
			"title": "✨ Select Option",
			"sections": []map[string]interface{}{
				{
					"title": "Main Features",
					"rows": []map[string]string{
						{
							"header":      "🤖",
							"title":       "AI Chat",
							"description": "Chat with Gemini",
							"id":          "row_ai",
						},
						{
							"header":      "📥",
							"title":       "Downloader",
							"description": "Save Videos",
							"id":          "row_dl",
						},
					},
				},
			},
		}
		sendNativeFlow(client, evt, "📂 *Main Menu*", "نیچے مینیو کھولیں۔", "single_select", listParams)
	}
}

// ---------------------------------------------------------
// 👇 HELPER FUNCTION (DEEP SEARCH COMPLIANT)
// ---------------------------------------------------------

func sendNativeFlow(client *whatsmeow.Client, evt *events.Message, title string, body string, btnName string, params interface{}) {
	
	// 1. Serialize Params to JSON String
	jsonBytes, err := json.Marshal(params)
	if err != nil {
		fmt.Printf("❌ JSON Error: %v\n", err)
		return
	}

	// 2. Construct Buttons Slice
	// 🚨 IMPORTANT: Using Named Fields to avoid "implicit assignment" errors
	buttons := []*waE2E.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
		{
			Name:             proto.String(btnName),
			ButtonParamsJSON: proto.String(string(jsonBytes)),
		},
	}

	// 3. Construct Native Flow Message
	nativeFlowMsg := &waE2E.InteractiveMessage_NativeFlowMessage{
		Buttons:           buttons,
		MessageParamsJSON: proto.String("{}"), // Mandatory empty JSON for some clients
		MessageVersion:    proto.Int32(3),     // Version 3 is critical for 2025/26
	}

	// 4. Construct Interactive Message
	interactiveMsg := &waE2E.InteractiveMessage{
		Header: &waE2E.InteractiveMessage_Header{
			Title:              proto.String(title),
			HasMediaAttachment: proto.Bool(false),
		},
		Body: &waE2E.InteractiveMessage_Body{
			Text: proto.String(body),
		},
		Footer: &waE2E.InteractiveMessage_Footer{
			Text: proto.String("🤖 Impossible Bot Beta"),
		},
		// Wrapper for OneOf Field
		InteractiveMessage: &waE2E.InteractiveMessage_NativeFlowMessage_{
			NativeFlowMessage: nativeFlowMsg,
		},
		// 🔥 Context Info (Forcing Render via Reply)
		ContextInfo: &waE2E.ContextInfo{
			StanzaID:      proto.String(evt.Info.ID),
			Participant:   proto.String(evt.Info.Sender.String()),
			QuotedMessage: evt.Message,
		},
	}

	// 5. Wrap in FutureProofMessage (The ViewOnce Hack)
	finalMsg := &waE2E.Message{
		ViewOnceMessage: &waE2E.FutureProofMessage{
			Message: &waE2E.Message{
				InteractiveMessage: interactiveMsg,
			},
		},
	}

	// 6. Send
	_, err = client.SendMessage(context.Background(), evt.Info.Chat, finalMsg)
	if err != nil {
		fmt.Printf("❌ Error sending buttons: %v\n", err)
	} else {
		fmt.Println("✅ Button Sent Successfully!")
	}
}
