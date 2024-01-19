if not "%MONTAGE_SOURCE%" == "" goto keepgoing
echo You must set the MONTAGE_SOURCE environment variable.
goto getout

:keepgoing

rm -fr %MONTAGE_SOURCE%\ffgl\build\windows\.vs
rm -fr %MONTAGE_SOURCE%\ffgl\build\windows\x64
rm -fr %MONTAGE_SOURCE%\ffgl\build\windows\x86
rm -fr %MONTAGE_SOURCE%\build\windows\ship
rm -fr %MONTAGE_SOURCE%\python\build
rm -fr %MONTAGE_SOURCE%\python\dist

:getout
