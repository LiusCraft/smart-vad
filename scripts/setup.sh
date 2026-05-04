#!/usr/bin/env bash
set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

info()  { echo -e "${CYAN}[INFO]${NC} $1"; }
ok()    { echo -e "${GREEN}[OK]${NC} $1"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
fail()  { echo -e "${RED}[FAIL]${NC} $1"; }

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
MODEL_FILE="$PROJECT_DIR/silero_vad.onnx"

ask() {
    echo -e -n "${CYAN}[?]${NC} $1 [Y/n]: "
    read -r ans
    [[ -z "$ans" || "$ans" =~ ^[Yy] ]]
}

detect_os() {
    case "$(uname -s)" in
        Darwin)  echo "macos" ;;
        Linux)   echo "linux" ;;
        *)       echo "unknown" ;;
    esac
}

check_onnxruntime_osx() {
    if brew list onnxruntime &>/dev/null 2>&1; then
        return 0
    fi
    if otool -L "$PROJECT_DIR/server" 2>/dev/null | grep -q onnxruntime; then
        return 0
    fi
    return 1
}

check_onnxruntime_linux() {
    if ldconfig -p 2>/dev/null | grep -q onnxruntime; then
        return 0
    fi
    if [[ -f /usr/lib/libonnxruntime.so || -f /usr/local/lib/libonnxruntime.so ]]; then
        return 0
    fi
    return 1
}

install_onnxruntime_macos() {
    if ! command -v brew &>/dev/null; then
        fail "Homebrew not found. Install it first: https://brew.sh"
        return 1
    fi
    info "Installing onnxruntime via Homebrew..."
    brew install onnxruntime
    ok "onnxruntime installed: $(brew --prefix onnxruntime)"
    echo "  Headers: $(brew --prefix onnxruntime)/include"
    echo "  Library: $(brew --prefix onnxruntime)/lib"
}

install_onnxruntime_linux() {
    VERSION="1.17.1"
    TMPDIR=$(mktemp -d)
    cd "$TMPDIR"
    info "Downloading onnxruntime v$VERSION for Linux..."
    curl -sLO "https://github.com/microsoft/onnxruntime/releases/download/v$VERSION/onnxruntime-linux-x64-$VERSION.tgz"
    tar xzf "onnxruntime-linux-x64-$VERSION.tgz"
    INSTALL_DIR="/usr/local"
    if [[ "$EUID" -ne 0 ]]; then
        INSTALL_DIR="$HOME/.local"
        mkdir -p "$INSTALL_DIR/lib" "$INSTALL_DIR/include"
    fi
    cp -r "onnxruntime-linux-x64-$VERSION/lib/"* "$INSTALL_DIR/lib/"
    cp -r "onnxruntime-linux-x64-$VERSION/include/"* "$INSTALL_DIR/include/"
    cd / && rm -rf "$TMPDIR"
    ok "onnxruntime installed to $INSTALL_DIR"
}

install_model() {
    info "Installing silero_vad.onnx model..."
    if command -v python3 &>/dev/null; then
        python3 -m pip install -q silero-vad 2>/dev/null || true
        MODEL_SRC=$(python3 -c "import silero_vad, os; print(os.path.join(os.path.dirname(silero_vad.__file__),'data','silero_vad.onnx'))" 2>/dev/null) || true
        if [[ -n "$MODEL_SRC" && -f "$MODEL_SRC" ]]; then
            cp "$MODEL_SRC" "$MODEL_FILE"
            ok "Model copied from silero-vad Python package"
            return 0
        fi
    fi
    warn "Could not get model via silero-vad pip package."
    info "Downloading directly from GitHub..."
    curl -sL -o "$MODEL_FILE" "https://github.com/snakers4/silero-vad/raw/master/files/silero_vad.onnx"
    if [[ -f "$MODEL_FILE" ]]; then
        ok "Model downloaded to $MODEL_FILE"
        return 0
    fi
    fail "Failed to download model. Place silero_vad.onnx manually in $PROJECT_DIR"
    return 1
}

