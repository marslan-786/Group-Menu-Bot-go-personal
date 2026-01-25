package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"
	"bytes"
	"strconv"
	"path/filepath"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

// 🛡️ گلوبل اسٹرکچرز


// اگر types.go میں TTState موجود ہے تو اسے یہاں سے ہٹا دیں

var ttCache = make(map[string]TTState)

// 💎 پریمیم کارڈ میکر (ہیلپر)
func sendPremiumCard(client *whatsmeow.Client, v *events.Message, title, site, info string) {
	card := fmt.Sprintf(`╔══════════════════════╗
║ ✨ %s DOWNLOADER
╠══════════════════════╣
║ 📝 Title: %s
║ 🌐 Site: %s
╠══════════════════════╣
║ ⏳ Status: Processing...
╚══════════════════════╝
%s`, strings.ToUpper(site), title, site, info)
	replyMessage(client, v, card)
}
// 📦 ڈاؤنلوڈ کا رزلٹ سٹور کرنے کے لیے سٹرکچر

// ✅ Fixed: Struct کا نام اب DLResult ہے تاکہ نیچے کوڈ سے میچ کرے
type DLResult struct {
	Path  string
	Title string
	Size  int64
	Mime  string
	Err   error
}
// کانسٹنٹ ویلیو: 1.5 جی بی (MB میں)
const MaxWhatsAppSizeMB = 1500.0

