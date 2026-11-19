[CmdletBinding()]
param(
  [string]$Version = $(if ($env:AACT_VERSION) { $env:AACT_VERSION } else { '' }),
  [string]$LocalArchive = '',
  [string]$BaseUrl = $(if ($env:AACT_DOWNLOAD_BASE) { $env:AACT_DOWNLOAD_BASE } else { 'https://github.com/paulharkink/another-agent-capability-toolkit/releases/download' }),
  [string]$InstallDir = $(if ($env:AACT_INSTALL_DIR) { $env:AACT_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'aact' }),
  [switch]$PrintTarget,
  [switch]$Force,
  [switch]$SkipUserPath
)
$ErrorActionPreference = 'Stop'
if ($PSVersionTable.PSEdition -eq 'Desktop') {
  $windowsPowerShellModules = Join-Path ([Environment]::SystemDirectory) 'WindowsPowerShell\v1.0\Modules'
  $utilityModule = Join-Path (Join-Path $windowsPowerShellModules 'Microsoft.PowerShell.Utility') 'Microsoft.PowerShell.Utility.psd1'
  if (-not (Test-Path -LiteralPath $utilityModule -PathType Leaf)) { throw "Windows PowerShell utility module is missing: $utilityModule" }
  Import-Module -Name $utilityModule -ErrorAction Stop
}
$fallbackControllerInstallDir = if ($env:AACT_INSTALL_DIR) { [IO.Path]::GetFullPath($env:AACT_INSTALL_DIR) } else { [IO.Path]::GetFullPath((Join-Path $env:LOCALAPPDATA 'aact')) }

