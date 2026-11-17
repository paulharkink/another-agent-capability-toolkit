$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$source = @'
using System;
using System.Runtime.InteropServices;

public static class AactCredMan {
    [StructLayout(LayoutKind.Sequential, CharSet = CharSet.Unicode)]
    public struct CREDENTIAL {
        public UInt32 Flags;
        public UInt32 Type;
        public string TargetName;
        public string Comment;
        public System.Runtime.InteropServices.ComTypes.FILETIME LastWritten;
        public UInt32 CredentialBlobSize;
        public IntPtr CredentialBlob;
        public UInt32 Persist;
        public UInt32 AttributeCount;
        public IntPtr Attributes;
        public string TargetAlias;
        public string UserName;
    }

    [DllImport("Advapi32.dll", EntryPoint="CredReadW", CharSet=CharSet.Unicode, SetLastError=true)]
    public static extern bool CredRead(string target, UInt32 type, UInt32 flags, out IntPtr credential);
    [DllImport("Advapi32.dll", EntryPoint="CredWriteW", CharSet=CharSet.Unicode, SetLastError=true)]
    public static extern bool CredWrite(ref CREDENTIAL credential, UInt32 flags);
    [DllImport("Advapi32.dll", EntryPoint="CredFree")]
    public static extern void CredFree(IntPtr buffer);
}
'@

function Write-Reply($status, $secret) {
    $reply = @{ status = $status }
    if ($null -ne $secret) { $reply.secret = $secret }
    [Console]::Out.WriteLine(($reply | ConvertTo-Json -Compress))
}

try {
    $raw = [Console]::In.ReadToEnd()
    if ($raw.Length -gt 1048576) { Write-Reply 'unavailable' $null; exit 0 }
    $request = $raw | ConvertFrom-Json
    if ($request.version -ne 1 -or $request.service -ne 'AACT shared Vault' -or $request.account -notmatch '^state-[a-f0-9]{64}$') {
        Write-Reply 'unavailable' $null; exit 0
    }
    Add-Type -TypeDefinition $source
    $target = $request.service + ':' + $request.account
    if ($request.action -eq 'get') {
        $credential = [IntPtr]::Zero
        if (-not [AactCredMan]::CredRead($target, 1, 0, [ref]$credential)) {
            if ([Runtime.InteropServices.Marshal]::GetLastWin32Error() -eq 1168) { Write-Reply 'missing' $null }
            else { Write-Reply 'unavailable' $null }
            exit 0
        }
        try {
            $item = [Runtime.InteropServices.Marshal]::PtrToStructure($credential, [type][AactCredMan+CREDENTIAL])
            if ($item.CredentialBlobSize -gt 2560) { Write-Reply 'unavailable' $null; exit 0 }
            $bytes = New-Object byte[] $item.CredentialBlobSize
            [Runtime.InteropServices.Marshal]::Copy($item.CredentialBlob, $bytes, 0, $bytes.Length)
            $encoded = [Text.Encoding]::ASCII.GetString($bytes)
            Write-Reply 'ok' $encoded
            [Array]::Clear($bytes, 0, $bytes.Length)
        } finally { [AactCredMan]::CredFree($credential) }
    } elseif ($request.action -eq 'set' -and $request.secret) {
        $encoded = [string]$request.secret
        $null = [Convert]::FromBase64String($encoded)
        $bytes = [Text.Encoding]::ASCII.GetBytes($encoded)
        if ($bytes.Length -gt 2560) { Write-Reply 'unavailable' $null; exit 0 }
        $blob = [Runtime.InteropServices.Marshal]::AllocHGlobal($bytes.Length)
        try {
            [Runtime.InteropServices.Marshal]::Copy($bytes, 0, $blob, $bytes.Length)
            $credential = New-Object AactCredMan+CREDENTIAL
            $credential.Type = 1
            $credential.TargetName = $target
            $credential.UserName = [Environment]::UserName
            $credential.CredentialBlob = $blob
            $credential.CredentialBlobSize = [UInt32]$bytes.Length
            $credential.Persist = 2
            Write-Reply $(if ([AactCredMan]::CredWrite([ref]$credential, 0)) { 'ok' } else { 'unavailable' }) $null
        } finally {
            [Runtime.InteropServices.Marshal]::FreeHGlobal($blob)
            [Array]::Clear($bytes, 0, $bytes.Length)
        }
    } else { Write-Reply 'unavailable' $null }
} catch { Write-Reply 'unavailable' $null }
