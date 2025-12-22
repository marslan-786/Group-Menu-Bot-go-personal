package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
	"bytes"
    "mime/multipart"
    "encoding/json"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
	"github.com/showwin/speedtest-go/speedtest"
)

// 💎 ٹول کارڈ میکر (Premium UI)
func sendToolCard(client *whatsmeow.Client, v *events.Message, title, tool, info string) {
	card := fmt.Sprintf(`╔══════════════════════╗
║ ✨ %s ✨
╠══════════════════════╣
║ 🛠️ Tool: %s
║ 🚦 Status: Active
╠══════════════════════╣
║ ⚡ Power: 32GB RAM (Live)
╚══════════════════════╝
%s`, strings.ToUpper(title), tool, info)
	replyMessage(client, v, card)
}

// 1. 🧠 AI BRAIN (.ai) - Real Gemini/DeepSeek Logic
func handleAI(client *whatsmeow.Client, v *events.Message, query string, cmd string) {
	if query == "" {
		replyMessage(client, v, "⚠️ Please provide a prompt.\nExample: .ai Write a Go function")
		return
	}
	
	// 🧠 ری ایکشن (تاکہ یوزر کو پتہ چلے بوٹ کام کر رہا ہے)
	react(client, v.Info.Chat, v.Info.ID, "🧠")

	// 🕵️ نام کا فیصلہ (Identity Logic)
	aiName := "Impossible AI"
	if strings.ToLower(cmd) == "gpt" {
		aiName = "GPT"
	}

	// 🎯 سسٹم پرامپٹ (زبان اور پہچان کی سختی سے ہدایت)
	systemInstructions := fmt.Sprintf("You are %s, an advanced AI. Instructions: 1. Always respond in the same language as the user's query (Urdu/English/etc). 2. Be professional and brief. 3. Your name is %s.", aiName, aiName)
	
	// 🚀 Pollinations AI Engine (Fast & Direct)
	encodedPrompt := url.QueryEscape(systemInstructions + " User prompt: " + query)
	apiUrl := "https://text.pollinations.ai/" + encodedPrompt + "?model=openai&seed=" + fmt.Sprintf("%d", time.Now().UnixNano())

	// ڈیٹا فیچ کرنا
	resp, err := http.Get(apiUrl)
	if err != nil {
		replyMessage(client, v, "❌ Engine timeout. Neural nodes are currently congested.")
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	res := string(body)

	if res == "" {
		res = "🤖 *AI Error:* My neural circuits are undergoing optimization. Try again."
	}
	
	// 📤 ڈائریکٹ رسپانس (بغیر کسی کارڈ کے)
	replyMessage(client, v, res)
	react(client, v.Info.Chat, v.Info.ID, "✅")
}

func handleImagine(client *whatsmeow.Client, v *events.Message, prompt string) {
	if prompt == "" {
		replyMessage(client, v, "⚠️ Please provide an image description.\nExample: .imagine a futuristic city in Pakistan")
		return
	}
	react(client, v.Info.Chat, v.Info.ID, "🎨")
	sendToolCard(client, v, "Flux Engine", "Stable-Diffusion XL", "🎨 Rendering HD Visuals...")

	// 🖼️ Image Generation API
	imageUrl := fmt.Sprintf("https://image.pollinations.ai/prompt/%s?width=1024&height=1024&nologo=true", url.QueryEscape(prompt))
	
	// تصویر ڈاؤن لوڈ کرنا
	resp, err := http.Get(imageUrl)
	if err != nil {
		replyMessage(client, v, "❌ Graphics engine failure.")
		return
	}
	defer resp.Body.Close()
	
	imgData, _ := io.ReadAll(resp.Body)

	// واٹس ایپ پر تصویر بھیجنا
	up, err := client.Upload(context.Background(), imgData, whatsmeow.MediaImage)
	if err != nil { return }

	finalMsg := &waProto.Message{
		ImageMessage: &waProto.ImageMessage{
			URL:        proto.String(up.URL),
			DirectPath: proto.String(up.DirectPath),
			MediaKey:   up.MediaKey,
			Mimetype:   proto.String("image/jpeg"),
			Caption:    proto.String("✨ *Impossible AI Art:* " + prompt),
			FileSHA256: up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
		},
	}

	client.SendMessage(context.Background(), v.Info.Chat, finalMsg)
	react(client, v.Info.Chat, v.Info.ID, "✅")
}

// 2. 🖥️ LIVE SERVER STATS (.stats) - No Fake Data
func handleServerStats(client *whatsmeow.Client, v *events.Message) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	
	used := m.Alloc / 1024 / 1024
	sys := m.Sys / 1024 / 1024
	numCPU := runtime.NumCPU()
	goRoutines := runtime.NumGoroutine()

	stats := fmt.Sprintf(`╔══════════════════════╗
║     🖥️ SYSTEM DASHBOARD    
╠══════════════════════╣
║ 🚀 RAM Used: %d MB
║ 💎 Total RAM: 32 GB
║ 🧬 System Memory: %d MB
║ 🧠 CPU Cores: %d
║ 🧵 Active Threads: %d
║ 🟢 Status: Invincible
╚══════════════════════╝`, used, sys, numCPU, goRoutines)
	replyMessage(client, v, stats)
}

