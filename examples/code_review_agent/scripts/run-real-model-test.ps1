# Runs an explicit, credentialed smoke test against an OpenAI-compatible model.
[CmdletBinding()]
param(
    [string]$ConfigPath = (Join-Path $PSScriptRoot "..\testdata\real_model\real_model_test_config.json")
)

$ErrorActionPreference = "Stop"
$resolvedConfig = (Resolve-Path -LiteralPath $ConfigPath).Path
$config = Get-Content -LiteralPath $resolvedConfig -Raw | ConvertFrom-Json

$apiKey = [string]$config.OPENAI_API_KEY
$baseURL = [string]$config.OPENAI_BASE_URL
$model = [string]$config.TRPC_AGENT_MODEL
$fixture = [string]$config.fixture
$timeoutSeconds = [int]$config.timeout_seconds

if ([string]::IsNullOrWhiteSpace($apiKey)) {
    throw "OPENAI_API_KEY is empty in $resolvedConfig"
}
if ([string]::IsNullOrWhiteSpace($model)) {
    throw "TRPC_AGENT_MODEL is empty in $resolvedConfig"
}
if ([string]::IsNullOrWhiteSpace($fixture)) {
    $fixture = "goroutine_context_leak"
}
if ($timeoutSeconds -le 0) {
    $timeoutSeconds = 90
}

$examplesDir = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$outputDir = Join-Path $PSScriptRoot "..\testdata\real_model\real_model_test_output"
$outputDir = [System.IO.Path]::GetFullPath($outputDir)
$dbPath = Join-Path $outputDir "review.db"

$previousAPIKey = $env:OPENAI_API_KEY
$previousBaseURL = $env:OPENAI_BASE_URL
$previousModel = $env:TRPC_AGENT_MODEL
$previousGoCache = $env:GOCACHE
$previousTemp = $env:TEMP
$previousTmp = $env:TMP
try {
    $env:OPENAI_API_KEY = $apiKey.Trim()
    $env:TRPC_AGENT_MODEL = $model.Trim()
    if ([string]::IsNullOrWhiteSpace($baseURL)) {
        Remove-Item Env:OPENAI_BASE_URL -ErrorAction SilentlyContinue
    } else {
        $env:OPENAI_BASE_URL = $baseURL.Trim()
    }

    New-Item -ItemType Directory -Force -Path $outputDir | Out-Null
    $env:GOCACHE = Join-Path $outputDir "go-cache"
    $env:TEMP = Join-Path $outputDir "tmp"
    $env:TMP = $env:TEMP
    New-Item -ItemType Directory -Force -Path $env:GOCACHE, $env:TEMP | Out-Null
    Push-Location $examplesDir
    try {
        & go run ./code_review_agent `
            --fixture $fixture `
            --mode llm `
            --model $env:TRPC_AGENT_MODEL `
            --sandbox mock `
            --dry-run `
            --timeout "${timeoutSeconds}s" `
            --out-dir $outputDir `
            --db $dbPath
        if ($LASTEXITCODE -ne 0) {
            throw "code review agent exited with code $LASTEXITCODE"
        }
    } finally {
        Pop-Location
    }

    $reportPath = Join-Path $outputDir "review_report.json"
    $report = Get-Content -LiteralPath $reportPath -Raw | ConvertFrom-Json
    if ($report.metrics.model_call_count -ne 1) {
        throw "model_call_count=$($report.metrics.model_call_count), expected 1"
    }
    if ($report.metrics.model_duration_ms -le 0) {
        throw "model_duration_ms=$($report.metrics.model_duration_ms), expected a positive duration"
    }
    if ($null -ne $report.metrics.exception_counts.model_error -and
        $report.metrics.exception_counts.model_error -ne 0) {
        throw "model_error=$($report.metrics.exception_counts.model_error); inspect $reportPath"
    }
    if ($report.summary -match "Model review failed") {
        throw "report indicates model degradation; inspect $reportPath"
    }

    Write-Host "Real-model test passed."
    Write-Host "Model: $model"
    Write-Host "Endpoint: $(if ($baseURL) { $baseURL } else { 'OpenAI default' })"
    Write-Host "Duration: $($report.metrics.model_duration_ms) ms"
    Write-Host "Report: $reportPath"
    $report.findings | Select-Object source, rule_id, file, line, title, confidence | Format-Table
} finally {
    $env:OPENAI_API_KEY = $previousAPIKey
    $env:OPENAI_BASE_URL = $previousBaseURL
    $env:TRPC_AGENT_MODEL = $previousModel
    $env:GOCACHE = $previousGoCache
    $env:TEMP = $previousTemp
    $env:TMP = $previousTmp
}
