#!/bin/sh
set -eu

request=$(/bin/cat)

# Resolve only from WSL's mounted Windows C: drive. PATH is attacker-controlled
# and is never used to find PowerShell. Nonstandard Windows system drives fail
# closed; users can configure their own absolute credential hook instead.
drive_root=/mnt/c
windows_root=$drive_root/Windows
system_root=$windows_root/System32/WindowsPowerShell/v1.0/powershell.exe
mount_rows=$(/usr/bin/findmnt -n -T "$drive_root" -o TARGET,SOURCE,FSTYPE 2>/dev/null || true)
mount_ok=1
set -- $mount_rows
if [ "$#" -eq 0 ] || [ $(( $# % 3 )) -ne 0 ]; then
  mount_ok=0
fi
while [ "$#" -ge 3 ]; do
  if [ "$1" != "$drive_root" ] || [ "$2" != 'C:\' ] || [ "$3" != 9p ]; then
    mount_ok=0
  fi
  shift 3
done
resolved=$(/usr/bin/readlink -f -- "$system_root" 2>/dev/null || true)

if [ "$mount_ok" -ne 1 ] || [ "$resolved" != "$system_root" ] || [ ! -f "$system_root" ] || [ ! -x "$system_root" ] || [ ! -x /usr/bin/wslpath ]; then
  printf '{"status":"unavailable"}\n'
  exit 0
fi

script_dir=$(CDPATH= /bin/sh -c 'cd -- "$(/usr/bin/dirname -- "$1")" && /bin/pwd -P' sh "$0")
windows_temp=$("$system_root" -NoLogo -NoProfile -NonInteractive -Command "[IO.Path]::Combine([Environment]::GetFolderPath('LocalApplicationData'), 'Temp')" 2>/dev/null | /usr/bin/tr -d '\r')
temp_parent=$(/usr/bin/wslpath -u "$windows_temp") || {
  printf '{"status":"unavailable"}\n'
  exit 0
}
temp_dir=$(/usr/bin/mktemp -d "$temp_parent/aact-keyring.XXXXXXXX") || {
  printf '{"status":"unavailable"}\n'
  exit 0
}
trap '/usr/bin/rm -f -- "$temp_dir/windows.ps1"; /usr/bin/rmdir -- "$temp_dir" 2>/dev/null || true' EXIT
/usr/bin/cp -- "$script_dir/windows.ps1" "$temp_dir/windows.ps1" || {
  printf '{"status":"unavailable"}\n'
  exit 0
}
windows_script=$(/usr/bin/wslpath -w "$temp_dir/windows.ps1") || {
  printf '{"status":"unavailable"}\n'
  exit 0
}
printf '%s' "$request" | "$system_root" -NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "$windows_script"
