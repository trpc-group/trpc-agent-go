param(
    [Parameter(Mandatory = $true)]
    [ValidateSet("managed", "container", "e2b")]
    [string]$Runtime
)

$ErrorActionPreference = "Stop"
$examplesRoot = Resolve-Path (Join-Path $PSScriptRoot "..\..")

try {
    Push-Location $examplesRoot
    switch ($Runtime) {
        "managed" {
            if ($env:OS -eq "Windows_NT") {
                throw "managed sandbox integration requires Linux or macOS"
            }
            $env:TRPC_CR_TEST_MANAGED = "1"
            go test ./code_review_agent/sandboxrunner -run ManagedSandboxIntegration -count=1 -v
        }
        "container" {
            docker version | Out-Null
            docker pull golang:1.24 | Out-Null
            $env:TRPC_CR_TEST_CONTAINER = "1"
            go test ./code_review_agent/sandboxrunner ./code_review_agent/skillrunner -run Container -count=1 -v
        }
        "e2b" {
            if ([string]::IsNullOrWhiteSpace($env:E2B_API_KEY)) {
                throw "E2B_API_KEY is required"
            }
            $env:TRPC_CR_TEST_E2B = "1"
            go test ./code_review_agent/sandboxrunner ./code_review_agent/skillrunner -run E2B -count=1 -v
        }
    }
}
finally {
    Remove-Item Env:TRPC_CR_TEST_MANAGED -ErrorAction SilentlyContinue
    Remove-Item Env:TRPC_CR_TEST_CONTAINER -ErrorAction SilentlyContinue
    Remove-Item Env:TRPC_CR_TEST_E2B -ErrorAction SilentlyContinue
    Pop-Location
}
