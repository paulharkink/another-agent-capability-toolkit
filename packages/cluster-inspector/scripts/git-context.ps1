param(
    [switch]$RepositoryOnly,
    [Parameter(Mandatory = $true, Position = 0)]
    [string]$RepositoryPath
)

$root = & git -C $RepositoryPath rev-parse --show-toplevel 2>$null
if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($root)) {
    Write-Error "Not inside a Git worktree: $RepositoryPath"
    exit 1
}

$repository = Split-Path -Leaf $root.Trim()

Write-Output "repository=$repository"
Write-Output "project_fragment=$repository"

if (-not $RepositoryOnly) {
    $branch = (& git -C $root.Trim() branch --show-current).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($branch)) {
        Write-Error "No current Git branch is available."
        exit 1
    }
    Write-Output "branch=$branch"
}
