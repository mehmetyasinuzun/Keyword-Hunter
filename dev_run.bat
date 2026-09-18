@echo off
setlocal
chcp 65001 > nul
echo [dev] Derleme + calistirma (LOG_LEVEL=debug)
set LOG_LEVEL=debug
go build -o keywordhunter.exe ./cmd
if %errorlevel% neq 0 exit /b %errorlevel%
keywordhunter.exe