function Get-NormalizedPathEntry([string]$entry) {
  $value = [Environment]::ExpandEnvironmentVariables($entry.Trim().Trim('"'))
  if (-not $value) { return '' }
  try { $value = [IO.Path]::GetFullPath($value) } catch { }
  $root = [IO.Path]::GetPathRoot($value)
  if ($value.Length -gt $root.Length) { $value = $value.TrimEnd([char[]]@('\', '/')) }
  return $value
}

function Test-PathContainsEntry([string]$pathValue, [string]$entry) {
  $wanted = Get-NormalizedPathEntry $entry
  foreach ($candidate in ($pathValue -split ';')) {
    if ($candidate -and [string]::Equals((Get-NormalizedPathEntry $candidate), $wanted, [StringComparison]::OrdinalIgnoreCase)) { return $true }
  }
  return $false
}

function Add-UserPathEntry([string]$entry) {
  $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
  if (-not $key) { $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment') }
  if (-not $key) { throw 'Could not open the current user environment settings' }
  try {
    $hasPath = $key.GetValueNames() -contains 'Path'
    $kind = [Microsoft.Win32.RegistryValueKind]::ExpandString
    $pathValue = ''
    if ($hasPath) {
      $kind = $key.GetValueKind('Path')
      if ($kind -notin @([Microsoft.Win32.RegistryValueKind]::String, [Microsoft.Win32.RegistryValueKind]::ExpandString)) {
        throw "User Path has unsupported registry type $kind"
      }
      $pathValue = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    }
    if (-not (Test-PathContainsEntry $pathValue $entry)) {
      $separator = if (-not $pathValue -or $pathValue.EndsWith(';')) { '' } else { ';' }
      $pathValue = $pathValue + $separator + $entry
      $key.SetValue('Path', $pathValue, $kind)
      return $true
    }
    return $false
  } finally { $key.Dispose() }
}

function Get-ManagedController([string]$root) {
  if (-not (Test-Path -LiteralPath $root -PathType Container)) { return $null }
  $root = [IO.Path]::GetFullPath($root)
  $launcher = Join-Path $root 'bin/aact.cmd'
  if (-not (Test-Path -LiteralPath $launcher -PathType Leaf)) { return $null }
  $content = Get-Content -LiteralPath $launcher -Raw
  $match = [regex]::Match($content, '(?i)\.\.\\releases\\([^\\]+)\\bin\\aact\.exe')
  if (-not $match.Success) { return $null }
  $releaseName = $match.Groups[1].Value
  $suffix = '-windows-' + $arch
  if (-not $releaseName.EndsWith($suffix, [StringComparison]::OrdinalIgnoreCase)) { return $null }
  $release = Join-Path (Join-Path $root 'releases') $releaseName
  $executable = Join-Path $release 'bin/aact.exe'
  $metadata = Join-Path $release '.archive-sha256'
  if (-not (Test-Path -LiteralPath $executable -PathType Leaf) -or -not (Test-Path -LiteralPath $metadata -PathType Leaf)) { return $null }
  if ((Get-Content -LiteralPath $metadata -Raw).Trim() -notmatch '^[a-fA-F0-9]{64}$') { return $null }
  $installer = Join-Path $release 'install.ps1'
  if (-not (Test-Path -LiteralPath $installer -PathType Leaf)) { $installer = '' }
  return [pscustomobject]@{ Controller = $executable; Installer = $installer }
}

function Send-EnvironmentChanged {
  if (-not ('AactEnvironmentBroadcast' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class AactEnvironmentBroadcast {
  [DllImport("user32.dll", CharSet = CharSet.Auto, SetLastError = true)]
  public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint message, UIntPtr wParam, string lParam, uint flags, uint timeout, out UIntPtr result);
}
'@
  }
  $result = [UIntPtr]::Zero
  return [AactEnvironmentBroadcast]::SendMessageTimeout([IntPtr]0xffff, 0x001A, [UIntPtr]::Zero, 'Environment', 0x0002, 5000, [ref]$result)
}

$architecture = $env:AACT_ARCH
if (-not $architecture) {
  $architecture = $env:PROCESSOR_ARCHITEW6432
  if (-not $architecture) { $architecture = $env:PROCESSOR_ARCHITECTURE }
  if (-not $architecture) { $architecture = [System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture.ToString() }
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
$controllerInstallDir = $InstallDir
$lifecycleFallback = $false
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
  if ($env:AACT_INSTALLER_LIFECYCLE -ne '1') {
    Copy-Item -LiteralPath $checksumFile -Destination (Join-Path $work 'checksums.txt')
    $rootManaged = Get-ManagedController $controllerInstallDir
    $fallbackManaged = $null
    if ($fallbackControllerInstallDir -and -not [string]::Equals($fallbackControllerInstallDir, $controllerInstallDir, [StringComparison]::OrdinalIgnoreCase)) {
      $fallbackManaged = Get-ManagedController $fallbackControllerInstallDir
    }
    $currentLifecycle = $null
    if ($rootManaged -and $rootManaged.Installer -and ((& $rootManaged.Controller __installer-lifecycle --probe 2>$null | Out-String).Trim() -eq 'aact-installer-lifecycle-v1')) {
      $currentLifecycle = $rootManaged
    } elseif ($fallbackManaged -and $fallbackManaged.Installer -and ((& $fallbackManaged.Controller __installer-lifecycle --probe 2>$null | Out-String).Trim() -eq 'aact-installer-lifecycle-v1')) {
      $currentLifecycle = $fallbackManaged
    }
    $staged = Join-Path $tree 'bin/aact.exe'
    $lifecycleController = ''
    $lifecycleScript = ''
    if ((& $staged __installer-lifecycle --probe 2>$null | Out-String).Trim() -eq 'aact-installer-lifecycle-v1') {
      $lifecycleController = $staged
      $stagedInstaller = Join-Path $tree 'install.ps1'
      if (Test-Path -LiteralPath $stagedInstaller -PathType Leaf) { $lifecycleScript = $stagedInstaller }
      elseif ($currentLifecycle) { $lifecycleScript = $currentLifecycle.Installer }
    } elseif ($currentLifecycle) {
      $lifecycleController = $currentLifecycle.Controller
      $lifecycleScript = $currentLifecycle.Installer
    }
    if ($lifecycleController -and $lifecycleScript) {
      $delegatedArgs = @('__installer-lifecycle', '--run', $lifecycleScript, '--install-dir', $InstallDir)
      $delegatedArgs += @('--', '-Version', $Version, '-LocalArchive', $archive, '-InstallDir', $InstallDir)
      if ($Force) { $delegatedArgs += '-Force' }
      if ($SkipUserPath) { $delegatedArgs += '-SkipUserPath' }
      & $lifecycleController @delegatedArgs
      exit $LASTEXITCODE
    }
    $controller = if ($rootManaged) { $rootManaged.Controller } elseif ($fallbackManaged) { $fallbackManaged.Controller } else { '' }
    if ($controller) {
      $status = ''
      try { $status = (& $controller daemon status 2>&1 | Out-String) } catch { }
      $stopCommand = 'daemon'
      if ($status -notmatch ' running at ') {
        try { $status = (& $controller vault status 2>&1 | Out-String) } catch { $status = '' }
        $stopCommand = 'vault'
      }
      $lifecycleFallback = $status -match ' running at '
      $targetHasDaemon = $Version -notmatch '^0\.[0-4]\.' -and $Version -notmatch '^0\.5\.(?:[0-9]|1[0-4])$'
      if ($lifecycleFallback -and $targetHasDaemon) { throw 'A running daemon cannot be safely updated by this installer/controller combination; install was not started.' }
      if ($lifecycleFallback) {
        & $controller $stopCommand stop
        if ($LASTEXITCODE -ne 0) { throw 'Could not stop the running AACT daemon; install was not started.' }
      }
    }
  }
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
  if ($lifecycleFallback -and $Version -notmatch '^0\.[0-4]\.' -and $Version -notmatch '^0\.5\.(?:[0-9]|1[0-4])$') {
    throw 'A running daemon cannot be safely updated by this installer/controller combination; install was not started.'
  }
  if ($lifecycleFallback) {
    Write-Warning "Installed AACT $Version without a daemon; the previous daemon remains stopped and Vault-dependent running MCPs are disconnected."
  }
  if (-not $SkipUserPath -and $env:AACT_INSTALLER_SKIP_USER_PATH -ne '1') {
  try {
    $processPathHadLauncher = Test-PathContainsEntry $env:PATH $launchers
    $addedToUserPath = Add-UserPathEntry $launchers
    if (-not $processPathHadLauncher) {
      $separator = if (-not $env:PATH -or $env:PATH.EndsWith(';')) { '' } else { ';' }
      $env:PATH = $env:PATH + $separator + $launchers
    }
    if ($addedToUserPath) {
      $broadcast = Send-EnvironmentChanged
      if ($broadcast -eq [IntPtr]::Zero) { Write-Warning 'User PATH was updated, but Windows did not confirm the environment-change notification.' }
      Write-Output "Added $launchers to your User PATH. Restart existing terminals if they do not recognize aact."
    } elseif (-not $processPathHadLauncher) {
      Write-Output 'Updated this installer process PATH. Restart existing terminals if they do not recognize aact.'
    }
  } catch {
    Write-Warning "Installed successfully, but could not add the launcher directory to User PATH: $($_.Exception.Message)"
    Write-Output "Run the launcher directly at $(Join-Path $launchers 'aact.cmd') or add that directory to PATH."
  }
  }
  $global:LASTEXITCODE = 0
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
