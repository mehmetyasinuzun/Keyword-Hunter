@echo off
setlocal EnableExtensions

echo ===================================================
echo [1] Docker Kontrolu
echo ===================================================

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

set "COMPOSE_CMD=docker compose"
docker compose version >nul 2>&1
if %errorlevel% neq 0 (
    set "COMPOSE_CMD=docker-compose"
    docker-compose version >nul 2>&1
    if %errorlevel% neq 0 (
        echo.
        echo [HATA] Ne "docker compose" ne de "docker-compose" bulundu.
        echo Lutfen Docker Compose kurulumunu kontrol edin.
        echo.
        pause
        exit /b
    )
)

echo [+] Compose komutu: %COMPOSE_CMD%

echo.
echo ===================================================
echo [2] Ortam Hazirlama
echo ===================================================

if not exist "data" (
    echo [*] data klasoru olusturuluyor...
    mkdir "data"
)

if not exist "data\.env" (
    echo [*] data\.env bulunamadi, .env.example kopyalaniyor...
    copy /Y ".env.example" "data\.env" >nul
)

if exist ".env" (
    echo [BILGI] Root .env Docker tarafinda artik kullanilmiyor. Kaynak dosya: data\.env
)

echo.
echo ===================================================
echo [3] Keyword Hunter Baslatiliyor (Docker)
echo ===================================================

%COMPOSE_CMD% down --remove-orphans
%COMPOSE_CMD% up -d --build

if %errorlevel% neq 0 (
    echo Baslatma sirasinda hata olustu.
    echo Hata kayitlarini gormek icin: %COMPOSE_CMD% logs -f
    pause
) else (
    echo.
    echo [BASARILI] Uygulama baslatildi: http://localhost:8080
    echo Loglari gormek icin: %COMPOSE_CMD% logs -f
    pause
)
