package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"
)

var userCountryPref = make(map[string]string)
var otpMutex sync.RWMutex

type KaminaResponse struct {
	TotalRecords int        `json:"iTotalRecords"`
	AaData       [][]string `json:"aaData"`
}

// 1️⃣ کمانڈ: .nset
func HandleNSet(client *whatsmeow.Client, v *events.Message, args []string) {
	senderID := v.Info.Sender.ToNonAD().String()
	if len(args) == 0 {
		replyMessage(client, v, "⚠️ *Usage:*\n.nset afghanistan\n.nset random")
		return
	}
	country := strings.ToLower(strings.Join(args, " "))
	otpMutex.Lock()
	if country == "random" {
		delete(userCountryPref, senderID)
		replyMessage(client, v, "✅ *Mode Changed:* Now fetching RANDOM numbers.")
	} else {
		userCountryPref[senderID] = country
		replyMessage(client, v, fmt.Sprintf("✅ *Target Set:* Searching for '%s'...", strings.Title(country)))
	}
	otpMutex.Unlock()
}

// 2️⃣ کمانڈ: .num
func HandleGetNumber(client *whatsmeow.Client, v *events.Message) {
	senderID := v.Info.Sender.ToNonAD().String()
	otpMutex.RLock()
	targetCountry, hasPref := userCountryPref[senderID]
	otpMutex.RUnlock()

	apiURL := "https://kamina-otp.up.railway.app/d-group/numbers"
	
	// یہ فنکشن اب ایرر کی تفصیل بھی دے گا
	data, errStr := fetchKaminaData(apiURL)
	if errStr != "" {
		replyMessage(client, v, "❌ API Error:\n"+errStr)
		return
	}

	var filtered []string
	for _, row := range data.AaData {
		if len(row) < 3 { continue }
		dbCountryName := strings.ToLower(row[0]) 
		phoneNumber := row[2]

		if hasPref {
			if strings.Contains(dbCountryName, targetCountry) {
				filtered = append(filtered, phoneNumber)
			}
		} else {
			filtered = append(filtered, phoneNumber)
		}
	}

	if len(filtered) == 0 {
		msg := fmt.Sprintf("❌ No numbers found for '%s'.", targetCountry)
		replyMessage(client, v, msg)
		return
	}

	rand.Seed(time.Now().UnixNano())
	pickedNum := filtered[rand.Intn(len(filtered))]
	mode := "Random"
	if hasPref { mode = strings.Title(targetCountry) }

	msg := fmt.Sprintf(`╔═══════════════════╗
║ 📱 *VIRTUAL NUMBER*
╠═══════════════════╣
║ 🌍 *Search:* %s
║ 🔢 *Number:* ║ `+"`%s`"+`
╠═══════════════════╣
║ 💡 Copy number & use
║ .otp [number] to check
╚═══════════════════╝`, mode, pickedNum)

	sendReplyMessage(client, v, msg)
}

// 3️⃣ کمانڈ: .otp (فل ڈیبگنگ کے ساتھ)
func HandleGetOTP(client *whatsmeow.Client, v *events.Message, args []string) {
	if len(args) == 0 {
		replyMessage(client, v, "⚠️ *Usage:* .otp 93788096687")
		return
	}

	targetNum := strings.TrimSpace(args[0])
	targetNum = strings.ReplaceAll(targetNum, "+", "")
	targetNum = strings.ReplaceAll(targetNum, " ", "")

	apiURL := "https://kamina-otp.up.railway.app/d-group/sms"
	
	// 🔥 یہاں میں نے خاص ایرر ہینڈلنگ لگائی ہے
	data, errStr := fetchKaminaData(apiURL)
	if errStr != "" {
		fmt.Printf("❌ OTP FETCH ERROR: %s\n", errStr) // کنسول میں ایرر پرنٹ ہوگا
		replyMessage(client, v, fmt.Sprintf("❌ Server Error:\n%s", errStr))
		return
	}

	found := false
	var msgResult string

	for _, row := range data.AaData {
		if len(row) < 5 { continue }

		apiNum := strings.ReplaceAll(row[2], " ", "")
		
		if strings.Contains(apiNum, targetNum) {
			service := row[3]
			smsBody := row[4]
			timeStr := row[0]

			msgResult = fmt.Sprintf(`╔═══════════════════╗
║ 📩 *OTP RECEIVED*
╠═══════════════════╣
║ 📱 *Num:* %s
║ 🏢 *App:* %s
║ ⏰ *Time:* %s
╠═══════════════════╣
║ 💬 *Message:*
║ %s
╚═══════════════════╝`, targetNum, service, timeStr, smsBody)
			
			found = true
			break 
		}
	}

	if found {
		sendReplyMessage(client, v, msgResult)
	} else {
		// اگر کنکشن ٹھیک تھا لیکن کوڈ نہیں ملا، تو یہ ایرر نہیں ہے، بس "Not Found" ہے
		replyMessage(client, v, fmt.Sprintf("⏳ No OTP received yet for: %s\nChecking again in a moment...", targetNum))
	}
}

// 🛠️ Helper: Advanced Fetcher with Debugging
func fetchKaminaData(url string) (*KaminaResponse, string) {
	// ⏰ 1. ٹائم آؤٹ بڑھا کر 60 سیکنڈ کر دیا
	client := &http.Client{Timeout: 60 * time.Second}
	
	fmt.Printf("🌐 Requesting: %s\n", url) // کنسول میں بتائے گا کہ ریکویسٹ جا رہی ہے

	resp, err := client.Get(url)
	if err != nil {
		fmt.Printf("❌ HTTP FAIL: %v\n", err)
		return nil, fmt.Sprintf("Network Fail: %v", err)
	}
	defer resp.Body.Close()

	// 2. اسٹیٹس کوڈ چیک کریں
	if resp.StatusCode != 200 {
		body, _ := ioutil.ReadAll(resp.Body)
		fmt.Printf("❌ BAD STATUS: %d | Body: %s\n", resp.StatusCode, string(body))
		return nil, fmt.Sprintf("Server Error (Code %d)", resp.StatusCode)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, "Failed to read body"
	}

	// 🔍 3. RAW RESPONSE PRINT (For Debugging)
	// اگر ریسپانس بہت بڑا ہے تو کنسول بھر جائے گا، لیکن ایرر ڈھونڈنے کے لیے ضروری ہے
	if len(body) < 1000 {
		fmt.Printf("✅ Raw Response: %s\n", string(body))
	} else {
		fmt.Printf("✅ Response Received (Size: %d bytes)\n", len(body))
	}

	var data KaminaResponse
	err = json.Unmarshal(body, &data)
	if err != nil {
		// اگر HTML آ گیا یا JSON غلط ہے تو یہاں پتہ چلے گا
		fmt.Printf("❌ JSON ERROR: %v\nRaw Body Start: %s\n", err, string(body[:100])) 
		return nil, "Invalid JSON Data"
	}
	return &data, ""
}
