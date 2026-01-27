import sys
import os
import time
from playwright.sync_api import sync_playwright

def download_file(url):
    # یہ اسکرپٹ خاموشی سے چلے گی اور صرف فائل کا پاتھ پرنٹ کرے گی
    with sync_playwright() as p:
        try:
            # 1. براؤزر لانچ کریں (Headless)
            browser = p.chromium.launch(headless=True, args=["--no-sandbox", "--disable-gpu"])
            
            # اصلی موبائل یا پی سی جیسا User-Agent
            context = browser.new_context(
                user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
            )
            page = context.new_page()
            
            # ٹائم آؤٹ (1 منٹ)
            page.set_default_timeout(60000)

            # 2. ڈاؤن لوڈ کا انتظار کریں
            # یہ فنکشن تب تک انتظار کرتا ہے جب تک کوئی فائل ڈاؤن لوڈ ہونا شروع نہ ہو
            with page.expect_download(timeout=60000) as download_info:
                # stderr پر ڈیبگ پرنٹ کریں (تاکہ Go اسے فائل کا نام نہ سمجھے)
                print(f"DEBUG: Opening {url}...", file=sys.stderr)
                page.goto(url)
                
                # 3. APKPure کے لیے بٹن کلک لاجک
                # اگر 5 سیکنڈ تک خود ڈاؤن لوڈ شروع نہ ہو تو بٹن ڈھونڈو
                time.sleep(5)
                try:
                    page.evaluate("""
                        let btn = document.querySelector('.download-btn') || 
                                  document.querySelector('a[href$=".apk"]') || 
                                  document.querySelector('a[href$=".xapk"]');
                        if(btn) btn.click();
                    """)
                except:
                    pass

            download = download_info.value
            
            # 4. فائل کو عارضی فولڈر میں محفوظ کریں
            # اصلی نام استعمال کریں جو سرور دے رہا ہے
            original_name = download.suggested_filename
            # نام میں سے اسپیس وغیرہ ہٹا دیں
            safe_name = f"temp_{int(time.time())}_{original_name}".replace(" ", "_")
            save_path = os.path.join(os.getcwd(), safe_name)
            
            download.save_as(save_path)
            
            # 5. سب سے اہم: فائل کا پاتھ پرنٹ کریں (Go اسے پڑھے گا)
            print(save_path)
            
            browser.close()

        except Exception as e:
            # اگر کوئی ایرر آئے
            print(f"ERROR: {str(e)}", file=sys.stderr)
            sys.exit(1)

if __name__ == "__main__":
    if len(sys.argv) < 2:
        sys.exit(1)
    download_file(sys.argv[1])
    