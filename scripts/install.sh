#!/usr/bin/env bash
set -euo pipefail
project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_dir="${XDG_DATA_HOME:-$HOME/.local/share}/chatgpt-import-to-joplin"
config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/chatgpt-import-to-joplin"
"$project_dir/scripts/build.sh"
mkdir -p "$install_dir/bin" "$config_dir"
temporary="$(mktemp "$install_dir/bin/.chatgpt-import-XXXXXX")"
trap 'rm -f "$temporary"' EXIT
install -m 755 "$project_dir/dist/chatgpt-import-to-joplin" "$temporary"
mv -f "$temporary" "$install_dir/bin/chatgpt-import-to-joplin"
install -m 600 "$project_dir/config/example.env" "$config_dir/config.env.example"
if [[ ! -e "$config_dir/config.env" ]]; then install -m 600 "$project_dir/config/example.env" "$config_dir/config.env"; fi
printf 'CLI installed: %s/bin/chatgpt-import-to-joplin\n' "$install_dir"
printf 'Optional shell configuration: %s/config.env\n' "$config_dir"
