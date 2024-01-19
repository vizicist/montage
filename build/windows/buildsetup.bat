echo ================ Creating installer
"c:\Program Files (x86)\Inno Setup 6\ISCC.exe" /Q montage_win_setup.iss
move Output\montage_*_win_setup.exe %MONTAGE_SOURCE%\release >nul
rmdir Output