print_manual_onnxruntime_macos() {
    echo "  Install with Homebrew:"
    echo "    brew install onnxruntime"
    echo ""
    echo "  Binary search path: Homebrew links onnxruntime to:"
    echo "    Headers: $(brew --prefix 2>/dev/null || echo "/opt/homebrew")/include/onnxruntime/"
    echo "    Library: $(brew --prefix 2>/dev/null || echo "/opt/homebrew")/lib/"
    echo ""
    echo "  CGO build flags used by this project:"
    echo "    CGO_CFLAGS=-I\$(brew --prefix)/include/onnxruntime"
    echo "    CGO_LDFLAGS=-L\$(brew --prefix)/lib"
}

print_manual_onnxruntime_linux() {
    echo "  Option 1 — Download onnxruntime release:"
    echo "    VERSION=1.17.1"
    echo "    curl -sLO https://github.com/microsoft/onnxruntime/releases/download/v\$VERSION/onnxruntime-linux-x64-\$VERSION.tgz"
    echo "    tar xzf onnxruntime-linux-x64-\$VERSION.tgz"
    echo "    sudo cp -r onnxruntime-linux-x64-\$VERSION/lib/* /usr/local/lib/"
    echo "    sudo cp -r onnxruntime-linux-x64-\$VERSION/include/* /usr/local/include/"
    echo "    sudo ldconfig"
    echo ""
    echo "  Option 2 — Install via system package manager (if available):"
    echo "    sudo apt install libonnxruntime-dev   # Debian/Ubuntu"
    echo "    sudo yum install onnxruntime-devel    # RHEL/Fedora"
}

print_manual_model() {
    echo "  Download the model file from the official Silero VAD repo:"
    echo "    curl -LO https://github.com/snakers4/silero-vad/raw/master/files/silero_vad.onnx"
    echo "  Or install via Python:"
    echo "    pip install silero-vad"
    echo "    cp \\\$(python3 -c \"import silero_vad, os; print(os.path.join(os.path.dirname(silero_vad.__file__),'data','silero_vad.onnx'))\") ."
    echo ""
    echo "  Place the file at: $MODEL_FILE"
    echo "  Or use -model <path> to specify a custom path."
}

main() {
    echo ""
    echo "==================================="
    echo "  Smart VAD — Dependency Setup"
    echo "==================================="
    echo ""

    OS=$(detect_os)
    info "Detected OS: $OS"
    echo ""

    # ---- onnxruntime ----
    ONNX_OK=false
    case "$OS" in
        macos) check_onnxruntime_osx && ONNX_OK=true ;;
        linux) check_onnxruntime_linux && ONNX_OK=true ;;
    esac

    if $ONNX_OK; then
        ok "onnxruntime is already installed"
    else
        warn "onnxruntime is NOT installed"
        echo "  The binary links against libonnxruntime — it won't start without it."
        echo ""
        if ask "Install onnxruntime now?"; then
            case "$OS" in
                macos) install_onnxruntime_macos ;;
                linux) install_onnxruntime_linux ;;
                *)     fail "Unsupported OS"; exit 1 ;;
            esac
        else
            echo ""
            case "$OS" in
                macos) print_manual_onnxruntime_macos ;;
                linux) print_manual_onnxruntime_linux ;;
            esac
            exit 1
        fi
    fi
    echo ""

    # ---- model ----
    if [[ -f "$MODEL_FILE" ]]; then
        ok "Model found: $MODEL_FILE"
    else
        warn "Model not found at: $MODEL_FILE"
        echo ""
        if ask "Install silero_vad.onnx model now?"; then
            install_model
        else
            echo ""
            print_manual_model
            exit 1
        fi
    fi

    echo ""
    echo "==================================="
    echo -e "  ${GREEN}All dependencies are ready!${NC}"
    echo ""
    echo "  Run the server:"
    echo "    cd $PROJECT_DIR"
    echo "    ./server -model silero_vad.onnx"
    echo "==================================="
}

main "$@"