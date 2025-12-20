const { Client } = require('pg');
const fs = require('fs');

async function debugDatabaseStructure() {
    console.log("\n" + "🔍".repeat(30));
    console.log("🕵️‍♂️ [DATABASE DIAGNOSTIC] ڈیٹا بیس کا معائنہ شروع...");
    console.log("🔍".repeat(30) + "\n");

    const client = new Client({
        connectionString: process.env.DATABASE_URL,
        ssl: { rejectUnauthorized: false }
    });

    try {
        await client.connect();
        console.log("✅ [CONNECTED] پوسٹ گریس سے رابطہ ہو گیا۔\n");

        // --- ٹیسٹ 1: whatsmeow_device ٹیبل کا کچا ڈیٹا ---
        console.log("📊 [TEST 1] whatsmeow_device ٹیبل چیک کر رہے ہیں...");
        const deviceRes = await client.query('SELECT * FROM whatsmeow_device LIMIT 5;');
        console.log("Raw Output (Devices):", JSON.stringify(deviceRes.rows, null, 2));

        // --- ٹیسٹ 2: ٹیبل کے کالمز کے نام چیک کرنا ---
        console.log("\n📑 [TEST 2] ٹیبل کے کالمز کے اصل نام معلوم کر رہے ہیں...");
        const columnsQuery = `
            SELECT column_name 
            FROM information_schema.columns 
            WHERE table_name = 'whatsmeow_contacts';
        `;
        const colRes = await client.query(columnsQuery);
        console.log("Contacts Table Columns:", colRes.rows.map(r => r.column_name).join(', '));

        // --- ٹیسٹ 3: تمام `@lid` والی آئی ڈیز کا نمونہ ---
        console.log("\n🆔 [TEST 3] ڈیٹا بیس میں موجود کوئی بھی 10 LID آئی ڈیز دکھائیں...");
        // یہاں ہم کوشش کریں گے کہ کوئی بھی آئی ڈی ملے جو @lid پر ختم ہو
        const sampleLids = await client.query("SELECT * FROM whatsmeow_contacts WHERE their_jid LIKE '%@lid' LIMIT 10;");
        
        if (sampleLids.rows.length > 0) {
            console.log("Found Sample LIDs:", JSON.stringify(sampleLids.rows, null, 2));
        } else {
            console.log("❌ کوئی بھی @lid والی آئی ڈی نہیں ملی۔");
        }

        // --- ٹیسٹ 4: بوٹ کے اپنے نام سے ملتا جلتا ڈیٹا ---
        console.log("\n👤 [TEST 4] بوٹ کے نمبر سے جڑا ہوا ڈیٹا تلاش کر رہے ہیں...");
        const generalSearch = await client.query("SELECT * FROM whatsmeow_contacts LIMIT 20;");
        console.log("First 20 Contacts (Summary):");
        generalSearch.rows.forEach(r => {
            console.log(`- JID: ${r.their_jid || r.jid} | Name: ${r.push_name || 'N/A'}`);
        });

    } catch (err) {
        console.error("\n❌ [CRITICAL ERROR]:", err.message);
    } finally {
        await client.end();
        console.log("\n🏁 [DIAGNOSTIC FINISHED] اب لاگز چیک کریں اور مجھے بتائیں کیا نظر آ رہا ہے۔");
        process.exit(0);
    }
}

debugDatabaseStructure();