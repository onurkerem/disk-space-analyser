#!/usr/bin/env bash
set -euo pipefail

REPO="https://github.com/onurkerem/disk-space-analyser.git"
INSTALL_DIR="$HOME/.disk-space-analyser-src"
BIN_NAME="dsa"
LOCAL_BIN="$HOME/.local/bin"
LINK_PATH="$LOCAL_BIN/$BIN_NAME"
MIN_GO_MAJOR=1
MIN_GO_MINOR=25

# ── Colors ───────────────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

info()  { echo -e "${CYAN}▸${NC} $*"; }
ok()    { echo -e "${GREEN}✔${NC} $*"; }
warn()  { echo -e "${YELLOW}⚠${NC} $*"; }
err()   { echo -e "${RED}✘${NC} $*"; }

# ── Helpers ──────────────────────────────────────────────────────────────────
detect_shell_profile() {
    local shell_name
    shell_name=$(basename "${SHELL:-bash}")
    case "$shell_name" in
        zsh)  echo "$HOME/.zshrc" ;;
        bash) echo "$HOME/.bashrc" ;;
        fish) echo "$HOME/.config/fish/config.fish" ;;
        *)    echo "$HOME/.profile" ;;
    esac
}

version_gte() {
    # Returns 0 if $1 >= $2 (semantic: major.minor, ignores patch)
    local major1 minor1 major2 minor2 rest
    IFS='.' read -r major1 minor1 rest <<< "$1"
    IFS='.' read -r major2 minor2 rest <<< "$2"
    major1=${major1:-0}; minor1=${minor1:-0}
    major2=${major2:-0}; minor2=${minor2:-0}
    # Strip any non-numeric suffix (e.g. "0rc1")
    minor1=${minor1%%[^0-9]*}
    minor2=${minor2%%[^0-9]*}
    [ "$major1" -gt "$major2" ] && return 0
    [ "$major1" -eq "$major2" ] && [ "$minor1" -ge "$minor2" ] && return 0
    return 1
}

# ── Prerequisite checks ──────────────────────────────────────────────────────
check_go() {
    if command -v go &>/dev/null; then
        local go_version
        go_version=$(go version | awk '{print $3}' | sed 's/go//')
        local go_major go_minor
        IFS='.' read -r go_major go_minor _ <<< "$go_version"

        if version_gte "${go_major}.${go_minor}" "${MIN_GO_MAJOR}.${MIN_GO_MINOR}"; then
            ok "Go ${go_version} found"
            return 0
        else
            warn "Go ${go_version} found, but ${MIN_GO_MAJOR}.${MIN_GO_MINOR}+ required"
            return 1
        fi
    fi
    return 1
}

install_go() {
    if [[ "$(uname -s)" != "Darwin" ]]; then
        err "Automatic Go installation is only supported on macOS (via Homebrew)."
        err "Install Go ${MIN_GO_MAJOR}.${MIN_GO_MINOR}+ manually: https://go.dev/dl/"
        exit 1
    fi

    if ! command -v brew &>/dev/null; then
        info "Installing Homebrew..."
        /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"

        # Add brew to PATH if not already there
        local brew_prefix
        brew_prefix="$(brew --prefix)"
        eval "$("${brew_prefix}/bin/brew" shellenv)"
    fi

    info "Installing Go via Homebrew..."
    brew install go

    if check_go; then
        ok "Go installed successfully"
    else
        err "Go installation failed"
        exit 1
    fi
}

ensure_prerequisites() {
    info "Checking prerequisites..."

    if ! check_go; then
        info "Installing Go..."
        install_go
    fi

    ok "All prerequisites met"
}

# ── Install / Reinstall ──────────────────────────────────────────────────────
remove_existing() {
    info "Removing existing installation..."

    # Remove symlink
    if [[ -L "$LINK_PATH" ]] || [[ -e "$LINK_PATH" ]]; then
        rm -f "$LINK_PATH"
        ok "Removed symlink at $LINK_PATH"
    fi

    # Remove source directory
    if [[ -d "$INSTALL_DIR" ]]; then
        rm -rf "$INSTALL_DIR"
        ok "Removed source at $INSTALL_DIR"
    fi

    # Clean up shell alias/function from profile
    local profile
    profile=$(detect_shell_profile)
    if [[ -f "$profile" ]]; then
        if grep -q 'disk-space-analyser\|# >>> dsa >>>\|# <<< dsa <<<' "$profile" 2>/dev/null; then
            # Remove block marker and any dsa-related lines
            sed -i.bak '/# >>> dsa >>>/,/# <<< dsa <<</d' "$profile"
            rm -f "${profile}.bak"
            ok "Cleaned shell profile ($profile)"
        fi
    fi

    # Remove old data directory
    if [[ -d "$HOME/.disk-space-analyser" ]]; then
        if [[ -t 0 ]]; then
            read -rp "$(echo -e "${YELLOW}Delete scan data at ~/.disk-space-analyser? [y/N]:${NC} ")" choice
        else
            choice="n"
        fi
        if [[ "$choice" =~ ^[Yy]$ ]]; then
            rm -rf "$HOME/.disk-space-analyser"
            ok "Removed data directory"
        fi
    fi
}

