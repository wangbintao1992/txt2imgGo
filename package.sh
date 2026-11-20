#!/usr/bin/env bash
#
# package.sh —— 把 TXT2Img 打包成可独立运行的 macOS 应用（.app / .zip / .dmg）
#
# 为什么需要这个脚本：
#   * go-fitz 依赖 MuPDF 静态库，必须开 CGO（CGO_ENABLED=1），否则运行时报
#     "找不到 libmupdf.dylib"；
#   * 直接 `go build` 出来的裸二进制没有 Info.plist，双击后没有 Dock 图标、
#     没有菜单栏，退出后还容易在后台留下进程；
#   * 打包成 .app 后 CFBundleExecutable / 图标 / 签名都齐全，是一个真正的
#     独立可执行文件，别人机器上不用装 Go、不用装任何依赖。
#
# 用法：
#   ./package.sh                  # 打 universal（arm64 + x86_64）zip 包
#   ./package.sh --dmg            # 同时生成 dmg 安装镜像
#   ./package.sh -p darwin/arm64  # 只打 Apple Silicon
#   ./package.sh --verify-run     # 打完做启动冒烟测试，检查有无残留进程
#   ./package.sh --open           # 打完直接打开
#   ./package.sh --stop           # 结束所有残留的 TXT2Img 进程后退出
#   ./package.sh --clean          # 先清掉 dist/ 再打包
#
# 产物：
#   dist/TXT2Img-<version>-macos-<arch>.zip   解压即用的 .app
#   dist/TXT2Img-<version>-macos-<arch>.dmg   （加 --dmg 时）
#
set -euo pipefail

# ---------------------------------------------------------------- 注意事项
#
# 变量展开后面紧跟中文时一律写成 "${VAR}"，不能写 $VAR：
# macOS 自带 bash 3.2，在 C locale（LANG=""/LC_CTYPE="C"）下按字节解析，
# 会把紧跟的 UTF-8 中文字节当成变量名的一部分，报
# "APP_VERSION（: unbound variable" 之类的错。
# 全角括号 （） 尤其容易踩到。
#

# ---------------------------------------------------------------- 基础路径

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
cd "$SCRIPT_DIR"

# ---------------------------------------------------------------- 输出样式

if [ -t 1 ] && [ "${NO_COLOR:-}" = "" ]; then
	C_RESET=$'\033[0m'; C_BOLD=$'\033[1m'; C_DIM=$'\033[2m'
	C_OK=$'\033[32m'; C_WARN=$'\033[33m'; C_ERR=$'\033[31m'; C_STEP=$'\033[36m'
else
	C_RESET=""; C_BOLD=""; C_DIM=""; C_OK=""; C_WARN=""; C_ERR=""; C_STEP=""
fi

step() { printf '%s==>%s %s\n' "$C_STEP" "$C_RESET" "$*"; }
ok()   { printf '%s  ✓%s %s\n' "$C_OK" "$C_RESET" "$*"; }
warn() { printf '%s  !%s %s\n' "$C_WARN" "$C_RESET" "$*" >&2; }
die()  { printf '%s  ✗%s %s\n' "$C_ERR" "$C_RESET" "$*" >&2; exit 1; }

# ---------------------------------------------------------------- 默认参数

PLATFORM="darwin/universal"
OUT_DIR="dist"
MAKE_DMG=0
MAKE_ZIP=1
DO_CLEAN=0
DO_OPEN=0
DO_VERIFY=0
STOP_ONLY=0

usage() {
	sed -n '/^# package.sh/,/^set -euo/p' "${BASH_SOURCE[0]}" | sed '$d' | sed 's/^# \{0,1\}//'
	exit 0
}

while [ $# -gt 0 ]; do
	case "$1" in
		-p|--platform) PLATFORM="${2:-}"; [ -n "$PLATFORM" ] || die "--platform 需要一个值，例如 darwin/arm64"; shift 2 ;;
		-o|--out)      OUT_DIR="${2:-}";  [ -n "$OUT_DIR" ]  || die "--out 需要一个目录"; shift 2 ;;
		--dmg)         MAKE_DMG=1; shift ;;
		--no-zip)      MAKE_ZIP=0; shift ;;
		--clean)       DO_CLEAN=1; shift ;;
		--open)        DO_OPEN=1; shift ;;
		--verify-run)  DO_VERIFY=1; shift ;;
		--stop)        STOP_ONLY=1; shift ;;
		-h|--help)     usage ;;
		*)             die "未知参数：${1}（用 --help 看用法）" ;;
	esac
