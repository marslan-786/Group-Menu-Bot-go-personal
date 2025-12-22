package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
	"encoding/json"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
	"github.com/redis/go-redis/v9"
)

// 🛡️ سیٹنگز کا ڈھانچہ (Structure)
// اس میں تم مزید چیزیں بھی ڈال سکتے ہو جیسے AntiLink، Welcome وغیرہ
type BotSettings struct {
	Prefix     string `json:"prefix"`
	SelfMode   bool   `json:"self_mode"`
	AutoStatus bool   `json:"auto_status"`
	OnlyGroup  bool   `json:"only_group"`
}

// 💾 1. تمام سیٹنگز ریڈیس میں محفوظ کرنا
func SaveAllSettings(rdb *redis.Client, botID string, settings BotSettings) {
	// ڈیٹا کو JSON میں بدلیں
	data, err := json.Marshal(settings)
	if err != nil {
		fmt.Println("❌ [REDIS] JSON encoding error:", err)
		return
	}

	// ریڈیس میں بوٹ کی آئی ڈی کے نام سے سیو کریں
	key := fmt.Sprintf("settings:%s", botID)
	err = rdb.Set(ctx, key, data, 0).Err() // 0 کا مطلب ہے کبھی ڈیلیٹ نہ ہو
	if err != nil {
		fmt.Println("❌ [REDIS] Save error:", err)
	} else {
		fmt.Printf("✅ [SAVED] Settings for %s stored in Redis\n", botID)
	}
}

// 📥 2. ریڈیس سے سیٹنگز واپس لوڈ کرنا
func LoadAllSettings(rdb *redis.Client, botID string) BotSettings {
	key := fmt.Sprintf("settings:%s", botID)
	val, err := rdb.Get(ctx, key).Result()

	var settings BotSettings
	if err == redis.Nil {
		// اگر پہلے سے کوئی سیٹنگ نہیں ہے تو ڈیفالٹ سیٹ کریں
		fmt.Println("ℹ️ [REDIS] No settings found, using defaults.")
		return BotSettings{Prefix: ".", SelfMode: false, AutoStatus: true}
	} else if err != nil {
		fmt.Println("❌ [REDIS] Load error:", err)
		return BotSettings{Prefix: "."}
	}

	// JSON سے واپس اسٹرکچر میں بدلیں
	err = json.Unmarshal([]byte(val), &settings)
	if err != nil {
		fmt.Println("❌ [REDIS] JSON decoding error:", err)
	}
	
	fmt.Printf("🚀 [LOADED] Settings for %s synced from Redis\n", botID)
	return settings
}

// 🛡️ گروپ سیکیورٹی سیٹنگز کا ڈھانچہ
type GroupSecurity struct {
	AntiLink   bool `json:"anti_link"`
	AllowAdmin bool `json:"allow_admin"` // جو آپ اسٹیج 1 میں پوچھ رہے ہیں
}

// 💾 گروپ سیٹنگ سیو کرنا (Group Specific)
func SaveGroupSecurity(rdb *redis.Client, botLID string, groupID string, data GroupSecurity) {
	key := fmt.Sprintf("sec:%s:%s", botLID, groupID)
	payload, _ := json.Marshal(data)
	
	err := rdb.Set(ctx, key, payload, 0).Err()
	if err != nil {
		fmt.Printf("❌ [REDIS] Save Error for Group %s: %v\n", groupID, err)
	}
}

// 📥 گروپ سیٹنگ لوڈ کرنا (Group Specific)
func LoadGroupSecurity(rdb *redis.Client, botLID string, groupID string) GroupSecurity {
	key := fmt.Sprintf("sec:%s:%s", botLID, groupID)
	val, err := rdb.Get(ctx, key).Result()
	
	var data GroupSecurity
	if err != nil {
		// اگر کوئی سیٹنگ نہیں ملی تو ڈیفالٹ (False) واپس کریں
		return GroupSecurity{AntiLink: false, AllowAdmin: false}
	}
	
	json.Unmarshal([]byte(val), &data)
	return data
}

