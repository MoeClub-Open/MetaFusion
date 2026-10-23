# Preview: pwsh -File scripts/clean-local.ps1 -IncludeGitArtifacts
# Delete the listed artifacts: add -Apply in an authorized local environment.
[CmdletBinding()]
param(
    [switch]$Apply,
    [switch]$IncludeGitArtifacts
)

$ErrorActionPreference = 'Stop'
$cleanupRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$cleanupPaths = @(
    '.tmp', 'tmp', '.playwright-cli', 'output',
    'frontend/.playwright-cli', 'frontend/output', 'frontend/.next',
    'frontend/tsconfig.tsbuildinfo'
)

if ($IncludeGitArtifacts) {
    # Explicit old job artifacts only; Git history, refs and configuration stay intact.
    $cleanupPaths += @(
        '.git/mf-key-release', '.git/mf-release-20261017',
        '.git/commit-review-api-final.log', '.git/commit-review-browser-checks.js',
        '.git/commit-review-browser-checks.log', '.git/commit-review-browser.js',
        '.git/commit-review-doc-routes.log', '.git/commit-review-editor.png',
        '.git/commit-review-en.png', '.git/commit-review-live-en.png',
        '.git/commit-review-live-zh.png', '.git/commit-review-release.py',
        '.git/commit-review-skills-final.log', '.git/commit-review-zh.png',
        '.git/mf-account-release-20261019.sh', '.git/mf-account-runner-20261019.sh',
        '.git/mf-context-release-20261019.sh', '.git/mf-context-runner-20261019.sh',
        '.git/mf-key-deploy.sh', '.git/mf-key-finish.sh', '.git/mf-key-prepare.sh',
        '.git/mf-key-ssh.py', '.git/mf-key-verify.sh',
        '.git/mf-protocol-release-20261019.sh', '.git/mf-protocol-runner-20261019.sh',
        '.git/mf-review-osa-recheck.sh', '.git/mf-review-osa.sh',
        '.git/mf-review-source.tar.gz', '.git/ts-migration-cli.log',
        '.git/ts-migration-errors.log', '.git/ts-migration-tests.log',
        '.git/ts-stage-warnings.log'
    )
}

# Complete preflight before deleting anything.
$cleanupItems = @(foreach ($relative in $cleanupPaths) {
    $candidate = Join-Path $cleanupRoot $relative
    if (-not (Test-Path -LiteralPath $candidate)) { continue }
    $resolved = (Resolve-Path -LiteralPath $candidate).Path
    if (-not $resolved.StartsWith($cleanupRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
        throw "Path outside repository: $relative"
    }
    for ($ancestor = $resolved; $ancestor -ne $cleanupRoot; $ancestor = Split-Path -Parent $ancestor) {
        if ((Get-Item -LiteralPath $ancestor -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
            throw "Linked cleanup path: $relative"
        }
    }
    $tracked = @(git -C $cleanupRoot --literal-pathspecs ls-files -- $relative)
    if ($LASTEXITCODE -ne 0) { throw 'Cannot inspect Git tracked files.' }
    if ($tracked.Count) { throw "Tracked content in cleanup path: $relative" }

    $item = Get-Item -LiteralPath $resolved -Force
    $entries = if ($item.PSIsContainer) { @(Get-ChildItem -LiteralPath $resolved -Recurse -Force) } else { @($item) }
    if (@($entries | Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint }).Count) {
        throw "Linked content in cleanup path: $relative"
    }
    $files = @($entries | Where-Object { -not $_.PSIsContainer })
    [pscustomobject]@{
        RelativePath = $relative
        AbsolutePath = $resolved
        Files = $files.Count
        Bytes = [long](($files | Measure-Object Length -Sum).Sum)
    }
})

$cleanupItems | Select-Object RelativePath, Files, @{Name='MiB'; Expression={[math]::Round($_.Bytes / 1MB, 2)}} | Format-Table -AutoSize
Write-Host ('Total: {0:N2} MiB' -f (($cleanupItems | Measure-Object Bytes -Sum).Sum / 1MB))
if (-not $Apply) {
    Write-Host 'Preview only. No files deleted.'
    return
}

foreach ($item in $cleanupItems) {
    Remove-Item -LiteralPath $item.AbsolutePath -Recurse -Force -ErrorAction Stop
    Write-Host ('Removed: ' + $item.RelativePath)
}
