@echo off

c:/windows/system32/taskkill /F /IM montage_gui.exe > nul 2>&1

set logdir=%LOCALAPPDATA%\Montage\logs

echo > "%logdir%\gui.log"
echo > "%logdir%\gui.stdout"
echo > "%logdir%\gui.stderr"
start /b "" "%MONTAGE%\bin\pyinstalled\montage_gui.exe" > "%logdir%\gui.stdout" 2> "%logdir%\gui.stderr"