func downloadAndSend(client *whatsmeow.Client, v *events.Message, ytUrl, mode string, optionalFormat ...string) {
	// 🧹 0️⃣ DISK CLEANUP (AUTO-WIPE)
	// ہر بار کمانڈ چلنے پر یہ چیک کرے گا کہ کوئی بھی پرانی فائل (جو 5 منٹ سے زیادہ پرانی ہو) اسے اڑا دے۔
	go func() {
		files, _ := filepath.Glob("*.*") // تمام فائلز چیک کریں
		for _, f := range files {
			if strings.HasSuffix(f, ".mp4") || strings.HasSuffix(f, ".mp3") || strings.HasPrefix(f, "temp_") {
				info, err := os.Stat(f)
				if err == nil && time.Since(info.ModTime()) > 5*time.Minute {
					fmt.Printf("🧹 [CLEANUP] Deleting old file: %s\n", f)
					os.Remove(f)
				}
			}
		}
	}()

	// 1️⃣ صارف کو بتائیں
	react(client, v.Info.Chat, v.Info.ID, "⬇️")
	statusMsgID := replyMessage(client, v, "⏳ *Downloading Media...* Please wait.")

	// 2️⃣ ٹائٹل فیچ کریں
	cmdTitle := exec.Command("yt-dlp", "--get-title", "--no-playlist", ytUrl)
	titleOut, _ := cmdTitle.Output()

	cleanTitle := "Media_File"
	if len(titleOut) > 0 {
		cleanTitle = strings.TrimSpace(string(titleOut))
		// نام صاف کریں تاکہ ایرر نہ آئے
		cleanTitle = strings.Map(func(r rune) rune {
			if strings.ContainsRune(`/\?%*:|"<>`, r) {
				return '-'
			}
			return r
		}, cleanTitle)
	}

	tempFileName := fmt.Sprintf("temp_%d.mp4", time.Now().UnixNano())
	
	// 🔥 Playability Fix: زبردستی H.264 فارمیٹ (جو واٹس ایپ پر 100٪ چلتا ہے)
	formatArg := "bestvideo[ext=mp4][vcodec^=avc]+bestaudio[ext=m4a]/best[ext=mp4]/best"
	if mode == "audio" {
		tempFileName = strings.Replace(tempFileName, ".mp4", ".mp3", 1)
		formatArg = "bestaudio" // آڈیو کے لیے الگ
	}

	args := []string{
		"--no-playlist", 
		"-f", formatArg, 
		"--merge-output-format", "mp4",
		"--force-ipv4", 
		"-o", tempFileName, 
		ytUrl,
	}

	if mode == "audio" {
		args = []string{"--no-playlist", "-f", "bestaudio", "--extract-audio", "--audio-format", "mp3", "-o", tempFileName, ytUrl}
	}

	// 3️⃣ ڈاؤنلوڈ شروع
	fmt.Printf("🛠️ [CMD] Downloading: %s\n", cleanTitle)
	cmd := exec.Command("yt-dlp", args...)
	cmd.Stderr = os.Stderr 
	err := cmd.Run()

	if err != nil {
		fmt.Println("❌ Download Error:", err)
		client.SendMessage(context.Background(), v.Info.Chat, &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text:      proto.String("❌ Download Failed!"),
				ContextInfo: &waE2E.ContextInfo{StanzaID: proto.String(statusMsgID)},
			},
		})
		return
	}

	// فائل کا اصلی نام اور سائز
	finalExt := ".mp4"
	if mode == "audio" { finalExt = ".mp3" }
	finalPath := cleanTitle + finalExt
	os.Rename(tempFileName, finalPath)

	info, _ := os.Stat(finalPath)
	fileSize := info.Size()
	fileSizeMB := float64(fileSize) / (1024 * 1024)

	// 4️⃣ مینیو دکھائیں
	card := fmt.Sprintf(`╔══════════════════════╗
║ ✅ DOWNLOAD COMPLETE
╠══════════════════════╣
║ 📝 File: %s
║ 📦 Size: %.2f MB
╠══════════════════════╣
║ ⚡ Select Action:
╚══════════════════════╝

1️⃣ Send to WhatsApp
2️⃣ Upload to Jazz Drive  ☁️

_(Default: WhatsApp)_`, cleanTitle, fileSizeMB)

	replyMessage(client, v, card)

	// یوزر کا جواب
	senderID := v.Info.Sender.ToNonAD().String()
	userChoice, success := WaitForUserReply(senderID, 300*time.Second)

	// ====================================================
	// 🚦 DECISION LOGIC
	// ====================================================

	// --- OPTION 1: WHATSAPP (SPLIT IF NEEDED) ---
	if !success || strings.TrimSpace(userChoice) == "1" {
		react(client, v.Info.Chat, v.Info.ID, "📤")

		// چیک کریں اگر فائل 1.5GB (MaxWhatsAppSizeMB) سے بڑی ہے
		if fileSizeMB > MaxWhatsAppSizeMB && mode != "audio" {
			replyMessage(client, v, fmt.Sprintf("⚠️ *File is large (%.2f GB).* Wait A few minutes", fileSizeMB/1024))
			
			// 🔥 1.5GB Split Function Call
			parts, err := splitVideoSmart(finalPath, MaxWhatsAppSizeMB) 
			if err != nil {
				replyMessage(client, v, "❌ Error splitting. Sending original (might fail).")
				uploadToWhatsApp(client, v, DLResult{Path: finalPath, Title: cleanTitle, Size: fileSize, Mime: mode}, mode)
			} else {
				// پارٹس بھیجیں
				for i, partPath := range parts {
					partTitle := fmt.Sprintf("%s (Part %d/%d)", cleanTitle, i+1, len(parts))
					pInfo, _ := os.Stat(partPath)
					
					fmt.Printf("📤 Sending Part %d: %s\n", i+1, partPath)
					uploadToWhatsApp(client, v, DLResult{Path: partPath, Title: partTitle, Size: pInfo.Size(), Mime: mode}, mode)
					
					os.Remove(partPath) 
					time.Sleep(3 * time.Second)
				}
				replyMessage(client, v, "✅ All parts sent!")
			}
		} else {
			// نارمل سینڈ
			uploadToWhatsApp(client, v, DLResult{Path: finalPath, Title: cleanTitle, Size: fileSize, Mime: mode}, mode)
		}
		os.Remove(finalPath)

	} else if strings.TrimSpace(userChoice) == "2" {
		// ==================================================
		// ☁️ OPTION 2: JAZZ DRIVE (Robust Retry Logic)
		// ==================================================
		react(client, v.Info.Chat, v.Info.ID, "☁️")
		
		// 1. Ask for Number
		replyMessage(client, v, "📱 *Enter Jazz Number (03XXXXXXXXX):*\n_(You have 2 mins)_")

		phone, ok := WaitForUserReply(senderID, 120*time.Second)
		if !ok || phone == "" {
			replyMessage(client, v, "❌ Timeout. Sending to WhatsApp instead.")
			uploadToWhatsApp(client, v, DLResult{Path: finalPath, Title: cleanTitle, Size: fileSize, Mime: mode}, mode)
			os.Remove(finalPath)
			return
		}

		// 2. Send OTP
		userID := fmt.Sprintf("user_%d", time.Now().Unix())
		replyMessage(client, v, "🔄 Sending OTP...")

		if jazzGenOTP(userID, phone) {
			replyMessage(client, v, "🔑 *OTP Sent! Enter 4-digit code:*")
			
			// 🔥 RETRY LOOP (2 Attempts)
			otpVerified := false
			for attempt := 1; attempt <= 2; attempt++ {
				otp, ok := WaitForUserReply(senderID, 120*time.Second)
				if !ok || otp == "" {
					break // Timeout will go to fallback
				}

				replyMessage(client, v, "🔐 Verifying...")

				if jazzVerifyOTP(userID, otp) {
					otpVerified = true
					
					// Upload to Drive
					replyMessage(client, v, "☁️ *Uploading to Jazz Drive...*\n_(This may take time)_")
					link, err := jazzUploadFile(userID, finalPath)
					
					if err == nil {
						finalText := fmt.Sprintf("🎉 *Upload Complete!*\n\n📂 *File:* %s\n📦 *Size:* %.2f MB\n🔗 *Link:* %s",
							cleanTitle, fileSizeMB, link)
						replyMessage(client, v, finalText)
					} else {
						replyMessage(client, v, "❌ Upload Failed: "+err.Error())
						// اگر اپلوڈ فیل ہو تو کیا واٹس ایپ پر بھیجیں؟ (User choice, currently just showing error)
					}
					break // Loop ختم، کام ہو گیا
				} else {
					if attempt < 2 {
						replyMessage(client, v, "❌ Invalid OTP! *Try Again (Last Chance):*")
					}
				}
			}

			// 🔥 FALLBACK: اگر 2 بار غلط ہوا یا ٹائم آؤٹ ہوا
			if !otpVerified {
				replyMessage(client, v, "❌ OTP Failed/Timeout. Sending to WhatsApp to save data...")
				uploadToWhatsApp(client, v, DLResult{Path: finalPath, Title: cleanTitle, Size: fileSize, Mime: mode}, mode)
			}

		} else {
			replyMessage(client, v, "❌ Failed to send OTP. Check number.")
			uploadToWhatsApp(client, v, DLResult{Path: finalPath, Title: cleanTitle, Size: fileSize, Mime: mode}, mode)
		}
		
		// 🧹 لازمی صفائی (چاہے اپلوڈ ہوا ہو یا واٹس ایپ پر گیا ہو)
		os.Remove(finalPath)

	} else {
		replyMessage(client, v, "❌ Invalid Option. Sending file here...")
		uploadToWhatsApp(client, v, DLResult{Path: finalPath, Title: cleanTitle, Size: fileSize, Mime: mode}, mode)
		os.Remove(finalPath)
	}
}



