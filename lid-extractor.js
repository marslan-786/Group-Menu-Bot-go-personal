const { Client } = require('pg');
const fs = require('fs');

async function extractLidMaster() {
    console.log("\n" + "╔" + "═".repeat(58) + "╗");
    console.log("║" + " ".repeat(18) + "💎 LID MASTER EXTRACTOR 💎" + " ".repeat(14) + "║");
    console.log("╚" + "═".repeat(58) + "╝");

    const client = new Client({
        connectionString: process.env.DATABASE_URL,
        ssl: { rejectUnauthorized: false }
    });

    try {
        await client.connect();
        console.log("✅ [DATABASE] پوسٹ گریس کے ساتھ کنکشن قائم ہو گیا ہے۔");

        // براہ راست ڈیوائس ٹیبل سے JID اور LID اٹھائیں
        const query = 'SELECT jid, lid FROM whatsmeow_device;';
        const res = await client.query(query);

        if (res.rows.length === 0) {
            console.log("⚠️ [EMPTY] کوئی سیشن نہیں ملا۔ بوٹ پیئر کریں!");
            process.exit(0);
        }

        console.log(`📊 [FOUND] کل ${res.rows.length} سیشنز کا ڈیٹا ملا ہے۔\n`);
        
        let botData = {};

        res.rows.forEach((row, index) => {
            if (row.jid && row.lid) {
                // ڈیٹا کو صاف کریں (ڈیوائس آئی ڈی ہٹائیں جیسے :61)
                const purePhone = row.jid.split('@')[0].split(':')[0];
                const pureLid = row.lid.split('@')[0].split(':')[0] + "@lid";

                console.log(`  ╭────────────── [ BOT #${index + 1} ] ──────────────`);
                console.log(`  │ 📱 فون نمبر : ${purePhone}`);
                console.log(`  │ 🆔 اصل LID  : ${pureLid}`);
                console.log(`  │ ✨ اسٹیٹس   : کامیابی سے محفوظ!`);
                console.log(`  ╰───────────────────────────────────────────\n`);

                // پرانا اسٹرکچر جو گو (Go) بوٹ کو چاہیے
                botData[purePhone] = {
                    phone: purePhone,
                    lid: pureLid,
                    extractedAt: new Date().toISOString()
                };
            }
        });

        // فائل میں سیو کریں
        const finalJson = {
            timestamp: new Date().toISOString(),
            count: Object.keys(botData).length,
            bots: botData
        };

        fs.writeFileSync('./lid_data.json', JSON.stringify(finalJson, null, 2));
        console.log("💾 [SUCCESS] سارا ڈیٹا 'lid_data.json' میں پش کر دیا گیا ہے۔");

    } catch (err) {
        console.error("❌ [CRITICAL ERROR]:", err.message);
    } finally {
        await client.end();
        console.log("\n🏁 [FINISHED] آپریشن مکمل ہوا۔");
        process.exit(0);
    }
}

extractLidMaster();