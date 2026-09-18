@echo off
setlocal EnableExtensions
chcp 65001 > nul
pushd "%~dp0" >nul 2>&1

echo ===================================================
echo  KeywordHunter - Docker ile Baslat
echo ===================================================

docker info >nul 2>&1
if %errorlevel% neq 0 (
    echo [HATA] Docker Desktop calismiyor. Once Docker Desktop'i baslatin.
    popd & pause & exit /b 1
)

set "COMPOSE_CMD=docker compose"
docker compose version >nul 2>&1 || set "COMPOSE_CMD=docker-compose"

if not exist "data" mkdir "data"

echo [*] Onceki KeywordHunter konteynerleri durduruluyor (yalnizca bu proje)...
%COMPOSE_CMD% down --remove-orphans >nul 2>&1

echo [*] Derleniyor ve baslatiliyor...
%COMPOSE_CMD% up -d --build --remove-orphans
if %errorlevel% neq 0 (
    echo [HATA] Baslatma basarisiz. Loglar: %COMPOSE_CMD% logs -f
    popd & pause & exit /b 1
)

echo.
echo [*] Ilk kurulumda uretilen yonetici parolasi icin loglara bakin:
echo     %COMPOSE_CMD% logs app ^| findstr Parola
echo [*] Arayuz: http://localhost:8080   (saglik: http://localhost:8080/healthz)
echo [*] Loglari izlemek icin: %COMPOSE_CMD% logs -f
popd
pause