// 🔥 SMART SPLIT FUNCTION (Time-based calculation for playability)
// یہ فنکشن فائل سائز کی بجائے ٹائم کیلکولیٹ کر کے کاٹے گا تاکہ ویڈیو پلے ہو سکے
func splitVideoSmart(inputPath string, targetMB float64) ([]string, error) {
	// 1. ویڈیو کی کل Duration (Seconds) حاصل کریں
	cmd := exec.Command("ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", inputPath)
	out, err := cmd.Output()
	if err != nil { return nil, err }
	
	durationSec, _ := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	
	// 2. فائل کا سائز دیکھیں
	info, _ := os.Stat(inputPath)
	totalSizeMB := float64(info.Size()) / (1024 * 1024)
	
	// 3. کیلکولیشن: اگر 5GB کی فائل 2 گھنٹے کی ہے، تو 1.5GB کتنے منٹ کی ہوگی؟
	// Formula: (TargetMB / TotalMB) * TotalDuration
	chunkDuration := (targetMB / totalSizeMB) * durationSec
	
	// تھوڑا سا بفر رکھیں (Safe margin 5%)
	chunkDuration = chunkDuration * 0.95

	fmt.Printf("✂️ Splitting video. Total: %.2f MB, Target: %.2f MB, Chunk Time: %.0f sec\n", totalSizeMB, targetMB, chunkDuration)

	// 4. FFmpeg Segment Command
	// -segment_time: ہر ٹکڑا کتنے سیکنڈ کا ہو
	// -reset_timestamps 1: یہ بہت ضروری ہے تاکہ ہر پارٹ شروع سے پلے ہو (00:00 سے)
	outputPattern := strings.Replace(inputPath, ".mp4", "_part%03d.mp4", 1)
	
	splitCmd := exec.Command("ffmpeg", 
		"-i", inputPath, 
		"-c", "copy",          // Re-encode نہیں کریں گے (Fastest)
		"-map", "0", 
		"-f", "segment", 
		"-segment_time", fmt.Sprintf("%.0f", chunkDuration), 
		"-reset_timestamps", "1", 
		outputPattern,
	)

	if err := splitCmd.Run(); err != nil {
		return nil, err
	}

	// 5. پارٹس کی لسٹ واپس کریں
	baseName := strings.TrimSuffix(outputPattern, "%03d.mp4")
	files, _ := filepath.Glob(baseName + "*")
	return files, nil
}