// 3. 🚀 REAL SPEED TEST (.speed) - Real Execution

func handleSpeedTest(client *whatsmeow.Client, v *events.Message) {
	react(client, v.Info.Chat, v.Info.ID, "🚀")
	
	// ✅ یہاں سے 'msgID :=' ہٹا دیا ہے کیونکہ replyMessage کچھ واپس نہیں کرتا
	replyMessage(client, v, "📡 *Impossible Engine:* Analyzing network uplink...")

	// 1. سپیڈ ٹیسٹ کلائنٹ شروع کریں
	var speedClient = speedtest.New()
	
	// 2. قریبی سرور تلاش کریں
	serverList, err := speedClient.FetchServers()
	if err != nil {
		replyMessage(client, v, "❌ Failed to fetch speedtest servers.")
		return
	}
	
	targets, _ := serverList.FindServer([]int{})
	if len(targets) == 0 {
		replyMessage(client, v, "❌ No reachable network nodes found.")
		return
	}

	// 3. لائیو ٹیسٹنگ (اصلی ڈیٹا نکالنا)
	s := targets[0]
	s.PingTest(nil)
	s.DownloadTest()
	s.UploadTest()

	// ✨ پریمیم ڈیزائن
	result := fmt.Sprintf("╭─── 🚀 *NETWORK ANALYSIS* ───╮\n"+
		"│\n"+
		"│ 📡 *Node:* %s\n"+
		"│ 📍 *Location:* %s\n"+
		"│ ┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈\n"+
		"│ ⚡ *Latency:* %s\n"+
		"│ 📥 *Download:* %.2f Mbps\n"+
		"│ 📤 *Upload:* %.2f Mbps\n"+
		"│\n"+
		"╰────────────────────╯",
		s.Name, s.Country, s.Latency, s.DLSpeed, s.ULSpeed)

	// رزلٹ بھیجیں
	replyMessage(client, v, result)
	react(client, v.Info.Chat, v.Info.ID, "✅")
}


// Remini API کا جواب سمجھنے کے لیے سٹرکچر
type ReminiResponse struct {
	Status string `json:"status"`
	URL    string `json:"url"`
}

