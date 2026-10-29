# Preview: pwsh -File scripts/clean-local.ps1 -IncludeSiblingRepositories
# Delete the listed artifacts: add -Apply in an authorized local environment.
[CmdletBinding()]
param(
    [switch]$Apply,
    [switch]$IncludeSiblingRepositories
)

$ErrorActionPreference = 'Stop'
$cleanupRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$cleanupRepositories = [ordered]@{
    '.' = @(
        '.playwright-cli', 'output', 'frontend/.playwright-cli', 'frontend/output',
        'frontend/.next', 'frontend/tsconfig.tsbuildinfo', 'scripts/__pycache__'
    )
}
if ($IncludeSiblingRepositories) {
    $cleanupRepositories['../metafusion-auth'] = @(
        'admin/.next', 'admin/tsconfig.tsbuildinfo', 'admin/.playwright-cli', 'admin/output',
        'user/.next', 'user/tsconfig.tsbuildinfo'
    )
    $cleanupRepositories['../metafusion-community'] = @('admin/.next', 'admin/tsconfig.tsbuildinfo')
    $cleanupRepositories['../metafusion-storage'] = @('admin/.next', 'admin/tsconfig.tsbuildinfo')
    $cleanupRepositories['../metafusion-docs'] = @('docs/.vitepress/cache', 'docs/.vitepress/dist')
    $cleanupRepositories['../metafusion-skills'] = @('skills/metafusion-relationship-query/scripts/__pycache__')
}

# Complete preflight before deleting anything.
$cleanupItems = @(foreach ($repository in $cleanupRepositories.Keys) {
    $repositoryPath = Join-Path $cleanupRoot $repository
    if (-not (Test-Path -LiteralPath $repositoryPath)) { continue }
    $repositoryRoot = (Resolve-Path -LiteralPath $repositoryPath).Path
    if ((Get-Item -LiteralPath $repositoryRoot -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
        throw "Linked repository: $repository"
    }
    foreach ($relative in $cleanupRepositories[$repository]) {
        $candidate = Join-Path $repositoryRoot $relative
        if (-not (Test-Path -LiteralPath $candidate)) { continue }
        $resolved = (Resolve-Path -LiteralPath $candidate).Path
        if (-not $resolved.StartsWith($repositoryRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
            throw "Path outside repository: $relative"
        }
        for ($ancestor = $resolved; $ancestor -ne $repositoryRoot; $ancestor = Split-Path -Parent $ancestor) {
            if ((Get-Item -LiteralPath $ancestor -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) {
                throw "Linked cleanup path: $relative"
            }
        }
        $tracked = @(git -C $repositoryRoot --literal-pathspecs ls-files -- $relative)
        if ($LASTEXITCODE -ne 0) { throw 'Cannot inspect Git tracked files.' }
        if ($tracked.Count) { throw "Tracked content in cleanup path: $relative" }

        $item = Get-Item -LiteralPath $resolved -Force
        $entries = if ($item.PSIsContainer) { @(Get-ChildItem -LiteralPath $resolved -Recurse -Force) } else { @($item) }
        if (@($entries | Where-Object { $_.Attributes -band [IO.FileAttributes]::ReparsePoint }).Count) {
            throw "Linked content in cleanup path: $relative"
        }
        $files = @($entries | Where-Object { -not $_.PSIsContainer })
        [pscustomobject]@{
            Repository = $repository
            RelativePath = $relative
            AbsolutePath = $resolved
            Files = $files.Count
            Bytes = [long](($files | Measure-Object Length -Sum).Sum)
        }
    }
})

$cleanupItems | Select-Object Repository, RelativePath, Files, @{Name='MiB'; Expression={[math]::Round($_.Bytes / 1MB, 2)}} | Format-Table -AutoSize
Write-Host ('Total: {0:N2} MiB' -f (($cleanupItems | Measure-Object Bytes -Sum).Sum / 1MB))
if (-not $Apply) {
    Write-Host 'Preview only. No files deleted.'
    return
}

foreach ($item in $cleanupItems) {
    Remove-Item -LiteralPath $item.AbsolutePath -Recurse -Force -ErrorAction Stop
    Write-Host ('Removed: ' + $item.Repository + '/' + $item.RelativePath)
}
