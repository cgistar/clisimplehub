#!/bin/bash
# Build script for CliSimpleHub (desktop + headless server).
# Supports macOS/Linux/Windows (amd64/arm64).

set -e

# Load .env file if exists
if [ -f ".env" ]; then
    export $(grep -v '^#' .env | xargs)
fi

VERSION="${VERSION:-}"
OUTPUT_DIR="dist"
BUILD_TAGS=""
SKIP_DEPS=0
WEB_UI_BUILT=0

# Apple signing configuration (from .env or environment variables)
APPLE_SIGN_IDENTITY="${APPLE_SIGN_IDENTITY:-}"
APPLE_ID="${APPLE_ID:-}"
APPLE_ID_PASSWORD="${APPLE_ID_PASSWORD:-}"
APPLE_TEAM_ID="${APPLE_TEAM_ID:-}"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

print_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

append_build_tags() {
    local cur="$1"
    local add="$2"

    add="${add#,}"
    add="${add%,}"
    if [ -z "$add" ]; then
        echo "$cur"
        return 0
    fi
    if [ -z "$cur" ]; then
        echo "$add"
        return 0
    fi
    echo "${cur},${add}"
}

variant_suffix() {
    local tags=",$1,"
    if [[ "$tags" == *",proxy,"* ]]; then
        echo "-proxy"
        return 0
    fi
    echo ""
}

remove_build_tag() {
    local cur="$1"
    local remove="$2"
    local out=""
    IFS=',' read -ra parts <<<"$cur"
    for part in "${parts[@]}"; do
        part="${part// /}"
        if [ -z "$part" ] || [ "$part" = "$remove" ]; then
            continue
        fi
        out="$(append_build_tags "$out" "$part")"
    done
    echo "$out"
}

detect_host_platform() {
    local os
    local arch

    os=$(uname -s | tr '[:upper:]' '[:lower:]')
    arch=$(uname -m)

    case "${arch}" in
        x86_64) arch="amd64" ;;
        aarch64|arm64) arch="arm64" ;;
    esac

    case "${os}" in
        darwin) os="darwin" ;;
        linux) os="linux" ;;
        mingw*|msys*|cygwin*) os="windows" ;;
    esac

    echo "${os}/${arch}"
}

split_platform() {
    local p="$1"
    local os="${p%%/*}"
    local arch="${p##*/}"
    if [ -z "$os" ] || [ -z "$arch" ] || [ "$os" = "$arch" ]; then
        return 1
    fi
    echo "$os" "$arch"
}

# Sign macOS app
sign_macos_app() {
    local app_path=$1

    if [ -z "$APPLE_SIGN_IDENTITY" ]; then
        print_warn "APPLE_SIGN_IDENTITY not set, skipping code signing"
        return 0
    fi

    print_info "Signing app with identity: ${APPLE_SIGN_IDENTITY}"
    codesign --force --deep --sign "$APPLE_SIGN_IDENTITY" --options runtime "$app_path"

    # Verify signature
    codesign --verify --verbose "$app_path"
    print_info "App signed successfully"
}

# Notarize macOS app
notarize_macos_app() {
    local app_path=$1
    local app_name=$(basename "$app_path")

    if [ -z "$APPLE_ID" ] || [ -z "$APPLE_ID_PASSWORD" ] || [ -z "$APPLE_TEAM_ID" ]; then
        print_warn "Apple notarization credentials not set, skipping notarization"
        return 0
    fi

    print_info "Creating zip for notarization..."
    local zip_path="/tmp/${app_name}-notarize.zip"
    ditto -c -k --keepParent "$app_path" "$zip_path"

    print_info "Submitting for notarization (this may take a few minutes)..."
    xcrun notarytool submit "$zip_path" \
        --apple-id "$APPLE_ID" \
        --password "$APPLE_ID_PASSWORD" \
        --team-id "$APPLE_TEAM_ID" \
        --wait

    print_info "Stapling notarization ticket..."
    xcrun stapler staple "$app_path"

    rm -f "$zip_path"
    print_info "Notarization complete"
}

# Check if wails is installed
check_wails() {
    if ! command -v wails &> /dev/null; then
        print_error "Wails CLI not found. Install it with: go install github.com/wailsapp/wails/v2/cmd/wails@latest"
        exit 1
    fi
}

# Install frontend dependencies
install_deps() {
    print_info "Installing frontend dependencies..."
    cd desktop/ui
    npm install
    cd ../..
}