done

# ---------------------------------------------------------------- 读取配置

read_config() { # read_config <key> <fallback>
	if command -v plutil >/dev/null 2>&1 && [ -f mygo.json ]; then
		plutil -extract "$1" raw mygo.json 2>/dev/null || printf '%s' "$2"
	else
		printf '%s' "$2"
	fi
}

APP_NAME=$(read_config name "TXT2Img")
APP_VERSION=$(read_config version "0.0.0")
APP_ID=$(read_config identifier "com.example.app")
# 平台 → 产物后缀：darwin/universal → macos-universal
ARCH_SUFFIX=$(printf '%s' "$PLATFORM" | tr '/' '-' | sed 's/^darwin-/macos-/; s/^windows-/windows-/; s/^linux-/linux-/')

EXEC_PATH_RE=""   # 打包后填，用于找残留进程

# ---------------------------------------------------------------- 进程清理

# kill_stray 结束所有指向本项目的 TXT2Img 进程（含裸二进制、.app 内的、dist 里的）。
# 先 TERM 让它走正常退出流程，1 秒后还在就 KILL。
kill_stray() {
	local pids
	pids=$(pgrep -f "$SCRIPT_DIR" 2>/dev/null | while read -r p; do
		# 只挑本项目的可执行进程，别误伤 grep / 脚本自身
		if ps -p "$p" -o command= 2>/dev/null | grep -Eq "(/$APP_NAME|txt2imgGo|/txt2img)( |$)"; then
			printf '%s\n' "$p"
		fi
	done || true)

	if [ -z "$pids" ]; then
		return 0
	fi

	warn "发现残留进程：$(printf '%s ' $pids)"
	kill $pids 2>/dev/null || true
	sleep 1
	local left
	left=$(printf '%s\n' $pids | while read -r p; do kill -0 "$p" 2>/dev/null && printf '%s\n' "$p"; done || true)
	if [ -n "$left" ]; then
		warn "强制结束：$(printf '%s ' $left)"
		kill -9 $left 2>/dev/null || true
		sleep 0.5
	fi
	ok "残留进程已清理"
}

if [ "$STOP_ONLY" -eq 1 ]; then
	step "结束残留进程"
	kill_stray
	exit 0
fi

# ---------------------------------------------------------------- 环境检查

step "检查构建环境"

[ "$(uname -s)" = "Darwin" ] || die "此脚本只用于 macOS 打包（当前 $(uname -s)）"
command -v go >/dev/null 2>&1 || die "未找到 go，请先安装 Go 1.27.3+（可用 asdf 按 .tool-versions 安装）"
command -v xcrun >/dev/null 2>&1 || die "未找到 xcrun，请安装 Xcode Command Line Tools：xcode-select --install"

GO_VERSION=$(go version | awk '{print $3}')
GO_ARCH=$(go env GOARCH)

# CGO 是硬性要求：go-fitz 的 MuPDF 是静态库，靠 cgo 链接进来。
if [ "$(go env CGO_ENABLED)" != "1" ]; then
	export CGO_ENABLED=1
	warn "CGO_ENABLED 默认关闭，已强制设为 1（go-fitz 必须）"
fi
CC_BIN=$(go env CC); [ -n "$CC_BIN" ] || CC_BIN=clang
command -v "$CC_BIN" >/dev/null 2>&1 || die "未找到 C 编译器 ${CC_BIN}（cgo 需要）"

ok "Go $GO_VERSION · $(go env GOOS)/$GO_ARCH"
ok "CC = $CC_BIN"

# 需要 Xcode 的 lipo / codesign / iconutil / ditto / hdiutil
for tool in lipo codesign ditto; do
	command -v "$tool" >/dev/null 2>&1 || die "缺少 ${tool}（Xcode Command Line Tools）"
done
if [ "$MAKE_DMG" -eq 1 ]; then
	command -v hdiutil >/dev/null 2>&1 || die "缺少 hdiutil，无法生成 dmg"
fi

# ---------------------------------------------------------------- 前置清理

