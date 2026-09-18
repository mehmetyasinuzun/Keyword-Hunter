@echo off
setlocal
chcp 65001 > nul
echo ===================================================
echo  KeywordHunter - Yerel Derleme ve Baslatma (Windows)
echo ===================================================

where go >nul 2>&1
if %errorlevel% neq 0 (
    echo [HATA] Go bulunamadi. https://go.dev/dl/ adresinden Go 1.26+ kurun.
    pause
    exit /b 1
)

if not exist ".env" (
    echo [*] .env bulunamadi, .env.example kopyalaniyor...
    copy /Y ".env.example" ".env" >nul
    echo [!] .env icindeki ADMIN_PASS degerini degistirmeden devam etmeyin.
)

echo [1/3] Eski surec durduruluyor...
taskkill /F /IM "keywordhunter.exe" >nul 2>&1

echo [2/3] Derleniyor...
go build -trimpath -o keywordhunter.exe ./cmd
if %errorlevel% neq 0 (
    echo [HATA] Derleme basarisiz.
    pause
    exit /b 1
)

echo [3/3] Baslatiliyor... (Tor Browser veya Tor servisi 9150/9050 portunda calisiyor olmali)
echo      URL: http://localhost:8080
echo.
keywordhunter.exe
