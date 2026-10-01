#!/bin/sh
set -eu

export PATH="$PATH:$HOME/.local/bin"

# Reuse an existing Go installation; otherwise install an official archive.
if ! command -v go >/dev/null 2>&1; then
    case "$(uname -s)" in
        Darwin) os=darwin ;;
        Linux) os=linux ;;
        *) echo "Automatic Go installation supports Linux and macOS only." >&2; exit 1 ;;
    esac
    case "$(uname -m)" in
        x86_64|amd64) arch=amd64 ;;
        aarch64|arm64) arch=arm64 ;;
        *) echo "Automatic Go installation supports amd64 and arm64 only." >&2; exit 1 ;;
    esac

    temp_dir=$(mktemp -d)
    trap 'rm -rf "$temp_dir"' EXIT
    curl -fsSL 'https://go.dev/VERSION?m=text' -o "$temp_dir/version"
    version=$(sed -n '1p' "$temp_dir/version")
    case "$version" in
        go[0-9]*.[0-9]*) ;;
        *) echo "Could not determine the stable Go version." >&2; exit 1 ;;
    esac
    archive="$version.$os-$arch.tar.gz"
    echo "Installing $version from go.dev..."
    curl -fsSL "https://go.dev/dl/$archive" -o "$temp_dir/go.tar.gz"
    curl -fsSL "https://dl.google.com/go/$archive.sha256" -o "$temp_dir/sha256"
    expected=$(cat "$temp_dir/sha256")
    if command -v sha256sum >/dev/null 2>&1; then
        actual=$(sha256sum "$temp_dir/go.tar.gz" | awk '{print $1}')
    else
        actual=$(shasum -a 256 "$temp_dir/go.tar.gz" | awk '{print $1}')
    fi
    if [ "$expected" != "$actual" ]; then
        echo "Go archive SHA-256 verification failed." >&2
        exit 1
    fi
    tar -xzf "$temp_dir/go.tar.gz" -C "$temp_dir"
    go_dir="$HOME/.local/lib/$version"
    if [ -e "$go_dir" ]; then
        echo "Refusing to overwrite $go_dir; remove it or add its bin directory to PATH." >&2
        exit 1
    fi
    mkdir -p "$HOME/.local/lib" "$HOME/.local/bin"
    mv "$temp_dir/go" "$go_dir"
    ln -s "$go_dir/bin/go" "$HOME/.local/bin/go"
    ln -s "$go_dir/bin/gofmt" "$HOME/.local/bin/gofmt"
    rm -rf "$temp_dir"
    trap - EXIT
fi

go version
required=$(awk '$1 == "go" {print $2; exit}' go.mod)
if ! go version | awk -v required="$required" '
    { sub(/^go/, "", $3); split($3, have, "."); split(required, need, ".");
      exit !(have[1] > need[1] || (have[1] == need[1] && have[2] >= need[2])) }'; then
    echo "Go $required or newer is required; upgrade the Go installation on PATH." >&2
    exit 1
fi

if ! command -v wuji >/dev/null 2>&1; then
    temp_dir=$(mktemp -d)
    trap 'rm -rf "$temp_dir"' EXIT
    curl -fsSL https://get.wuji.tech/cli -o "$temp_dir/install-wuji.sh"
    INSTALL_DIR="$HOME/.local/bin" WUJI_SKIP_CLI=0 WUJI_SKIP_SKILLS=1 sh "$temp_dir/install-wuji.sh"
fi
wuji --version
echo 'Dependencies ready. For direct use, add to your shell profile: export PATH="$PATH:$HOME/.local/bin"'