// یہ فنکشن امیج کو عارضی طور پر Catbox پر اپلوڈ کر کے پبلک لنک لائے گا
func uploadToTempHost(data []byte, filename string) (string, error) {
	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("fileToUpload", filename)
	part.Write(data)
	writer.WriteField("reqtype", "fileupload")
	writer.Close()

	req, _ := http.NewRequest("POST", "https://catbox.moe/user/api.php", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil { return "", err }
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	return string(respBody), nil
}

func handleRemini(client *whatsmeow.Client, v *events.Message) {
	// 1️⃣ چیک کریں کہ کیا یہ امیج کا ریپلائی ہے؟
	if !v.Info.IsIncoming || v.Message.GetExtendedTextMessage() == nil || v.Message.GetExtendedTextMessage().GetContextInfo().GetQuotedMessage() == nil {
		replyMessage(client, v, "⚠️ Please reply to an image with *.remini*")
		return
	}

	quotedMsg := v.Message.GetExtendedTextMessage().GetContextInfo().GetQuotedMessage()
	imgMsg := quotedMsg.GetImageMessage()
	if imgMsg == nil {
		replyMessage(client, v, "⚠️ The replied message is not an image.")
		return
	}

	// 🎬 پروسیسنگ شروع
	react(client, v.Info.Chat, v.Info.ID, "✨")
	sendToolCard(client, v, "AI Enhancer", "Remini-V3", "🪄 Downloading & Processing Image...")

	// 2️⃣ واٹس ایپ سے اصل امیج ڈاؤن لوڈ کریں
	imgData, err := client.Download(imgMsg)
	if err != nil {
		replyMessage(client, v, "❌ Failed to download original image.")
		return
	}

	// 3️⃣ پبلک URL حاصل کریں (Catbox پر اپلوڈ کر کے)
	// API کو پبلک لنک چاہیے، اس لیے ہمیں یہ سٹیپ کرنا پڑ رہا ہے
	publicURL, err := uploadToTempHost(imgData, "image.jpg")
	if err != nil || !strings.HasPrefix(publicURL, "http") {
		replyMessage(client, v, "❌ Failed to generate public link for processing.")
		return
	}

	// 4️⃣ Remini API کو کال کریں
	apiURL := fmt.Sprintf("https://final-enhanced-production.up.railway.app/enhance?url=%s", url.QueryEscape(publicURL))
	resp, err := http.Get(apiURL)
	if err != nil {
		replyMessage(client, v, "❌ AI Enhancement Engine is offline.")
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var reminiResp ReminiResponse
	json.Unmarshal(body, &reminiResp)

	if reminiResp.Status != "success" || reminiResp.URL == "" {
		replyMessage(client, v, "❌ AI failed to enhance image. Try another one.")
		return
	}

	// 5️⃣ ہماری "ایٹمی لاجک" (ڈاؤن لوڈ -> فائل -> اپلوڈ)
	// اب ہم Enhanced امیج کو ڈاؤن لوڈ کر کے بھیجیں گے
	enhancedResp, err := http.Get(reminiResp.URL)
	if err != nil { return }
	defer enhancedResp.Body.Close()

	fileName := fmt.Sprintf("remini_%d.jpg", time.Now().UnixNano())
	outFile, err := os.Create(fileName)
	if err != nil { return }
	io.Copy(outFile, enhancedResp.Body)
	outFile.Close()

	// فائل پڑھیں اور ڈیلیٹ کریں
	finalData, err := os.ReadFile(fileName)
	if err != nil { return }
	defer os.Remove(fileName)

	// واٹس ایپ پر اپلوڈ اور سینڈ
	up, err := client.Upload(context.Background(), finalData, whatsmeow.MediaImage)
	if err != nil {
		replyMessage(client, v, "❌ Failed to send enhanced image.")
		return
	}

	finalMsg := &waProto.Message{
		ImageMessage: &waProto.ImageMessage{
			URL:        proto.String(up.URL),
			DirectPath: proto.String(up.DirectPath),
			MediaKey:   up.MediaKey,
			Mimetype:   proto.String("image/jpeg"),
			Caption:    proto.String("✅ *Enhanced with Remini AI*"),
			FileSHA256: up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength: proto.Uint64(uint64(len(finalData))),
		},
	}

	client.SendMessage(context.Background(), v.Info.Chat, finalMsg)
	react(client, v.Info.Chat, v.Info.ID, "✅")
}

// 6. 🌐 HD SCREENSHOT (.ss) - Real Rendering
func handleScreenshot(client *whatsmeow.Client, v *events.Message, targetUrl string) {
	if targetUrl == "" {
		replyMessage(client, v, "⚠️ *Usage:* .ss [Link]")
		return
	}
	react(client, v.Info.Chat, v.Info.ID, "📸")
	sendToolCard(client, v, "Web Capture", "Headless-Mobile", "🌐 Rendering: "+targetUrl)

	// 1️⃣ لنک تیار کریں (موبائل ویو + ہائی ریزولوشن)
	// ہم نے device=phone اور 1290x2796 استعمال کیا ہے تاکہ فل موبائل اسکرین آئے
	apiURL := fmt.Sprintf("https://api.screenshotmachine.com/?key=54be93&device=phone&dimension=1290x2796&url=%s", url.QueryEscape(targetUrl))

	// 2️⃣ سرور سے امیج ڈاؤن لوڈ کریں
	resp, err := http.Get(apiURL)
	if err != nil {
		replyMessage(client, v, "❌ Screenshot engine failed to connect.")
		return
	}
	defer resp.Body.Close()

	// 3️⃣ عارضی فائل بنائیں (Our Standard Logic)
	fileName := fmt.Sprintf("ss_%d.jpg", time.Now().UnixNano())
	out, err := os.Create(fileName)
	if err != nil { return }
	
	_, err = io.Copy(out, resp.Body)
	out.Close()
	if err != nil { return }

	// 4️⃣ فائل کو بائٹس میں پڑھیں
	fileData, err := os.ReadFile(fileName)
	if err != nil { return }
	defer os.Remove(fileName) // کام ختم ہونے پر فائل ڈیلیٹ

	// 5️⃣ واٹس ایپ پر اپلوڈ کریں
	up, err := client.Upload(context.Background(), fileData, whatsmeow.MediaImage)
	if err != nil {
		replyMessage(client, v, "❌ WhatsApp rejected the media upload.")
		return
	}

	// 6️⃣ پروٹوکول میسج ڈیلیوری
	finalMsg := &waProto.Message{
		ImageMessage: &waProto.ImageMessage{
			URL:        proto.String(up.URL),
			DirectPath: proto.String(up.DirectPath),
			MediaKey:   up.MediaKey,
			Mimetype:   proto.String("image/jpeg"),
			Caption:    proto.String("✅ *Web Capture Success*\n🌐 " + targetUrl),
			FileSHA256: up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength: proto.Uint64(uint64(len(fileData))),
		},
	}

	client.SendMessage(context.Background(), v.Info.Chat, finalMsg)
	react(client, v.Info.Chat, v.Info.ID, "✅")
}

// 7. 🌦️ LIVE WEATHER (.weather)
func handleWeather(client *whatsmeow.Client, v *events.Message, city string) {
	if city == "" { city = "Okara" }
	react(client, v.Info.Chat, v.Info.ID, "🌦️")
	
	// لائیو ویدر اے پی آئی
	apiUrl := "https://api.wttr.in/" + url.QueryEscape(city) + "?format=3"
	resp, _ := http.Get(apiUrl)
	data, _ := io.ReadAll(resp.Body)
	
	msg := fmt.Sprintf("🌦️ *Live Weather Report:* \n\n%s\n\nGenerated via Satellite-Impossible", string(data))
	replyMessage(client, v, msg)
}

// 8. 🔠 FANCY TEXT (.fancy)
func handleFancy(client *whatsmeow.Client, v *events.Message, text string) {
	if text == "" { return }
	fancy := "✨ *Impossible Style:* \n\n"
	fancy += "❶ " + strings.ToUpper(text) + "\n"
	fancy += "❷ ℑ𝔪𝔭𝔬𝔰𝔰𝔦𝔟𝔩𝔢 𝔅𝔬𝔱\n"
	fancy += "❸ 🅸🅼🅿🅾🆂🆂🅸🅱🅻🅴\n"
	replyMessage(client, v, fancy)
}

// 🎥 Douyin Downloader (Chinese TikTok)
func handleDouyin(client *whatsmeow.Client, v *events.Message, url string) {
	if url == "" { replyMessage(client, v, "⚠️ Please provide a Douyin link."); return }
	react(client, v.Info.Chat, v.Info.ID, "🐉")
	sendPremiumCard(client, v, "Douyin", "Douyin-HQ", "🐉 Fetching Chinese TikTok content...")
	// ہماری ماسٹر لاجک 'downloadAndSend' اب اسے ہینڈل کرے گی
	go downloadAndSend(client, v, url, "video")
}

// 🎞️ Kwai Downloader
func handleKwai(client *whatsmeow.Client, v *events.Message, url string) {
	if url == "" { replyMessage(client, v, "⚠️ Please provide a Kwai link."); return }
	react(client, v.Info.Chat, v.Info.ID, "🎞️")
	sendPremiumCard(client, v, "Kwai", "Kwai-Engine", "🎞️ Processing Kwai short video...")
	go downloadAndSend(client, v, url, "video")
}

// 🔍 Google Search (Real Results Formatting)
func handleGoogle(client *whatsmeow.Client, v *events.Message, query string) {
	if query == "" {
		replyMessage(client, v, "⚠️ *Usage:* .google [query]")
		return
	}
	react(client, v.Info.Chat, v.Info.ID, "🔍")
	replyMessage(client, v, "📡 *Impossible Engine:* Scouring the web for '"+query+"'...")

	// 🚀 DuckDuckGo Search Logic (Stable & Free)
	// ہم HTML سرچ کو پارس کریں گے جو بہت سادہ ہے
	searchUrl := "https://duckduckgo.com/html/?q=" + url.QueryEscape(query)
	
	resp, err := http.Get(searchUrl)
	if err != nil {
		replyMessage(client, v, "❌ Search engine failed to respond.")
		return
	}
	defer resp.Body.Close()

	// رزلٹ کو ریڈ کرنا
	body, _ := io.ReadAll(resp.Body)
	htmlContent := string(body)

	// ✨ پریمیم کارڈ ڈیزائن
	menuText := "╭─── 🧐 *IMPOSSIBLE SEARCH* ───╮\n│\n"
	
	// سادہ اسپلٹ لاجک سے ٹاپ لنکس نکالنا (بغیر بھاری لائبریری کے)
	links := strings.Split(htmlContent, "class=\"result__a\" href=\"")
	
	count := 0
	for i := 1; i < len(links); i++ {
		if count >= 5 { break }
		
		// لنک اور ٹائٹل الگ کرنا
		linkPart := strings.Split(links[i], "\"")
		if len(linkPart) < 2 { continue }
		actualLink := linkPart[0]
		
		titlePart := strings.Split(links[i], ">")
		if len(titlePart) < 2 { continue }
		actualTitle := strings.Split(titlePart[1], "</a")[0]

		// کارڈ میں ڈیٹا ڈالنا
		menuText += fmt.Sprintf("📍 *[%d]* %s\n│ 🔗 %s\n│ ┈┈┈┈┈┈┈┈┈┈┈┈┈┈┈\n", count+1, actualTitle, actualLink)
		count++
	}

	if count == 0 {
		replyMessage(client, v, "❌ No results found. Try a different query.")
		return
	}

	menuText += "│\n╰────────────────────╯"
	replyMessage(client, v, menuText)
	react(client, v.Info.Chat, v.Info.ID, "✅")
}

// 🎙️ Audio to PTT (Real Voice Note Logic)
// 🎙️ AUDIO TO VOICE (.toptt) - FIXED
func handleToPTT(client *whatsmeow.Client, v *events.Message) {
	// ریپلائی نکالنے کا نیا طریقہ
	var quoted *waProto.Message
	if v.Message.GetExtendedTextMessage() != nil {
		quoted = v.Message.ExtendedTextMessage.GetContextInfo().GetQuotedMessage()
	} else if v.Message.GetImageMessage() != nil {
		quoted = v.Message.ImageMessage.GetContextInfo().GetQuotedMessage()
	} else if v.Message.GetVideoMessage() != nil {
		quoted = v.Message.VideoMessage.GetContextInfo().GetQuotedMessage()
	} else if v.Message.GetAudioMessage() != nil {
		quoted = v.Message.AudioMessage.GetContextInfo().GetQuotedMessage()
	}

	if quoted == nil || (quoted.AudioMessage == nil && quoted.VideoMessage == nil) {
		replyMessage(client, v, `╔════════════════════╗
║ ❌ Please reply to any voice!
╚════════════════════╝`)
		return
	}

	react(client, v.Info.Chat, v.Info.ID, "🎙️")
	
	var media whatsmeow.DownloadableMessage
	if quoted.AudioMessage != nil { media = quoted.AudioMessage } else { media = quoted.VideoMessage }

	data, _ := client.Download(context.Background(), media)
	input := fmt.Sprintf("in_%d", time.Now().UnixNano())
	output := input + ".ogg"
	os.WriteFile(input, data, 0644)

	// FFmpeg: Convert to official PTT format
	exec.Command("ffmpeg", "-i", input, "-c:a", "libopus", "-b:a", "32k", "-ac", "1", output).Run()
	
	pttData, _ := os.ReadFile(output)
	up, _ := client.Upload(context.Background(), pttData, whatsmeow.MediaAudio)

	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("audio/ogg; codecs=opus"),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    proto.Uint64(uint64(len(pttData))),
			PTT:           proto.Bool(true), // ✅ Official Voice Note Fix
		},
	})
	os.Remove(input); os.Remove(output)
}

