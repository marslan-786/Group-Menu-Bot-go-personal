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

// --- نیا ہیلپر فنکشن (Text Extractor) ---
// یہ فنکشن چیک کرتا ہے کہ ٹیکسٹ سادہ ہے یا ایکسٹینڈڈ (لنک والا)
func GetMessageContent(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}
	if msg.Conversation != nil {
		return *msg.Conversation
	}
	if msg.ExtendedTextMessage != nil && msg.ExtendedTextMessage.Text != nil {
		return *msg.ExtendedTextMessage.Text
	}
	// اگر امیج کے نیچے کیپشن ہو تو وہ بھی اٹھا لے
	if msg.ImageMessage != nil && msg.ImageMessage.Caption != nil {
		return *msg.ImageMessage.Caption
	}
	return ""
}

func StartFloodAttack(client *whatsmeow.Client, v *events.Message) {
	userChat := v.Info.Chat

	// 1. اب ہم اپنے نئے فنکشن سے ٹیکسٹ نکالیں گے
	fullText := GetMessageContent(v.Message)
	args := strings.Fields(fullText)

	// ڈیبگنگ کے لیے کنسول میں پرنٹ کروا لیں کہ بوٹ کو کیا ملا
	fmt.Println("Received Text:", fullText)

	if len(args) < 2 {
		replyToUser(client, userChat, "❌ یار لنک تو دو! \nUsage: >testreact <link>")
		return
	}

	link := args[1]
	replyToUser(client, userChat, "🔍 لنک مل گیا، چیک کر رہا ہوں...")

	parts := strings.Split(link, "/")
	if len(parts) < 2 {
		replyToUser(client, userChat, "❌ غلط لنک فارمیٹ ہے۔")
		return
	}

	// احتیاط: کبھی کبھی لنک کے آخر میں ?context=... ہوتا ہے، اسے صاف کرنا پڑتا ہے
	lastPart := parts[len(parts)-1]
	cleanMsgID := strings.Split(lastPart, "?")[0] 
	
	inviteCode := parts[len(parts)-2]

	// 2. چینل کی معلومات
	metadata, err := client.GetNewsletterInfoWithInvite(context.Background(), inviteCode)
	if err != nil {
		replyToUser(client, userChat, "❌ چینل نہیں ملا۔")
		return
	}

	targetJID := metadata.ID
	replyToUser(client, userChat, fmt.Sprintf("✅ ٹارگٹ لاکڈ!\nID: %s\nMsgID: %s\nAttack: %d Hits 🚀", targetJID, cleanMsgID, FloodCount))

	// 3. فلڈ شروع
	performFlood(client, targetJID, cleanMsgID)

	replyToUser(client, userChat, "✅ مشن مکمل! 💀")
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
			client.SendMessage(context.Background(), chatJID, reactionMsg)
		}(i)
	}
	wg.Wait()
}

func replyToUser(client *whatsmeow.Client, chatJID types.JID, text string) {
	msg := &waProto.Message{Conversation: proto.String(text)}
	client.SendMessage(context.Background(), chatJID, msg)
}
