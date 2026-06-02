باشه، این هم **کل README یکپارچه، تمیز و اصلاح‌شده (همه‌چیز در یک جا)** 👇
فقط کامل کپی کن جای README قبلی:

````md
# [MHR-CFW](https://github.com/denuitt1/mhr-cfw) بازنویسی شده به زبان Go

> بهبود عملکرد، رفع مشکل پشتیبانی YouTube و افزایش سرعت و پایداری

[![GitHub](https://img.shields.io/badge/GitHub-useraref-blue?logo=github)](https://github.com/useraref/mhr-cfw-go)

---

## 🚀 بهبودها نسبت به نسخه Python

### ✅ 1. رفع مشکل پشتیبانی YouTube
- مدیریت صحیح CORS و هندل کردن preflight OPTIONS
- بهبود Content-Encoding (پشتیبانی بهتر gzip / brotli)
- پشتیبانی کامل‌تر از Range Requests برای استریم ویدیو

---

### ⚡ 2. بهبود سرعت
- استفاده از HTTP/2 به جای HTTP/1.1 (Multiplexing سریع‌تر)
- Connection Pooling (استفاده مجدد از اتصال‌ها)
- Request Coalescing (اشتراک چند درخواست مشابه)
- Response Caching (کش LRU با TTL برای منابع استاتیک)

---

### 🔐 3. امنیت
- ارتقا گواهی‌های MITM به RSA 4096-bit

---

### 🛡️ 4. قابلیت اطمینان
- خاموش شدن تمیز (Graceful Shutdown با Ctrl+C)
- مدیریت بهتر خطاها و پاسخ‌ها

---

### 🧠 5. کیفیت کد
- بازنویسی کامل با Go (Type-safe و سریع‌تر)
- کاهش وابستگی‌ها و استفاده از استاندارد لایبرری‌ها

---

### ✨ 6. شعار
> Internet for everyone or no one

---

## ⚙️ نحوه کار

این برنامه روی سیستم شما اجرا می‌شود و درخواست‌ها را از طریق یک زیرساخت واسط (Google Apps Script) عبور می‌دهد تا به مقصد نهایی برسد.

---

## 🚀 شروع سریع

### 📦 پیش‌نیازها
- Go 1.22+

اگر مشکل دانلود پکیج داشتی:
```bash
GOPROXY=https://mirror-go.runflare.com go mod download
````

---

### 🛠️ ساخت پروژه

```bash
go build -ldflags "-s -w" -o mhr-cfw-go.exe ./cmd/mhr-cfw
```

یا:

```powershell
.\build.bat
```

---

### ⚙️ تنظیمات

فایل `config.json`:

```json
{
  "auth_key": "your-secret-password",
  "script_id": "YOUR_DEPLOYMENT_ID"
}
```

یا از Setup Wizard داخل برنامه استفاده کن.

---

### ▶️ اجرا

```powershell
.\mhr-cfw-go.exe
```

بعد از اجرا، از منو گزینه **Start proxy** را انتخاب کن.

---

### 🔐 نصب CA Certificate (HTTPS)

از منو گزینه Install CA certificate را بزن.

این کار برای رهگیری HTTPS لازم است.

---

## 🧭 راه‌اندازی کامل

1. به ریپوی اصلی برو:
   [https://github.com/denuitt1/mhr-cfw](https://github.com/denuitt1/mhr-cfw)

2. مراحل گرفتن Deployment ID را طبق README دنبال کن.

---

## 🏗️ ساخت از سورس

```bash
go build -ldflags "-s -w" -o mhr-cfw-go.exe ./cmd/mhr-cfw
```

---

## 🧩 گزینه‌های خط فرمان

| Option           | Description         |
| ---------------- | ------------------- |
| --no-menu        | اجرای بدون TUI      |
| --port           | تغییر پورت          |
| --host           | تغییر host          |
| --socks5-port    | SOCKS5 port         |
| --disable-socks5 | غیرفعال کردن SOCKS5 |
| --log-level      | سطح لاگ             |
| --install-cert   | نصب CA              |
| --uninstall-cert | حذف CA              |
| --scan           | اسکن IP             |
| --setup          | Setup wizard        |
| --version        | نمایش نسخه          |

---

## ⚠️ سلب مسئولیت

استفاده از این پروژه باید مطابق قوانین سرویس‌ها و کشور شما باشد.
مسئولیت استفاده نادرست بر عهده کاربر است.

---

## 📌 پروژه اصلی

بر پایه:

* [https://github.com/denuitt1/mhr-cfw](https://github.com/denuitt1/mhr-cfw)

---

## 📄 License

MIT

```