// ---------------------------------------------------------
// 📤 HELPER: Upload To WhatsApp (Updated with filepath)
// ---------------------------------------------------------
func uploadToWhatsApp(client *whatsmeow.Client, v *events.Message, res DLResult, mode string) {
	// فائل سائز چیک (1.5GB Split Logic)
	const SplitLimit = 1500 * 1024 * 1024
	if res.Size > SplitLimit {
		replyMessage(client, v, fmt.Sprintf("⚠️ *File is Huge!* (%.2f GB)\n✂️ Splitting for WhatsApp...", float64(res.Size)/(1024*1024*1024)))
		splitAndSend(client, v, res.Path, res.Path, SplitLimit)
		return
	}

	fileData, err := os.ReadFile(res.Path)
	if err != nil {
		fmt.Println("❌ Read File Error:", err)
		return
	}

	var mType whatsmeow.MediaType
	// 90MB سے بڑی فائل ہمیشہ ڈاکومنٹ بنے گی
	forceDoc := res.Size > 90*1024*1024

	if mode == "audio" || forceDoc {
		mType = whatsmeow.MediaDocument
	} else {
		mType = whatsmeow.MediaVideo
	}

	// اپلوڈ ٹائم آؤٹ
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()

	up, err := client.Upload(ctx, fileData, mType)
	if err != nil {
		replyMessage(client, v, "❌ WhatsApp Upload Failed (Network/Size Issue).")
		return
	}

	var finalMsg waProto.Message

	if mType == whatsmeow.MediaDocument {
		mime := "application/octet-stream"
		if mode == "video" {
			mime = "video/mp4"
		}
		if mode == "audio" {
			mime = "audio/mpeg"
		}

		finalMsg.DocumentMessage = &waProto.DocumentMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String(mime),
			FileName:      proto.String(filepath.Base(res.Path)), // ✅ Filepath Used Correctly
			FileLength:    proto.Uint64(uint64(res.Size)),
			Caption:       proto.String("✅ " + res.Title),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
		}
	} else {
		finalMsg.VideoMessage = &waProto.VideoMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("video/mp4"),
			Caption:       proto.String("✅ " + res.Title),
			FileLength:    proto.Uint64(uint64(res.Size)),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
		}
	}

	client.SendMessage(context.Background(), v.Info.Chat, &finalMsg)
	react(client, v.Info.Chat, v.Info.ID, "✅")
}

// ------------------- تمام ہینڈلرز (بھرے ہوئے!) -------------------

// 📱 سوشل میڈیا
func handleFacebook(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Facebook Video", "Facebook", "🎥 Extracting High Quality Content...")
	go downloadAndSend(client, v, url, "video")
}

func handleInstagram(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Instagram Reel", "Instagram", "📸 Capturing Media...")
	go downloadAndSend(client, v, url, "video")
}

