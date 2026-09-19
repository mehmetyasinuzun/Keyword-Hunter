@echo off
:: KeywordHunter — Windows (cmd) başlatıcı. Asıl mantık run.ps1 içindedir.
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0run.ps1" %*
