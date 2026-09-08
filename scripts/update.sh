#!/usr/bin/env bash
set -euo pipefail
project_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
install_dir="${XDG_DATA_HOME:-$HOME/.local/share}/go-openai-import-to-joplin"
if [[ ! -x "$install_dir/bin/go-openai-import-to-joplin" ]]; then
  echo "Not installed; run scripts/install.sh first" >&2
  exit 1
fi
exec "$project_dir/scripts/install.sh"
