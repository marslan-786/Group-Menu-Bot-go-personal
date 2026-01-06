package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
	
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// ⚙️ SETTINGS
const (
	MongoURI = "mongodb://mongo:AEvrikOWlrmJCQrDTQgfGtqLlwhwLuAA@crossover.proxy.rlwy.net:29609"
)

// 🗄️ MongoDB Collections
var (
	msgCollection      *mongo.Collection // میسج محفوظ کرنے کے لیے
	settingsCollection *mongo.Collection // ہر بوٹ کی سیٹنگ (Anti-Delete On/Off + GroupID)
)

// 📦 DB Structs
type SavedMsg struct {
	ID        string `bson:"_id"`
	Sender    string `bson:"sender"`
	Content   []byte `bson:"content"`
	Timestamp int64  `bson:"timestamp"`
}

type BotSettings struct {
	BotJID       string `bson:"_id"`          // بوٹ کا اپنا نمبر (بطور ID)
	IsAntiDelete bool   `bson:"is_antidelete"`
	DumpGroupID  string `bson:"dump_group_id"`
}

// 🚀 1. SETUP FUNCTION (Call this in main)
func SetupFeatures() {
	clientOptions := options.Client().ApplyURI(MongoURI)
	client, err := mongo.Connect(context.TODO(), clientOptions)
	if err != nil {
		log.Fatal("❌ MongoDB Connection Failed:", err)
	}
	
	db := client.Database("whatsapp_bot_multi")
	msgCollection = db.Collection("messages")
	settingsCollection = db.Collection("bot_settings")
	
	fmt.Println("✅ Features Module Loaded (Multi-Device Supported)")
}

// 🔥 2. MAIN EVENT LISTENER
func ListenForFeatures(client *whatsmeow.Client, evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		
		// --- A: STATUS SAVER LOGIC (Simple Forwarding) ---
		// اگر آپ سٹیٹس سیور کو بھی DB پر شفٹ کرنا چاہتے ہیں تو بتا دینا، فی الحال یہ Simple رکھا ہے۔
		// (اس حصے کو آپ اپنی پرانی لاجک کے مطابق رکھ سکتے ہیں)

		// --- B: ANTI-DELETE LOGIC (Personal Chats Only) ---
		if !v.Info.IsGroup && !v.Info.IsFromMe {
			
			// 1. Save Normal Message (ہر آنے والا میسج محفوظ کریں)
			if v.Message.GetProtocolMessage() == nil {
				saveMsgToDB(v)
				return
			}

			// 2. Detect Revoke (Message Deleted)
			if v.Message.GetProtocolMessage() != nil && 
			   v.Message.GetProtocolMessage().GetType() == waProto.ProtocolMessage_REVOKE {
				
				handleDelete(client, v)
			}
		}
	}
}

// 🛠️ ANTI-DELETE HANDLER
func handleDelete(client *whatsmeow.Client, v *events.Message) {
	// 1. چیک کریں کہ اس بوٹ کے لیے فیچر آن ہے یا نہیں؟
	botID := client.Store.ID.User
	var settings BotSettings
	err := settingsCollection.FindOne(context.TODO(), bson.M{"_id": botID}).Decode(&settings)
	
	// اگر سیٹنگ نہیں ملی، یا فیچر آف ہے، یا گروپ سیٹ نہیں ہے -> تو ریٹرن کر جاؤ
	if err != nil || !settings.IsAntiDelete || settings.DumpGroupID == "" {
		return
	}

	// 2. اصل میسج DB سے نکالیں
	deletedID := v.Message.GetProtocolMessage().GetKey().GetId()
	var result SavedMsg
	err = msgCollection.FindOne(context.TODO(), bson.M{"_id": deletedID}).Decode(&result)
	
	if err != nil {
		return // میسج نہیں ملا (شاید بوٹ بند تھا جب میسج آیا)
	}

	// 3. میسج کو Unmarshal کریں
	var content waProto.Message
	proto.Unmarshal(result.Content, &content)

	// 4. ٹارگٹ گروپ (جہاں میسج بھیجنا ہے)
	targetGroup, _ := types.ParseJID(settings.DumpGroupID)

	// --- Step 1: اصل میسج بھیجیں (Recovered Post) ---
	sentMsg, err := client.SendMessage(context.Background(), targetGroup, &content)
	if err != nil {
		fmt.Println("Failed to forward deleted msg:", err)
		return
	}

	// --- Step 2: تفصیلات کے ساتھ رپلائی کریں (Info Reply) ---
	senderJID := v.Info.Sender
	senderName := v.Info.PushName
	if senderName == "" { senderName = "Unknown" }
	
	msgTime := time.Unix(result.Timestamp, 0).Format("03:04:05 PM")
	deleteTime := time.Now().Format("03:04:05 PM")

	caption := fmt.Sprintf(`⚠️ *ANTIDELETE ALERT*
	
👤 *User:* %s
📱 *Number:* @%s
⏰ *Sent:* %s
🗑️ *Deleted:* %s`, senderName, senderJID.User, msgTime, deleteTime)

	// رپلائی میسج بنانا
	replyMsg := &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(caption),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:      proto.String(sentMsg.ID), // اسی میسج کو رپلائی کرے گا جو ابھی بھیجا ہے
				Participant:   proto.String(client.Store.ID.String()), // بوٹ اپنی طرف سے رپلائی کر رہا ہے
				QuotedMessage: &content,
				MentionedJID:  []string{senderJID.String()}, // یوزر کو ٹیگ کریں
			},
		},
	}

	client.SendMessage(context.Background(), targetGroup, replyMsg)
}