func handleTikTok(client *whatsmeow.Client, v *events.Message, urlStr string) {
	if urlStr == "" { return }
	react(client, v.Info.Chat, v.Info.ID, "🎵")
	
	apiUrl := "https://www.tikwm.com/api/?url=" + url.QueryEscape(urlStr)
	var r struct { 
		Code int `json:"code"`
		Data struct { Play, Music, Title string; Size uint64 } `json:"data"` 
	}
	getJson(apiUrl, &r)

	if r.Code == 0 {
		// کیش میں ڈیٹا محفوظ کریں
		sender := v.Info.Sender.ToNonAD().String() // ✅ بہتر جے آئی ڈی ہینڈلنگ
		ttCache[sender] = TTState{
			PlayURL: r.Data.Play, 
			MusicURL: r.Data.Music, 
			Title: r.Data.Title, 
			Size: int64(r.Data.Size),
		}

		// 👑 پریمیم ورٹیکل مینیو
		menuText := fmt.Sprintf("📝 *Title:* %s\n\n", r.Data.Title)
		menuText += "🔢 *Reply with a number:*\n\n"
		menuText += "  【 1 】 🎬 *Video (No WM)*\n"
		menuText += "  【 2 】 🎵 *Audio (MP3)*\n"
		menuText += "  【 3 】 📄 *Full Info*\n\n"
		menuText += "⏳ *Timeout:* 2 Minutes"

		sendPremiumCard(client, v, "TikTok Downloader", "TikWM Engine", menuText)
	} else {
		replyMessage(client, v, "❌ *Error:* Could not fetch TikTok data.")
	}
}

// ❌ پرانی لائن (جو ۳ پیرامیٹرز لے رہی تھی):
// func handleTikTokReply(client *whatsmeow.Client, v *events.Message, input string)
func sendAudio(client *whatsmeow.Client, v *events.Message, audioURL string) {
	// 1️⃣ آڈیو ڈاؤن لوڈ کرنا
	resp, err := http.Get(audioURL)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return
	}

	// 2️⃣ واٹس ایپ پر اپلوڈ کرنا
	up, err := client.Upload(context.Background(), data, whatsmeow.MediaAudio)
	if err != nil {
		return
	}

	// 3️⃣ اوریجنل آڈیو بھیجنا (بطور میوزک فائل)
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("audio/mpeg"), // ✅ میوزک فارمیٹ
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
			PTT:           proto.Bool(false), // ❌ وائس نوٹ (PTT) بند کر دیا
		},
	})
}
// ✅ نئی اور صحیح لائن (جس میں senderID شامل ہے):
// ✅ فنکشن کے ہیڈر میں پیرامیٹرز بالکل صحیح ہیں
func handleTikTokReply(client *whatsmeow.Client, v *events.Message, input string, senderID string) {
	// 1️⃣ کیش سے ڈیٹا نکالیں
	state, exists := ttCache[senderID]
	if !exists { return }

	// 🛠️ فکس ۱: یہاں 'senderID :=' نہیں کرنا، کیونکہ وہ اوپر پیرامیٹر میں موجود ہے
	// اگر دوبارہ نکالنا بھی ہو تو صرف '=' استعمال کریں (بغیر سیمی کولن کے)
	senderID = v.Info.Sender.ToNonAD().String() 

	input = strings.TrimSpace(input)

	switch input {
	case "1":
		react(client, v.Info.Chat, v.Info.ID, "🎬")
		sendVideo(client, v, state.PlayURL, "✅ *TikTok Video Generated*")
		delete(ttCache, senderID) 

	case "2":
		react(client, v.Info.Chat, v.Info.ID, "🎵")
		// 🛠️ فکس ۲: یہاں 'v' مسنگ تھا، اب ۳ پیرامیٹرز پورے کر دیے ہیں
		sendAudio(client, v, state.MusicURL)  
		delete(ttCache, senderID)

	case "3":
		infoMsg := fmt.Sprintf("╔═══════════════════╗\n"+
			"║      ✨ TIKTOK INFO ✨     ║\n"+
			"╠═══════════════════╣\n"+
			"║ 📝 Title: %s\n"+
			"║ 📊 Size: %.2f MB\n"+
			"╚═══════════════════╝", state.Title, float64(state.Size)/(1024*1024))
		replyMessage(client, v, infoMsg)
		delete(ttCache, senderID)
	}
}

func handleTwitter(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "X Video", "Twitter/X", "🐦 Speeding through X servers...")
	go downloadAndSend(client, v, url, "video")
}

func handlePinterest(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Pin Media", "Pinterest", "📌 Extracting Media Asset...")
	go downloadAndSend(client, v, url, "video")
}

func handleThreads(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Threads Clip", "Threads", "🧵 Processing Thread...")
	go downloadAndSend(client, v, url, "video")
}

func handleSnapchat(client *whatsmeow.Client, v *events.Message, url string) {
	if url == "" { return }
	react(client, v.Info.Chat, v.Info.ID, "👻")
	sendPremiumCard(client, v, "Snapchat", "Snap-Engine", "👻 Capturing Snap Spotlight... Please wait.")
	
	// سنیپ چیٹ کے لیے ہم مخصوص کوالٹی پیرامیٹرز استعمال کریں گے
	go downloadAndSend(client, v, url, "video")
}

