package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
    // "context" // اگر آپ اصلی SendMessage فنکشن کھولیں تو اسے ان-کمنٹ کریں
    // "go.mau.fi/whatsmeow/proto/waE2E" // اگر پروٹوکول کی ضرورت ہو
)

// TCS API Request Structure
type TCSRequestBody struct {
	Body struct {
		URL     string            `json:"url"`
		Type    string            `json:"type"`
		Headers map[string]string `json:"headers"`
		Payload struct{}          `json:"payload"`
		Param   string            `json:"param"`
	} `json:"body"`
}

// TCS API Response Structure
type TCSResponse struct {
	IsSuccess    bool `json:"isSuccess"`
	ResponseData struct {
		ShipmentInfo []struct {
			ConsignmentNo string `json:"consignmentno"`
			BookingDate   string `json:"bookingdate"`
			Shipper       string `json:"shipper"`
			Consignee     string `json:"consignee"`
			Origin        string `json:"origin"`
			Destination   string `json:"destination"`
			Status        string `json:"status"`
		} `json:"shipmentinfo"`
		Checkpoints []struct {
			Datetime   string `json:"datetime"`
			RecievedBy string `json:"recievedby"`
			Status     string `json:"status"`
		} `json:"checkpoints"`
		ShipmentSummary string `json:"shipmentsummary"`
	} `json:"responseData"`
}

// ---------------------------------------------------------
// کمانڈ ہینڈلر (Command Handler)
// ---------------------------------------------------------
// پرانا: func HandleTCSCommand(chatID string, args []string) {
// نیا (یہ لکھیں):
func HandleTCSCommand(chatID string, msgText string) {

    // 1. میسج کو یہاں توڑیں (یہ ہر قسم کی اسپیس کو ہینڈل کرتا ہے)
    args := strings.Fields(msgText)

    // ڈیبگنگ (یہ console میں پرنٹ کرے گا کہ بوٹ کو کیا ملا)
    fmt.Printf("DEBUG ARGS: %q\n", args) 

    if len(args) < 2 {
        response := "⚠️ *غلط طریقہ!*\n\nبرائے مہربانی ٹریکنگ نمبر ساتھ لکھیں۔\nمثال: `.tcs 306063207909`"
        SendMessage(chatID, response)
        return
    }

    trackingID := args[1]
    
    // ... باقی کوڈ وہی رہے گا ...
    // ... API Call Logic ...
}


// ---------------------------------------------------------
// TCS ڈیٹا حاصل کرنے والا فنکشن
// ---------------------------------------------------------
func GetTCSData(trackingID string) (string, error) {
	url := "https://www.tcsexpress.com/apibridge"

	// TCS Special Header Logic (Breaking ID into index map)
	headerMap := make(map[string]string)
	for i, char := range trackingID {
		headerMap[fmt.Sprintf("%d", i)] = string(char)
	}

	// Prepare Request Payload
	reqBody := TCSRequestBody{}
	reqBody.Body.URL = "trackapinew"
	reqBody.Body.Type = "GET"
	reqBody.Body.Headers = headerMap
	reqBody.Body.Payload = struct{}{} // Empty JSON Object
	reqBody.Body.Param = "consignee=" + trackingID

	jsonBytes, _ := json.Marshal(reqBody)

	// Create Request
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", err
	}

	// Set Headers to mimic real browser
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Mobile Safari/537.36")
	req.Header.Set("Origin", "https://www.tcsexpress.com")
	req.Header.Set("Referer", "https://www.tcsexpress.com/track/"+trackingID)

	// Execute Request
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := ioutil.ReadAll(resp.Body)

	// Parse Response
	var tcsResp TCSResponse
	if err := json.Unmarshal(body, &tcsResp); err != nil {
		return "", fmt.Errorf("JSON پارسنگ ایرر")
	}

	// Check Success
	if !tcsResp.IsSuccess || len(tcsResp.ResponseData.ShipmentInfo) == 0 {
		return "", fmt.Errorf("کوئی ریکارڈ نہیں ملا۔ ٹریکنگ نمبر چیک کریں۔")
	}

	// Beautify Output
	info := tcsResp.ResponseData.ShipmentInfo[0]
	var sb strings.Builder
	
	sb.WriteString("🚚 *TCS Tracking Details*\n")
	sb.WriteString("━━━━━━━━━━━━━━━━\n")
	sb.WriteString(fmt.Sprintf("📦 *CN:* `%s`\n", info.ConsignmentNo))
	sb.WriteString(fmt.Sprintf("📅 *Date:* %s\n", info.BookingDate))
	sb.WriteString(fmt.Sprintf("📍 *Route:* %s ➡️ %s\n", info.Origin, info.Destination))
	sb.WriteString(fmt.Sprintf("👤 *Sender:* %s\n", info.Shipper))
	sb.WriteString(fmt.Sprintf("🏠 *Receiver:* %s\n", info.Consignee))
	sb.WriteString("━━━━━━━━━━━━━━━━\n")
	
	// Checkpoints Loop
	sb.WriteString("*🔄 Tracking History:*\n")
	if len(tcsResp.ResponseData.Checkpoints) > 0 {
		for _, cp := range tcsResp.ResponseData.Checkpoints {
			sb.WriteString(fmt.Sprintf("🔹 %s\n   🕒 %s | 📍 %s\n", cp.Status, cp.Datetime, cp.RecievedBy))
		}
	} else {
		sb.WriteString("   (مزید تفصیلات دستیاب نہیں)\n")
	}
	
	// Summary
    // sb.WriteString(fmt.Sprintf("\n📝 %s", tcsResp.ResponseData.ShipmentSummary))

	return sb.String(), nil
}

// ---------------------------------------------------------
// میسج بھیجنے والا فنکشن (اپنا والا کوڈ یہاں لگائیں)
// ---------------------------------------------------------
func SendMessage(jid, text string) {
    // ⚠️ نوٹ: یہاں آپ اپنے واٹس ایپ لائبریری کا کوڈ ان-کمنٹ کریں
    // مثال کے طور پر:
    
    // globalClient.SendMessage(context.Background(), jid, &waProto.Message{
    //     Conversation: proto.String(text),
    // })

    fmt.Println("Bot Reply to", jid, ":", text)
}
