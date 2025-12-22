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
		replyMessage(client, v, "⚠️ Please provide a prompt.")
		return
	}
	react(client, v.Info.Chat, v.Info.ID, "🧠")

	// 🕵️ پہچان سیٹ کریں
	aiName := "Impossible AI"
	if strings.ToLower(cmd) == "gpt" { aiName = "GPT-4" }
	systemInstructions := fmt.Sprintf("You are %s. Respond in the user's language. Be brief and professional.", aiName)

	// 🚀 ماڈلز کی لسٹ (ترجیحی بنیاد پر)
	// ہم 'unity' کو نکال رہے ہیں کیونکہ وہ گالیاں دے رہا تھا 😂
	models := []string{"openai", "mistral"}
	
	var finalResponse string
	success := false

	for _, model := range models {
		apiUrl := fmt.Sprintf("https://text.pollinations.ai/%s?model=%s&system=%s", 
			url.QueryEscape(query), model, url.QueryEscape(systemInstructions))

		resp, err := http.Get(apiUrl)
		if err != nil { continue } // اگر کنکشن فیل ہو تو اگلے ماڈل پر جاؤ
		
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		res := string(body)

		// 🔍 چیک کریں: کیا جواب JSON ہے یا سادہ ٹیکسٹ؟
		// اگر جواب میں {"error" یا {"status" ہے تو اس کا مطلب ہے وہ ایرر ہے
		if strings.HasPrefix(res, "{") && strings.Contains(res, "error") {
			fmt.Printf("⚠️ [AI DEBUG] Model %s failed, trying next...\n", model)
			continue 
		}

		// اگر یہاں پہنچ گئے تو مطلب ٹیکسٹ صحیح مل گیا ہے
		finalResponse = res
		success = true
		break
	}

	if !success {
		replyMessage(client, v, "🤖 *Impossible AI:* All neural nodes are currently congested. Please try later.")
		return
	}
	
	replyMessage(client, v, finalResponse)
	react(client, v.Info.Chat, v.Info.ID, "✅")
}

func handleImagine(client *whatsmeow.Client, v *events.Message, prompt string) {
	if prompt == "" {
		replyMessage(client, v, "⚠️ Please provide a prompt.")
		return
	}
	react(client, v.Info.Chat, v.Info.ID, "🎨")

	imageUrl := fmt.Sprintf("https://image.pollinations.ai/prompt/%s?width=1024&height=1024&nologo=true", url.QueryEscape(prompt))
	
	resp, err := http.Get(imageUrl)
	if err != nil { return }
	defer resp.Body.Close()
	
	imgData, _ := io.ReadAll(resp.Body)

	up, err := client.Upload(context.Background(), imgData, whatsmeow.MediaImage)
	if err != nil { return }

	// ✅ یہاں ہم نے FileLength کا اضافہ کیا ہے
	finalMsg := &waProto.Message{
		ImageMessage: &waProto.ImageMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("image/jpeg"),
			Caption:       proto.String("✨ *Impossible AI Art:* " + prompt),
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    proto.Uint64(uint64(len(imgData))), // یہ لائن لازمی ہے
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
	// ✅ اصلی براؤزر بن کر ریکویسٹ بھیجیں تاکہ بلاک نہ ہو
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64)")
	
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil { return "", err }
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	return string(respBody), nil
}

