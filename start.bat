@echo off
chcp 65001 >nul
title 小柯
cd /d "%~dp0"

if not exist "web\dist\index.html" (
  echo [小柯] 首次运行，构建前端...
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