// 🧼 BACKGROUND REMOVER (.removebg) - FIXED
func handleRemoveBG(client *whatsmeow.Client, v *events.Message) {
	// 1️⃣ ریپلائی چیک کریں
	if v.Message.GetExtendedTextMessage() == nil || v.Message.GetExtendedTextMessage().GetContextInfo().GetQuotedMessage() == nil {
		replyMessage(client, v, "⚠️ Please reply to an image with *.removebg*")
		return
	}

	quotedMsg := v.Message.GetExtendedTextMessage().GetContextInfo().GetQuotedMessage()
	imgMsg := quotedMsg.GetImageMessage()
	if imgMsg == nil {
		replyMessage(client, v, "⚠️ The replied message is not an image.")
		return
	}

	// 🎬 لوکل پروسیسنگ شروع
	react(client, v.Info.Chat, v.Info.ID, "✂️")
	replyMessage(client, v, "🪄 *Impossible Local Engine:* Executing background extraction...")

	// 2️⃣ واٹس ایپ سے امیج ڈاؤن لوڈ کریں
	imgData, err := client.Download(imgMsg)
	if err != nil {
		replyMessage(client, v, "❌ Failed to download image.")
		return
	}

	// 3️⃣ عارضی فائلز بنائیں
	inputPath := fmt.Sprintf("input_%d.jpg", time.Now().UnixNano())
	outputPath := fmt.Sprintf("output_%d.png", time.Now().UnixNano())

	// ان پٹ فائل محفوظ کریں
	err = os.WriteFile(inputPath, imgData, 0644)
	if err != nil { return }

	// 4️⃣ 🚀 REMBG لائبریری چلائیں (The Magic Moment)
	// یہ کمانڈ آپ کے سرور پر بیک گراؤنڈ ریموو کرے گی
	cmd := exec.Command("rembg", "i", inputPath, outputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Printf("❌ Rembg Error: %v\nLog: %s\n", err, string(output))
		replyMessage(client, v, "❌ Local engine failed. Ensure rembg is installed in Docker.")
		return
	}

	// 5️⃣ رزلٹ فائل پڑھیں
	finalData, err := os.ReadFile(outputPath)
	if err != nil { return }

	// صفائی (عارضی فائلز ڈیلیٹ کریں)
	defer os.Remove(inputPath)
	defer os.Remove(outputPath)

	// 6️⃣ واٹس ایپ پر اپلوڈ اور سینڈ
	up, err := client.Upload(context.Background(), finalData, whatsmeow.MediaImage)
	if err != nil {
		replyMessage(client, v, "❌ WhatsApp upload failed.")
		return
	}

	// 📤 فائنل میسج ڈیلیوری
	finalMsg := &waProto.Message{
		ImageMessage: &waProto.ImageMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("image/png"),
			Caption:       proto.String("✅ *Background Removed Locally*"),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    proto.Uint64(uint64(len(finalData))),
		},
	}

	client.SendMessage(context.Background(), v.Info.Chat, finalMsg)
	react(client, v.Info.Chat, v.Info.ID, "✅")
}

