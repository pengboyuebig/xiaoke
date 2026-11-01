#!/usr/bin/env bash
#
# 小柯发版脚本
# ---------------------------------------------------------------
# 做四件事：
#   1. 计算新版本号（默认 patch +1；用 -v 指定则直接用指定值）
#   2. 用「上个 tag..HEAD」的 git 提交记录重写 internal/version/version.go
#      （说明整体替换，不累积历史）
#   3. 提交版本改动 → 合并到 main（--no-ff）→ 打 tag
#   4. 推送 dev / main / tags，最后切回 dev
#
# 用法：
#   ./scripts/release.sh              # 当前 0.0.1 → 0.0.2
#   ./scripts/release.sh -v 0.1.1     # 直接发 0.1.1（下次默认 0.1.2）
#   ./scripts/release.sh -n           # 只预览，不改任何东西
#
# 约定：日常开发在 dev，发版合到 main。脚本只认这两个分支名，
# 在别的分支上会拒绝执行（避免误合并）。
# ---------------------------------------------------------------
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

SOURCE_BRANCH="dev"
TARGET_BRANCH="main"
VERSION_FILE="internal/version/version.go"
MAX_NOTES=60      # 更新说明最多列多少条提交

EXPLICIT=""
DRY_RUN=0
while getopts "v:nh" opt; do
  case $opt in
    v) EXPLICIT="$OPTARG" ;;
    n) DRY_RUN=1 ;;
    h) sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "用法: $0 [-v x.y.z] [-n]" >&2; exit 1 ;;
  esac
done

die() { echo "✗ $*" >&2; exit 1; }
info() { echo "· $*"; }

# ---------- 前置检查 ----------
[ -f "$VERSION_FILE" ] || die "找不到 $VERSION_FILE"
command -v git >/dev/null || die "需要 git"

CURRENT_BRANCH="$(git rev-parse --abbrev-ref HEAD)"
[ "$CURRENT_BRANCH" = "$SOURCE_BRANCH" ] || die "当前在 $CURRENT_BRANCH，请先切回 $SOURCE_BRANCH"

if [ -n "$(git status --porcelain)" ]; then
  die "工作区不干净，请先提交或暂存改动
$(git status --short)"
fi

# ---------- 版本号 ----------
CUR_VERSION="$(sed -n 's/^const Version = "\(.*\)"$/\1/p' "$VERSION_FILE" | head -1)"
[ -n "$CUR_VERSION" ] || die "无法从 $VERSION_FILE 读出当前版本号"

if [ -n "$EXPLICIT" ]; then
  echo "$EXPLICIT" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$' \
    || die "-v 需要形如 0.1.1 的版本号，收到: $EXPLICIT"
  NEXT_VERSION="$EXPLICIT"
else
  IFS='.' read -r V_MAJOR V_MINOR V_PATCH <<< "$CUR_VERSION"
  NEXT_VERSION="$V_MAJOR.$V_MINOR.$((V_PATCH + 1))"
fi

# ---------- 更新说明（上一次发版 tag 到 HEAD） ----------
# 注意：tag 打在 main 的合并提交上，而脚本在 dev 上跑，
# git describe 找不到「指向 main 的 tag」，所以按版本号取最高的 tag。
# vX..HEAD 的语义是「HEAD 有、tag 没有的提交」，与祖先关系无关，正好可用。
LAST_TAG="$(git tag --list 'v[0-9]*' --sort=-v:refname | head -1)"
if [ -n "$LAST_TAG" ] && git rev-parse --verify "$LAST_TAG" >/dev/null 2>&1; then
  RANGE="$LAST_TAG..HEAD"
  info "更新说明范围：$LAST_TAG → HEAD"
else
  RANGE="HEAD"
  info "没有历史 tag，更新说明取全部提交（最多 $MAX_NOTES 条）"
fi

# 提交标题 → Markdown 列表项；反引号替换掉（避免破坏 Go 原始字符串）
NOTES_BODY="$(
  git log --no-merges --pretty=format:'- %s' "$RANGE" \
    | sed 's/`/'"'"'/g' \
    | head -n "$MAX_NOTES"
)"
[ -n "$NOTES_BODY" ] || NOTES_BODY="- 本次发版以修复与维护为主。"

TODAY="$(date +%F)"

# ---------- 预览 ----------
echo
echo "版本：$CUR_VERSION → $NEXT_VERSION"
echo "日期：$TODAY"
echo "提交条数：$(printf '%s\n' "$NOTES_BODY" | wc -l | tr -d ' ')"
echo "---- 更新说明 ----"
printf '%s\n' "$NOTES_BODY"
echo "------------------"
echo

if [ "$DRY_RUN" = "1" ]; then
  info "预览模式（-n），未做任何改动"
  exit 0
fi

# ---------- 写版本文件 ----------
cat > "$VERSION_FILE" <<EOF
// Package version 存放当前版本号与本次版本的更新说明。
//
// 这个文件由 scripts/release.sh 自动改写（升级版本 + 用 git 提交记录重写说明），
// 手工改动会被下次发版覆盖。版本号规则：合并到 main 时 patch 位 +1；
// 需要用 minor/major 时传 -v（例如 ./scripts/release.sh -v 0.1.0）。
package version

// Version 当前版本号（语义化：主.次.修订）。
const Version = "$NEXT_VERSION"

// ReleaseDate 本次版本的发布日期（YYYY-MM-DD）。
const ReleaseDate = "$TODAY"

// ReleaseNotes 本次版本的更新说明（Markdown）。只保留最新一次，
// 每次发版整体替换——不做历史累积。
const ReleaseNotes = \`$NOTES_BODY
\`
EOF

gofmt -w "$VERSION_FILE"

git add "$VERSION_FILE"
git commit -q -m "chore(release): v$NEXT_VERSION"
info "已提交版本改动"

# ---------- 合并到 main ----------
git checkout -q "$TARGET_BRANCH"
# 目标分支落后时先快进到远程，避免推不上去
if git rev-parse --verify "origin/$TARGET_BRANCH" >/dev/null 2>&1; then
  git merge --ff-only "origin/$TARGET_BRANCH" >/dev/null 2>&1 || true
fi
git merge --no-ff "$SOURCE_BRANCH" -m "merge: v$NEXT_VERSION（$SOURCE_BRANCH → $TARGET_BRANCH）"
info "已合并 $SOURCE_BRANCH → $TARGET_BRANCH"

git tag -a "v$NEXT_VERSION" -m "v$NEXT_VERSION"
info "已打标签 v$NEXT_VERSION"

# ---------- 推送 ----------
git push origin "$SOURCE_BRANCH"
git push origin "$TARGET_BRANCH"
git push origin "v$NEXT_VERSION"
info "已推送 dev / main / v$NEXT_VERSION"

# ---------- 切回开发分支 ----------
git checkout -q "$SOURCE_BRANCH"
info "已切回 $SOURCE_BRANCH"

echo
echo "✓ 发版完成：v$NEXT_VERSION（$(printf '%s\n' "$NOTES_BODY" | wc -l | tr -d ' ') 条更新说明）"
echo "  下次不带参数运行本脚本会升级到 v$(echo "$NEXT_VERSION" | awk -F. '{print $1"."$2"."$3+1}')"