func handleReddit(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Reddit Post", "Reddit", "👽 Merging Audio & Video...")
	go downloadAndSend(client, v, url, "video")
}

// 📺 ویڈیو اور اسٹریمز
func handleYoutubeVideo(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "YouTube HD", "YouTube", "🎬 Fetching 720p/1080p Stream...")
	go downloadAndSend(client, v, url, "video")
}

func handleYoutubeAudio(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "YouTube MP3", "YouTube", "🎶 Converting to 320kbps Audio...")
	go downloadAndSend(client, v, url, "audio")
}

func handleTwitch(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Twitch Clip", "Twitch", "🎮 Grabbing Stream Moment...")
	go downloadAndSend(client, v, url, "video")
}

func handleDailyMotion(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "DailyMotion", "DailyMotion", "📺 Packing Video Stream...")
	go downloadAndSend(client, v, url, "video")
}

func handleVimeo(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Vimeo Pro", "Vimeo", "✨ Professional Extraction...")
	go downloadAndSend(client, v, url, "video")
}

func handleRumble(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Rumble Stream", "Rumble", "🥊 Fetching Rumble Media...")
	go downloadAndSend(client, v, url, "video")
}

func handleBilibili(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Anime Video", "Bilibili", "💮 Accessing Bilibili Nodes...")
	go downloadAndSend(client, v, url, "video")
}

func handleBitChute(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Alt Video", "BitChute", "🎞️ Extraction Started...")
	go downloadAndSend(client, v, url, "video")
}

// 🎵 میوزک پلیٹ فارمز
func handleSoundCloud(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Music Track", "SoundCloud", "🎧 Ripping HQ Audio...")
	go downloadAndSend(client, v, url, "audio")
}

func handleSpotify(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Spotify Track", "Spotify", "🎵 Extracting from Spotify...")
	go downloadAndSend(client, v, url, "audio")
}

func handleAppleMusic(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Apple Preview", "AppleMusic", "🎶 Grabbing High-Fi Clip...")
	go downloadAndSend(client, v, url, "audio")
}

func handleDeezer(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Deezer HQ", "Deezer", "🎼 Converting Track...")
	go downloadAndSend(client, v, url, "audio")
}

func handleTidal(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Tidal Master", "Tidal", "💎 Fetching Lossless Audio...")
	go downloadAndSend(client, v, url, "audio")
}

func handleMixcloud(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "DJ Mixset", "Mixcloud", "🎧 Extracting Long Set...")
	go downloadAndSend(client, v, url, "audio")
}

func handleNapster(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Legacy Track", "Napster", "🎶 Downloading Music...")
	go downloadAndSend(client, v, url, "audio")
}

func handleBandcamp(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Indie Music", "Bandcamp", "🎸 Grabbing Artist Track...")
	go downloadAndSend(client, v, url, "audio")
}

// 🖼️ میڈیا اثاثے
func handleImgur(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Imgur Media", "Imgur", "🖼️ Extracting Image/Video...")
	go downloadAndSend(client, v, url, "video")
}

func handleGiphy(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Animated GIF", "Giphy", "🎞️ Rendering GIF Stream...")
	go downloadAndSend(client, v, url, "video")
}

func handleFlickr(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "HQ Assets", "Flickr", "📸 Fetching Media...")
	go downloadAndSend(client, v, url, "video")
}

func handle9Gag(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Meme Video", "9Gag", "🤣 Grabbing Viral Content...")
	go downloadAndSend(client, v, url, "video")
}

func handleIfunny(client *whatsmeow.Client, v *events.Message, url string) {
	sendPremiumCard(client, v, "Funny Media", "iFunny", "🤡 Processing Meme...")
	go downloadAndSend(client, v, url, "video")
}

