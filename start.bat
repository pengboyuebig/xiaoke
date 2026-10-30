@echo off
chcp 65001 >nul
title 小柯
cd /d "%~dp0"

rem 传 --rebuild 强制重建前后端（改了源码但界面/二进制没更新时用）
if /I "%~1"=="--rebuild" (
  echo [小柯] 强制重建...
  if exist "xiaoke.exe" del /q "xiaoke.exe"
  if exist "web\dist" rmdir /s /q "web\dist"
)

if not exist "web\dist\index.html" (
  echo [小柯] 构建前端...
  pushd web
  call npm install || goto :err
  call npm run build || goto :err
  popd
)

if not exist "xiaoke.exe" (
  echo [小柯] 编译后端...
  go build -o xiaoke.exe . || goto :err
)

echo [小柯] 启动中... 打开 http://127.0.0.1:18790
start "" http://127.0.0.1:18790
xiaoke.exe
goto :eof

:err
echo [小柯] 构建失败，请检查上面的错误信息。
pause
