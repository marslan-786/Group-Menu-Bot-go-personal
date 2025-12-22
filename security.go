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
func onResponse(client *whatsmeow.Client, v *events.Message, choice string) {
	senderID := v.Info.Sender.String()
	state, exists := setupMap[senderID]

	// 1. کیا یہ بندہ سیٹ اپ موڈ میں ہے؟
	if !exists { return }

	// 2. کیا اس نے میسج کو ریپلائی (Quote) کیا ہے؟
	if v.Message.GetExtendedTextMessage().GetContextInfo() == nil {
		return // اگر ریپلائی نہیں ہے تو خاموش رہے
	}

	// 3. کیا ریپلائی اسی بوٹ کے میسج کو کیا گیا ہے؟
	quotedID := v.Message.ExtendedTextMessage.ContextInfo.GetStanzaID() // ✅ Fixed: ID caps mein
	if quotedID != state.BotMsgID {
		return // اگر کسی اور کے میسج کو ریپلائی کیا تو اگنور کریں
	}

	// 4. اگر سب ٹھیک ہے تو ریڈیس میں سیو کریں
	key := fmt.Sprintf("group:sec:%s:%s:%s", state.BotLID, state.GroupID, state.Type)
	rdb.Set(context.Background(), key, choice, 0)

	// اگلا مینیو دکھائیں یا ختم کریں
	replyMessage(client, v, "✅ Setting Saved Successfully!")
	delete(setupMap, senderID)
}

func startSecuritySetup(client *whatsmeow.Client, v *events.Message, secType string) {
	if !v.Info.IsGroup {
		replyMessage(client, v, "╔════════════════╗\n║ ❌ GROUP ONLY\n╚════════════════╝")
		return
	}

	// ایڈمن چیک (وہی ڈیزائن)
	isAdmin := false
	groupInfo, _ := client.GetGroupInfo(context.Background(), v.Info.Chat)
	if groupInfo != nil {
		for _, p := range groupInfo.Participants {
			if p.JID.User == v.Info.Sender.User && (p.IsAdmin || p.IsSuperAdmin) {
				isAdmin = true; break
			}
		}
	}
	if !isAdmin && !isOwner(client, v.Info.Sender) {
		replyMessage(client, v, "╔════════════════╗\n║ 👮 ADMIN ONLY\n╚════════════════╝")
		return
	}

	// 🛠️ آئی ڈی کلیننگ (بہت ضروری)
	botLID := getBotLIDFromDB(client)
	cleanSender := v.Info.Sender.ToBare().String() // ✅ فالتو ڈیوائس آئی ڈی ختم
	groupID := v.Info.Chat.String()

	// یونیک کی بنائیں
	mapKey := fmt.Sprintf("%s:%s", botLID, cleanSender)

	msgText := fmt.Sprintf(`╔════════════════╗
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
╚════════════════╝`, strings.ToUpper(secType), groupID[:10]+"...")

	// کارڈ بھیجیں اور اس کی آئی ڈی کیش کریں
	resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(msgText)},
	})

	if err != nil { return }

	// 💾 کلین ڈیٹا محفوظ کریں
	setupMap[mapKey] = &SetupState{
		Type:     secType,
		Stage:    1,
		GroupID:  groupID,
		User:     cleanSender, // کلین آئی ڈی
		BotLID:   botLID,
		BotMsgID: resp.ID,     // کارڈ آئی ڈی
	}

	go func() {
		time.Sleep(2 * time.Minute)
		delete(setupMap, mapKey)
	}()
}

func handleSetupResponse(client *whatsmeow.Client, v *events.Message) {
	// 1. میسج کا ڈیٹا اور کلین آئی ڈی لیں
	cleanSender := v.Info.Sender.ToBare().String()
	botLID := getBotLIDFromDB(client)
	mapKey := fmt.Sprintf("%s:%s", botLID, cleanSender)

	// 2. چیک کریں کیا اس کلین آئی ڈی کا کوئی سیشن ہے؟
	state, exists := setupMap[mapKey]
	if !exists { return }

	// 3. کارڈ ریپلائی ویریفیکیشن
	extMsg := v.Message.GetExtendedTextMessage()
	if extMsg == nil || extMsg.ContextInfo == nil || extMsg.ContextInfo.GetStanzaID() != state.BotMsgID {
		return // غلط ریپلائی یا رینڈم میسج پر خاموشی
	}

	txt := strings.TrimSpace(getText(v.Message))
	s := getGroupSettings(state.GroupID)

	// --- اسٹیج 1 ---
	if state.Stage == 1 {
		if txt == "1" { s.AntilinkAdmin = true } else if txt == "2" { s.AntilinkAdmin = false } else { return }
		
		state.Stage = 2
		nextMsg := fmt.Sprintf(`╔════════════════╗
║ ⚡ %s (2/2)
╠════════════════╣
║ Choose Action:
║ 1️⃣ DELETE ONLY
║ 2️⃣ DELETE + KICK
║ 3️⃣ DELETE + WARN
╚════════════════╝`, strings.ToUpper(state.Type))

		// اگلے کارڈ کی آئی ڈی اپڈیٹ کریں تاکہ اگلا ریپلائی بھی ٹریک ہو
		resp, _ := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(nextMsg)},
		})
		state.BotMsgID = resp.ID 
		return
	}

	// --- اسٹیج 2 ---
	if state.Stage == 2 {
		var actionText string
		switch txt {
		case "1": s.AntilinkAction = "delete"; actionText = "Delete Only"
		case "2": s.AntilinkAction = "deletekick"; actionText = "Delete + Kick"
		case "3": s.AntilinkAction = "deletewarn"; actionText = "Delete + Warn"
		default: return
		}

		// فیچر اپڈیٹ کریں
		switch state.Type {
		case "antilink": s.Antilink = true
		case "antipic": s.AntiPic = true
		case "antivideo": s.AntiVideo = true
		case "antisticker": s.AntiSticker = true
		}

		saveGroupSettings(s)
		delete(setupMap, mapKey) // کام ختم، میموری صاف

		adminAllow := "YES ✅"; if !s.AntilinkAdmin { adminAllow = "NO ❌" }
		finalMsg := fmt.Sprintf(`╔════════════════╗
║ ✅ %s ENABLED
╠════════════════╣
║ Admin Bypass: %s
║ Action: %s
╚════════════════╝`, strings.ToUpper(state.Type), adminAllow, actionText)

		replyMessage(client, v, finalMsg)
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