// 💻 ڈویلپر اور آرکائیو
func handleGithub(client *whatsmeow.Client, v *events.Message, urlStr string) {
	if urlStr == "" { return }
	
	// ✅ فکس: اگر لنک کے آخر میں .git ہو تو اسے صاف کریں
	urlStr = strings.TrimSuffix(urlStr, ".git")
	urlStr = strings.TrimSuffix(urlStr, "/")
	
	react(client, v.Info.Chat, v.Info.ID, "💻")
	sendPremiumCard(client, v, "Repo Source", "GitHub", "📁 Packing Repository ZIP...")

	zipURL := urlStr + "/zipball/HEAD"

	// ڈاؤن لوڈ لاجک
	resp, err := http.Get(zipURL)
	if err != nil || resp.StatusCode != 200 {
		replyMessage(client, v, "❌ *GitHub Error:* Repo not found. Ensure it is public.")
		return
	}
	defer resp.Body.Close()

	fileName := fmt.Sprintf("repo_%d.zip", time.Now().UnixNano())
	out, _ := os.Create(fileName)
	io.Copy(out, resp.Body)
	out.Close()

	fileData, _ := os.ReadFile(fileName)
	defer os.Remove(fileName)

	up, err := client.Upload(context.Background(), fileData, whatsmeow.MediaDocument)
	if err != nil { return }

	// ✅ فکسڈ میسج (MediaType کو IMAGE کر دیا ہے)
		client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
			DocumentMessage: &waProto.DocumentMessage{
				URL:           proto.String(up.URL),
				DirectPath:    proto.String(up.DirectPath),
				MediaKey:      up.MediaKey,
				Mimetype:      proto.String("application/octet-stream"),
				Title:         proto.String(fileName),
				FileName:      proto.String(fileName),
				FileLength:    proto.Uint64(uint64(len(fileData))),
				FileSHA256:    up.FileSHA256,
				FileEncSHA256: up.FileEncSHA256,
				ContextInfo: &waProto.ContextInfo{
					ExternalAdReply: &waProto.ContextInfo_ExternalAdReplyInfo{
						Title:     proto.String("Impossible Mega Engine"),
						Body:      proto.String("File: " + fileName),
						SourceURL: proto.String(urlStr),
						MediaType: waProto.ContextInfo_ExternalAdReplyInfo_IMAGE.Enum(), // 🛠️ فکس: یہاں IMAGE ہی چلے گا
					},
				},
			},
		})
	react(client, v.Info.Chat, v.Info.ID, "✅")
}

// 📺 یوٹیوب سرچ اور مینو (YTS)
func handleYTS(client *whatsmeow.Client, v *events.Message, query string) {
	if query == "" { return }
	react(client, v.Info.Chat, v.Info.ID, "🔍")
	fmt.Printf("🔍 [YTS START] Query: %s\n", query)

	myID := getCleanID(client.Store.ID.User)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// 🔥 FINAL FIX: --flat-playlist (This is the magic flag)
	// یہ ویڈیو کو لوڈ نہیں کرتا، صرف ٹائٹل اور آئی ڈی اٹھاتا ہے جو کہ بہت تیز اور بلاک پروف ہے۔
	cmd := exec.CommandContext(ctx, "yt-dlp", 
		"ytsearch5:"+query,
		"--flat-playlist",  // <--- یہ سب سے اہم ہے
		"--print", "%(title)s|||%(id)s",
		"--no-warnings",
		"--force-ipv4",
		"--extractor-args", "youtube:player_client=android", // Android Bypass
	)

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	fmt.Println("⏳ [YTS] Executing yt-dlp (Flat Playlist Mode)...")
	out, err := cmd.Output()

	if ctx.Err() == context.DeadlineExceeded {
		replyMessage(client, v, "⚠️ Search Timeout!")
		return
	}

	if err != nil {
		fmt.Printf("❌ [YTS FAIL] Error: %v\n⚠️ [STDERR]: %s\n", err, stderr.String())
		replyMessage(client, v, "❌ Search Error.")
		return
	}

	outputStr := strings.TrimSpace(string(out))
	lines := strings.Split(outputStr, "\n")
	
	// خالی رزلٹ چیک
	if len(lines) == 0 || outputStr == "" { 
		fmt.Println("⚠️ [YTS] No results found (Empty Output).")
		replyMessage(client, v, "❌ No results found. Try a different keyword.")
		return 
	}

	var results []YTSResult
	menuText := "╭─── 📺 *YOUTUBE SEARCH* ───╮\n│\n"
	
	count := 0
	for _, line := range lines {
		parts := strings.Split(line, "|||")
		if len(parts) < 2 { continue }

		title := strings.TrimSpace(parts[0])
		vidID := strings.TrimSpace(parts[1])
		
		results = append(results, YTSResult{Title: title, Url: "https://www.youtube.com/watch?v=" + vidID})
		
		count++
		menuText += fmt.Sprintf("📍 *[%d]* %s\n│ ┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈\n", count, title)
	}

	if count == 0 {
		replyMessage(client, v, "❌ Could not parse results.")
		return
	}

	menuText += "│\n╰────────────────────╯"

	resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(menuText)},
	})

	if err == nil {
		fmt.Printf("✅ [YTS SENT] Menu sent with %d results.\n", count)
		ytCache[resp.ID] = YTSession{Results: results, SenderID: v.Info.Sender.User, BotLID: myID}
		go func() { time.Sleep(2 * time.Minute); delete(ytCache, resp.ID) }()
	}
}