// 💾 DB HELPER: Save Message
func saveMsgToDB(v *events.Message) {
	// میسج کو Bytes میں کنورٹ کریں
	bytes, _ := proto.Marshal(v.Message)
	
	doc := SavedMsg{
		ID:        v.Info.ID,
		Sender:    v.Info.Sender.User,
		Content:   bytes,
		Timestamp: v.Info.Timestamp.Unix(),
	}
	
	// اگر پہلے سے موجود ہے تو اگنور کرے گا، ورنہ انسرٹ
	// (آپ چاہیں تو ReplaceOne بھی استعمال کر سکتے ہیں)
	_, err := msgCollection.InsertOne(context.TODO(), doc)
	if err != nil {
		// Duplicate key error is fine, ignore it
	}
}

// 🎮 COMMAND HANDLER (Use this in Switch Case)
func HandleAntiDeleteCommand(client *whatsmeow.Client, msg *events.Message, args []string) {
	// 1. صرف اونر استعمال کر سکتا ہے (Call your existing logic)
	// (اگر آپ کے پاس isOwner کا فنکشن مین فائل میں ہے تو یہ یہاں کال نہیں ہوگا کیونکہ یہ دوسری فائل ہے)
	// اس لیے ہم یہاں ایک Simple Check لگا سکتے ہیں یا آپ اسے مین فائل کے سوئچ میں ہینڈل کریں۔
	
	// فی الحال ہم فرض کرتے ہیں کہ یہ کمانڈ صرف اونر نے لگائی ہے۔
	
	if len(args) == 0 {
		client.SendMessage(context.Background(), msg.Info.Chat, &waProto.Message{
			Conversation: proto.String("❌ Usage:\n.antidelete on\n.antidelete off\n.antidelete set (in group)"),
		})
		return
	}

	botID := client.Store.ID.User
	cmd := strings.ToLower(args[0])

	if cmd == "set" {
		if !msg.Info.IsGroup {
			client.SendMessage(context.Background(), msg.Info.Chat, &waProto.Message{Conversation: proto.String("⚠️ یہ کمانڈ صرف گروپ میں استعمال کریں۔")})
			return
		}

		// Update DB with GroupID
		filter := bson.M{"_id": botID}
		update := bson.M{"$set": bson.M{"dump_group_id": msg.Info.Chat.String(), "is_antidelete": true}}
		opts := options.Update().SetUpsert(true)
		
		_, err := settingsCollection.UpdateOne(context.TODO(), filter, update, opts)
		if err != nil {
			client.SendMessage(context.Background(), msg.Info.Chat, &waProto.Message{Conversation: proto.String("❌ Database Error!")})
			return
		}
		
		client.SendMessage(context.Background(), msg.Info.Chat, &waProto.Message{
			Conversation: proto.String("✅ This group is set as Anti-Delete Log Channel for your bot."),
		})
		return
	}

	if cmd == "on" || cmd == "off" {
		status := (cmd == "on")
		
		filter := bson.M{"_id": botID}
		update := bson.M{"$set": bson.M{"is_antidelete": status}}
		opts := options.Update().SetUpsert(true)

		_, err := settingsCollection.UpdateOne(context.TODO(), filter, update, opts)
		if err != nil {
			client.SendMessage(context.Background(), msg.Info.Chat, &waProto.Message{Conversation: proto.String("❌ Database Error!")})
			return
		}

		statusText := "Disabled ❌"
		if status { statusText = "Enabled ✅" }
		client.SendMessage(context.Background(), msg.Info.Chat, &waProto.Message{
			Conversation: proto.String("🛡️ Anti-Delete is now " + statusText),
		})
	}
}