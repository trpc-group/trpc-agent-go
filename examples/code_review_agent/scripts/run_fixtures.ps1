param([string]$OutputRoot = "")

$ErrorActionPreference = "Stop"
$root = (Resolve-Path (Join-Path $PSScriptRoot "..")).Path
if ([string]::IsNullOrWhiteSpace($OutputRoot)) { $OutputRoot = Join-Path $root "output\fixtures" }
$OutputRoot = [IO.Path]::GetFullPath($OutputRoot)
New-Item -ItemType Directory -Path $OutputRoot -Force | Out-Null
$fixtures = @("clean", "secret", "goroutine", "context", "resource", "database", "errors", "missing_test", "duplicate", "sandbox_failure", "sql_injection")
$summary = Join-Path $OutputRoot "summary.tsv"
"fixture`tfindings`twarnings`tneeds_human_review`tstatus" | Set-Content -LiteralPath $summary -Encoding utf8

Push-Location $root
try {
    foreach ($fixture in $fixtures) {
        $dir = Join-Path $OutputRoot $fixture
        New-Item -ItemType Directory -Path $dir -Force | Out-Null
        $db = Join-Path $dir "reviews.sqlite"
        if ($fixture -eq "sandbox_failure") {
            go run . --fixture $fixture --executor fake-fail --output-dir $dir --db $db
        } else {
            go run . --fixture $fixture --dry-run --output-dir $dir --db $db
        }
        if ($LASTEXITCODE -ne 0) { throw "fixture $fixture failed" }
        $reportFile = Get-ChildItem -Path $dir -Filter review_report.json -Recurse | Select-Object -First 1
        $report = Get-Content -LiteralPath $reportFile.FullName -Raw | ConvertFrom-Json
        "$fixture`t$($report.findings.Count)`t$($report.warnings.Count)`t$($report.needs_human_review.Count)`t$($report.task.status)" |
            Add-Content -LiteralPath $summary -Encoding utf8
    }
} finally { Pop-Location }

Write-Output "Fixture reports written to $OutputRoot"
Write-Output "Summary written to $summary"
