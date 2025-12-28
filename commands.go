package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// ✅ Main Handler (Entry Point)
func handler(client *whatsmeow.Client, rawEvt interface{}) {
	// 🔥 "go func" کا مطلب ہے ہر ایونٹ الگ تھریڈ (Background Task) میں چلے گا۔
	// یہ مین بوٹ کو کبھی بھی بلاک نہیں کرے گا۔ (The "Separate Tab" Logic)
	go func() {
		switch evt := rawEvt.(type) {

		case *events.Message:
			// 1. میسج کو Redis میں محفوظ کریں (بیک گراؤنڈ میں)
			saveMessageToRedis(evt)
			
			// 2. کمانڈز پروسیس کریں
			ProcessCommand(client, evt)

		case *events.MessageRevoke:
			// 3. اینٹی ڈیلیٹ سسٹم
			handleAntiDelete(client, evt)
		}
	}()
}

// ✅ کمانڈ پروسیسر (اب یہ ڈائنامک پریفکس کو سپورٹ کرتا ہے)
func ProcessCommand(client *whatsmeow.Client, evt *events.Message) {
	// 1. میسج ٹیکسٹ نکالیں
	txt := GetMessageContent(evt.Message)
	if txt == "" {
		return
	}

	botID := getCleanID(client.Store.ID.User)
	chatID := evt.Info.Chat.String()

	// 2. پریفکس چیک کریں (RAM سے)
	prefixMutex.RLock()
	currentPrefix, exists := botPrefixes[botID]
	prefixMutex.RUnlock()

	// اگر پریفکس سیٹ نہیں ہے تو ڈیفالٹ "." مان لیں
	if !exists || currentPrefix == "" {
		currentPrefix = "."
	}

	// 3. کیا میسج ہمارے پریفکس سے شروع ہو رہا ہے؟
	if !strings.HasPrefix(txt, currentPrefix) {
		return // اگر پریفکس نہیں ہے تو اگنور کریں
	}

	// 4. پریفکس ہٹا کر کمانڈ اور آرگیومنٹس الگ کریں
	// مثال: "!delete on" (اگر پریفکس ! ہے) -> cmd="delete", args=["on"]
	cleanTxt := strings.TrimPrefix(txt, currentPrefix)
	parts := strings.Fields(cleanTxt)
	
	if len(parts) == 0 { return }

	cmd := strings.ToLower(parts[0]) // کمانڈ (e.g., delete)
	
	// 🔥 SWITCH CASE - نئے فیچرز یہاں ایڈ ہوں گے
	switch cmd {

	case "setprefix":
		// ✅ نیا فیچر: پریفکس چینج کمانڈ
		handleSetPrefix(client, botID, chatID, evt, parts)

	case "delete":
		handleDeleteCommand(client, botID, chatID, evt, parts)

	case "ping":
		ReplyText(client, chatID, evt.Info.ID, fmt.Sprintf("🏓 *Pong!*\nCurrent Prefix: `%s`", currentPrefix))

	case "menu":
		ReplyText(client, chatID, evt.Info.ID, fmt.Sprintf("📜 *Control Panel:*\nPrefix: `%s`\n\n1. %sdelete on/off\n2. %ssetprefix [symbol]\n3. %sping", currentPrefix, currentPrefix, currentPrefix, currentPrefix))

	default:
		// نامعلوم کمانڈ (اگنور کریں)
	}
}

// ✅ فیچر 1: SET PREFIX FUNCTION
func handleSetPrefix(client *whatsmeow.Client, botID, chatID string, evt *events.Message, parts []string) {
	// چیک: کیا یوزر نے نیا سمبل دیا ہے؟
	if len(parts) < 2 {
		ReplyText(client, chatID, evt.Info.ID, "⚠️ *Error:* Please provide a symbol.\nExample: `setprefix !` or `setprefix #`")
		return
	}

	newPrefix := strings.TrimSpace(parts[1])

	// زیادہ لمبا پریفکس نہ ہو (Max 1 character recommended, but allowed up to 3)
	if len(newPrefix) > 3 {
		ReplyText(client, chatID, evt.Info.ID, "❌ Prefix too long! Keep it short (e.g., `.`, `!`, `#`).")
		return
	}

	// 1. Redis اور RAM میں اپڈیٹ کریں (یہ فنکشن main.go میں موجود ہے)
	updatePrefixDB(botID, newPrefix)

	ReplyText(client, chatID, evt.Info.ID, fmt.Sprintf("✅ *Prefix Updated!*\nNew Prefix: `%s`\nNow use: `%smenu`", newPrefix, newPrefix))
}

