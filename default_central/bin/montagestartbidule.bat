@echo on
call montagestopbidule.bat
set patch="%LOCALAPPDATA%\Montage\config\montage.bidule"
echo Starting Bidule on %patch%
start /b "" "C:\Program Files\Plogue\Bidule\PlogueBidule_x64.exe" %patch%
