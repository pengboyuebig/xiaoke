#!/usr/bin/env bash
# 小柯 · 一键构建启动
# 传 --rebuild 强制重建前后端
set -e
cd "$(dirname "$0")"

if [ "$1" = "--rebuild" ]; then
  echo "[小柯] 强制重建..."
  rm -f xiaoke.exe xiaoke
  rm -rf web/dist
fi

if [ ! -f web/dist/index.html ]; then
  echo "[小柯] 构建前端..."
  (cd web && npm install && npm run build)
fi

if [ ! -f xiaoke.exe ] && [ ! -f xiaoke ]; then
  echo "[小柯] 编译后端..."
  go build -o xiaoke.exe . || go build -o xiaoke .
fi

echo "[小柯] 启动: http://127.0.0.1:18790"
if [ -f xiaoke.exe ]; then
  exec ./xiaoke.exe
else
  exec ./xiaoke
fi
