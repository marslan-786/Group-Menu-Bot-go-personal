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

// یوزر کی سیٹنگز (کونسا ملک سلیکٹ کیا ہے)
var userCountryPref = make(map[string]string)
var otpMutex sync.RWMutex

// API کا سٹرکچر
type KaminaResponse struct {
	TotalRecords int        `json:"iTotalRecords"`
	AaData       [][]string `json:"aaData"`
}

// 1️⃣ کمانڈ: .nset (کنٹری سیٹ کرنے کے لیے)
func HandleNSet(client *whatsmeow.Client, v *events.Message, args []string) {
	senderID := v.Info.Sender.ToNonAD().String()

	if len(args) == 0 {
		replyMessage(client, v, "⚠️ *Usage:*\n.nset afghanistan\n.nset pakistan\n.nset random")
		return
	}

	// یوزر کا میسج چھوٹا کر دیں (Lower Case) تاکہ میچنگ میں مسئلہ نہ ہو
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

// 2️⃣ کمانڈ: .num (نمبر نکالنے کے لیے)
func HandleGetNumber(client *whatsmeow.Client, v *events.Message) {
	senderID := v.Info.Sender.ToNonAD().String()

	otpMutex.RLock()
	targetCountry, hasPref := userCountryPref[senderID]
	otpMutex.RUnlock()

	apiURL := "https://kamina-otp.up.railway.app/d-group/numbers"
	data, err := fetchKaminaData(apiURL)
	if err != nil {
		replyMessage(client, v, "❌ API Error: Could not connect to database.")
		return
	}

	var filtered []string
	
	// ڈیٹا بیس کو چھاننا (Filtering)
	for _, row := range data.AaData {
		// Index 0 = Country Name + Garbage (e.g. Afghanistan 2x2TP...)
		// Index 2 = Phone Number
		if len(row) < 3 { continue }
		
		// ڈیٹا بیس والا نام چھوٹا کر دیں
		dbCountryName := strings.ToLower(row[0]) 
		phoneNumber := row[2]

		if hasPref {
			// 🔥 MAGIC LINE: یہ چیک کرتا ہے کہ کیا نام کے اندر وہ لفظ موجود ہے؟
			// مثلاً: "afghanistan 2x2tp" کے اندر "afghanistan" موجود ہے، تو یہ OK کر دے گا
			if strings.Contains(dbCountryName, targetCountry) {
				filtered = append(filtered, phoneNumber)
			}
		} else {
			// اگر رینڈم ہے تو سب جانے دو
			filtered = append(filtered, phoneNumber)
		}
	}

	if len(filtered) == 0 {
		msg := fmt.Sprintf("❌ No numbers found for '%s'.\nTry generic name e.g., 'afghan' instead of full name.", targetCountry)
		replyMessage(client, v, msg)
		return
	}

	// لسٹ میں سے ایک رینڈم نمبر نکالنا
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

// 3️⃣ کمانڈ: .otp (کوڈ چیک کرنے کے لیے)
func HandleGetOTP(client *whatsmeow.Client, v *events.Message, args []string) {
	if len(args) == 0 {
		replyMessage(client, v, "⚠️ *Usage:* .otp 93788096687")
		return
	}

	// نمبر سے پلس اور اسپیس ختم کرنا
	targetNum := strings.TrimSpace(args[0])
	targetNum = strings.ReplaceAll(targetNum, "+", "")
	targetNum = strings.ReplaceAll(targetNum, " ", "")

	apiURL := "https://kamina-otp.up.railway.app/d-group/sms"
	data, err := fetchKaminaData(apiURL)
	if err != nil {
		replyMessage(client, v, "❌ API Error: Could not fetch SMS.")
		return
	}

	found := false
	var msgResult string

	for _, row := range data.AaData {
		// Index 2 = Phone Number
		// Index 3 = Service (WhatsApp/FB)
		// Index 4 = Message (Code)
		if len(row) < 5 { continue }

		apiNum := strings.ReplaceAll(row[2], " ", "")
		
		// یہاں بھی Contains لگایا ہے تاکہ اگر نمبر کے ساتھ کچھ اسپیس ہو تو بھی پکڑ لے
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
		replyMessage(client, v, fmt.Sprintf("❌ No OTP found yet for: %s\nWait 10s and try again.", targetNum))
	}
}

// Helper: API سے ڈیٹا لانے والا فنکشن
func fetchKaminaData(url string) (*KaminaResponse, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var data KaminaResponse
	err = json.Unmarshal(body, &data)
	if err != nil {
		return nil, err
	}
	return &data, nil
}