install_webui_deps() {
    print_info "Installing web UI dependencies..."
    cd web/ui
    npm install
    cd ../..
}

build_webui() {
    if [ "$WEB_UI_BUILT" -eq 1 ]; then
        return 0
    fi

    if [ "$SKIP_DEPS" -ne 1 ]; then
        install_webui_deps
    fi

    print_info "Building web UI..."
    cd web/ui
    npm run build
    cd ../..

    WEB_UI_BUILT=1
}

# Check if port 5600 is in use
check_port_5600() {
    if ! command -v lsof &> /dev/null; then
        return 0
    fi
    local pids=$(lsof -ti :5600 2>/dev/null)
    if [ -n "$pids" ]; then
        print_warn "Port 5600 is in use by process(es): $pids"
        print_warn "Not allowed to stop processes? Use a different port for this build/run:"
        print_warn "  PORT=5601 wails build"
        print_warn "Or update the app setting 'port' in ~/.clisimplehub/config.json"
        return 1
    fi
    return 0
}

run_wails_build() {
    local platform="$1"
    local arch="$2"
    local tags="$3"

    local goflags="${GOFLAGS:-}"
    if [[ "$goflags" == *"-tags="* ]]; then
        print_warn "GOFLAGS already contains -tags, overriding for this build"
        goflags="$(echo "$goflags" | sed -E 's/(^| )-tags=[^ ]+//g' | xargs)"
    fi

    local cmd=(wails build -platform "${platform}/${arch}" -clean)
    if [ -n "$tags" ]; then
        cmd+=(-tags "$tags")
    fi

    GOFLAGS="$goflags" "${cmd[@]}"
}

package_desktop() {
    local platform="$1"
    local arch="$2"
    local variant_name="$3"

    mkdir -p "${OUTPUT_DIR}"

    case "${platform}" in
        darwin)
            local app_name="CliSimpleHub.app"
            local archive_path="${OUTPUT_DIR}/${variant_name}${VERSION}-${platform}-${arch}.zip"
            if [ -d "desktop/build/bin/${app_name}" ]; then
                sign_macos_app "desktop/build/bin/${app_name}"
                notarize_macos_app "desktop/build/bin/${app_name}"

                rm -f "${archive_path}"
                cd desktop/build/bin
                zip -r "../../../${archive_path}" "${app_name}"
                cd ../../..
                print_info "Created: ${archive_path}"
                rm -rf "desktop/build/bin/${app_name}"
                print_info "Removed original: desktop/build/bin/${app_name}"
            else
                print_warn "desktop/build/bin/${app_name} not found, skipping packaging"
            fi
            ;;
        linux)
            local bin_name="cliSimpleHub"
            if [ ! -f "desktop/build/bin/${bin_name}" ] && [ -f "desktop/build/bin/CliSimpleHub" ]; then
                bin_name="CliSimpleHub"
            fi
            if [ -f "desktop/build/bin/${bin_name}" ]; then
                tar -czf "${OUTPUT_DIR}/${variant_name}${VERSION}-${platform}-${arch}.tar.gz" -C desktop/build/bin "${bin_name}"
                print_info "Created: ${OUTPUT_DIR}/${variant_name}${VERSION}-${platform}-${arch}.tar.gz"
                rm -f "desktop/build/bin/${bin_name}"
                print_info "Removed original: desktop/build/bin/${bin_name}"
            else
                print_warn "desktop/build/bin/${bin_name} not found, skipping packaging"
            fi
            ;;
        windows)
            local exe_name="cliSimpleHub.exe"
            local archive_path="${OUTPUT_DIR}/${variant_name}${VERSION}-${platform}-${arch}.zip"
            if [ ! -f "desktop/build/bin/${exe_name}" ] && [ -f "desktop/build/bin/CliSimpleHub.exe" ]; then
                exe_name="CliSimpleHub.exe"
            fi
            if [ -f "desktop/build/bin/${exe_name}" ]; then
                rm -f "${archive_path}"
                zip -j "${archive_path}" "desktop/build/bin/${exe_name}"
                print_info "Created: ${archive_path}"
                rm -f "desktop/build/bin/${exe_name}"
                print_info "Removed original: desktop/build/bin/${exe_name}"
            else
                print_warn "desktop/build/bin/${exe_name} not found, skipping packaging"
            fi
            ;;
        *)
            print_warn "Unknown desktop platform: ${platform}"
            ;;
    esac
}

