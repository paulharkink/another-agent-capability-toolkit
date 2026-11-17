#!/bin/sh
set -eu

request=$(cat)

system_root=
old_ifs=$IFS
IFS=:
for directory in ${PATH:-}; do
  case "$(printf '%s' "$directory" | tr '[:upper:]' '[:lower:]')" in
    */windows/system32/windowspowershell/v1.0)
      candidate=$directory/powershell.exe
      if [ -f "$candidate" ] && [ -x "$candidate" ]; then
        resolved=$(readlink -f "$candidate" 2>/dev/null || true)
        case "$(printf '%s' "$resolved" | tr '[:upper:]' '[:lower:]')" in
          */windows/system32/windowspowershell/v1.0/powershell.exe)
            system_root=$resolved
            break
            ;;
        esac
      fi
      ;;
  esac
done
IFS=$old_ifs

if [ -z "$system_root" ] || [ ! -x /usr/bin/wslpath ]; then
  printf '{"status":"unavailable"}\n'
  exit 0
fi

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd -P)
windows_script=$(/usr/bin/wslpath -w "$script_dir/windows.ps1") || {
  printf '{"status":"unavailable"}\n'
  exit 0
}
printf '%s' "$request" | "$system_root" -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$windows_script"