step "清理旧进程与旧产物"
kill_stray
if [ "$DO_CLEAN" -eq 1 ]; then
	rm -rf "$OUT_DIR"
	ok "已删除 $OUT_DIR"
fi
mkdir -p "$OUT_DIR"

# ---------------------------------------------------------------- 开始构建

step "编译 $APP_NAME ${APP_VERSION}（${PLATFORM}）"

BUILD_FLAGS=()
[ "$MAKE_DMG" -eq 1 ] || BUILD_FLAGS+=("-skip-dmg")

# go tool mygo build 会：
#   * 关掉 inspector（-tags mygo_noinspector）
#   * 注入 -X github.com/egoist/mygo.production=1（开发特性关闭）
#   * 生成 Info.plist / PkgInfo / AppIcon.icns / Assets.car
#   * 把 resources/ 复制进 Contents/Resources
#   * 用 ad-hoc 签名签好整个 bundle
BUILD_START=$(date +%s)
# 注意：macOS 自带 bash 3.2，set -u 下展开空数组会报错，要用 ${arr[@]+...} 形式
if ! CGO_ENABLED=1 go tool mygo build -platform "$PLATFORM" -o "$OUT_DIR" ${BUILD_FLAGS[@]+"${BUILD_FLAGS[@]}"}; then
	die "构建失败"
fi
BUILD_SECS=$(( $(date +%s) - BUILD_START ))

# mygo 把产物放在 <out>/<goos>-<goarch>/
BUILD_DIR="$OUT_DIR/$(printf '%s' "$PLATFORM" | tr '/' '-')"
[ -d "$BUILD_DIR" ] || die "构建产物目录不存在：$BUILD_DIR"
APP_BUNDLE="$BUILD_DIR/$APP_NAME.app"
[ -d "$APP_BUNDLE" ] || die "未生成 $APP_NAME.app"
EXECUTABLE="$APP_BUNDLE/Contents/MacOS/$APP_NAME"
[ -f "$EXECUTABLE" ] || die "bundle 里没有可执行文件 $APP_NAME"
EXEC_PATH_RE="$EXECUTABLE"

ok "编译完成，用时 ${BUILD_SECS}s"

# ---------------------------------------------------------------- 产物校验

step "校验产物是否真的可独立运行"

# 1) 架构
ARCH_INFO=$(lipo -archs "$EXECUTABLE" 2>/dev/null || echo "unknown")
case "$PLATFORM" in
	darwin/universal)
		case "$ARCH_INFO" in
			*arm64*x86_64*|*x86_64*arm64*) ok "架构：${ARCH_INFO}（universal）" ;;
			*) die "期望 arm64 + x86_64，实际只有 $ARCH_INFO" ;;
		esac ;;
	darwin/arm64)  case "$ARCH_INFO" in *arm64*) ok "架构：$ARCH_INFO" ;; *) die "架构不符：$ARCH_INFO" ;; esac ;;
	darwin/amd64)  case "$ARCH_INFO" in *x86_64*) ok "架构：$ARCH_INFO" ;; *) die "架构不符：$ARCH_INFO" ;; esac ;;
esac

# 2) 动态库依赖：只允许系统库，出现任何第三方 .dylib 就说明不能独立运行
#    universal 二进制的 otool 输出会带 "(architecture xxx):" 分节标题行，要滤掉
BAD_DEPS=$(otool -L "$EXECUTABLE" | tail -n +2 |
	grep -v '(architecture ' | awk '{print $1}' |
	grep -Ev '^$|^(/usr/lib/|/System/Library/|@rpath/libswift|@executable_path/|@loader_path/)' || true)
if [ -n "$BAD_DEPS" ]; then
	warn "发现非系统依赖，可能无法独立运行："
	printf '%s\n' "$BAD_DEPS" | sed 's/^/      /' >&2
	die "请检查 cgo 链接参数（MuPDF 应静态链接）"
fi
ok "依赖仅系统库（MuPDF 已静态链接）"

# 3) 代码签名（ad-hoc 也算，至少保证没被改坏）
if codesign --verify --deep --strict "$APP_BUNDLE" 2>/dev/null; then
	SIGN_INFO=$(codesign -dv "$APP_BUNDLE" 2>&1 | awk -F= '/^Authority|^Signature/{print $2}' | paste -sd' ' -)
	ok "签名有效：${SIGN_INFO:-adhoc}"
