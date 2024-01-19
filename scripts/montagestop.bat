@echo off
c:/windows/system32/taskkill /F /IM montage_gui.exe > nul 2>&1
c:/windows/system32/taskkill /F /IM montage_engine.exe > nul 2>&1
c:/windows/system32/taskkill /F /IM __debug_bin.exe > nul 2>&1
