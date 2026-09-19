@echo off
set LOG_LEVEL=debug
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0run.ps1" run