// فرض کریں یوزر نے 'antilink' آن کرنے کا فیصلہ کر لیا ہے
func finalizeSecurity(client *whatsmeow.Client, senderLID string, choice string) {
	state := setupMap[senderLID]
	if state == nil { return }

	allowAdmin := (choice == "1") // اگر یوزر نے 1 دبایا تو ایڈمن الاؤ ہیں
	
	// سیٹنگز تیار کریں
	newConfig := GroupSecurity{
		AntiLink:   true, // کیونکہ وہ اینٹی لنک کا سیٹ اپ کر رہا تھا
		AllowAdmin: allowAdmin,
	}

	// 💾 ریڈیس میں اس گروپ کے لیے مخصوص سیو کریں
	SaveGroupSecurity(rdb, state.BotLID, state.GroupID, newConfig)
	
	// میپ سے ڈیلیٹ کر دیں
	delete(setupMap, senderLID)
}
// ==================== سیکورٹی سسٹم ====================
func checkSecurity(client *whatsmeow.Client, v *events.Message) {
	if !v.Info.IsGroup {
		return
	}

	s := getGroupSettings(v.Info.Chat.String())
	if s.Mode == "private" {
		return
	}

	// ✅ Anti-link check - NO admin bypass for deletion
	if s.Antilink && containsLink(getText(v.Message)) {
		// Delete link regardless of who sent it
		takeSecurityAction(client, v, s, s.AntilinkAction, "Link detected")
		return
	}

	// Anti-picture check
	if s.AntiPic && v.Message.ImageMessage != nil {
		takeSecurityAction(client, v, s, "delete", "Image not allowed")
		return
	}

	// Anti-video check
	if s.AntiVideo && v.Message.VideoMessage != nil {
		takeSecurityAction(client, v, s, "delete", "Video not allowed")
		return
	}

	// Anti-sticker check
	if s.AntiSticker && v.Message.StickerMessage != nil {
		takeSecurityAction(client, v, s, "delete", "Sticker not allowed")
		return
	}
}

func containsLink(text string) bool {
	if text == "" {
		return false
	}

	text = strings.ToLower(text)
	linkPatterns := []string{
		"http://", "https://", "www.",
		"chat.whatsapp.com/", "t.me/", "youtube.com/",
		"youtu.be/", "instagram.com/", "fb.com/",
		"facebook.com/", "twitter.com/", "x.com/",
	}

	for _, pattern := range linkPatterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}

	return false
}

