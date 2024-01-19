
@echo off

if not "%MONTAGE_SOURCE%" == "" goto keepgoing
echo You must set the MONTAGE_SOURCE environment variable.
goto getout

:keepgoing

set ship=%MONTAGE_SOURCE%\build\windows\ship
set bin=%ship%\bin
rm -fr %ship% > nul 2>&1
mkdir %ship%
mkdir %bin%

echo ================ Upgrading Python
python -m pip install pip | grep -v "already.*satisfied"
pip install codenamize pip install python-osc pip install asyncio-nats-client pyinstaller get-mac mido pyperclip | grep -v "already satisfied"

echo ================ Creating montage_engine.exe

pushd %MONTAGE_SOURCE%\cmd\montage_engine
go build montage_engine.go > gobuild.out 2>&1
type nul > emptyfile
fc gobuild.out emptyfile > nul
if errorlevel 1 goto notempty
goto continue1
:notempty
echo Error in building montage_engine.exe
cat gobuild.out
popd
goto getout
:continue1
move montage_engine.exe %bin%\montage_engine.exe > nul

popd

echo ================ Creating montage_gui.exe
pushd %MONTAGE_SOURCE%\python
rm -fr dist
pyinstaller -i ..\default\config\montage.ico montage_gui.py > pyinstaller.out 2>&1
pyinstaller testcursor.py > pyinstaller.out 2>&1
pyinstaller osc.py > pyinstaller.out 2>&1

rem merge all the pyinstalled things into one
move dist\montage_gui dist\pyinstalled >nul

rem merge the other executables into that one
move dist\testcursor\testcursor.exe dist\pyinstalled >nul
move dist\osc\osc.exe dist\pyinstalled >nul
move dist\pyinstalled %bin% >nul
popd

echo ================ Compiling FFGL plugin
set MSBUILDCMD=C:\Program Files (x86)\Microsoft Visual Studio\2019\Community\Common7\Tools\vsmsbuildcmd.bat
call "%MSBUILDCMD%" > nul
pushd %MONTAGE_SOURCE%\ffgl\build\windows
msbuild /t:Build /p:Configuration=Debug /p:Platform="x64" FFGLPlugins.sln > nul
popd

echo ================ Copying FFGL plugin
mkdir %ship%\ffgl
pushd %MONTAGE_SOURCE%\ffgl\binaries\x64\Debug
copy Montage*.dll %ship%\ffgl > nul
copy Montage*.pdb %ship%\ffgl > nul
copy %MONTAGE_SOURCE%\build\windows\pthreadvc2.dll %ship%\ffgl >nul
popd

echo ================ Copying binaries
copy %MONTAGE_SOURCE%\binaries\nats\nats-pub.exe %bin% >nul
copy %MONTAGE_SOURCE%\binaries\nats\nats-sub.exe %bin% >nul
copy %MONTAGE_SOURCE%\binaries\nircmdc.exe %bin% >nul

echo ================ Copying scripts
pushd %MONTAGE_SOURCE%\scripts
copy montagestart*.bat %bin% >nul
copy montagestop*.bat %bin% >nul
copy montagetasks.bat %bin% >nul
copy testcursor.bat %bin% >nul
copy osc.bat %bin% >nul
copy ipaddress.bat %bin% >nul
copy taillog.bat %bin% >nul
copy natsmon.bat %bin% >nul
copy delay.bat %bin% >nul

popd

echo ================ Copying config
mkdir %ship%\config
copy %MONTAGE_SOURCE%\default\config\*.json %ship%\config >nul
copy %MONTAGE_SOURCE%\default\config\*.conf %ship%\config >nul
copy %MONTAGE_SOURCE%\default\config\Montage*.avc %ship%\config >nul
copy %MONTAGE_SOURCE%\default\config\Montage.ico %ship%\config >nul

echo ================ Copying midifiles
mkdir %ship%\midifiles
copy %MONTAGE_SOURCE%\default\midifiles\*.* %ship%\midifiles >nul

echo ================ Copying isf files
mkdir %ship%\isf
copy %MONTAGE_SOURCE%\default\isf\*.* %ship%\isf >nul

echo ================ Copying windows-specific things
copy %MONTAGE_SOURCE%\SenselLib\x64\LibSensel.dll %bin% >nul
copy %MONTAGE_SOURCE%\SenselLib\x64\LibSenselDecompress.dll %bin% >nul

echo ================ Copying presets
mkdir %ship%\presets
xcopy /e /y %MONTAGE_SOURCE%\default\presets %ship%\presets > nul

echo ================ Removing unused things
rm -fr %bin%\pyinstalled\tcl\tzdata

call buildsetup.bat

:getout
