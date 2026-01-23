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

// ==========================================
// ⚙️ API CONFIGURATION (یہاں اپنی APIs ایڈ کریں)
// ==========================================

type SMSConfig struct {
	Name      string
	NumberURL string
	SmsURL    string
}

// یہاں آپ جتنی مرضی APIs ایڈ کر سکتے ہیں (1, 2, 3...)
var SMS_APIS = map[string]SMSConfig{
	"1": {
		Name:      "Kamina Server 1",
		NumberURL: "https://api-kami-nodejs-production.up.railway.app/api?type=numbers",
		SmsURL:    "https://api-kami-nodejs-production.up.railway.app/api?type=sms",
	},
	"2": {
		Name:      "Server 2 (D-group)",
		NumberURL: "https://kamina-otp.up.railway.app/d-group/numbers",
		SmsURL:    "https://kamina-otp.up.railway.app/d-group/sms", // یہاں غلطی تھی
	},
	"3": {
		Name:      "Server 3 (Npm-neon)",
		NumberURL: "https://kamina-otp.up.railway.app/npm-neon/numbers",
		SmsURL:    "https://kamina-otp.up.railway.app/npm-neon/sms", // یہاں غلطی تھی
	},
	"4": {
		Name:      "Server 4 (mait)",
		NumberURL: "https://kamina-otp.up.railway.app/mait/numbers",
		SmsURL:    "https://kamina-otp.up.railway.app/mait/sms", // یہاں غلطی تھی
	},
}


// ==========================================

var userCountryPref = make(map[string]string)
var otpMutex sync.RWMutex

// JSON Structure (Universal)
type KaminaResponse struct {
	TotalRecords interface{}     `json:"iTotalRecords"`
	AaData       [][]interface{} `json:"aaData"`
}

// 1️⃣ کمانڈ: .nset (Country Setting)
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

// 2️⃣ کمانڈ: .num [API_ID] (Default: 1)
func HandleGetNumber(client *whatsmeow.Client, v *events.Message, args []string) {
	senderID := v.Info.Sender.ToNonAD().String()
	
	// 1. API Selection Logic
	apiID := "1" // ڈیفالٹ API
	if len(args) > 0 {
		apiID = args[0] // اگر یوزر نے .num 2 لکھا ہے
	}

	// چیک کریں کہ API موجود ہے یا نہیں
	config, exists := SMS_APIS[apiID]
	if !exists {
		replyMessage(client, v, fmt.Sprintf("❌ Invalid API ID: %s\nAvailable: 1", apiID))
		return
	}

	otpMutex.RLock()
	targetCountry, hasPref := userCountryPref[senderID]
	otpMutex.RUnlock()

	// 2. Fetch Data using Selected URL
	data, errStr := fetchKaminaData(config.NumberURL)
	if errStr != "" {
		replyMessage(client, v, fmt.Sprintf("❌ API [%s] Error:\n%s", apiID, errStr))
		return
	}

	var filtered []string
	
	// 3. Filtering
	for _, row := range data.AaData {
		if len(row) < 3 { continue }
		
		countryRaw, ok1 := row[0].(string)
		phoneRaw, ok2 := row[2].(string)

		if !ok1 || !ok2 { continue }

		dbCountryName := strings.ToLower(countryRaw) 
		phoneNumber := phoneRaw

		if hasPref {
			if strings.Contains(dbCountryName, targetCountry) {
				filtered = append(filtered, phoneNumber)
			}
		} else {
			filtered = append(filtered, phoneNumber)
		}
	}

	if len(filtered) == 0 {
		msg := fmt.Sprintf("❌ No numbers found for '%s' on Server %s.", targetCountry, apiID)
		replyMessage(client, v, msg)
		return
	}

	// 4. Random Pick & Formatting
	rand.Seed(time.Now().UnixNano())
	pickedNum := filtered[rand.Intn(len(filtered))]
	
	displayNum := pickedNum
	if !strings.HasPrefix(displayNum, "+") {
		displayNum = "+" + displayNum
	}

	mode := "Random"
	if hasPref { mode = strings.Title(targetCountry) }

	msg := fmt.Sprintf(`╔═══════════════════╗
║ 📱 *VIRTUAL NUMBER*
╠═══════════════════╣
║ 📡 *Server:* %s (ID: %s)
║ 🌍 *Search:* %s
║ 🔢 *Number:* ║ `+"`%s`"+`
╠═══════════════════╣
║ 💡 Usage:
║ .otp %s [number]
╚═══════════════════╝`, config.Name, apiID, mode, displayNum, apiID)

	sendReplyMessage(client, v, msg)
}

