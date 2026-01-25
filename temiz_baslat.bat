@echo off
setlocal
echo ===================================================
echo [1] 8080 Portunu Temizle
echo ===================================================

:: 8080 portunu kullanan PID'yi bul
for /f "tokens=5" %%a in ('netstat -aon ^| find ":8080" ^| find "LISTENING"') do (
    echo Port 8080 PID: %%a
    taskkill /F /PID %%a
    echo Port 8080 serbest birakildi.
)

echo.
echo ===================================================
echo [2] Eski Docker Konteynerlarini Temizle
echo ===================================================

:: Docker çalışıyor mu kontrol et
docker info >nul 2>&1
if %errorlevel% neq 0 (
    echo.
    echo [HATA] Docker Desktop calismiyor!
    echo Lutfen once Docker Desktop uygulamasini baslatin.
    echo Ardindan bu scripti tekrar calistirin.
    echo.
    pause
    exit /b
)

:: Çalışan tüm konteynerleri durdur
echo Durduruluyor...
for /f "tokens=*" %%i in ('docker ps -q') do (
    docker stop %%i
)

:: Durmuş tüm konteynerleri sil
echo Siliniyor...
for /f "tokens=*" %%i in ('docker ps -a -q') do (
    docker rm %%i
)

echo.
echo ===================================================
echo [3] Keyword Hunter Baslatiliyor (Docker)
echo ===================================================

docker-compose up -d --build

if %errorlevel% neq 0 (
    echo Baslatma sirasinda hata olustu.
    pause
) else (
    echo.
    echo [BASARILI] Uygulama baslatildi: http://localhost:8080
    echo Loglari gormek icin: docker-compose logs -f
    pause
)
