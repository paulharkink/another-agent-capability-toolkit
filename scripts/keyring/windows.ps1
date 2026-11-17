$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$WarningPreference = 'SilentlyContinue'
$InformationPreference = 'SilentlyContinue'

function Write-Reply([string]$status, $secret) {
    $reply = @{ status = $status }
    if ($null -ne $secret) { $reply.secret = $secret }
    [Console]::Out.WriteLine(($reply | ConvertTo-Json -Compress))
}

try {
    $raw = [Console]::In.ReadToEnd()
    if ($raw.Length -gt 2097152) { Write-Reply 'unavailable' $null; exit 0 }
    $request = $raw | ConvertFrom-Json
    if ($request.version -ne 1 -or $request.service -ne 'AACT shared Vault' -or $request.account -notmatch '^state-[a-f0-9]{64}$') {
        Write-Reply 'unavailable' $null; exit 0
    }
    if (-not (Get-Module -ListAvailable -Name Microsoft.PowerShell.SecretManagement)) {
        Write-Reply 'unavailable' $null; exit 0
    }
    Import-Module Microsoft.PowerShell.SecretManagement -ErrorAction Stop | Out-Null
    $vault = 'AACT'
    $target = $request.service + ':' + $request.account
    $vaultInfo = Get-SecretVault -Name $vault -ErrorAction SilentlyContinue
    if ($null -eq $vaultInfo) { Write-Reply 'unavailable' $null; exit 0 }

    if ($request.action -eq 'get') {
        $found = @(Get-SecretInfo -Name $target -Vault $vault -ErrorAction Stop)
        if ($found.Count -eq 0) { Write-Reply 'missing' $null; exit 0 }
        try {
            $secret = Get-Secret -Name $target -Vault $vault -ErrorAction Stop
        } catch {
            Write-Reply 'unavailable' $null
            exit 0
        }
        if ($secret -isnot [string] -or $secret.Length -eq 0) { Write-Reply 'unavailable' $null; exit 0 }
        Write-Reply 'ok' $secret
    } elseif ($request.action -eq 'set' -and $request.secret) {
        $null = [Convert]::FromBase64String([string]$request.secret)
        Set-Secret -Name $target -Secret ([string]$request.secret) -Vault $vault -ErrorAction Stop | Out-Null
        Write-Reply 'ok' $null
    } else {
        Write-Reply 'unavailable' $null
    }
} catch {
    Write-Reply 'unavailable' $null
}
