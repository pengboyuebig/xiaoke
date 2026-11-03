#!/usr/bin/env bash
#
# 小柯发版脚本
# ---------------------------------------------------------------
# 做五件事：
#   1. 计算新版本号（默认 patch +1；用 -v 指定则直接用指定值）
#   2. 先跑后端测试（可选前端构建），失败则不动仓库
#   3. 用「上个 tag..HEAD」的 git 提交记录重写 internal/version/version.go
#   4. 提交版本改动 → 合并到 main（--no-ff）→ 打 tag
#   5. 原子推送 dev / main / tag，最后切回 dev
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
command -v go >/dev/null || die "需要 go（发版前会跑测试）"

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
  # 10# 前缀避免 0.0.08 被 bash 当八进制解析
  V_MAJOR=$((10#$V_MAJOR))
  V_MINOR=$((10#$V_MINOR))
  V_PATCH=$((10#$V_PATCH))
  NEXT_VERSION="$V_MAJOR.$V_MINOR.$((V_PATCH + 1))"
fi

if git rev-parse --verify "refs/tags/v$NEXT_VERSION" >/dev/null 2>&1; then
  die "标签 v$NEXT_VERSION 已存在：上次发版可能中断了。请先确认远程状态并手动清理本地 tag/版本提交。"
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

# 提交标题 → Markdown 列表项；反引号替换掉（避免破坏 Go 原始字符串）。
# 用 --max-count 而不是 head，避免管道提前关闭触发 SIGPIPE。
NOTES_BODY="$(
  git log --no-merges --max-count="$MAX_NOTES" --pretty=format:'- %s' "$RANGE" \
    | sed 's/`/'"'"'/g'
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

# ---------- 同步远程（避免 push 时才失败） ----------
git fetch origin --tags || die "拉取远程失败，请检查网络/SSH"
ORIGIN_MAIN=""
if git rev-parse --verify "origin/$TARGET_BRANCH" >/dev/null 2>&1; then
  ORIGIN_MAIN="$(git rev-parse "origin/$TARGET_BRANCH")"
fi
if git rev-parse --verify "refs/tags/v$NEXT_VERSION" >/dev/null 2>&1; then
  die "远程已存在标签 v$NEXT_VERSION：上次发版可能中断了。请先确认并手动清理。"
fi
if git rev-parse --verify "origin/$SOURCE_BRANCH" >/dev/null 2>&1; then
  BEHIND="$(git rev-list --count "HEAD..origin/$SOURCE_BRANCH")"
  [ "$BEHIND" = "0" ] || die "本地 $SOURCE_BRANCH 落后 origin/$SOURCE_BRANCH  $BEHIND 个提交，请先 pull/rebase"
fi

# ---------- 发版前验证（在写版本文件之前，失败不留任何改动） ----------
info "运行后端测试（跳过 projects/ 下的用户项目）…"
PKGS="$(go list ./... | grep -v '/projects/' || true)"
[ -n "$PKGS" ] || die "没有找到可测试的 Go 包"
# shellcheck disable=SC2086
go test -count=1 $PKGS
if [ -d web/node_modules ]; then
  info "构建前端…"
  (cd web && npm run build)
else
  info "web/node_modules 不存在，跳过前端构建（部署前请自行 npm ci && npm run build）"
fi
[ -z "$(git status --porcelain)" ] || die "验证过程产生了未提交改动，请先检查工作区"

# ---------- 失败兜底：不要把仓库留在 main ----------
BASE_HEAD="$(git rev-parse HEAD)"
RELEASE_COMMITTED=0
TAG_CREATED=0
PUSHED=0
cleanup() {
  local code=$?
  if [ "$code" -eq 0 ]; then
    return
  fi
  set +e
  echo "✗ 发版中断（退出码 $code）。" >&2
  local branch
  branch="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
  if [ "$branch" = "$TARGET_BRANCH" ]; then
    if git checkout -q "$SOURCE_BRANCH" 2>/dev/null; then
      echo "  已切回 $SOURCE_BRANCH。" >&2
      branch="$SOURCE_BRANCH"
    fi
  fi
  if [ "$PUSHED" = "1" ]; then
    echo "  远程已推送成功；本地只需确认分支状态，不要回滚。" >&2
    return
  fi
  if [ "$RELEASE_COMMITTED" = "1" ]; then
    if [ "$branch" = "$SOURCE_BRANCH" ]; then
      echo "  本地可能已产生版本提交；确认远程未成功后可用以下命令恢复：" >&2
      echo "    git tag -d v$NEXT_VERSION 2>/dev/null || true" >&2
      echo "    git reset --hard $BASE_HEAD" >&2
      if [ -n "$ORIGIN_MAIN" ]; then
        echo "    git branch -f $TARGET_BRANCH $ORIGIN_MAIN" >&2
      fi
    else
      echo "  当前仍在 $branch（可能有未完成 merge）；请先手动 git merge --abort / git checkout $SOURCE_BRANCH，再处理本地 tag 与版本提交。" >&2
    fi
  fi
  if [ "$TAG_CREATED" = "1" ]; then
    echo "  本地 tag v$NEXT_VERSION 已创建；远程 push 是原子的，失败时不会留下半推送状态。" >&2
  fi
}
trap cleanup EXIT

# ---------- 写版本文件 ----------
# 用 printf 拼接而不是未加引号的 heredoc：提交标题里的 $、\ 不会被 shell 展开。
{
  cat <<'EOF'
// Package version 存放当前版本号与本次版本的更新说明。
//
// 这个文件由 scripts/release.sh 自动改写（升级版本 + 用 git 提交记录重写说明），
// 手工改动会被下次发版覆盖。版本号规则：合并到 main 时 patch 位 +1；
// 需要用 minor/major 时传 -v（例如 ./scripts/release.sh -v 0.1.0）。
package version

EOF
  printf '// Version 当前版本号（语义化：主.次.修订）。\nconst Version = "%s"\n\n' "$NEXT_VERSION"
  printf '// ReleaseDate 本次版本的发布日期（YYYY-MM-DD）。\nconst ReleaseDate = "%s"\n\n' "$TODAY"
  printf '// ReleaseNotes 本次版本的更新说明（Markdown）。只保留最新一次，\n'
  printf '// 每次发版整体替换——不做历史累积。\n'
  printf 'const ReleaseNotes = `%s\n`\n' "$NOTES_BODY"
} > "$VERSION_FILE"

gofmt -w "$VERSION_FILE"

git add "$VERSION_FILE"
git commit -q -m "chore(release): v$NEXT_VERSION"
RELEASE_COMMITTED=1
info "已提交版本改动"

# ---------- 合并到 main ----------
git checkout -q "$TARGET_BRANCH"
# 目标分支落后时先快进到远程；分叉则直接失败，避免把本地 main 推成非快进
if [ -n "$ORIGIN_MAIN" ]; then
  git merge --ff-only "$ORIGIN_MAIN" >/dev/null 2>&1 || die "本地 $TARGET_BRANCH 与 origin/$TARGET_BRANCH 分叉，请先手动处理"
fi
git merge --no-ff "$SOURCE_BRANCH" -m "merge: v$NEXT_VERSION（$SOURCE_BRANCH → $TARGET_BRANCH）"
info "已合并 $SOURCE_BRANCH → $TARGET_BRANCH"

git tag -a "v$NEXT_VERSION" -m "v$NEXT_VERSION"
TAG_CREATED=1
info "已打标签 v$NEXT_VERSION"

# ---------- 原子推送 ----------
# --atomic：三个 ref 要么全成功，要么全失败，避免「dev 推上去了但 main/tag 没上去」。
git push --atomic origin "$SOURCE_BRANCH" "$TARGET_BRANCH" "refs/tags/v$NEXT_VERSION"
PUSHED=1
info "已推送 dev / main / v$NEXT_VERSION"

# ---------- 切回开发分支 ----------
git checkout -q "$SOURCE_BRANCH"
info "已切回 $SOURCE_BRANCH"

echo
echo "✓ 发版完成：v$NEXT_VERSION（$(printf '%s\n' "$NOTES_BODY" | wc -l | tr -d ' ') 条更新说明）"
echo "  下次不带参数运行本脚本会升级到 v$(echo "$NEXT_VERSION" | awk -F. '{print $1"."$2"."$3+1}')"
