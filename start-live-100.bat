@echo off
setlocal

cd /d "%~dp0"

set "PMBATTLE_KALSHI_ENV=production"
set "PMBATTLE_SIMULATED=false"
set "PMBATTLE_MAX_CASH_RISK=100"
set "PMBATTLE_TRADING_ENABLED=true"

if not defined PMBATTLE_CREDENTIAL_DIR set "PMBATTLE_CREDENTIAL_DIR=%USERPROFILE%\Desktop\kalshi.env"

if not defined PMBATTLE_KALSHI_KEY_ID (
  if exist "%PMBATTLE_CREDENTIAL_DIR%\api key.txt" (
    set /p PMBATTLE_KALSHI_KEY_ID=<"%PMBATTLE_CREDENTIAL_DIR%\api key.txt"
  )
)

if not defined PMBATTLE_KALSHI_PRIVATE_KEY_PATH (
  if exist "%PMBATTLE_CREDENTIAL_DIR%\private key.txt" (
    set "PMBATTLE_KALSHI_PRIVATE_KEY_PATH=%PMBATTLE_CREDENTIAL_DIR%\private key.txt"
  )
)

if not defined PMBATTLE_KALSHI_KEY_ID (
  echo ERROR: Kalshi API key ID was not found.
  echo Expected: "%PMBATTLE_CREDENTIAL_DIR%\api key.txt"
  exit /b 1
)

if not defined PMBATTLE_KALSHI_PRIVATE_KEY_PATH (
  echo ERROR: Kalshi private key was not found.
  echo Expected: "%PMBATTLE_CREDENTIAL_DIR%\private key.txt"
  exit /b 1
)

if not exist "%PMBATTLE_KALSHI_PRIVATE_KEY_PATH%" (
  echo ERROR: Kalshi private key file does not exist.
  echo Path: "%PMBATTLE_KALSHI_PRIVATE_KEY_PATH%"
  exit /b 1
)

if not exist "%~dp0pmbattle.exe" (
  echo ERROR: pmbattle.exe was not found beside this launcher.
  exit /b 1
)

echo PMBattle production trading configuration:
echo   Environment: production
echo   Per-order cash-risk cap: $100
echo   Markets: all mapped markets
echo   Credentials: loaded from local files

if /i "%~1"=="--check" (
  echo CHECK ONLY: PMBattle was not started and trading was not enabled.
  exit /b 0
)

echo.
echo WARNING: This starts REAL Kalshi trading. Close any PMBattle process already using port 8080.
echo Press Ctrl+C now to stop, or
pause

"%~dp0pmbattle.exe"

endlocal
