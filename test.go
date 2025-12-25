package main

import (
	"context"
	"fmt"
	"strconv"
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
	if len(parts) < 2 {
		replyToUser(client, userChat, "❌ غلط لنک۔")
		return
	}

	// 1. IDs نکالنا
	strMsgID := strings.Split(parts[len(parts)-1], "?")[0]
	inviteCode := parts[len(parts)-2]

	// لنک والی ID کو نمبر (Int) میں بدلنا ضروری ہے تاکہ fetch کر سکیں
	serverMsgID, err := strconv.Atoi(strMsgID)
	if err != nil {
		replyToUser(client, userChat, "❌ Message ID غلط ہے۔")
		return
	}

	replyToUser(client, userChat, "🔍 سرور سے میسج ڈھونڈ رہا ہوں...")

	// 2. چینل Resolve کرنا
	metadata, err := client.GetNewsletterInfoWithInvite(context.Background(), inviteCode)
	if err != nil {
		replyToUser(client, userChat, fmt.Sprintf("❌ چینل نہیں ملا: %v", err))
		return
	}
	targetJID := metadata.ID

	// 3. FETCH LOGIC (یہ سب سے اہم حصہ ہے)
	// ہم سرور سے کہتے ہیں: "فلاں ID والا میسج مجھے لا کر دو"
	// ہم اس آئی ڈی سے اگلی آئی ڈی (Before) مانگیں گے تو ہمیں پچھلا میسج مل جائے گا
	fetchParams := &whatsmeow.GetNewsletterMessagesParams{
		Count:  1,
		Before: types.MessageServerID(serverMsgID + 1), // Trick to fetch exact ID
	}

	fetchedMsgs, err := client.GetNewsletterMessages(context.Background(), targetJID, fetchParams)
	if err != nil {
		replyToUser(client, userChat, fmt.Sprintf("❌ Fetch Error: %v", err))
		return
	}

	if len(fetchedMsgs) == 0 {
		replyToUser(client, userChat, "❌ میسج نہیں ملا (شاید ڈیلیٹ ہو چکا ہے یا بہت پرانا ہے)۔")
		return
	}

	// میسج مل گیا!
	foundMsg := fetchedMsgs[0]
	
	// اب ہم چیک کریں گے کہ کیا واقعی یہی وہ میسج ہے؟
	if int(foundMsg.ServerID) != serverMsgID {
		replyToUser(client, userChat, fmt.Sprintf("❌ آئی ڈی میچ نہیں ہوئی!\nFound: %d, Wanted: %d", foundMsg.ServerID, serverMsgID))
		// لیکن پھر بھی ہم اسی پر اٹیک کریں گے جو ملا ہے، شاید کام کر جائے
	}

	replyToUser(client, userChat, fmt.Sprintf("✅ میسج مل گیا! (ServerID: %d)\nفلڈ شروع... 🚀", foundMsg.ServerID))

	// 4. FLOOD using EXACT KEY
	// اب ہم "تکا" نہیں لگا رہے، جو Key سرور نے دی ہے وہی واپس بھیج رہے ہیں
	performFlood(client, targetJID, foundMsg.Message.Key)
	
	replyToUser(client, userChat, "✅ مشن مکمل۔")
}

// اس فنکشن کو تبدیل کیا ہے تاکہ یہ Original Key قبول کرے
func performFlood(client *whatsmeow.Client, chatJID types.JID, originalKey *waProto.MessageKey) {
	var wg sync.WaitGroup
	fmt.Printf(">>> Flooding on Msg ID: %s\n", originalKey.GetId())

	for i := 0; i < FloodCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			
			// Original Key کو کاپی کر کے نیا ری ایکٹ بنائیں
			reactionMsg := &waProto.Message{
				ReactionMessage: &waProto.ReactionMessage{
					Key: &waProto.MessageKey{
						RemoteJID: originalKey.RemoteJID,
						FromMe:    originalKey.FromMe, // جو سرور نے دیا وہی استعمال ہوگا
						ID:        originalKey.ID,
					},
					Text:              proto.String(TargetEmoji),
					SenderTimestampMS: proto.Int64(time.Now().UnixMilli()), 
				},
			}
			
			_, err := client.SendMessage(context.Background(), chatJID, reactionMsg)
			if err != nil && idx == 0 {
				fmt.Printf("Flood Err: %v\n", err)
			}
		}(i)
	}
	wg.Wait()
}