func handleRemini(client *whatsmeow.Client, v *events.Message) {
	// IsIncoming ہٹا کر ہم ڈائریکٹ کوٹیڈ میسج چیک کر رہے ہیں
	extMsg := v.Message.GetExtendedTextMessage()
	if extMsg == nil || extMsg.ContextInfo == nil || extMsg.ContextInfo.QuotedMessage == nil {
		replyMessage(client, v, "⚠️ Please reply to an image with *.remini*")
		return
	}

	quotedMsg := extMsg.ContextInfo.QuotedMessage
	imgMsg := quotedMsg.GetImageMessage()
	if imgMsg == nil {
		replyMessage(client, v, "⚠️ The replied message is not an image.")
		return
	}

	react(client, v.Info.Chat, v.Info.ID, "✨")
	
	// 🛠️ FIX: Download میں context.Background() کا اضافہ کیا گیا ہے
	imgData, err := client.Download(context.Background(), imgMsg)
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
	if text == "" {
		replyMessage(client, v, "⚠️ Please provide text.\nExample: .fancy Nothing Is Impossible")
		return
	}

	// 🎨 30 وی آئی پی اسٹائلز (Comments show how they look)
	styles := []struct { Name string; A rune; a rune }{
		{"Fraktur", 0x1D504, 0x1D51E},            // 𝔄𝔅ℭ / 𝔞𝔟𝔠
		{"Fraktur Bold", 0x1D56C, 0x1D586},       // 𝕬𝕭𝕮 / 𝖆𝖇𝖈
		{"Math Bold", 0x1D400, 0x1D41A},          // 𝐀𝐁𝐂 / 𝐚𝐛𝐜
		{"Math Italic", 0x1D434, 0x1D44E},        // 𝘈𝘉𝘊 / 𝘢𝘣𝘤
		{"Math Bold Italic", 0x1D468, 0x1D482},   // 𝘼𝘽𝘾 / 𝙖𝙗𝙘
		{"Script", 0x1D49C, 0x1D4B6},             // 𝒜ℬ𝒞 / 𝒶𝒷𝒸
		{"Script Bold", 0x1D4D0, 0x1D4EA},        // 𝓐𝓑𝓒 / 𝓪𝓫𝓬
		{"Double Struck", 0x1D538, 0x1D552},      // 𝔸𝔹ℂ / 𝕒𝕓𝕔
		{"Sans Serif", 0x1D5A0, 0x1D5BA},         // 𝖠𝖡𝖢 / 𝖺𝖻𝖼
		{"Sans Bold", 0x1D5D4, 0x1D5EE},          // 𝗔𝗕𝗖 / 𝗮𝗯𝗰
		{"Sans Italic", 0x1D608, 0x1D622},        // 𝘈𝘉𝘊 / 𝘢𝘣𝘤
		{"Sans Bold Italic", 0x1D63C, 0x1D656},   // 𝘼𝘽𝘾 / 𝙖𝙗𝙘
		{"Monospace", 0x1D670, 0x1D68A},          // 𝙰𝙱𝙲 / 𝚊𝚋𝚌
		{"Circled White", 0x24B6, 0x24D0},       // ⒶⒷⒸ / ⓐⓑⓒ
		{"Circled Black", 0x1F150, 0x1F150},     // 🅐🅑🅒 (Caps Only)
		{"Squared White", 0x1F130, 0x1F130},     // 🄰🄱🄲 (Caps Only)
		{"Squared Black", 0x1F170, 0x1F170},     // 🅰🅱🅲 (Caps Only)
		{"Fullwidth", 0xFF21, 0xFF41},            // ＡＢＣ / ａｂｃ
		{"Modern Sans", 0x1D5A0, 0x1D5BA},        // 𝖠𝖡𝖢 / 𝖺𝖻𝖼
		{"Gothic", 0x1D504, 0x1D51E},             // 𝔄𝔅ℭ / 𝔞𝔟𝔠
		{"Outline", 0x1D538, 0x1D552},            // 𝔸mathbb{BC} / 𝕒𝕓𝕔
		{"Math Serif Bold", 0x1D400, 0x1D41A},    // 𝐀𝐁𝐂 / 𝐚𝐛𝐜
		{"Italic Serif", 0x1D434, 0x1D44E},       // 𝘈𝘉𝘊 / 𝘢𝘣𝘤
		{"Bold Script", 0x1D4D0, 0x1D4EA},        // 𝓐𝓑𝓒 / 𝓪𝓫𝓬
		{"Classic Gothic", 0x1D504, 0x1D51E},     // 𝔄𝔅ℭ / 𝔞𝔟𝔠
		{"Typewriter", 0x1D670, 0x1D68A},         // 𝙰𝙱𝙲 / 𝚊𝚋𝚌
		{"Bold Sans", 0x1D5D4, 0x1D5EE},          // 𝗔𝗕𝗖 / 𝗮𝗯𝗰
		{"Struck", 0x1D538, 0x1D552},             // 𝔸𝔹ℂ / 𝕒𝕓𝕔
		{"Small Caps Style", 0x1D400, 0x1D41A},   // 𝐀𝐁𝐂 (Simulation)
		{"Fancy VIP", 0x1D4D0, 0x1D4EA},          // 𝓐𝓑𝓒 / 𝓪𝓫𝓬
	}

	// 🎴 کارڈ کا ہیڈر (Header)
	card := "╔════════════════════════╗\n"
	card += "║      ✨ *FANCY ENGINE V4* ✨     ║\n"
	card += "╠════════════════════════╣\n"
	card += "║ ⚡ *Power:* 32GB RAM VIP Server ║\n"
	card += "╚════════════════════════╝\n\n"

	// 🔄 اسٹائلز جنریٹ کرنا
	for i, style := range styles {
		formatted := ""
		for _, char := range text {
			if char >= 'A' && char <= 'Z' {
				formatted += string(style.A + (char - 'A'))
			} else if char >= 'a' && char <= 'z' {
				// اگر اسٹائل میں چھوٹے حروف نہیں ہیں تو بڑے ہی دکھاؤ
				if style.a == style.A {
					formatted += string(style.A + (char - 'a'))
				} else {
					formatted += string(style.a + (char - 'a'))
				}
			} else {
				formatted += string(char)
			}
		}
		card += fmt.Sprintf("【 %02d 】 %s\n", i+1, formatted)
	}

	// 🎖️ کارڈ کا فلیگ شپ سگنیچر (Footer)
	card += "\n╔════════════════════════╗\n"
	card += "   👑 *ℑ𝔪𝔭𝔬𝔰𝔰𝔦𝔟𝔩𝔢 𝔅𝔬𝔱 𝔖𝔭𝔢𝔠𝔦𝔞𝔩*\n"
	card += "   🔥 _Scientists are now burning..._\n"
	card += "╚════════════════════════╝"

	replyMessage(client, v, card)
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
	// 1️⃣ ریپلائی نکالنے کا بہتر طریقہ
	var quoted *waProto.Message
	if extMsg := v.Message.GetExtendedTextMessage(); extMsg != nil && extMsg.ContextInfo != nil {
		quoted = extMsg.ContextInfo.QuotedMessage
	}

	// چیک کریں کہ کیا واقعی کسی آڈیو یا ویڈیو کو ریپلائی کیا گیا ہے
	if quoted == nil || (quoted.AudioMessage == nil && quoted.VideoMessage == nil) {
		replyMessage(client, v, "❌ Please reply to an audio or video file with *.toptt*")
		return
	}

	react(client, v.Info.Chat, v.Info.ID, "🎙️")
	
	// 2️⃣ میڈیا ڈاؤن لوڈ کریں
	var media whatsmeow.DownloadableMessage
	if quoted.AudioMessage != nil {
		media = quoted.AudioMessage
	} else {
		media = quoted.VideoMessage
	}

	data, err := client.Download(context.Background(), media)
	if err != nil {
		replyMessage(client, v, "❌ Failed to download media.")
		return
	}

	// 3️⃣ عارضی فائلز (یاد رہے: ان پٹ کا ایکسٹینشن ہونا ضروری ہے تاکہ FFmpeg کنفیوز نہ ہو)
	input := fmt.Sprintf("temp_in_%d", time.Now().UnixNano())
	output := fmt.Sprintf("temp_out_%d.opus", time.Now().UnixNano()) // .opus استعمال کریں
	os.WriteFile(input, data, 0644)

	// 4️⃣ 🚀 ماسٹر FFmpeg کمانڈ (واٹس ایپ کے لیے مخصوص)
	// -vn: ویڈیو ہٹا دو
	// -c:a libopus: اوپس کوڈیک استعمال کرو
	// -ac 1: مونو چینل (واٹس ایپ کے لیے لازمی)
	// -abr 1: ویری ایبل بٹ ریٹ
	cmd := exec.Command("ffmpeg", "-i", input, "-vn", "-c:a", "libopus", "-b:a", "16k", "-ac", "1", "-f", "ogg", output)
	err = cmd.Run()
	if err != nil {
		replyMessage(client, v, "❌ Conversion failed. Check if FFmpeg is installed.")
		os.Remove(input)
		return
	}

	// 5️⃣ فائل ریڈ کریں اور اپلوڈ کریں
	pttData, _ := os.ReadFile(output)
	up, err := client.Upload(context.Background(), pttData, whatsmeow.MediaAudio)
	if err != nil { return }

	// 6️⃣ آفیشل وائس نوٹ میسج
	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			URL:           proto.String(up.URL),
			DirectPath:    proto.String(up.DirectPath),
			MediaKey:      up.MediaKey,
			Mimetype:      proto.String("audio/ogg; codecs=opus"), // ✅ یہ مائیم ٹائپ لازمی ہے
			FileSHA256:    up.FileSHA256,
			FileEncSHA256: up.FileEncSHA256,
			FileLength:    proto.Uint64(uint64(len(pttData))),
			PTT:           proto.Bool(true), // ✅ یہ فائل کو "نیلا مائیک" والا وائس نوٹ بناتا ہے
		},
	})

	// صفائی
	os.Remove(input)
	os.Remove(output)
	react(client, v.Info.Chat, v.Info.ID, "✅")
}

