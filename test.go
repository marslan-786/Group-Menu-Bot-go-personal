package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

const FloodCount = 50
const TargetEmoji = "❤️" 

func GetMessageContent(msg *waProto.Message) string {
	if msg == nil { return "" }
	if msg.Conversation != nil { return *msg.Conversation }
	if msg.ExtendedTextMessage != nil && msg.ExtendedTextMessage.Text != nil { return *msg.ExtendedTextMessage.Text }
	if msg.ImageMessage != nil && msg.ImageMessage.Caption != nil { return *msg.ImageMessage.Caption }
	return ""
}

func replyToUser(client *whatsmeow.Client, chatJID types.JID, text string) {
	msg := &waProto.Message{Conversation: proto.String(text)}
	client.SendMessage(context.Background(), chatJID, msg)
}

func StartFloodAttack(client *whatsmeow.Client, v *events.Message) {
	userChat := v.Info.Chat
	fullText := GetMessageContent(v.Message)
	args := strings.Fields(fullText)

	if len(args) < 2 {
		replyToUser(client, userChat, "❌ لنک مہیا کریں۔")
		return
	}

	link := args[1]
	parts := strings.Split(link, "/")
	// لنک فارمیٹ: https://whatsapp.com/channel/CODE/ID
	if len(parts) < 2 {
		replyToUser(client, userChat, "❌ غلط لنک۔")
		return
	}

	// آئی ڈی اور کوڈ نکالنا
	lastPart := parts[len(parts)-1]
	msgID := strings.Split(lastPart, "?")[0] // صفائی
	inviteCode := parts[len(parts)-2]

	fmt.Printf("Debug: Invite=%s, MsgID=%s\n", inviteCode, msgID)
	replyToUser(client, userChat, "🔍 چینل ڈیٹا اٹھا رہا ہوں...")

	// 1. چینل کی معلومات (Metadata)
	metadata, err := client.GetNewsletterInfoWithInvite(context.Background(), inviteCode)
	if err != nil {
		replyToUser(client, userChat, fmt.Sprintf("❌ چینل نہیں ملا: %v", err))
		return
	}

	targetJID := metadata.ID
	replyToUser(client, userChat, fmt.Sprintf("✅ چینل: %s\nID: %s\n ٹیسٹ شاٹ لے رہا ہوں...", metadata.ThreadMetadata.Name.Text, msgID))

	// ---------------------------------------------------------
	// 2. TEST SHOT (پہلے ایک ری ایکٹ چیک کریں)
	// ---------------------------------------------------------
	
	// چینل میسجز میں FromMe ہمیشہ false ہوتا ہے
	// RemoteJID چینل کی JID ہوتی ہے
	testReaction := &waProto.Message{
		ReactionMessage: &waProto.ReactionMessage{
			Key: &waProto.MessageKey{
				RemoteJID: proto.String(targetJID.String()),
				FromMe:    proto.Bool(false), 
				ID:        proto.String(msgID),
			},
			Text:              proto.String(TargetEmoji),
			SenderTimestampMS: proto.Int64(time.Now().UnixMilli()), 
		},
	}

	// ہم یہاں ایرر چیک کریں گے
	resp, err := client.SendMessage(context.Background(), targetJID, testReaction)
	if err != nil {
		fmt.Println("Reaction Error:", err)
		replyToUser(client, userChat, fmt.Sprintf("❌ ری ایکٹ فیل ہوگیا!\nوجہ: %v", err))
		return
	}

	fmt.Println("Test Shot Success. Server ID:", resp.ID)
	replyToUser(client, userChat, "✅ ٹیسٹ کامیاب! اب فلڈ کر رہا ہوں... 🚀")

	// 3. اگر ٹیسٹ پاس ہو گیا، تو فلڈ کریں
	performFlood(client, targetJID, msgID)
	
	replyToUser(client, userChat, "✅ مشن مکمل۔")
}

func performFlood(client *whatsmeow.Client, chatJID types.JID, msgID string) {
	var wg sync.WaitGroup
	fmt.Printf(">>> Stacking %s on Msg: %s\n", TargetEmoji, msgID)

	for i := 0; i < FloodCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			reactionMsg := &waProto.Message{
				ReactionMessage: &waProto.ReactionMessage{
					Key: &waProto.MessageKey{
						RemoteJID: proto.String(chatJID.String()),
						FromMe:    proto.Bool(false),
						ID:        proto.String(msgID),
					},
					Text:              proto.String(TargetEmoji),
					SenderTimestampMS: proto.Int64(time.Now().UnixMilli()), 
				},
			}
			// یہاں اب بھی ایرر پرنٹ کروا لیتے ہیں تاکہ کنسول میں پتہ چلے
			_, err := client.SendMessage(context.Background(), chatJID, reactionMsg)
			if err != nil {
				fmt.Printf("Flood Err %d: %v\n", idx, err)
			}
		}(i)
	}
	wg.Wait()
}
