package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"
)

// TCS API Request Structure
type TCSRequestBody struct {
	Body struct {
		URL     string            `json:"url"`
		Type    string            `json:"type"`
		Headers map[string]string `json:"headers"` // Special mapping logic needed here
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
			Status        string `json:"status"` // Sometimes status is here
		} `json:"shipmentinfo"`
		Checkpoints []struct {
			Datetime   string `json:"datetime"`
			RecievedBy string `json:"recievedby"`
			Status     string `json:"status"`
		} `json:"checkpoints"`
		ShipmentSummary string `json:"shipmentsummary"`
	} `json:"responseData"`
}

func HandleTCSCommand(chatID string, args []string) {
    // 1. Validation: چیک کریں کہ ٹریکنگ نمبر موجود ہے
    if len(args) < 2 {
        response := "⚠️ *غلط طریقہ!*\n\nبرائے مہربانی ٹریکنگ نمبر ساتھ لکھیں۔\nمثال: `.tcs 306063207909`"
        SendMessage(chatID, response) // یہ فنکشن آپ کے بوٹ کا میسج بھیجنے والا فنکشن ہوگا
        return
    }

    trackingID := args[1]

    // 2. User Feedback: (آپشنل) اگر نیٹ سلو ہو تو یوزر کو بتا دیں
    // SendMessage(chatID, "🔍 ڈیٹا چیک کیا جا رہا ہے...")

    // 3. API Call Logic
    result, err := GetTCSData(trackingID)
    if err != nil {
        SendMessage(chatID, "❌ *مسئلہ:* TCS سرور سے رابطہ نہیں ہو سکا یا نمبر غلط ہے۔\n" + err.Error())
        return
    }

    // 4. Success: جواب بھیج دیں
    SendMessage(chatID, result)
}

// یہ صرف میسج بھیجنے کا ایک فرضی فنکشن ہے، آپ اپنا والا یوز کریں
func SendMessage(jid, text string) {
    // client.SendMessage(context.Background(), jid, &waProto.Message{Conversation: proto.String(text)})
    fmt.Println("Sending to", jid, ":", text)
}


func GetTCSData(trackingID string) (string, error) {
    url := "https://www.tcsexpress.com/apibridge"

    // TCS Special Header Logic
    headerMap := make(map[string]string)
    for i, char := range trackingID {
        headerMap[fmt.Sprintf("%d", i)] = string(char)
    }

    // Request Structure
    reqBody := TCSRequestBody{} // (Struct اوپر والی فائل سے لیں)
    reqBody.Body.URL = "trackapinew"
    reqBody.Body.Type = "GET"
    reqBody.Body.Headers = headerMap
    reqBody.Body.Param = "consignee=" + trackingID

    jsonBytes, _ := json.Marshal(reqBody)

    // HTTP Request
    req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonBytes))
    if err != nil {
        return "", err
    }

    req.Header.Set("Content-Type", "application/json; charset=UTF-8")
    req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 10; K) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/143.0.0.0 Mobile Safari/537.36")
    req.Header.Set("Origin", "https://www.tcsexpress.com")
    req.Header.Set("Referer", "https://www.tcsexpress.com/track/"+trackingID)

    client := &http.Client{Timeout: 10 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        return "", err
    }
    defer resp.Body.Close()

    body, _ := ioutil.ReadAll(resp.Body)

    var tcsResp TCSResponse
    if err := json.Unmarshal(body, &tcsResp); err != nil {
        return "", err
    }

    if !tcsResp.IsSuccess || len(tcsResp.ResponseData.ShipmentInfo) == 0 {
        return "", fmt.Errorf("کوئی ریکارڈ نہیں ملا")
    }

    // Beautify String
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
    
    // Checkpoints
    sb.WriteString("*🔄 History:*\n")
    for _, cp := range tcsResp.ResponseData.Checkpoints {
        sb.WriteString(fmt.Sprintf("🔹 %s\n   🕒 %s | 📍 %s\n", cp.Status, cp.Datetime, cp.RecievedBy))
    }
    
    return sb.String(), nil
}
