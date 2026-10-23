[CmdletBinding()]
param(
  [string]$Version = $(if ($env:AACT_VERSION) { $env:AACT_VERSION } else { '' }),
  [string]$LocalArchive = '',
  [string]$BaseUrl = $(if ($env:AACT_DOWNLOAD_BASE) { $env:AACT_DOWNLOAD_BASE } else { 'https://github.com/paulharkink/another-agent-capability-toolkit/releases/download' }),
  [string]$InstallDir = $(if ($env:AACT_INSTALL_DIR) { $env:AACT_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'aact' }),
  [switch]$PrintTarget,
  [switch]$Force
)
$ErrorActionPreference = 'Stop'
$architecture = $env:AACT_ARCH
if (-not $architecture) {
  $architecture = $env:PROCESSOR_ARCHITEW6432
  if (-not $architecture) { $architecture = $env:PROCESSOR_ARCHITECTURE }
}
switch -Regex ($architecture) {
  '^(AMD64|x86_64|x64)$' { $arch = 'amd64'; break }
  '^(ARM64|aarch64)$' { $arch = 'arm64'; break }
  default { throw "Unsupported architecture: $architecture" }
}
if ($PrintTarget) { Write-Output "windows_$arch"; return }
if ($LocalArchive) {
  $LocalArchive = [IO.Path]::GetFullPath($LocalArchive)
  if (-not (Test-Path -LiteralPath $LocalArchive -PathType Leaf)) { throw "Local archive is not a regular file: $LocalArchive" }
  $archiveName = [IO.Path]::GetFileName($LocalArchive)
  $archiveMatch = [regex]::Match($archiveName, "^aact_(?<version>[A-Za-z0-9][A-Za-z0-9._-]*)_windows_$arch\.zip$")
  if (-not $archiveMatch.Success) { throw "Local archive does not match this platform (windows/$arch): $archiveName" }
  $archiveVersion = $archiveMatch.Groups['version'].Value
  if ($Version -and $Version -ne $archiveVersion) { throw "Requested version $Version does not match local archive version $archiveVersion" }
  $Version = $archiveVersion
} elseif (-not $Version) {
  throw 'Specify -Version or -LocalArchive'
}
if ($Version -notmatch '^[A-Za-z0-9][A-Za-z0-9._-]*$') { throw 'Invalid release version' }
$InstallDir = [IO.Path]::GetFullPath($InstallDir)
$releases = Join-Path $InstallDir 'releases'
$launchers = Join-Path $InstallDir 'bin'
[IO.Directory]::CreateDirectory($releases) | Out-Null
[IO.Directory]::CreateDirectory($launchers) | Out-Null
$work = Join-Path $releases ('.install-' + [Guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($work) | Out-Null
$next = Join-Path $launchers ('.aact-next-' + [Guid]::NewGuid().ToString('N'))
$releaseBackup = ''
$releaseCommitted = $false
try {
  [Net.ServicePointManager]::SecurityProtocol = [Net.SecurityProtocolType]::Tls12
  $filename = "aact_${Version}_windows_${arch}.zip"
  $archive = Join-Path $work $filename
  if ($LocalArchive) {
    Copy-Item -LiteralPath $LocalArchive -Destination $archive
    $checksumFile = Join-Path (Split-Path -Parent $LocalArchive) 'checksums.txt'
    if (-not (Test-Path -LiteralPath $checksumFile -PathType Leaf)) { throw "Adjacent checksums.txt is missing: $checksumFile" }
  } else {
    Invoke-WebRequest -UseBasicParsing -Uri "$($BaseUrl.TrimEnd('/'))/v$Version/$filename" -OutFile $archive
    $checksumFile = Join-Path $work 'checksum'
    Invoke-WebRequest -UseBasicParsing -Uri "$($BaseUrl.TrimEnd('/'))/v$Version/checksums.txt" -OutFile $checksumFile
  }
  $checksumPattern = '^([a-fA-F0-9]{64})\s+' + [regex]::Escape($filename) + '$'
  $checksumMatch = Select-String -LiteralPath $checksumFile -Pattern $checksumPattern | Select-Object -First 1
  if (-not $checksumMatch) { throw "No checksum found for $filename" }
  $expected = $checksumMatch.Matches[0].Groups[1].Value.ToLowerInvariant()
  if ($expected -notmatch '^[a-f0-9]{64}$') { throw 'Invalid release checksum' }
  $actual = (Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash.ToLowerInvariant()
  if ($expected -ne $actual) { throw 'Release checksum mismatch; existing installation preserved' }
  Add-Type -AssemblyName System.IO.Compression.FileSystem
  $zip = [IO.Compression.ZipFile]::OpenRead($archive)
  try {
    foreach ($entry in $zip.Entries) {
      $parts = $entry.FullName -split '[/\\]'
      if ([IO.Path]::IsPathRooted($entry.FullName) -or ($parts -contains '..') -or $entry.FullName.Contains(':')) { throw 'Unsafe archive path' }
      # POSIX symlink entries are not produced by AACT releases.
      if ((($entry.ExternalAttributes -shr 16) -band 0xF000) -eq 0xA000) { throw 'Archive links are not allowed' }
    }
  } finally { $zip.Dispose() }
  $tree = Join-Path $work 'tree'
  Expand-Archive -LiteralPath $archive -DestinationPath $tree
  if (-not (Test-Path -LiteralPath (Join-Path $tree 'bin/aact.exe') -PathType Leaf) -or -not (Test-Path -LiteralPath (Join-Path $tree 'packages') -PathType Container)) { throw 'Release archive is incomplete' }
  [IO.File]::WriteAllText((Join-Path $tree '.archive-sha256'), $expected + "`n", [Text.UTF8Encoding]::new($false))
  $release = Join-Path $releases "$Version-windows-$arch"
  if (Test-Path -LiteralPath $release) {
    $existingHash = Join-Path $release '.archive-sha256'
    $existingRelease = Get-Item -LiteralPath $release -Force
    if (($existingRelease.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0) { throw 'Existing version directory is a reparse point; refusing to replace it' }
    if (-not (Test-Path -LiteralPath $existingHash -PathType Leaf)) {
      if ($Force) { throw 'Existing version directory is not an AACT-managed release; refusing to replace it with -Force' }
      throw 'Existing version directory differs; refusing to replace referenced resources without -Force'
    }
    if ((Get-Content -LiteralPath $existingHash -Raw).Trim() -ne $expected) {
      if (-not $Force) { throw 'Existing version directory differs; refusing to replace referenced resources without -Force' }
      $releaseBackup = Join-Path $releases ('.aact-backup-' + $Version + '-windows-' + $arch + '-' + [Guid]::NewGuid().ToString('N'))
      try {
        [IO.Directory]::Move($release, $releaseBackup)
      } catch {
        throw "Could not stage the existing version directory; close running AACT processes and retry with -Force. Details: $($_.Exception.Message)"
      }
      try {
        [IO.Directory]::Move($tree, $release)
      } catch {
        $activationError = $_.Exception.Message
        try {
          [IO.Directory]::Move($releaseBackup, $release)
          $releaseBackup = ''
        } catch {
          throw "Could not activate the staged release ($activationError), and rollback failed: $($_.Exception.Message). Previous release remains at $releaseBackup"
        }
        throw "Could not activate the staged release; previous version was restored. Details: $activationError"
      }
    }
  } else { [IO.Directory]::Move($tree, $release) }
  $command = '@"%~dp0..\releases\' + $Version + '-windows-' + $arch + '\bin\aact.exe" %*' + "`r`n"
  [IO.File]::WriteAllText($next, $command, [Text.UTF8Encoding]::new($false))
  $launcher = Join-Path $launchers 'aact.cmd'
  if (Test-Path -LiteralPath $launcher) {
    $backup = Join-Path $work 'previous-launcher.cmd'
    [IO.File]::Replace($next, $launcher, $backup)
  } else { [IO.File]::Move($next, $launcher) }
  $releaseCommitted = $true
  if ($releaseBackup -and (Test-Path -LiteralPath $releaseBackup)) {
    try { Remove-Item -LiteralPath $releaseBackup -Recurse -Force -ErrorAction Stop }
    catch { Write-Warning "Installed successfully, but could not remove previous release backup $releaseBackup. Close running AACT processes and remove it later." }
    $releaseBackup = ''
  }
  Write-Output "Installed AACT $Version (windows/$arch) at $release"
  if (($env:PATH -split ';') -notcontains $launchers) { Write-Output "Add $launchers to PATH to run aact. Your shell profiles were not modified." }
} catch {
  if (-not $releaseCommitted -and $releaseBackup -and (Test-Path -LiteralPath $releaseBackup)) {
    try {
      if (Test-Path -LiteralPath $release) { Remove-Item -LiteralPath $release -Recurse -Force -ErrorAction Stop }
      [IO.Directory]::Move($releaseBackup, $release)
      $releaseBackup = ''
    } catch {
      throw "Install failed and rollback could not restore the previous version directory: $($_.Exception.Message). Previous release remains at $releaseBackup"
    }
  }
  throw
} finally {
  if (Test-Path -LiteralPath $next) { Remove-Item -LiteralPath $next -Force }
  if (Test-Path -LiteralPath $work) { Remove-Item -LiteralPath $work -Recurse -Force }
}