build_desktop_variant() {
    local platform="$1"
    local arch="$2"
    local variant_name="$3"
    local tags="$4"

    print_info "Building desktop variant ${variant_name}: ${platform}/${arch} tags=${tags:-none}"

    cd desktop
    run_wails_build "${platform}" "${arch}" "${tags}"
    cd ..

    package_desktop "${platform}" "${arch}" "${variant_name}"
}

# Build desktop for a specific platform
build_desktop_platform() {
    local platform=$1
    local arch=$2
    local default_tags
    local proxy_tags

    default_tags="$(remove_build_tag "$BUILD_TAGS" "proxy")"
    proxy_tags="$(append_build_tags "$default_tags" "proxy")"

    print_info "Building for ${platform}/${arch}..."

    # Check port availability (warning only, don't block build)
    check_port_5600 || print_warn "Continuing build despite port conflict..."

    build_desktop_variant "${platform}" "${arch}" "cliSimpleHub" "${default_tags}"
    build_desktop_variant "${platform}" "${arch}" "cliSimpleHub-proxy" "${proxy_tags}"
}

package_server_variant() {
    local platform="$1"
    local arch="$2"
    local variant_name="$3"
    local tags="$4"
    local staging_dir="${OUTPUT_DIR}/.staging"

    mkdir -p "${OUTPUT_DIR}"
    mkdir -p "${staging_dir}"

    local ext=""
    if [ "$platform" = "windows" ]; then
        ext=".exe"
    fi

    local base="${variant_name}${VERSION}-${platform}-${arch}"
    local package_bin_name="${variant_name%-proxy}${ext}"
    local bin_path="${staging_dir}/${package_bin_name}"

    local tags_args=()
    if [ -n "$tags" ]; then
        tags_args=(-tags "$tags")
    fi

    print_info "Building server binary: ${variant_name} ${platform}/${arch} tags=${tags:-none}"
    GOOS="$platform" GOARCH="$arch" CGO_ENABLED="${CGO_ENABLED:-0}" go build -trimpath "${tags_args[@]}" -o "$bin_path" ./cmd/server

    if [ "$platform" = "windows" ]; then
        local archive_path="${OUTPUT_DIR}/${base}.zip"
        rm -f "${archive_path}"
        zip -j "${archive_path}" "$bin_path"
        print_info "Created: ${archive_path}"
    else
        tar -czf "${OUTPUT_DIR}/${base}.tar.gz" -C "$staging_dir" "${package_bin_name}"
        print_info "Created: ${OUTPUT_DIR}/${base}.tar.gz"
    fi

    rm -f "$bin_path"
    rmdir "$staging_dir" 2>/dev/null || true
}

build_server_platform() {
    local platform="$1"
    local arch="$2"
    local default_tags
    local proxy_tags

    build_webui

    default_tags="$(remove_build_tag "$BUILD_TAGS" "proxy")"
    proxy_tags="$(append_build_tags "$default_tags" "proxy")"

    package_server_variant "$platform" "$arch" "cliSimpleHub-server" "$default_tags"
    package_server_variant "$platform" "$arch" "cliSimpleHub-server-proxy" "$proxy_tags"
}

# Show usage
usage() {
    echo "Usage: $0 [desktop|server|both] [command] [options]"
    echo ""
    echo "Commands:"
    echo "  current     Build for current platform only (default)"
    echo "  macos       Build for macOS (amd64 and arm64)"
    echo "  linux       Build for Linux (amd64 and arm64)"
    echo "  windows     Build for Windows (amd64 and arm64)"
    echo "  all         Build for all platforms"
    echo "  clean       Clean build artifacts"
    echo ""
    echo "Options:"
    echo "  -v, --version VERSION    Set version string (default: dev)"
    echo "  --tags TAG,LIST          Additional Go build tags (comma-separated)"
    echo "  --platform OS/ARCH       Build a specific platform (repeatable)"
    echo "  --no-deps                Skip npm install (desktop/web UI only)"
    echo ""
    echo "Environment variables for macOS signing:"
    echo "  APPLE_SIGN_IDENTITY      Code signing identity (e.g., 'Developer ID Application: Name (TEAM_ID)')"
    echo "  APPLE_ID                 Apple ID email for notarization"
    echo "  APPLE_ID_PASSWORD        App-specific password for notarization"
    echo "  APPLE_TEAM_ID            Apple Developer Team ID"
    echo ""
    echo "Examples:"
    echo "  $0                                 # desktop current"
    echo "  $0 linux                           # desktop linux + desktop linux-proxy"
    echo "  $0 server current                  # server current + server current-proxy"
    echo "  $0 both --platform linux/amd64     # desktop+server linux/amd64"
}

