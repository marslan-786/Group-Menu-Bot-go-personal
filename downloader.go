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

	"go.mau.fi/whatsmeow"
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



// 🚀 ہیوی ڈیوٹی میڈیا انجن (The Scientific Power)
// 🚀 ہیوی ڈیوٹی میڈیا انجن (Updated with Splitter & Real Names)
// 🚀 ہیوی ڈیوٹی میڈیا انجن (Updated: Global Jazz Drive + Background DL)
func downloadAndSend(client *whatsmeow.Client, v *events.Message, ytUrl, mode string, optionalFormat ...string) {
	// 1️⃣ ٹائٹل نکالیں (فوری طور پر)
	fmt.Println("🔍 Fetching Title...")
	cmdTitle := exec.Command("yt-dlp", "--get-title", "--no-playlist", ytUrl)
	titleOut, _ := cmdTitle.Output()
	
	cleanTitle := "Media_File"
	if len(titleOut) > 0 {
		cleanTitle = strings.TrimSpace(string(titleOut))
		cleanTitle = strings.ReplaceAll(cleanTitle, "/", "-")
		cleanTitle = strings.ReplaceAll(cleanTitle, "\\", "-")
		cleanTitle = strings.ReplaceAll(cleanTitle, "\"", "'")
	}

	// 2️⃣ مینو کارڈ بھیجیں
	card := fmt.Sprintf(`╔══════════════════════╗
║ ✨ %s DOWNLOADER
╠══════════════════════╣
║ 📝 Title: %s
║ 🌐 Source: External Link
╠══════════════════════╣
║ ⏳ Status: Downloading...
╚══════════════════════╝

*Choose Action:*
1️⃣ Send to WhatsApp (Default)
2️⃣ Upload to Jazz Drive ☁️

_(Auto-send in 1 min if no reply)_`, strings.ToUpper(mode), cleanTitle)

	replyMessage(client, v, card)

	// 3️⃣ بیک گراؤنڈ ڈاؤنلوڈ شروع کریں (چینل کے ذریعے)
	type DownloadResult struct {
		Path string
		Size int64
		Err  error
	}
	dlChan := make(chan DownloadResult, 1) // بفرڈ چینل تاکہ ڈیڈ لاک نہ ہو

	go func() {
		tempFileName := fmt.Sprintf("temp_%d", time.Now().UnixNano())
		formatArg := "bestvideo[ext=mp4]+bestaudio[ext=m4a]/best[ext=mp4]/best"
		if len(optionalFormat) > 0 && optionalFormat[0] != "" {
			formatArg = optionalFormat[0]
		}

		var args []string
		finalExt := ".mp4"

		if mode == "audio" {
			tempFileName += ".mp3"
			finalExt = ".mp3"
			args = []string{"--no-playlist", "-f", "bestaudio", "--extract-audio", "--audio-format", "mp3", "-o", tempFileName, ytUrl}
		} else {
			tempFileName += ".mp4"
			args = []string{"--no-playlist", "-f", formatArg, "--merge-output-format", "mp4", "-o", tempFileName, ytUrl}
		}

		fmt.Printf("🛠️ [BG] Downloading: %s\n", cleanTitle)
		cmd := exec.Command("yt-dlp", args...)
		err := cmd.Run() // Run wait کرتا ہے جب تک مکمل نہ ہو

		if err != nil {
			dlChan <- DownloadResult{Err: err}
			return
		}

		// نام تبدیل کریں
		finalPath := cleanTitle + finalExt
		os.Rename(tempFileName, finalPath)
		info, _ := os.Stat(finalPath)

		dlChan <- DownloadResult{Path: finalPath, Size: info.Size(), Err: nil}
	}()

	// 4️⃣ یوزر کے جواب کا انتظار کریں (60 سیکنڈ ٹائم آؤٹ)
	senderID := v.Info.Sender.ToNonAD().String()
	userChoice, timedOut := WaitForUserReply(senderID, 60*time.Second)

	// ====================================================
	// 🚦 DECISION LOGIC
	// ====================================================

	if timedOut || strings.TrimSpace(userChoice) == "1" {
		// --- OPTION 1: WHATSAPP (OR TIMEOUT) ---
		if timedOut {
			// اگر ٹائم آؤٹ ہوا تو مطلع کریں اور فائل بھیج دیں
			// replyMessage(client, v, "⌛ Timeout! Sending here...") 
			// (خاموشی سے بھیجنا بہتر ہے)
		} else {
			react(client, v.Info.Chat, v.Info.ID, "📤")
		}

		// ڈاؤنلوڈ ختم ہونے کا انتظار کریں
		res := <-dlChan
		if res.Err != nil {
			replyMessage(client, v, "❌ Download Failed.")
			return
		}
		defer os.Remove(res.Path) // واٹس ایپ پر بھیجنے کے بعد ڈیلیٹ

		// فائل سائز چیک (1.5GB Split Logic)
		const SplitLimit = 1500 * 1024 * 1024
		if res.Size > SplitLimit {
			replyMessage(client, v, fmt.Sprintf("⚠️ *File is Huge!* (%.2f GB)\n✂️ Splitting for WhatsApp...", float64(res.Size)/(1024*1024*1024)))
			splitAndSend(client, v, res.Path, res.Path, SplitLimit)
			return
		}

		// نارمل واٹس ایپ اپلوڈ
		fileData, err := os.ReadFile(res.Path)
		if err != nil { return }

		var mType whatsmeow.MediaType
		forceDoc := res.Size > 90*1024*1024 // 90MB Limit Logic

		if mode == "audio" || forceDoc {
			mType = whatsmeow.MediaDocument
		} else {
			mType = whatsmeow.MediaVideo
		}

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()

		up, err := client.Upload(ctx, fileData, mType)
		if err != nil {
			replyMessage(client, v, "❌ WhatsApp Upload Failed (Network).")
			return
		}

		var finalMsg waProto.Message
		if mType == whatsmeow.MediaDocument {
			mime := "application/octet-stream"
			if mode == "video" { mime = "video/mp4" }
			if mode == "audio" { mime = "audio/mpeg" }
			finalMsg.DocumentMessage = &waProto.DocumentMessage{
				URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey,
				Mimetype: proto.String(mime), FileName: proto.String(filepath.Base(res.Path)),
				FileLength: proto.Uint64(uint64(res.Size)), Caption: proto.String("✅ " + cleanTitle),
				FileSHA256: up.FileSHA256, FileEncSHA256: up.FileEncSHA256,
			}
		} else {
			finalMsg.VideoMessage = &waProto.VideoMessage{
				URL: proto.String(up.URL), DirectPath: proto.String(up.DirectPath), MediaKey: up.MediaKey,
				Mimetype: proto.String("video/mp4"), Caption: proto.String("✅ " + cleanTitle),
				FileLength: proto.Uint64(uint64(res.Size)),
				FileSHA256: up.FileSHA256, FileEncSHA256: up.FileEncSHA256,
			}
		}
		client.SendMessage(context.Background(), v.Info.Chat, &finalMsg)
		react(client, v.Info.Chat, v.Info.ID, "✅")

	} else if strings.TrimSpace(userChoice) == "2" {
		// --- OPTION 2: JAZZ DRIVE ---
		react(client, v.Info.Chat, v.Info.ID, "☁️")
		replyMessage(client, v, "📱 *Enter Jazz Number (03XXXXXXXXX):*\n_(You have 60s)_")

		// بیک گراؤنڈ سے رزلٹ لے لیں (تاکہ پروسیس بلاک نہ ہو)
		// ہم اسے 'res' میں محفوظ رکھیں گے جب تک نمبر/OTP نہ مل جائے
		res := <-dlChan
		if res.Err != nil {
			replyMessage(client, v, "❌ Download Failed in background.")
			return
		}
		defer os.Remove(res.Path) // اپلوڈ کے بعد ڈیلیٹ

		// 1. Get Number
		phone, ok := WaitForUserReply(senderID, 60*time.Second)
		if !ok || phone == "" { replyMessage(client, v, "❌ Timeout."); return }

		// 2. Send OTP
		userID := fmt.Sprintf("user_%d", time.Now().Unix())
		replyMessage(client, v, "🔄 Sending OTP...")
		
		if jazzGenOTP(userID, phone) {
			replyMessage(client, v, "🔑 *OTP Sent! Enter 4-digit code:*")
			otp, ok := WaitForUserReply(senderID, 60*time.Second)
			if !ok || otp == "" { replyMessage(client, v, "❌ Timeout."); return }

			// 3. Verify & Upload
			replyMessage(client, v, "🔐 Verifying...")
			if jazzVerifyOTP(userID, otp) {
				replyMessage(client, v, "☁️ *Uploading to Jazz Drive...*\n_(This may take time depending on file size)_")
				
				link, err := jazzUploadFile(userID, res.Path)
				if err == nil {
					finalText := fmt.Sprintf("🎉 *Upload Complete!*\n\n📂 *File:* %s\n📦 *Size:* %.2f MB\n🔗 *Link:* %s", 
						res.Title, float64(res.Size)/(1024*1024), link)
					replyMessage(client, v, finalText)
				} else {
					replyMessage(client, v, "❌ Upload Failed: "+err.Error())
				}
			} else {
				replyMessage(client, v, "❌ Invalid OTP.")
			}
		} else {
			replyMessage(client, v, "❌ Failed to send OTP. Check number.")
		}

	} else {
		// غلط ان پٹ
		replyMessage(client, v, "❌ Invalid Option. Sending file directly...")
		res := <-dlChan
		if res.Err == nil {
			defer os.Remove(res.Path)
			// یہاں آپ دوبارہ اوپر والا WhatsApp Upload کوڈ کال کر سکتے ہیں 
			// یا کوڈ ریپیٹ سے بچنے کے لیے ایک الگ فنکشن بنا لیں (تجویز کردہ)
		}
	}
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
	
	// بوٹ کی کلین آئی ڈی لیں
	myID := getCleanID(client.Store.ID.User)

	cmd := exec.Command("yt-dlp", "ytsearch5:"+query, "--get-title", "--get-id", "--no-playlist")
	out, _ := cmd.Output()
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 { return }

	var results []YTSResult
	// ✨ Bullet Style Design: یہ کبھی نہیں ٹوٹتا
	menuText := "╭─── 📺 *YOUTUBE SEARCH* ───╮\n│\n"
	
	for i := 0; i < len(lines)-1; i += 2 {
		title := lines[i]
		results = append(results, YTSResult{Title: title, Url: "https://www.youtube.com/watch?v=" + lines[i+1]})
		menuText += fmt.Sprintf("📍 *[%d]* %s\n│ ┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈\n", (i/2)+1, title)
	}
	menuText += "│\n╰────────────────────╯"

	resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(menuText)},
	})

	if err == nil {
		ytCache[resp.ID] = YTSession{Results: results, SenderID: v.Info.Sender.User, BotLID: myID}
		go func() { time.Sleep(2 * time.Minute); delete(ytCache, resp.ID) }()
	}
}

