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
)

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
		_, err := client.SendMessage(context.Background(), v.Info.Chat, client.BuildRevoke(v.Info.Chat, types.EmptyJID, v.Info.ID))
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
		
		client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: &msg,
				ContextInfo: &waProto.ContextInfo{
					MentionedJID: []string{v.Info.Sender.String()},
					StanzaID:     &v.Info.ID,
					Participant:  &v.Info.Sender.String(),
				},
			},
		})

	case "deletekick":
		// ✅ Delete for everyone
		_, err := client.SendMessage(context.Background(), v.Info.Chat, client.BuildRevoke(v.Info.Chat, types.EmptyJID, v.Info.ID))
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
		
		client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: &msg,
				ContextInfo: &waProto.ContextInfo{
					MentionedJID: []string{v.Info.Sender.String()},
				},
			},
		})

	case "deletewarn":
		senderKey := v.Info.Sender.String()
		s.Warnings[senderKey]++
		warnCount := s.Warnings[senderKey]

		// ✅ Delete for everyone
		_, err := client.SendMessage(context.Background(), v.Info.Chat, client.BuildRevoke(v.Info.Chat, types.EmptyJID, v.Info.ID))
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
			
			client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: &msg,
					ContextInfo: &waProto.ContextInfo{
						MentionedJID: []string{v.Info.Sender.String()},
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
			
			client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: &msg,
					ContextInfo: &waProto.ContextInfo{
						MentionedJID: []string{v.Info.Sender.String()},
						StanzaID:     &v.Info.ID,
						Participant:  &v.Info.Sender.String(),
					},
				},
			})
		}

		saveGroupSettings(s)
	}
}

func startSecuritySetup(client *whatsmeow.Client, v *events.Message, secType string) {
	if !v.Info.IsGroup {
		msg := `╔════════════════╗
║ ❌ GROUP ONLY
╠════════════════╣
║ Works in groups
╚════════════════╝`
		replyMessage(client, v, msg)
		return
	}

	if !isOwner(client, v.Info.Sender) {
		msg := `╔════════════════╗
║ 👑 OWNER ONLY
╠════════════════╣
║ ❌ YOU ARE NOT
║ THE OWNER
╚════════════════╝`
		replyMessage(client, v, msg)
		return
	}

	// ✅ Store with 2-minute timeout
	setupMap[v.Info.Sender.String()] = &SetupState{
		Type:    secType,
		Stage:   1,
		GroupID: v.Info.Chat.String(),
		User:    v.Info.Sender.String(),
	}

	// ✅ Auto-cleanup after 2 minutes
	go func() {
		time.Sleep(2 * time.Minute)
		delete(setupMap, v.Info.Sender.String())
	}()

	msg := fmt.Sprintf(`╔════════════════╗
║ 🛡️ %s (1/2)
╠════════════════╣
║ Allow Admins?
║ 1️⃣ YES
║ 2️⃣ NO
║
║ ⏱️ Timeout: 2 min
╚════════════════╝`, strings.ToUpper(secType))

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

	// ✅ Kick/Remove event - Only show if MANUAL leave
	if v.Leave != nil && len(v.Leave) > 0 {
		for _, left := range v.Leave {
			// Check if there's a kicker (removed by admin)
			if v.PrevParticipantVersionID != "" {
				// This was a KICK by admin - show kick message
				msg := fmt.Sprintf(`╔════════════════╗
║ 👢 MEMBER KICKED
╠════════════════╣
║ User: @%s
║ By: Admin
╚════════════════╝`, left.User)

				client.SendMessage(context.Background(), v.JID, &waProto.Message{
					ExtendedTextMessage: &waProto.ExtendedTextMessage{
						Text: &msg,
						ContextInfo: &waProto.ContextInfo{
							MentionedJID: []string{left.String()},
						},
					},
				})
			} else {
				// MANUAL leave - show leave message
				msg := fmt.Sprintf(`╔════════════════╗
║ 👋 MEMBER LEFT
╠════════════════╣
║ User: @%s
║ Left manually
╚════════════════╝`, left.User)

				client.SendMessage(context.Background(), v.JID, &waProto.Message{
					ExtendedTextMessage: &waProto.ExtendedTextMessage{
						Text: &msg,
						ContextInfo: &waProto.ContextInfo{
							MentionedJID: []string{left.String()},
						},
					},
				})
			}
		}
	}

	// ✅ Promote event
	if v.Promote != nil && len(v.Promote) > 0 {
		for _, promoted := range v.Promote {
			msg := fmt.Sprintf(`╔════════════════╗
║ 👑 PROMOTED
╠════════════════╣
║ User: @%s
║ 🎉 Congrats!
╚════════════════╝`, promoted.User)

			client.SendMessage(context.Background(), v.JID, &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: &msg,
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
║ User: @%s
║ 📉 Removed
╚════════════════╝`, demoted.User)

			client.SendMessage(context.Background(), v.JID, &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: &msg,
					ContextInfo: &waProto.ContextInfo{
						MentionedJID: []string{demoted.String()},
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
║ User: @%s
║ 🎉 Welcome!
╚════════════════╝`, joined.User)

			client.SendMessage(context.Background(), v.JID, &waProto.Message{
				ExtendedTextMessage: &waProto.ExtendedTextMessage{
					Text: &msg,
					ContextInfo: &waProto.ContextInfo{
						MentionedJID: []string{joined.String()},
					},
				},
			})
		}
	}
}