// 3️⃣ کمانڈ: .otp [API_ID] [NUMBER]
func HandleGetOTP(client *whatsmeow.Client, v *events.Message, args []string) {
	// کم از کم 2 چیزیں چاہیے: ID اور Number
	if len(args) < 2 {
		replyMessage(client, v, "⚠️ *Usage:* .otp [ID] [Number]\nExample: `.otp 1 +923001234567`")
		return
	}

	apiID := args[0]
	numberArgs := strings.Join(args[1:], "") // باقی سب نمبر ہے

	// 1. Validate API ID
	config, exists := SMS_APIS[apiID]
	if !exists {
		replyMessage(client, v, fmt.Sprintf("❌ Invalid API ID: %s", apiID))
		return
	}

	// 2. Clean Number
	targetNum := strings.TrimSpace(numberArgs)
	targetNum = strings.ReplaceAll(targetNum, "+", "")
	targetNum = strings.ReplaceAll(targetNum, " ", "")
	targetNum = strings.ReplaceAll(targetNum, "-", "")

	// 3. Fetch SMS using Selected URL
	data, errStr := fetchKaminaData(config.SmsURL)
	if errStr != "" {
		fmt.Printf("❌ OTP FETCH ERROR (API %s): %s\n", apiID, errStr)
		replyMessage(client, v, fmt.Sprintf("❌ Server %s Error:\n%s", apiID, errStr))
		return
	}

	found := false
	var msgResult string

	for _, row := range data.AaData {
		if len(row) < 5 { continue }

		phoneRaw, okPh := row[2].(string)
		serviceRaw, okSvc := row[3].(string)
		msgRaw, okMsg := row[4].(string)
		timeRaw, okTime := row[0].(string)

		if !okPh || !okSvc || !okMsg || !okTime { continue }

		apiNum := strings.ReplaceAll(phoneRaw, " ", "")
		apiNum = strings.ReplaceAll(apiNum, "+", "")
		
		if strings.Contains(apiNum, targetNum) {
			msgResult = fmt.Sprintf(`╔═══════════════════╗
║ 📩 *OTP RECEIVED*
╠═══════════════════╣
║ 📡 *Source:* Server %s
║ 📱 *Num:* +%s
║ 🏢 *App:* %s
║ ⏰ *Time:* %s
╠═══════════════════╣
║ 💬 *Message:*
║ %s
╚═══════════════════╝`, apiID, targetNum, serviceRaw, timeRaw, msgRaw)
			
			found = true
			break 
		}
	}

	if found {
		sendReplyMessage(client, v, msgResult)
	} else {
		replyMessage(client, v, fmt.Sprintf("⏳ Server %s: No OTP for +%s\nChecking again...", apiID, targetNum))
	}
}

// 🛠️ Helper (Same as before)
func fetchKaminaData(url string) (*KaminaResponse, string) {
	client := &http.Client{Timeout: 60 * time.Second}
	fmt.Printf("🌐 Requesting: %s\n", url) 
	resp, err := client.Get(url)
	if err != nil {
		fmt.Printf("❌ HTTP FAIL: %v\n", err)
		return nil, fmt.Sprintf("Network Fail: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Sprintf("Server Error (Code %d)", resp.StatusCode)
	}
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, "Failed to read body"
	}
	fmt.Printf("✅ Response Received (Size: %d bytes)\n", len(body))
	var data KaminaResponse
	err = json.Unmarshal(body, &data)
	if err != nil {
		fmt.Printf("❌ JSON ERROR: %v\nRaw Body Start: %s\n", err, string(body[:100])) 
		return nil, "Invalid JSON Data"
	}
	return &data, ""
}
