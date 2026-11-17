$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

function Write-Reply([string]$status, $secret) {
    $reply = @{ status = $status }
    if ($null -ne $secret) { $reply.secret = $secret }
    [Console]::Out.WriteLine(($reply | ConvertTo-Json -Compress))
}

function Is-CredentialMissing($exception) {
	while ($null -ne $exception.InnerException) { $exception = $exception.InnerException }
	return $exception.HResult.ToString('X8') -eq '80070490'
}

try {
    $raw = [Console]::In.ReadToEnd()
    if ($raw.Length -gt 2097152) { Write-Reply 'unavailable' $null; exit 0 }
    $request = $raw | ConvertFrom-Json
    if ($request.version -ne 1 -or $request.service -ne 'AACT shared Vault' -or $request.account -notmatch '^state-[a-f0-9]{64}$') {
        Write-Reply 'unavailable' $null; exit 0
    }

    # This uses the Windows Runtime Credential Locker directly. It needs no
    # Add-Type, downloaded module, native helper, or secret-bearing arguments.
    $null = [Windows.Security.Credentials.PasswordVault,Windows.Security.Credentials,ContentType=WindowsRuntime]
    $locker = New-Object Windows.Security.Credentials.PasswordVault
    $resource = $request.service
    $account = $request.account

    if ($request.action -eq 'get') {
        try {
            $credential = $locker.Retrieve($resource, $account)
        } catch {
            if (Is-CredentialMissing $_.Exception) { Write-Reply 'missing' $null }
            else { Write-Reply 'unavailable' $null }
            exit 0
        }
        try {
            $credential.RetrievePassword()
            $secret = [string]$credential.Password
            if ($secret.Length -eq 0) { Write-Reply 'unavailable' $null; exit 0 }
            $null = [Convert]::FromBase64String($secret)
            Write-Reply 'ok' $secret
        } catch {
            Write-Reply 'unavailable' $null
        }
    } elseif ($request.action -eq 'set' -and $request.secret) {
        $secret = [string]$request.secret
        $decoded = [Convert]::FromBase64String($secret)
        [Array]::Clear($decoded, 0, $decoded.Length)
        try {
            $credential = New-Object Windows.Security.Credentials.PasswordCredential($resource, $account, $secret)
            $locker.Add($credential)
            Write-Reply 'ok' $null
        } catch {
            Write-Reply 'unavailable' $null
        }
    } else {
        Write-Reply 'unavailable' $null
    }
} catch {
    Write-Reply 'unavailable' $null
}
