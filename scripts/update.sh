#!/usr/bin/env bash
set -euo pipefail
project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_dir="${XDG_DATA_HOME:-$HOME/.local/share}/openai-import-to-joplin"
legacy_install_dir="${XDG_DATA_HOME:-$HOME/.local/share}/chatgpt-import-to-joplin"
if [[ ! -x "$install_dir/bin/openai-import-to-joplin" && ! -x "$legacy_install_dir/bin/chatgpt-import-to-joplin" && ! -x "$legacy_install_dir/venv/bin/chatgpt-import-to-joplin" ]]; then
  echo "Not installed; run scripts/install.sh first" >&2
  exit 1
fi
exec "$project_dir/scripts/install.sh"