func takeSecurityAction(client *whatsmeow.Client, v *events.Message, s *GroupSettings, action, reason string) {
	switch action {
	case "delete":
		// ✅ Delete for everyone
		_, err := client.SendMessage(context.Background(), v.Info.Chat, client.BuildRevoke(v.Info.Chat, v.Info.Sender, v.Info.ID))
		if err != nil {
			log.Printf("❌ Delete failed: %v", err)
			msg := `╔════════════════╗
║ ❌ DELETE FAILED
╠════════════════╣
║ Bot needs admin
╚════════════════╝`
			replyMessage(client, v, msg)
			return
		}

		log.Printf("✅ Message deleted successfully")

		msg := fmt.Sprintf(`╔════════════════╗
║ 🚫 DELETED
╠════════════════╣
║ Reason: %s
║ User: @%s
╚════════════════╝`, reason, v.Info.Sender.User)
		
		senderStr := v.Info.Sender.String()
		client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String(msg),
				ContextInfo: &waProto.ContextInfo{
					MentionedJID: []string{senderStr},
					StanzaID:     proto.String(v.Info.ID),
					Participant:  proto.String(senderStr),
				},
			},
		})

	case "deletekick":
		// ✅ Delete for everyone
		_, err := client.SendMessage(context.Background(), v.Info.Chat, client.BuildRevoke(v.Info.Chat, v.Info.Sender, v.Info.ID))
		if err != nil {
			log.Printf("❌ Delete failed: %v", err)
			msg := `╔════════════════╗
║ ❌ DELETE FAILED
╠════════════════╣
║ Bot needs admin
╚════════════════╝`
			replyMessage(client, v, msg)
			return
		}

		log.Printf("✅ Message deleted successfully")

		_, err = client.UpdateGroupParticipants(context.Background(), v.Info.Chat,
			[]types.JID{v.Info.Sender}, whatsmeow.ParticipantChangeRemove)
		
		if err != nil {
			log.Printf("❌ Kick failed: %v", err)
			msg := `╔════════════════╗
║ ⚠️ KICK FAILED
╠════════════════╣
║ Bot needs admin
╚════════════════╝`
			replyMessage(client, v, msg)
			return
		}

		log.Printf("✅ User kicked successfully")
		
		msg := fmt.Sprintf(`╔════════════════╗
║ 👢 KICKED
╠════════════════╣
║ Reason: %s
║ User: @%s
║ Action: Delete+Kick
╚════════════════╝`, reason, v.Info.Sender.User)
		
		senderStr := v.Info.Sender.String()
		client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String(msg),
				ContextInfo: &waProto.ContextInfo{
					MentionedJID: []string{senderStr},
				},
			},
		})

	case "deletewarn":
		senderKey := v.Info.Sender.String()
		s.Warnings[senderKey]++
		warnCount := s.Warnings[senderKey]

		// ✅ Delete for everyone
		_, err := client.SendMessage(context.Background(), v.Info.Chat, client.BuildRevoke(v.Info.Chat, v.Info.Sender, v.Info.ID))
		if err != nil {
			log.Printf("❌ Delete failed: %v", err)
			msg := `╔════════════════╗
║ ❌ DELETE FAILED
╠════════════════╣
║ Bot needs admin
╚════════════════╝`
			replyMessage(client, v, msg)
			return
		}

		log.Printf("✅ Message deleted successfully")

		if warnCount >= 3 {
			_, err := client.UpdateGroupParticipants(context.Background(), v.Info.Chat,
				[]types.JID{v.Info.Sender}, whatsmeow.ParticipantChangeRemove)
			
			if err != nil {
				log.Printf("❌ Kick failed after 3 warnings: %v", err)
				msg := `╔════════════════╗
║ ⚠️ KICK FAILED
╠════════════════╣
║ Bot needs admin
╚════════════════╝`
				replyMessage(client, v, msg)
				return
			}

			log.Printf("✅ User kicked after 3 warnings")

			delete(s.Warnings, senderKey)
			
			msg := fmt.Sprintf(`╔════════════════╗
║ 🚫 KICKED
╠════════════════╣
║ User: @%s
║ Warning: 3/3
║ Kicked Out
╚════════════════╝`, v.Info.Sender.User)
			
			senderStr := v.Info.Sender.String()
			client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: proto.String(msg),
					ContextInfo: &waProto.ContextInfo{
						MentionedJID: []string{senderStr},
					},
				},
			})
		} else {
			msg := fmt.Sprintf(`╔════════════════╗
║ ⚠️ WARNING
╠════════════════╣
║ User: @%s
║ Count: %d/3
║ Reason: %s
║ 3 = Kick
╚════════════════╝`, v.Info.Sender.User, warnCount, reason)
			
			senderStr := v.Info.Sender.String()
			client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: proto.String(msg),
					ContextInfo: &waProto.ContextInfo{
						MentionedJID: []string{senderStr},
						StanzaID:     proto.String(v.Info.ID),
						Participant:  proto.String(senderStr),
					},
				},
			})
		}

		saveGroupSettings(s)
	}
}
// مثال کے طور پر
func onResponse(choice string) {
    state := setupMap[v.Info.Sender.String()]
    // ریڈیس میں کی بنے گی: sec:[BotLID]:[GroupID]:[secType]
    key := fmt.Sprintf("sec:%s:%s:%s", state.BotLID, state.GroupID, state.Type)
    rdb.Set(ctx, key, choice, 0)
}

