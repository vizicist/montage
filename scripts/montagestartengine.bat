@echo off

c:/windows/system32/taskkill /F /IM montage_engine.exe > nul 2>&1

set logdir=%LOCALAPPDATA%\Montage\logs

echo > "%logdir%\engine.log"
echo > "%logdir%\engine.stdout"
echo > "%logdir%\engine.stderr"
start /b "" "%MONTAGE%\bin\montage_engine.exe" > "%logdir%\engine.stdout" 2> "%logdir%\engine.stderr"