# Clean build artifacts
clean() {
    print_info "Cleaning build artifacts..."
    rm -rf "${OUTPUT_DIR}"
    rm -rf desktop/build/bin
    rm -rf internal/proxy/webui/dist
    print_info "Clean complete"
}

# Build selected targets/platforms
build_targets() {
    local target="$1"
    shift

    local platforms=("$@")
    local host_platform
    host_platform="$(detect_host_platform)"

    if [ "${#platforms[@]}" -eq 0 ]; then
        print_error "No platforms selected"
        exit 1
    fi

    if [ "$target" = "desktop" ] || [ "$target" = "both" ]; then
        check_wails
        if [ "$SKIP_DEPS" -ne 1 ]; then
            install_deps
        fi
        for p in "${platforms[@]}"; do
            read -r os arch <<<"$(split_platform "$p")"
            if [ -z "$os" ] || [ -z "$arch" ]; then
                print_error "Invalid --platform: $p (expected OS/ARCH)"
                exit 1
            fi
            if [ "$os" != "${host_platform%%/*}" ]; then
                print_warn "Desktop cross-build: host=${host_platform} target=${os}/${arch} (may require extra setup)"
            fi
            build_desktop_platform "$os" "$arch"
        done
    fi

    if [ "$target" = "server" ] || [ "$target" = "both" ]; then
        for p in "${platforms[@]}"; do
            read -r os arch <<<"$(split_platform "$p")"
            if [ -z "$os" ] || [ -z "$arch" ]; then
                print_error "Invalid --platform: $p (expected OS/ARCH)"
                exit 1
            fi
            build_server_platform "$os" "$arch"
        done
    fi
}

# Parse arguments
TARGET="desktop"
MODE=""
PLATFORMS=()

while [[ $# -gt 0 ]]; do
    case $1 in
        -v|--version)
            VERSION="-$2"
            shift 2
            ;;
        desktop|server|both)
            TARGET="$1"
            shift
            ;;
        current)
            MODE="current"
            shift
            ;;
        macos)
            MODE="macos"
            shift
            ;;
        linux)
            MODE="linux"
            shift
            ;;
        windows)
            MODE="windows"
            shift
            ;;
        all)
            MODE="all"
            shift
            ;;
        clean)
            MODE="clean"
            shift
            ;;
        --platform)
            PLATFORMS+=("$2")
            shift 2
            ;;
        --tags)
            BUILD_TAGS="$(append_build_tags "$BUILD_TAGS" "$2")"
            shift 2
            ;;
        --no-deps)
            SKIP_DEPS=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            print_error "Unknown option: $1"
            usage
            exit 1
            ;;
    esac
done

# Default mode
MODE="${MODE:-current}"

if [ "${MODE}" = "clean" ]; then
    clean
    exit 0
fi

if [ "${#PLATFORMS[@]}" -eq 0 ]; then
    host_platform="$(detect_host_platform)"
    case "${MODE}" in
        current)
            PLATFORMS+=("$host_platform")
            ;;
        macos)
            PLATFORMS+=("darwin/amd64" "darwin/arm64")
            ;;
        linux)
            PLATFORMS+=("linux/amd64" "linux/arm64")
            ;;
        windows)
            PLATFORMS+=("windows/amd64" "windows/arm64")
            ;;
        all)
            if [ "$TARGET" = "desktop" ] || [ "$TARGET" = "both" ]; then
                PLATFORMS+=("darwin/amd64" "darwin/arm64" "linux/amd64" "linux/arm64")
                print_warn "Desktop 'all' excludes Windows by default (cross-build requires extra setup). Use: $0 desktop windows"
            else
                PLATFORMS+=("darwin/amd64" "darwin/arm64" "linux/amd64" "linux/arm64" "windows/amd64" "windows/arm64")
            fi
            ;;
        *)
            print_error "Unknown command: ${MODE}"
            usage
            exit 1
            ;;
    esac
fi

build_targets "$TARGET" "${PLATFORMS[@]}"

print_info "Build complete! Output in: ${OUTPUT_DIR}/"
