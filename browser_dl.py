import sys
import os
import time
import re
from playwright.sync_api import sync_playwright

def download_file(url):
    with sync_playwright() as p:
        browser = None
        try:
            # -------------------------------------------------
            # 1. SETUP BROWSER
            # -------------------------------------------------
            browser = p.chromium.launch(headless=True, args=["--no-sandbox", "--disable-gpu"])
            
            # اصلی موبائل/پی سی مکس User-Agent تاکہ ویب سائٹ بلاک نہ کرے
            context = browser.new_context(
                user_agent="Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36",
                accept_downloads=True
            )
            page = context.new_page()
            # ٹائم آؤٹ کو 2 منٹ کر دیں (کیونکہ کچھ سائٹس سلو ہوتی ہیں)
            page.set_default_timeout(120000) 

            print(f"DEBUG: Processing URL: {url}", file=sys.stderr)

            # -------------------------------------------------
            # 2. MASTER DOWNLOAD HANDLER
            # -------------------------------------------------
            # ہم 'expect_download' کو پورے پروسیس کے گرد لگائیں گے
            # تاکہ فائل چاہے فوراً ملے، یا انتظار کے بعد، یا کلک کے بعد، یہ اسے پکڑ لے۔
            with page.expect_download(timeout=120000) as download_info:
                
                # --- STEP A: لنک اوپن کریں (Direct Check) ---
                try:
                    # 'domcontentloaded' کافی ہے، پورا پیج لوڈ ہونے کا انتظار مت کرو
                    page.goto(url, wait_until="domcontentloaded", timeout=45000)
                except Exception as e:
                    # اگر لنک کھلتے ہی فائل آ گئی تو پلے رائٹ ایرر دیتا ہے، اسے اگنور کریں
                    if "Download is starting" in str(e) or "net::ERR_ABORTED" in str(e):
                        print("DEBUG: Direct file detected immediately!", file=sys.stderr)
                    else:
                        print(f"DEBUG: Page Load Status: {str(e)}", file=sys.stderr)

                # --- STEP B: انتظار (Redirect Check) ---
                # اگر پیج کھل گیا ہے تو 8 سیکنڈ انتظار کریں (APKPure اکثر 5 سیکنڈ لیتا ہے)
                print("DEBUG: Waiting for redirects...", file=sys.stderr)
                time.sleep(8)

                # --- STEP C: بٹن کلک (Force Click) ---
                # اگر ابھی تک ڈاؤن لوڈ شروع نہیں ہوا تو ہم بٹن ڈھونڈیں گے
                # یہ چیک کرنے کا طریقہ کہ کیا ڈاؤن لوڈ شروع ہوا یا نہیں مشکل ہے، 
                # اس لیے ہم احتیاطاً بٹن کلک کی کوشش کر لیتے ہیں۔
                
                try:
                    if page.url != "about:blank":
                        print("DEBUG: Searching for download buttons...", file=sys.stderr)
                        # یہ اسکرپٹ ہر قسم کا بٹن ڈھونڈتی ہے (Text یا Class سے)
                        found = page.evaluate("""
                            () => {
                                const keywords = ['download', 'install', 'apk', 'xapk', 'get'];
                                
                                // 1. Try Specific Selectors (APKPure, etc)
                                let btn = document.querySelector('.download-btn') || 
                                          document.querySelector('a.da') || 
                                          document.querySelector('a.download_link');
                                
                                // 2. Try Link HREF extensions
                                if (!btn) {
                                    btn = Array.from(document.querySelectorAll('a')).find(el => 
                                        el.href.includes('.apk') || el.href.includes('.xapk')
                                    );
                                }

                                // 3. Try Text Content (Last Resort)
                                if (!btn) {
                                    btn = Array.from(document.querySelectorAll('a, button')).find(el => {
                                        const text = el.innerText.toLowerCase();
                                        return keywords.some(k => text.includes(k)) && el.offsetHeight > 0;
                                    });
                                }

                                if (btn) {
                                    btn.click();
                                    return true;
                                }
                                return false;
                            }
                        """)
                        if found:
                            print("DEBUG: Clicked a potential download button.", file=sys.stderr)
                        else:
                            print("DEBUG: No download button found via JS.", file=sys.stderr)
                except Exception as e:
                    print(f"DEBUG: Button click skip: {e}", file=sys.stderr)

            # -------------------------------------------------
            # 3. SAVE FILE
            # -------------------------------------------------
            download = download_info.value
            
            # فائل کا نام صاف کریں
            original_name = download.suggested_filename
            # نام میں سے عجیب و غریب کیریکٹرز ہٹا دیں
            clean_name = re.sub(r'[\\/*?:"<>|]', "", original_name).replace(" ", "_")
            
            # ٹائم سٹیمپ لگائیں تاکہ نام مکس نہ ہوں
            timestamp = int(time.time())
            final_name = f"temp_{timestamp}_{clean_name}"
            
            save_path = os.path.join(os.getcwd(), final_name)
            
            print(f"DEBUG: Saving to {save_path}", file=sys.stderr)
            download.save_as(save_path)
            
            # صرف آخری لائن میں پاتھ پرنٹ کریں (Go کے لیے)
            print(save_path)

        except Exception as e:
            # ایرر صرف stderr پر بھیجیں تاکہ Go کنفیوز نہ ہو
            print(f"ERROR: {str(e)}", file=sys.stderr)
            sys.exit(1)
            
        finally:
            if browser:
                browser.close()

if __name__ == "__main__":
    if len(sys.argv) < 2:
        print("Usage: python3 browser_dl.py <url>", file=sys.stderr)
        sys.exit(1)
    
    download_file(sys.argv[1])