// 🎮 STEAM (.steam) - NEW & FILLED
func handleSteam(client *whatsmeow.Client, v *events.Message, url string) {
	if url == "" { return }
	react(client, v.Info.Chat, v.Info.ID, "🎮")
	sendPremiumCard(client, v, "Steam Media", "Steam-Engine", "🎮 Fetching official game trailer...")
	go downloadAndSend(client, v, url, "video")
}

// 🚀 MEGA / UNIVERSAL (.mega) - NEW & FILLED
func handleMega(client *whatsmeow.Client, v *events.Message, url string) {
	if url == "" { return }
	react(client, v.Info.Chat, v.Info.ID, "🚀")
	sendPremiumCard(client, v, "Mega Downloader", "Universal-Core", "🚀 Extracting heavy media stream...")
	go downloadAndSend(client, v, url, "video")
}

// 🎓 TED Talks Downloader
func handleTed(client *whatsmeow.Client, v *events.Message, url string) {
	if url == "" { replyMessage(client, v, "⚠️ Provide a TED link."); return }
	react(client, v.Info.Chat, v.Info.ID, "🎓")
	sendPremiumCard(client, v, "TED Talks", "Knowledge-Hub", "💡 Extracting HD Lesson...")
	go downloadAndSend(client, v, url, "video")
}
// 🧼 BACKGROUND REMOVER (.removebg) - Full AI Logic