func handleYTDownloadMenu(client *whatsmeow.Client, v *events.Message, ytUrl string) {
	myID := getCleanID(client.Store.ID.User)
	senderLID := v.Info.Sender.User

	menu := `╔════════════════════╗
║    🎬 QUALITY SELECTOR 
╠════════════════════╣
║ 1️⃣ 144p  (Tiny)
║ 2️⃣ 240p  (Low)
║ 3️⃣ 360p  (Normal)
║ 4️⃣ 720p  (HD)
║ 5️⃣ 1080p (FHD)
║ 6️⃣ 4K    (Ultra)
║ 7️⃣ 8K    (Extreme)
║ 8️⃣ MP3   (Audio)
║
║ ⏳ Reply with number
╚════════════════════╝`

	resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(menu)},
	})

	if err == nil {
		// 💾 کیشے سیو کریں
		ytDownloadCache[resp.ID] = YTState{
			Url:      ytUrl,
			BotLID:   myID,
			SenderID: senderLID,
		}
		fmt.Printf("📂 [YT-MENU] Cached ID: %s for Bot: %s\n", resp.ID, myID)
		
		// 1 منٹ بعد صفائی
		go func() {
			time.Sleep(1 * time.Minute)
			delete(ytDownloadCache, resp.ID)
		}()
	}
}


func handleYTDownload(client *whatsmeow.Client, v *events.Message, ytUrl, choice string, isAudio bool) {
	// ⏳ ری ایکشن
	react(client, v.Info.Chat, v.Info.ID, "⏳")

	mode := "video"
	// ڈیفالٹ فارمیٹ (اگر کوئی غلط نمبر دبائے تو 720p آئے گا)
	format := "bestvideo[height<=720]+bestaudio/best"

	// اگر یوزر نے 8 دبایا ہے تو اسے آڈیو کر دیں
	if choice == "8" {
		isAudio = true
	}

	if isAudio {
		mode = "audio"
	} else {
		switch choice {
		case "1": // 144p
			format = "bestvideo[height<=144]+bestaudio/best"
		case "2": // 240p
			format = "bestvideo[height<=240]+bestaudio/best"
		case "3": // 360p
			format = "bestvideo[height<=360]+bestaudio/best"
		case "4": // 720p
			format = "bestvideo[height<=720]+bestaudio/best"
		case "5": // 1080p
			format = "bestvideo[height<=1080]+bestaudio/best"
		case "6": // 4K (2160p)
			format = "bestvideo[height<=2160]+bestaudio/best"
		case "7": // 8K (4320p)
			format = "bestvideo[height<=4320]+bestaudio/best"
		}
	}

	// 🚀 ڈاؤنلوڈ شروع کریں
	go downloadAndSend(client, v, ytUrl, mode, format)
}


// ------------------- مددگار فنکشنز (Helpers) -------------------

func getJson(url string, target interface{}) error {
	r, err := http.Get(url); if err != nil { return err }; defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(target)
}

func sendVideo(client *whatsmeow.Client, v *events.Message, videoURL, caption string) {
	go downloadAndSend(client, v, videoURL, "video")
}

func sendDocument(client *whatsmeow.Client, v *events.Message, docURL, name, mime string) {
	resp, err := http.Get(docURL); if err != nil { return }; defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	up, _ := client.Upload(context.Background(), data, whatsmeow.MediaDocument)
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		DocumentMessage: &waProto.DocumentMessage{
			URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey,
			Mimetype: proto.String(mime), FileName: proto.String(name), FileLength: proto.Uint64(uint64(len(data))),
		},
	})
}