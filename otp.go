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
	TotalRecords interface{}     `json:"iTotalRecords"` 
	AaData       [][]interface{} `json:"aaData"`
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

// 2️⃣ کمانڈ: .num (اپڈیٹڈ: اب نمبر کے ساتھ + آئے گا)
func HandleGetNumber(client *whatsmeow.Client, v *events.Message) {
	senderID := v.Info.Sender.ToNonAD().String()
	otpMutex.RLock()
	targetCountry, hasPref := userCountryPref[senderID]
	otpMutex.RUnlock()

	apiURL := "https://kamina-otp.up.railway.app/d-group/numbers"
	data, errStr := fetchKaminaData(apiURL)
	if errStr != "" {
		replyMessage(client, v, "❌ API Error:\n"+errStr)
		return
	}

	var filtered []string
	
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
		msg := fmt.Sprintf("❌ No numbers found for '%s'.", targetCountry)
		replyMessage(client, v, msg)
		return
	}

	rand.Seed(time.Now().UnixNano())
	pickedNum := filtered[rand.Intn(len(filtered))]
	
	// 🔥 UPDATE: نمبر کے شروع میں + کا اضافہ
	displayNum := pickedNum
	if !strings.HasPrefix(displayNum, "+") {
		displayNum = "+" + displayNum
	}

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
╚═══════════════════╝`, mode, displayNum) // یہاں اب + والا نمبر شو ہوگا

	sendReplyMessage(client, v, msg)
}

// 3️⃣ کمانڈ: .otp (سمارٹ ان پٹ: + کے ساتھ یا بغیر دونوں چلیں گے)
func HandleGetOTP(client *whatsmeow.Client, v *events.Message, args []string) {
	if len(args) == 0 {
		replyMessage(client, v, "⚠️ *Usage:* .otp +93788096687")
		return
	}

	targetNum := strings.TrimSpace(args[0])
	
	// 🔥 UPDATE: یوزر چاہے + بھیجے یا نہ، ہم اسے صاف کر کے API سے میچ کریں گے
	targetNum = strings.ReplaceAll(targetNum, "+", "")
	targetNum = strings.ReplaceAll(targetNum, " ", "")
	targetNum = strings.ReplaceAll(targetNum, "-", "") // اگر یوزر 123-456 لکھ دے تو بھی چلے گا

	apiURL := "https://kamina-otp.up.railway.app/d-group/sms"
	data, errStr := fetchKaminaData(apiURL)
	if errStr != "" {
		fmt.Printf("❌ OTP FETCH ERROR: %s\n", errStr)
		replyMessage(client, v, fmt.Sprintf("❌ Server Error:\n%s", errStr))
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

		// API والے نمبر سے بھی سپیس وغیرہ ختم کریں تاکہ میچنگ پکی ہو
		apiNum := strings.ReplaceAll(phoneRaw, " ", "")
		apiNum = strings.ReplaceAll(apiNum, "+", "")
		
		if strings.Contains(apiNum, targetNum) {
			msgResult = fmt.Sprintf(`╔═══════════════════╗
║ 📩 *OTP RECEIVED*
╠═══════════════════╣
║ 📱 *Num:* +%s
║ 🏢 *App:* %s
║ ⏰ *Time:* %s
╠═══════════════════╣
║ 💬 *Message:*
║ %s
╚═══════════════════╝`, targetNum, serviceRaw, timeRaw, msgRaw)
			
			found = true
			break 
		}
	}

	if found {
		sendReplyMessage(client, v, msgResult)
	} else {
		replyMessage(client, v, fmt.Sprintf("⏳ No OTP received yet for: +%s\nChecking again in a moment...", targetNum))
	}
}

// 🛠️ Helper
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