else
	die "签名校验失败：codesign --verify 不通过"
fi

# 4) Info.plist 关键字段
for key in CFBundleExecutable CFBundleIdentifier CFBundleShortVersionString; do
	val=$(plutil -extract "$key" raw "$APP_BUNDLE/Contents/Info.plist" 2>/dev/null || true)
	[ -n "$val" ] || die "Info.plist 缺少 $key"
done
ok "Info.plist 完整（$APP_ID ${APP_VERSION}）"

# 5) 体积
SIZE=$(du -sh "$APP_BUNDLE" | awk '{print $1}')
ok "bundle 体积：$SIZE"

# ---------------------------------------------------------------- 启动冒烟测试

verify_run() {
	step "启动冒烟测试"
	kill_stray

	open "$APP_BUNDLE"
	sleep 3

	local pids count
	pids=$(pgrep -f "$EXECUTABLE" 2>/dev/null || true)
	count=$(printf '%s' "$pids" | grep -c . || true)
	if [ "$count" -eq 0 ]; then
		die "启动后没有找到进程，应用可能秒退（可手动双击 $APP_BUNDLE 看报错）"
	fi
	ok "应用已启动，进程数：$count"

	# 点窗口关闭按钮，验证关窗即退出、不留后台进程
	osascript -e "tell application \"System Events\" to tell process \"$APP_NAME\" to perform action \"AXPress\" of (first button of window 1 whose subrole is \"AXCloseButton\")" >/dev/null 2>&1 ||
		osascript -e "tell application \"$APP_NAME\" to quit" >/dev/null 2>&1 || true
	sleep 3

	local left
	left=$(pgrep -f "$EXECUTABLE" 2>/dev/null || true)
	if [ -n "$left" ]; then
		warn "关闭窗口后仍有残留进程：$(printf '%s ' $left)"
		kill_stray
		die "关窗后进程没有退出（README 里记录了尊重窗口生命周期的方式）"
	else
		ok "关窗后进程已退出，无残留后台进程"
	fi
}

if [ "$DO_VERIFY" -eq 1 ]; then
	verify_run
fi

# ---------------------------------------------------------------- 打包 zip

if [ "$MAKE_ZIP" -eq 1 ]; then
	step "打包 zip"
	ZIP_NAME="$APP_NAME-$APP_VERSION-$ARCH_SUFFIX.zip"
	ZIP_PATH="$OUT_DIR/$ZIP_NAME"
	rm -f "$ZIP_PATH"
	# ditto 保留扩展属性与签名封条；zip 命令会破坏 .app 的签名
	ditto -c -k --sequesterRsrc --keepParent "$APP_BUNDLE" "$ZIP_PATH"
	ok "${ZIP_PATH}（$(du -sh "$ZIP_PATH" | awk '{print $1}')）"
fi

# ---------------------------------------------------------------- 汇总

step "完成"
printf '\n'
printf '  %s产物%s\n' "$C_BOLD" "$C_RESET"
printf '    %s\n' "$APP_BUNDLE"
if [ "$MAKE_ZIP" -eq 1 ]; then
	printf '    %s\n' "$OUT_DIR/$APP_NAME-$APP_VERSION-$ARCH_SUFFIX.zip"
fi
if [ "$MAKE_DMG" -eq 1 ]; then
	DMG=$(ls -1 "$BUILD_DIR"/*.dmg 2>/dev/null | head -1 || true)
	[ -n "$DMG" ] && printf '    %s\n' "$DMG"
fi
printf '\n'
printf '  %s提示%s\n' "$C_BOLD" "$C_RESET"
printf '    · 双击 .app 即可运行，不需要 Go 环境和任何依赖\n'
printf '    · 首次打开若被 Gatekeeper 拦截：右键 → 打开，或执行\n'
printf '        xattr -dr com.apple.quarantine "%s"\n' "$APP_BUNDLE"
printf '    · 分发到别人机器：直接发 zip，对方解压即用\n'
printf '    · 关闭后若有残留进程：./package.sh --stop\n'
printf '    · Word 转 PDF 仍需目标机器装 LibreOffice（这是外部程序，不随包分发）\n'
printf '\n'

if [ "$DO_OPEN" -eq 1 ]; then
	open "$APP_BUNDLE"
fi