func startSecuritySetup(client *whatsmeow.Client, v *events.Message, secType string) {
	// 1️⃣ صرف گروپ میں چلے گا
	if !v.Info.IsGroup {
		replyMessage(client, v, "╔════════════════╗\n║ ❌ GROUP ONLY\n╚════════════════╝")
		return
	}

	// 2️⃣ ایڈمن چیک لاجک (Admin Only Check)
	isAdmin := false
	groupInfo, err := client.GetGroupInfo(context.Background(), v.Info.Chat)
	if err == nil {
		for _, participant := range groupInfo.Participants {
			// اگر بندہ ایڈمن یا سپر ایڈمن ہے
			if participant.JID.User == v.Info.Sender.User && (participant.IsAdmin || participant.IsSuperAdmin) {
				isAdmin = true
				break
			}
		}
	}
	
	// اگر بندہ اونر ہے تو اسے بھی اجازت دیں (صرف اضافی سیکیورٹی کے لیے)
	if !isAdmin && isOwner(client, v.Info.Sender) {
		isAdmin = true
	}

	if !isAdmin {
		replyMessage(client, v, "╔════════════════╗\n║ 👮 ADMIN ONLY\n╠════════════════╣\n║ ❌ YOU ARE NOT\n║ AN ADMIN\n╚════════════════╝")
		return
	}

	// 3️⃣ بوٹ کی اپنی LID حاصل کریں (تاکہ ریڈیس میں ڈیٹا مکس نہ ہو)
	botLID := getBotLIDFromDB(client)
	senderStr := v.Info.Sender.String()
	groupID := v.Info.Chat.String()

	// 4️⃣ عارضی میپ میں مکمل ڈیٹا محفوظ کریں
	setupMap[senderStr] = &SetupState{
		Type:    secType, // یہاں Antilink, Anti-Video, Anti-Picture کچھ بھی ہو سکتا ہے
		Stage:   1,
		GroupID: groupID,
		User:    senderStr,
		BotLID:  botLID,
	}

	// آٹو کلین اپ (2 منٹ بعد خود ہی میموری سے غائب)
	go func() {
		time.Sleep(2 * time.Minute)
		delete(setupMap, senderStr)
	}()

	// 5️⃣ پریمیم مینیو رسپانس
	title := strings.ToUpper(secType)
	msg := fmt.Sprintf(`╔════════════════╗
║ 🛡️ %s (1/2)
╠════════════════╣
║ 📍 Group: %s
║ 🛠️ Admin: Verified
╠════════════════╣
║ Allow Admins?
║ 1️⃣ YES (No Kick)
║ 2️⃣ NO (Strict)
║
║ ⏱️ Timeout: 2 min
╚════════════════╝`, title, groupID[:10]+"...")

	replyMessage(client, v, msg)
}

func handleSetupResponse(client *whatsmeow.Client, v *events.Message, state *SetupState) {
	// ✅ ONLY respond to the same user who started setup
	if v.Info.Sender.String() != state.User {
		return
	}

	txt := strings.TrimSpace(getText(v.Message))
	s := getGroupSettings(state.GroupID)

	if state.Stage == 1 {
		if txt == "1" {
			s.AntilinkAdmin = true
		} else if txt == "2" {
			s.AntilinkAdmin = false
		} else {
			msg := `╔════════════════╗
║ ❌ INVALID
╠════════════════╣
║ Reply: 1 or 2
╚════════════════╝`
			replyMessage(client, v, msg)
			return
		}
		state.Stage = 2

		msg := fmt.Sprintf(`╔════════════════╗
║ ⚡ %s (2/2)
╠════════════════╣
║ Choose Action:
║ 1️⃣ DELETE ONLY
║ 2️⃣ DELETE + KICK
║ 3️⃣ DELETE + WARN
╚════════════════╝`, strings.ToUpper(state.Type))

		replyMessage(client, v, msg)
		return
	}

	if state.Stage == 2 {
		var actionText string
		switch txt {
		case "1":
			s.AntilinkAction = "delete"
			actionText = "Delete Only"
		case "2":
			s.AntilinkAction = "deletekick"
			actionText = "Delete + Kick"
		case "3":
			s.AntilinkAction = "deletewarn"
			actionText = "Delete + Warn"
		default:
			msg := `╔════════════════╗
║ ❌ INVALID
╠════════════════╣
║ Reply: 1, 2, 3
╚════════════════╝`
			replyMessage(client, v, msg)
			return
		}

		switch state.Type {
		case "antilink":
			s.Antilink = true
		case "antipic":
			s.AntiPic = true
		case "antivideo":
			s.AntiVideo = true
		case "antisticker":
			s.AntiSticker = true
		}

		saveGroupSettings(s)
		delete(setupMap, state.User)

		adminAllow := "YES ✅"
		if !s.AntilinkAdmin {
			adminAllow = "NO ❌"
		}

		msg := fmt.Sprintf(`╔════════════════╗
║ ✅ %s ENABLED
╠════════════════╣
║ Feature: %s
║ Admin: %s
║ Action: %s
╚════════════════╝`,
			strings.ToUpper(state.Type),
			strings.ToUpper(state.Type),
			adminAllow,
			actionText)

		replyMessage(client, v, msg)
	}
}