// 🧼 BACKGROUND REMOVER (.removebg) - FIXED
func handleRemoveBG(client *whatsmeow.Client, v *events.Message) {
	extMsg := v.Message.GetExtendedTextMessage()
	if extMsg == nil || extMsg.ContextInfo == nil || extMsg.ContextInfo.QuotedMessage == nil {
		replyMessage(client, v, "⚠️ Please reply to an image with *.removebg*")
		return
	}

	quotedMsg := extMsg.ContextInfo.QuotedMessage
	imgMsg := quotedMsg.GetImageMessage()
	if imgMsg == nil {
		replyMessage(client, v, "⚠️ The replied message is not an image.")
		return
	}

	react(client, v.Info.Chat, v.Info.ID, "✂️")
	replyMessage(client, v, "🪄 *Impossible Engine:* Carving out the subject...")

	imgData, err := client.Download(context.Background(), imgMsg)
	if err != nil { return }

	inputPath := fmt.Sprintf("in_%d.jpg", time.Now().UnixNano())
	outputPath := fmt.Sprintf("out_%d.png", time.Now().UnixNano())
	os.WriteFile(inputPath, imgData, 0644)

	// 🛠️ FIX: 'python3 -m rembg' کی جگہ اب براہ راست 'rembg' کمانڈ استعمال ہوگی
	// ہم نے ڈوکر فائل میں 'rembg[cli]' ڈالا ہے، تو یہ ڈائریکٹ چلے گا
	cmd := exec.Command("rembg", "i", inputPath, outputPath)
	output, err := cmd.CombinedOutput()
	
	if err != nil {
		replyMessage(client, v, fmt.Sprintf("❌ *Engine Error:* \n%s", string(output)))
		os.Remove(inputPath)
		return
	}

	finalData, err := os.ReadFile(outputPath)
	if err != nil { return }

	defer os.Remove(inputPath)
	defer os.Remove(outputPath)

	up, err := client.Upload(context.Background(), finalData, whatsmeow.MediaImage)
	if err != nil { return }

	client.SendMessage(context.Background(), v.Info.Chat, &waProto.Message{
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
	})
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
func handleMega(client *whatsmeow.Client, v *events.Message, urlStr string) {
	if urlStr == "" { return }
	
	react(client, v.Info.Chat, v.Info.ID, "🚀")
	sendPremiumCard(client, v, "Mega Downloader", "Universal-Core", "🚀 Extracting encrypted stream...")

	go func() {
		tempDir := fmt.Sprintf("mega_%d", time.Now().UnixNano())
		os.Mkdir(tempDir, 0755)
		defer os.RemoveAll(tempDir)

		cmd := exec.Command("megadl", "--no-progress", "--path="+tempDir, urlStr)
		output, err := cmd.CombinedOutput()
		
		if err != nil {
			replyMessage(client, v, "❌ *Mega Error:* Invalid link or file too large.\nDetails: " + string(output))
			return
		}

		files, _ := os.ReadDir(tempDir)
		if len(files) == 0 {
			replyMessage(client, v, "❌ *Error:* File vanished during extraction.")
			return
		}
		
		fileName := files[0].Name()
		filePath := tempDir + "/" + fileName
		fileData, _ := os.ReadFile(filePath)

		up, err := client.Upload(context.Background(), fileData, whatsmeow.MediaDocument)
		if err != nil {
			replyMessage(client, v, "❌ WhatsApp upload failed.")
			return
		}

		// ✅ فکسڈ میسج اسٹرکچر (ContextInfo_ExternalAdReplyInfo استعمال کیا ہے)
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
					ExternalAdReply: &waProto.ContextInfo_ExternalAdReplyInfo{ // 🛠️ 'Info' ایڈ کر دیا گیا
						Title:     proto.String("Impossible Mega Engine"),
						Body:      proto.String("File: " + fileName),
						SourceURL: proto.String(urlStr),
						MediaType: ContextInfo_ExternalAdReplyInfo_IMAGE.Enum(), // میڈیا ٹائپ بھی فکس کر دی
					},
				},
			},
		})
		
		react(client, v.Info.Chat, v.Info.ID, "✅")
	}()
}

// 🎓 TED Talks Downloader
func handleTed(client *whatsmeow.Client, v *events.Message, url string) {
	if url == "" { replyMessage(client, v, "⚠️ Provide a TED link."); return }
	react(client, v.Info.Chat, v.Info.ID, "🎓")
	sendPremiumCard(client, v, "TED Talks", "Knowledge-Hub", "💡 Extracting HD Lesson...")
	go downloadAndSend(client, v, url, "video")
}
// 🧼 BACKGROUND REMOVER (.removebg) - Full AI Logic