// ✅ فیچر 2: DELETE COMMAND (بغیر تبدیلی کے، بس اب یہ نئے پریفکس پر چلے گا)
func handleDeleteCommand(client *whatsmeow.Client, botID, chatID string, evt *events.Message, parts []string) {
	if len(parts) < 2 {
		ReplyText(client, chatID, evt.Info.ID, "⚠️ *Use:* `delete on` or `delete off`")
		return
	}

	subCmd := strings.ToLower(parts[1])
	settings := getGroupSettings(botID, chatID)

	switch subCmd {
	case "on":
		settings.Antidelete = true 
		saveGroupSettings(botID, settings)
		ReplyText(client, chatID, evt.Info.ID, "🛡️ *Anti-Delete Active!*")
	case "off":
		settings.Antidelete = false
		saveGroupSettings(botID, settings)
		ReplyText(client, chatID, evt.Info.ID, "💤 *Anti-Delete Disabled!*")
	default:
		ReplyText(client, chatID, evt.Info.ID, "❌ Use `on` or `off`")
	}
}

// ✅ فیچر 3: ANTI-DELETE SYSTEM (وہی ہیوی کارڈ والا)
func handleAntiDelete(client *whatsmeow.Client, evt *events.MessageRevoke) {
	key := "msg_cache:" + evt.ID
	val, err := rdb.Get(ctx, key).Bytes()
	
	if err != nil { return } // میسج نہیں ملا

	originalMsg := &waE2E.Message{}
	proto.Unmarshal(val, originalMsg)

	chatID := evt.Chat.String()
	botID := getCleanID(client.Store.ID.User)
	settings := getGroupSettings(botID, chatID)
	
	if !settings.Antidelete { return }

	senderJID := evt.Participant
	if senderJID.IsEmpty() { senderJID = evt.Chat }
	senderNum := strings.Split(senderJID.User, "@")[0]
	msgTime := time.Now().Format("03:04 PM")

	// 🎨 HEAVY CARD DESIGN
	caption := fmt.Sprintf(
		"█▀▀▀▀▀▀▀▀▀▀▀▀▀█\n"+
		"█ 🚫 *ANTI-DELETE* █\n"+
		"█▄▄▄▄▄▄▄▄▄▄▄▄▄█\n"+
		"┃ 👤 @%s\n"+
		"┃ 🕒 %s\n"+
		"┃ 🗑️ *Recovered*\n"+
		"╰━━━━━━━━━━━━━━⪼",
		senderNum, msgTime,
	)

	forwardedMsg := &waE2E.Message{
		Conversation:        originalMsg.Conversation,
		ImageMessage:        originalMsg.ImageMessage,
		VideoMessage:        originalMsg.VideoMessage,
		AudioMessage:        originalMsg.AudioMessage,
		ExtendedTextMessage: originalMsg.ExtendedTextMessage,
		StickerMessage:      originalMsg.StickerMessage,
	}

	contextInfo := &waE2E.ContextInfo{
		StanzaId:      proto.String(evt.ID),
		Participant:   proto.String(senderJID.String()),
		MentionedJid:  []string{senderJID.String()},
		IsForwarded:   proto.Bool(true),
	}

	if forwardedMsg.ImageMessage != nil {
		forwardedMsg.ImageMessage.Caption = proto.String(caption)
		forwardedMsg.ImageMessage.ContextInfo = contextInfo
	} else if forwardedMsg.VideoMessage != nil {
		forwardedMsg.VideoMessage.Caption = proto.String(caption)
		forwardedMsg.VideoMessage.ContextInfo = contextInfo
	} else if forwardedMsg.StickerMessage != nil {
		ReplyText(client, chatID, evt.ID, caption)
		forwardedMsg.StickerMessage.ContextInfo = contextInfo
	} else {
		finalText := caption + "\n\n💬: " + GetMessageContent(originalMsg)
		forwardedMsg.Conversation = nil
		forwardedMsg.ExtendedTextMessage = &waE2E.ExtendedTextMessage{
			Text:        proto.String(finalText),
			ContextInfo: contextInfo,
		}
	}

	client.SendMessage(context.Background(), evt.Chat, forwardedMsg)
}

// 🛠️ Helper Functions
func saveMessageToRedis(evt *events.Message) {
	if evt.Info.ID == "" || evt.Message == nil { return }
	msgBytes, _ := proto.Marshal(evt.Message)
	rdb.Set(ctx, "msg_cache:"+evt.Info.ID, msgBytes, 24*time.Hour)
}

func GetMessageContent(msg *waE2E.Message) string {
	if msg == nil { return "" }
	if msg.Conversation != nil { return *msg.Conversation }
	if msg.ExtendedTextMessage != nil { return *msg.ExtendedTextMessage.Text }
	if msg.ImageMessage != nil { return *msg.ImageMessage.Caption }
	if msg.VideoMessage != nil { return *msg.VideoMessage.Caption }
	return ""
}
