@echo off
chcp 65001 > nul
echo ===================================================
echo 🕵️ KeywordHunter - Geliştirici Başlatıcı
echo ===================================================

echo [1/4] Derleme yapılıyor...
go build -o keywordhunter.exe cmd/main.go
if %errorlevel% neq 0 (
    echo ❌ Derleme hatası!
    exit /b %errorlevel%
)
echo ✅ Derleme başarılı.

echo [2/4] 8080 portunu kullanan işlem aranıyor...

for /f "tokens=5" %%a in ('netstat -aon ^| find ":8080" ^| find "LISTENING"') do (
    set PID=%%a
)

if "%PID%"=="" (
    echo ✅ Port 8080 zaten boş.
) else (
    echo ⚠️ Port 8080 dolu! (PID: %PID%)
    echo 🛑 İşlem sonlandırılıyor...
    taskkill /F /PID %PID%
    if %errorlevel% neq 0 (
        echo ❌ İşlem sonlandırılamadı!
    ) else (
        echo ✅ Port 8080 başarıyla boşaltıldı.
    )
)

echo [3/4] Uygulama başlatılıyor...
echo ⚠️ Lütfen Tor Browser'ın açık olduğundan emin olun!
echo.

keywordhunter.exe
