@echo off
setlocal EnableExtensions EnableDelayedExpansion

set "SCRIPT_DIR=%~dp0"
for %%I in ("%SCRIPT_DIR%.") do set "SCRIPT_DIR=%%~fI"

pushd "%SCRIPT_DIR%" >nul 2>&1

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
    popd >nul 2>&1
    pause
    exit /b 1
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
        popd >nul 2>&1
        pause
        exit /b 1
    )
)

echo [+] Compose komutu: %COMPOSE_CMD%
echo [+] Calisma dizini: %SCRIPT_DIR%

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
    echo [BILGI] Root .env Docker tarafinda kullanilmiyor. Kaynak dosya: data\.env
)

echo.
echo ===================================================
echo [3] Cakisma Temizligi
echo ===================================================

set "PARENT_DIR="
set "CHILD_DIR=%SCRIPT_DIR%\Keyword-Hunter"
for %%I in ("%SCRIPT_DIR%\..") do set "PARENT_DIR=%%~fI"

call :compose_down "%SCRIPT_DIR%"

if /I not "%PARENT_DIR%"=="%SCRIPT_DIR%" (
    if exist "%PARENT_DIR%\docker-compose.yml" (
        call :compose_down "%PARENT_DIR%"
    )
)

if /I not "%CHILD_DIR%"=="%SCRIPT_DIR%" (
    if exist "%CHILD_DIR%\docker-compose.yml" (
        call :compose_down "%CHILD_DIR%"
    )
)

for %%C in (keywordhunter-app keywordhunter-tor) do (
    for /f "delims=" %%N in ('docker ps -a --format "{{.Names}}" ^| findstr /R /I "^%%C$"') do (
        echo [*] Eski container siliniyor: %%N
        docker rm -f %%N >nul 2>&1
    )
)

echo [*] Port cakismasi olusturan host processleri temizleniyor...
powershell -NoProfile -ExecutionPolicy Bypass -Command "$ports=@(8080,9051);$skip=@('com.docker.backend','docker','vpnkit','wslhost');foreach($port in $ports){$conns=Get-NetTCPConnection -State Listen -LocalPort $port -ErrorAction SilentlyContinue;foreach($c in $conns){$pid=$c.OwningProcess;if($pid -le 0){continue};try{$p=Get-Process -Id $pid -ErrorAction Stop;$name=$p.ProcessName.ToLowerInvariant();if($skip -contains $name){continue};Stop-Process -Id $pid -Force -ErrorAction SilentlyContinue;Write-Output ('[KILL] Port {0} -> PID {1} ({2})' -f $port,$pid,$name)}catch{}}}"

echo.
echo ===================================================
echo [4] Keyword Hunter Baslatiliyor (Docker)
echo ===================================================

%COMPOSE_CMD% up -d --build --remove-orphans
if %errorlevel% neq 0 (
    echo.
    echo [HATA] Baslatma sirasinda hata olustu.
    echo Loglari incelemek icin: %COMPOSE_CMD% logs -f
    popd >nul 2>&1
    pause
    exit /b 1
)

set "HEALTH_STATUS="
set /a RETRY=45

:wait_health
set "HEALTH_STATUS="
for /f %%H in ('docker inspect -f "{{.State.Health.Status}}" keywordhunter-app 2^>nul') do set "HEALTH_STATUS=%%H"

if /I "!HEALTH_STATUS!"=="healthy" goto health_ok
if /I "!HEALTH_STATUS!"=="unhealthy" goto health_bad

set /a RETRY-=1
if !RETRY! LEQ 0 goto health_timeout

ping 127.0.0.1 -n 3 >nul
goto wait_health

:health_ok
set "HTTP_STATUS=0"
for /f %%S in ('powershell -NoProfile -ExecutionPolicy Bypass -Command "try{(Invoke-WebRequest -Uri ''http://localhost:8080/login'' -UseBasicParsing -TimeoutSec 15).StatusCode}catch{0}"') do set "HTTP_STATUS=%%S"

echo.
if "!HTTP_STATUS!"=="200" (
    echo [BASARILI] Uygulama hazir: http://localhost:8080
) else (
    echo [UYARI] Container healthy ama HTTP kontrol kodu: !HTTP_STATUS!
)
echo Loglari izlemek icin: %COMPOSE_CMD% logs -f
popd >nul 2>&1
pause
exit /b 0

:health_bad
echo.
echo [HATA] App container unhealthy durumda.
echo Loglari incelemek icin: %COMPOSE_CMD% logs -f app
popd >nul 2>&1
pause
exit /b 1

:health_timeout
echo.
echo [UYARI] Health kontrol suresi doldu. Durumu kontrol edin.
echo Komut: %COMPOSE_CMD% ps
popd >nul 2>&1
pause
exit /b 1

:compose_down
set "TARGET_DIR=%~1"
if not exist "%TARGET_DIR%\docker-compose.yml" goto :eof
echo [*] Stack kapatiliyor: %TARGET_DIR%
pushd "%TARGET_DIR%" >nul 2>&1
%COMPOSE_CMD% down --remove-orphans >nul 2>&1
popd >nul 2>&1
goto :eof
