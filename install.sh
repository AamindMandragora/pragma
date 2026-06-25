#!/bin/sh
set -e

REPO="AamindMandragora/pragma"
INSTALL_DIR="$HOME/.pragma/bin"

# detect platform
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)  ARCH="amd64" ;;
    aarch64) ARCH="arm64" ;;
    arm64)   ARCH="arm64" ;;
    *)       echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# map os name
case "$OS" in
    linux)  ;;
    darwin) ;;
    *)      echo "Unsupported OS: $OS (use install.ps1 for Windows)"; exit 1 ;;
esac

# fetch latest release tag
LATEST=$(curl -sL "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
if [ -z "$LATEST" ]; then
    echo "Failed to fetch latest release"
    exit 1
fi

# constructs the tarball and url
TARBALL="pragma-${OS}-${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$LATEST/$TARBALL"

echo "Installing pragma $LATEST for $OS/$ARCH..."

# download and extract
mkdir -p "$INSTALL_DIR"
curl -sL "$URL" | tar xz -C "$INSTALL_DIR"
chmod +x "$INSTALL_DIR/pragma"

# add to PATH if not already there
if ! echo "$PATH" | grep -q "$INSTALL_DIR"; then
    SHELL_NAME=$(basename "$SHELL")
    case "$SHELL_NAME" in
        zsh)  RC="$HOME/.zshrc" ;;
        bash) RC="$HOME/.bashrc" ;;
        fish) RC="$HOME/.config/fish/config.fish" ;;
        *)    RC="$HOME/.profile" ;;
    esac
    echo "export PATH=\"$INSTALL_DIR:\$PATH\"" >> "$RC"
    echo "Added $INSTALL_DIR to PATH in $RC"
    echo "Run 'source $RC' or open a new terminal"
fi

echo "pragma $LATEST installed to $INSTALL_DIR/pragma"