@echo off
:: KeywordHunter — Docker ile başlat (Windows). Asıl mantık run.ps1 içindedir.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0run.ps1" docker
pause
