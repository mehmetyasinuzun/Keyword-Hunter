@echo off
chcp 65001 > nul
echo ===================================================
echo 🕵️ KeywordHunter - Başlatıcı
echo ===================================================

echo [1/3] 8080 portunu kullanan işlem aranıyor...

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
        echo ❌ İşlem sonlandırılamadı! Yönetici olarak çalıştırmayı deneyin.
        pause
        exit /b
    ) else (
        echo ✅ Port 8080 başarıyla boşaltıldı.
    )
)

echo [2/3] Uygulama başlatılıyor...
echo.
echo ⚠️ Lütfen Tor Browser'ın açık olduğundan emin olun!
echo.

keywordhunter.exe

pause
