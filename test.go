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
	
	if len(parts) < 2 {
		replyToUser(client, userChat, "❌ غلط لنک۔")
		return
	}

	lastPart := parts[len(parts)-1]
	msgID := strings.Split(lastPart, "?")[0]
	inviteCode := parts[len(parts)-2]

	replyToUser(client, userChat, "🔍 چینل ڈیٹا اٹھا رہا ہوں...")

	// 1. Resolve Channel
	metadata, err := client.GetNewsletterInfoWithInvite(context.Background(), inviteCode)
	if err != nil {
		replyToUser(client, userChat, fmt.Sprintf("❌ چینل نہیں ملا: %v", err))
		return
	}

	targetJID := metadata.ID
	
	// 2. SMART TEST SHOT (Auto-Fix for Admins)
	isSuccess := false
	
	// کوشش نمبر 1: نارمل طریقے سے
	fmt.Println("Attempt 1: FromMe=False")
	testReaction := buildReaction(targetJID, msgID, false)
	resp, err1 := client.SendMessage(context.Background(), targetJID, testReaction)
	
	if err1 == nil {
		isSuccess = true
		fmt.Println("Success on Try 1. ID:", resp.ID)
	} else {
		// اگر فیل ہوا تو ایرر دیکھیں
		fmt.Println("Try 1 Failed:", err1)
		
		// کوشش نمبر 2: ایڈمن موڈ (FromMe=True)
		// کبھی کبھی ایڈمن کو اپنے بھیجے ہوئے میسج پر ری ایکٹ کرنے کے لیے یہ چاہیے ہوتا ہے
		fmt.Println("Attempt 2: FromMe=True (Admin Mode)")
		testReaction2 := buildReaction(targetJID, msgID, true)
		resp2, err2 := client.SendMessage(context.Background(), targetJID, testReaction2)
		
		if err2 == nil {
			isSuccess = true
			fmt.Println("Success on Try 2. ID:", resp2.ID)
		} else {
			// دونوں فیل ہو گئے
			replyToUser(client, userChat, fmt.Sprintf("❌ ری ایکٹ دونوں طریقوں سے فیل ہوگیا!\nID: %s\nError 1: %v\nError 2: %v", msgID, err1, err2))
			return
		}
	}

	if isSuccess {
		replyToUser(client, userChat, "✅ ٹیسٹ کامیاب! اب فلڈ شروع... 🚀")
		// فلڈ اسی طریقے سے کریں گے جو کامیاب رہا
		// یہاں ہم دونوں کو parallel چلا دیتے ہیں تاکہ ٹینشن ہی ختم ہو
		performFlood(client, targetJID, msgID)
		replyToUser(client, userChat, "✅ مشن مکمل۔")
	}
}

// ہیلپر فنکشن: ری ایکٹ میسج بنانے کے لیے
func buildReaction(chatJID types.JID, msgID string, fromMe bool) *waProto.Message {
	return &waProto.Message{
		ReactionMessage: &waProto.ReactionMessage{
			Key: &waProto.MessageKey{
				RemoteJID: proto.String(chatJID.String()),
				FromMe:    proto.Bool(fromMe), // یہ اہم ہے
				ID:        proto.String(msgID),
			},
			Text:              proto.String(TargetEmoji),
			SenderTimestampMS: proto.Int64(time.Now().UnixMilli()), 
		},
	}
}

func performFlood(client *whatsmeow.Client, chatJID types.JID, msgID string) {
	var wg sync.WaitGroup
	fmt.Printf(">>> Stacking %s on Msg: %s\n", TargetEmoji, msgID)

	for i := 0; i < FloodCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			
			// ہم "FromMe" کو مکس کر کے بھیجیں گے تاکہ جو لگنا ہو لگ جائے
			// آدھے False ہوں گے، آدھے True
			fromMe := false
			if idx%2 == 0 {
				fromMe = true
			}

			msg := buildReaction(chatJID, msgID, fromMe)
			client.SendMessage(context.Background(), chatJID, msg)
		}(i)
	}
	wg.Wait()
}