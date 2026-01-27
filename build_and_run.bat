@echo off
setlocal
echo ===================================================
echo [1] Temizlik Islemi Baslatiliyor...
echo ===================================================

:: 1. Calisan uygulamayi durdur
echo [*] Calisan KeywordHunter islemleri kontrol ediliyor...
taskkill /F /IM "keywordhunter.exe" >nul 2>&1
if %errorlevel% equ 0 (
    echo [+] Eski islem durduruldu.
) else (
    echo [-] Calisan islem bulunamadi or zaten durdurulmus.
)

:: 2. Eski derlemeyi sil
echo [*] Eski derleme dosyasi siliniyor...
if exist "keywordhunter.exe" (
    del "keywordhunter.exe"
    echo [+] Eski keywordhunter.exe silindi.
)

echo.
echo ===================================================
echo [2] Derleme Islemi (Build)
echo ===================================================

:: 3. Yeni surumu derle
echo [*] Go build calistiriliyor...
go build -o keywordhunter.exe ./cmd
if %errorlevel% neq 0 (
    echo.
    echo [HATA] Derleme basarisiz oldu! Lutfen kod hatalarini kontrol edin.
    pause
    exit /b
)
echo [+] Derleme BASARILI.

echo.
echo ===================================================
echo [3] Uygulama Baslatiliyor
echo ===================================================

:: 4. Uygulamayi baslat
echo [*] KeywordHunter baslatiliyor...
echo [*] URL: http://localhost:8080
echo.
start keywordhunter.exe

echo [BIDILGI] Uygulama arka planda calisiyor.
echo Durdurmak icin bu pencereyi kapatabilirsiniz ama uygulama calismaya devam edebilir.
echo Tam durdurmak icin gorev yoneticisini kullanin veya scripti tekrar calistirin.
pause