func handleGroupEvents(client *whatsmeow.Client, evt interface{}) {
	switch v := evt.(type) {
	case *events.GroupInfo:
		handleGroupInfoChange(client, v)
	}
}

func handleGroupInfoChange(client *whatsmeow.Client, v *events.GroupInfo) {
	if v.JID.IsEmpty() {
		return
	}

	// ✅ کک یا لیو (Leave/Kick) ایونٹ
	if v.Leave != nil && len(v.Leave) > 0 {
		for _, left := range v.Leave {
			sender := v.Sender // ایکشن لینے والا (ایڈمن یا خود ممبر)
			leftStr := left.String()
			senderStr := sender.String()

			// اگر سینڈر اور لیفٹ ممبر ایک ہی ہیں، تو یہ MANUAL LEAVE ہے
			if sender.User == left.User {
				msg := fmt.Sprintf(`╔════════════════╗
║ 👋 MEMBER LEFT
╠════════════════╣
║ 👤 User: @%s
║ 📉 Status: Self Leave
╚════════════════╝`, left.User)

				client.SendMessage(context.Background(), v.JID, &waProto.Message{
					ExtendedTextMessage: &waProto.ExtendedTextMessage{
						Text: proto.String(msg),
						ContextInfo: &waProto.ContextInfo{
							MentionedJID: []string{leftStr},
						},
					},
				})
			} else {
				// اگر سینڈر الگ ہے، تو یہ KICK ہے - اب ایڈمن کو منشن کرے گا
				msg := fmt.Sprintf(`╔════════════════╗
║ 👢 MEMBER KICKED
╠════════════════╣
║ 👤 User: @%s
║ 👮 By: @%s
╚════════════════╝`, left.User, sender.User)

				client.SendMessage(context.Background(), v.JID, &waProto.Message{
					ExtendedTextMessage: &waProto.ExtendedTextMessage{
						Text: proto.String(msg),
						ContextInfo: &waProto.ContextInfo{
							MentionedJID: []string{leftStr, senderStr}, // ممبر اور ایڈمن دونوں منشن
						},
					},
				})
			}
		}
	}

	// باقی فنکشنز (Promote, Demote, Join) کو بھی پریمیم لک میں برقرار رکھا ہے...
	
	// ✅ Promote event
	if v.Promote != nil && len(v.Promote) > 0 {
		for _, promoted := range v.Promote {
			msg := fmt.Sprintf(`╔════════════════╗
║ 👑 PROMOTED
╠════════════════╣
║ 👤 User: @%s
║ 🎉 Congrats!
╚════════════════╝`, promoted.User)

			promotedStr := promoted.String()
			client.SendMessage(context.Background(), v.JID, &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: proto.String(msg),
					ContextInfo: &waProto.ContextInfo{
						MentionedJID: []string{promotedStr},
					},
				},
			})
		}
	}

	// ✅ Demote event
	if v.Demote != nil && len(v.Demote) > 0 {
		for _, demoted := range v.Demote {
			msg := fmt.Sprintf(`╔════════════════╗
║ 👤 DEMOTED
╠════════════════╣
║ 👤 User: @%s
║ 📉 Rank Removed
╚════════════════╝`, demoted.User)

			demotedStr := demoted.String()
			client.SendMessage(context.Background(), v.JID, &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: proto.String(msg),
					ContextInfo: &waProto.ContextInfo{
						MentionedJID: []string{demotedStr},
					},
				},
			})
		}
	}

	// ✅ Join event
	if v.Join != nil && len(v.Join) > 0 {
		for _, joined := range v.Join {
			msg := fmt.Sprintf(`╔════════════════╗
║ 👋 JOINED
╠════════════════╣
║ 👤 User: @%s
║ 🎉 Welcome!
╚════════════════╝`, joined.User)

			joinedStr := joined.String()
			client.SendMessage(context.Background(), v.JID, &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: proto.String(msg),
					ContextInfo: &waProto.ContextInfo{
						MentionedJID: []string{joinedStr},
					},
				},
			})
		}
	}
}