func handleYTDownloadMenu(client *whatsmeow.Client, v *events.Message, ytUrl string) {
	myID := getCleanID(client.Store.ID.User)
	senderLID := v.Info.Sender.User

	menu := `╔════════════════════╗
║    🎬 VIDEO SELECTOR 
╠════════════════════╣
║ 1️⃣ 360p (Fast)
║ 2️⃣ 720p (HD)
║ 3️⃣ 1080p (FHD)
║ 4️⃣ MP3 (Audio)
║
║ ⏳ Select an option by 
║ replying to this card.
╚════════════════════╝`

	resp, err := client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{Text: proto.String(menu)},
	})

	if err == nil {
		// 💾 میسج آئی ڈی کے ساتھ کیش کریں
		ytDownloadCache[resp.ID] = YTState{
			Url:      ytUrl,
			BotLID:   myID,
			SenderID: senderLID,
		}
		fmt.Printf("📂 [YT-MENU] Cached ID: %s for Bot: %s\n", resp.ID, myID)
		
		// ۱ منٹ بعد صفائی
		go func() {
			time.Sleep(1 * time.Minute)
			delete(ytDownloadCache, resp.ID)
		}()
	}
}

func handleYTDownload(client *whatsmeow.Client, v *events.Message, ytUrl, choice string, isAudio bool) {
	react(client, v.Info.Chat, v.Info.ID, "⏳")
	
	mode := "video"
	format := "bestvideo[height<=720]+bestaudio/best" // Default

	if isAudio {
		mode = "audio"
	} else {
		switch choice {
		case "1": format = "bestvideo[height<=360]+bestaudio/best"
		case "2": format = "bestvideo[height<=720]+bestaudio/best"
		case "3": format = "bestvideo[height<=1080]+bestaudio/best"
		}
	}

	// ✅ اب یہ 5 چیزیں بھیجے گا اور بوٹ اسے قبول کر لے گا
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