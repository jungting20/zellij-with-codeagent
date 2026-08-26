#!/usr/bin/env bash

set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
repo_root="$(cd -- "$script_dir/.." && pwd -P)"
manifest_path="$repo_root/plugins/agent-dashboard-sidebar/Cargo.toml"
artifact_path="$repo_root/plugins/agent-dashboard-sidebar/target/wasm32-wasip1/release/agent_dashboard_sidebar.wasm"
plugin_dir="${ZELLIJ_PLUGIN_DIR:-${XDG_CONFIG_HOME:-$HOME/.config}/zellij/plugins}"
rustup_toolchain="${AGENT_DASHBOARD_RUSTUP_TOOLCHAIN:-1.88.0}"
temporary_file=""

cleanup() {
    local status=$?
    if (( status != 0 )) && [[ -n "$temporary_file" ]]; then
        rm -f -- "$temporary_file"
    fi
    exit "$status"
}
trap cleanup EXIT

if ! command -v rustup >/dev/null 2>&1; then
    echo "agent dashboard sidebar requires Rustup toolchain $rustup_toolchain; PATH cargo is not used" >&2
    exit 1
fi

if ! rustup run "$rustup_toolchain" cargo --version >/dev/null 2>&1; then
    echo "Rustup toolchain $rustup_toolchain is unavailable; install it with: rustup toolchain install $rustup_toolchain" >&2
    exit 1
fi

rustc_path="$(rustup which --toolchain "$rustup_toolchain" rustc 2>/dev/null)" || {
    echo "Rustup toolchain $rustup_toolchain has no rustc executable" >&2
    exit 1
}

if ! rustup target list --toolchain "$rustup_toolchain" --installed | grep -qx 'wasm32-wasip1'; then
    echo "Rustup toolchain $rustup_toolchain is missing wasm32-wasip1; install it with: rustup target add wasm32-wasip1 --toolchain $rustup_toolchain" >&2
    exit 1
fi

RUSTC="$rustc_path" rustup run "$rustup_toolchain" cargo build \
    --release \
    --target wasm32-wasip1 \
    --manifest-path "$manifest_path"

if [[ ! -s "$artifact_path" ]]; then
    echo "agent dashboard sidebar artifact was not produced: $artifact_path" >&2
    exit 1
fi

mkdir -p -- "$plugin_dir"
plugin_dir="$(cd -- "$plugin_dir" && pwd -P)"
destination_path="$plugin_dir/agent-dashboard-sidebar-v2.wasm"
temporary_file="$(mktemp "$plugin_dir/.agent-dashboard-sidebar-v2.wasm.XXXXXX")"

cp -- "$artifact_path" "$temporary_file"
chmod 0644 "$temporary_file"
mv -f -- "$temporary_file" "$destination_path"

echo "$destination_path"