clone_source() {
    info "Cloning repository..."
    git clone "$REPO" "$INSTALL_DIR"
    ok "Cloned to $INSTALL_DIR"
}

update_source() {
    info "Updating source code..."
    cd "$INSTALL_DIR"
    git fetch origin
    git reset --hard origin/main
    ok "Updated to latest"
}

build_binary() {
    info "Building ${BIN_NAME}..."
    cd "$INSTALL_DIR"
    CGO_ENABLED=0 go build -o "$BIN_NAME" ./packages/cli/cmd/disk-space-analyser
    ok "Built ${BIN_NAME}"
}

install_binary() {
    local binary="$INSTALL_DIR/$BIN_NAME"

    # Ensure ~/.local/bin exists
    mkdir -p "$LOCAL_BIN"

    # Symlink — updates on rebuild without re-installing
    ln -sf "$binary" "$LINK_PATH"
    ok "Linked $LINK_PATH -> $binary"

    # Ensure ~/.local/bin is in PATH via shell profile
    ensure_path
}

ensure_path() {
    local profile
    profile=$(detect_shell_profile)

    # Check if already in PATH
    if echo ":${PATH}:" | grep -q ":${LOCAL_BIN}:"; then
        return 0
    fi

    # Check if profile already has our marker
    if [[ -f "$profile" ]] && grep -q '# >>> dsa >>>' "$profile" 2>/dev/null; then
        return 0
    fi

    info "Adding $LOCAL_BIN to PATH in $profile"
    {
        echo ""
        echo "# >>> dsa >>>"
        echo "export PATH=\"\$HOME/.local/bin:\$PATH\""
        echo "# <<< dsa <<<"
    } >> "$profile"
    ok "Updated $profile — open a new terminal or run: source $profile"

    # Make it available in this session too
    export PATH="$LOCAL_BIN:$PATH"
}

verify_installation() {
    if command -v "$BIN_NAME" &>/dev/null; then
        local resolved
        resolved=$(command -v "$BIN_NAME")
        ok "${BIN_NAME} installed at ${resolved}"
        echo ""
        ok "Installation complete. Run '${BIN_NAME} start /path' to begin."
    else
        err "${BIN_NAME} not found in PATH"
        err "Open a new terminal or run: source $(detect_shell_profile)"
        exit 1
    fi
}

# ── Main ─────────────────────────────────────────────────────────────────────
main() {
    echo -e "${BOLD}Disk Space Analyser — Installer${NC}"
    echo ""

    # Check if already installed
    local already_installed=false
    if [[ -d "$INSTALL_DIR" ]] || [[ -L "$LINK_PATH" ]] || [[ -e "$LINK_PATH" ]]; then
        already_installed=true
    fi

    if $already_installed; then
        echo -e "${YELLOW}Existing installation detected.${NC}"
        echo ""
        echo "  1) Reinstall from scratch (remove everything, clone fresh)"
        echo "  2) Update source and rebuild (keep data)"
        echo "  3) Uninstall completely"
        echo ""
        if [[ -t 0 ]]; then
            read -rp "Choose [1/2/3]: " choice
        else
            err "Run this script interactively to manage an existing installation."
            exit 1
        fi
        case "$choice" in
            1)
                remove_existing
                ensure_prerequisites
                clone_source
                build_binary
                install_binary
                verify_installation
                ;;
            2)
                ensure_prerequisites
                update_source
                build_binary
                install_binary
                verify_installation
                ;;
            3)
                remove_existing
                ok "Uninstalled"
                exit 0
                ;;
            *)
                err "Invalid choice"
                exit 1
                ;;
        esac
    else
        ensure_prerequisites
        clone_source
        build_binary
        install_binary
        verify_installation
    fi
}

main "$@"
