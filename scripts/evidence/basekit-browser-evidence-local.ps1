param(
  [string]$OwnerEmail = "admin@example.com",
  [string]$OwnerUsername = "admin",
  [string]$OwnerDisplayName = "StackKit Owner",
  [string]$EvidenceRoot = ".",
  [string]$PlaywrightModuleDir = $env:STACKKIT_PLAYWRIGHT_MODULE_DIR,
  [string]$BrowserChannel = $env:STACKKIT_BROWSER_CHANNEL,
  [string]$PreflightReportPath = "",
  [string]$DockerConfigJson = $env:STACKKIT_FRESH_VM_DOCKER_CONFIG_JSON,
  [string]$DockerConfigPath = $env:STACKKIT_FRESH_VM_DOCKER_CONFIG,
  [switch]$Headed,
  [switch]$KeepVM,
  [switch]$SkipFreshVM,
  [switch]$SkipBrowserPreflight,
  [switch]$NoInstallPlaywright,
  [switch]$PreflightOnly
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"
if ($BrowserChannel) {
  $BrowserChannel = $BrowserChannel.Trim().ToLowerInvariant()
  if (@("default", "playwright-chromium", "chromium") -contains $BrowserChannel) {
    $BrowserChannel = ""
  }
}

$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot "..\..")).Path
$RequestedEvidenceRootPath = Join-Path $RepoRoot $EvidenceRoot
New-Item -ItemType Directory -Force -Path $RequestedEvidenceRootPath | Out-Null
$EvidenceRootPath = (Resolve-Path -LiteralPath $RequestedEvidenceRootPath).Path
$ScenarioDir = Join-Path $EvidenceRootPath "artifacts\scenarios\SK-S1"
$HomelabPath = Join-Path $ScenarioDir "homelab.json"
$BrowserEvidencePath = Join-Path $ScenarioDir "browser-evidence.json"
$SetupStatePath = Join-Path $ScenarioDir "setup-state.yaml"
$BrowserEvidenceRunId = $env:STACKKIT_BROWSER_EVIDENCE_RUN_ID
if ($BrowserEvidenceRunId) {
  $BrowserEvidenceRunId = $BrowserEvidenceRunId.Trim()
}
if (-not $BrowserEvidenceRunId) {
  $BrowserEvidenceRunId = "browser-" + (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH-mm-ss-fffZ")
}
$DefaultPlaywrightModuleDir = Join-Path $RepoRoot ".stackkit\tools\browser-evidence"
if (-not $PlaywrightModuleDir) {
  $PlaywrightModuleDir = $DefaultPlaywrightModuleDir
}
if (-not $PreflightReportPath) {
  $PreflightReportPath = Join-Path $ScenarioDir "browser-evidence-preflight.json"
}
$PhaseTimeoutSeconds = 14 * 60
$ShouldCleanupFreshVM = $false
$script:PreflightChecks = [System.Collections.Generic.List[object]]::new()
$script:CurrentPhase = "wrapper"
$script:LastNativeCommand = $null
$script:LastFailedNativeCommand = $null

function Set-WrapperPhase {
  param([string]$Phase)
  if ($Phase) {
    $script:CurrentPhase = $Phase
  }
}

function Invoke-BoundedNative {
  param(
    [Parameter(Mandatory = $true)]
    [string]$Name,
    [Parameter(Mandatory = $true)]
    [string]$FilePath,
    [string[]]$Arguments = @(),
    [hashtable]$Environment = @{},
    [int]$TimeoutSeconds = $PhaseTimeoutSeconds
  )

  Write-Host ""
  Write-Host "==> $Name"
  $script:LastNativeCommand = [ordered]@{
    name = $Name
    filePath = $FilePath
    arguments = @($Arguments)
    timeoutSeconds = $TimeoutSeconds
  }
  $psi = [System.Diagnostics.ProcessStartInfo]::new()
  $psi.FileName = $FilePath
  foreach ($Argument in $Arguments) {
    [void]$psi.ArgumentList.Add($Argument)
  }
  $psi.WorkingDirectory = $RepoRoot
  $psi.UseShellExecute = $false
  $psi.RedirectStandardOutput = $true
  $psi.RedirectStandardError = $true
  foreach ($Key in $Environment.Keys) {
    $psi.Environment[$Key] = [string]$Environment[$Key]
  }

  $process = [System.Diagnostics.Process]::new()
  $process.StartInfo = $psi
  try {
    $started = $process.Start()
  } catch {
    $startError = [string]$_.Exception.Message
    $script:LastNativeCommand["failureClass"] = "start_failed"
    $hostIssue = Get-NativeStartHostIssue -Message $startError
    if ($hostIssue) {
      $script:LastNativeCommand["hostIssue"] = $hostIssue
    }
    $script:LastFailedNativeCommand = $script:LastNativeCommand
    throw "$Name failed to start '$FilePath': $startError"
  }
  if (-not $started) {
    $script:LastNativeCommand["failureClass"] = "start_failed"
    $script:LastFailedNativeCommand = $script:LastNativeCommand
    throw "$Name failed to start '$FilePath'"
  }
  $stdoutTask = $process.StandardOutput.ReadToEndAsync()
  $stderrTask = $process.StandardError.ReadToEndAsync()
  if (-not $process.WaitForExit($TimeoutSeconds * 1000)) {
    try {
      $process.Kill($true)
    } catch {
      $process.Kill()
    }
    $script:LastNativeCommand["failureClass"] = "timeout"
    $script:LastFailedNativeCommand = $script:LastNativeCommand
    throw "$Name exceeded ${TimeoutSeconds}s. Split or diagnose the phase; do not raise the timeout."
  }
  $stdout = $stdoutTask.GetAwaiter().GetResult()
  $stderr = $stderrTask.GetAwaiter().GetResult()
  if ($stdout.Trim()) {
    Write-Host $stdout
  }
  if ($stderr.Trim()) {
    Write-Host $stderr
  }
  if ($process.ExitCode -ne 0) {
    $script:LastNativeCommand["failureClass"] = "exit_nonzero"
    $script:LastNativeCommand["exitCode"] = $process.ExitCode
    $script:LastFailedNativeCommand = $script:LastNativeCommand
    throw "$Name failed with exit code $($process.ExitCode)"
  }
  $script:LastNativeCommand = $null
  return $stdout
}

function Get-NativeStartHostIssue {
  param([string]$Message)
  $value = [string]$Message
  if ($value -match "CreateProcessAsUserW failed: 5" -or $value -match "Access is denied") {
    return "windows-createprocessasuser-access-denied"
  }
  return ""
}

function Assert-Command {
  param([string]$Name)
  $command = Get-Command $Name -ErrorAction SilentlyContinue
  if (-not $command) {
    $message = "Required command not found: $Name"
    Add-PreflightCheck -Name "Required command: $Name" -Status "fail" -TimeoutSeconds 0 -ErrorMessage $message
    return $false
  }
  Add-PreflightCheck -Name "Required command: $Name" -Status "pass" -TimeoutSeconds 0 -Evidence @{
    source = [string]$command.Source
  }
  return $true
}

function Add-PreflightCheck {
  param(
    [string]$Name,
    [string]$Status,
    [int]$TimeoutSeconds,
    [string]$ErrorMessage = "",
    [hashtable]$Evidence = @{},
    [object]$NativeCommand = $null
  )

  $entry = [ordered]@{
    name = $Name
    status = $Status
    timeoutSeconds = $TimeoutSeconds
  }
  if ($ErrorMessage) {
    $entry["error"] = $ErrorMessage
  }
  if ($Evidence.Count -gt 0) {
    $entry["evidence"] = $Evidence
  }
  if ($NativeCommand) {
    $entry["nativeCommand"] = $NativeCommand
  }
  [void]$script:PreflightChecks.Add($entry)
}

function Get-FailedNativeCommandForCheck {
  param([string]$Name)
  if (-not $script:LastFailedNativeCommand) {
    return $null
  }
  if ([string]$script:LastFailedNativeCommand["name"] -ne $Name) {
    return $null
  }
  return $script:LastFailedNativeCommand
}

function Write-PreflightReport {
  param(
    [string]$Status,
    [string]$ErrorMessage = ""
  )

  $parent = Split-Path -Parent $PreflightReportPath
  if ($parent) {
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
  }
  $BrowserChannelLabel = if ($BrowserChannel) { $BrowserChannel } else { "playwright-chromium" }
  $report = [ordered]@{
    scenarioId = "SK-S1"
    runId = $BrowserEvidenceRunId
    kind = "browser-evidence-preflight"
    status = $Status
    generatedAt = (Get-Date).ToUniversalTime().ToString("o")
    evidenceRoot = $EvidenceRootPath
    playwrightModuleDir = $PlaywrightModuleDir
    browserChannel = $BrowserChannelLabel
    phaseTimeoutSeconds = $PhaseTimeoutSeconds
    checks = $script:PreflightChecks.ToArray()
  }
  $failedChecks = Get-PreflightFailures
  if ($failedChecks.Count -gt 0) {
    $report["failedChecks"] = @($failedChecks | ForEach-Object { $_.name })
  }
  if ($ErrorMessage) {
    $report["error"] = $ErrorMessage
  }
  $report | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $PreflightReportPath -Encoding UTF8
}

function Write-BrowserEvidenceFailure {
  param(
    [string]$Phase,
    [string]$ErrorMessage
  )

  $parent = Split-Path -Parent $BrowserEvidencePath
  if ($parent) {
    New-Item -ItemType Directory -Force -Path $parent | Out-Null
  }
  $BrowserChannelLabel = if ($BrowserChannel) { $BrowserChannel } else { "playwright-chromium" }
  $report = [ordered]@{
    scenarioId = "SK-S1"
    runId = $BrowserEvidenceRunId
    status = "fail"
    generatedAt = (Get-Date).ToUniversalTime().ToString("o")
    ownerEmail = $OwnerEmail
    ownerUsername = $OwnerUsername
    browserChannel = $BrowserChannelLabel
    browserUrl = "http://base.home.localhost"
    error = $ErrorMessage
    failurePhase = $Phase
    checks = @()
    screenshots = @()
    diagnostics = [ordered]@{
      wrapper = [ordered]@{
        phase = $Phase
        evidenceRoot = $EvidenceRootPath
        preflightReportPath = $PreflightReportPath
        homelabPath = $HomelabPath
      }
    }
  }
  if ($script:LastFailedNativeCommand) {
    $report.diagnostics.wrapper["nativeCommand"] = $script:LastFailedNativeCommand
  } elseif ($script:LastNativeCommand) {
    $report.diagnostics.wrapper["nativeCommand"] = $script:LastNativeCommand
  }
  $report | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $BrowserEvidencePath -Encoding UTF8
}

function Invoke-PreflightReportValidation {
  Invoke-BoundedNative `
    -Name "Validate SK-S1 browser preflight report" `
    -FilePath "go" `
    -Arguments @("test", "-v", "-tags", "production", "-run", "^TestBaseKitBrowserEvidencePreflightReport$", "-timeout", "2m", "./tests/production") `
    -Environment @{
      STACKKIT_BROWSER_PREFLIGHT_PATH = $PreflightReportPath
    } `
    -TimeoutSeconds 120 | Out-Null
}

function Invoke-PreflightReportValidationIfAvailable {
  $goCheck = $script:PreflightChecks | Where-Object { $_.name -eq "Required command: go" } | Select-Object -First 1
  if (-not $goCheck -or $goCheck.status -ne "pass") {
    Write-Host "Skipping browser preflight report validation because go is not available."
    return
  }
  Invoke-PreflightReportValidation
}

function Invoke-BrowserEvidenceFailureValidationIfAvailable {
  $goCheck = $script:PreflightChecks | Where-Object { $_.name -eq "Required command: go" } | Select-Object -First 1
  if (-not $goCheck -or $goCheck.status -ne "pass") {
    Write-Host "Skipping browser failure evidence validation because go is not available."
    return
  }
  Invoke-BoundedNative `
    -Name "Validate SK-S1 browser failure evidence manifest" `
    -FilePath "go" `
    -Arguments @("test", "-v", "-tags", "production", "-run", "^TestBaseKitBrowserEvidenceFailureManifest$", "-timeout", "2m", "./tests/production") `
    -Environment @{
      STACKKIT_BROWSER_FAILURE_EVIDENCE_PATH = $BrowserEvidencePath
    } `
    -TimeoutSeconds 120 | Out-Null
}

function Get-PreflightFailures {
  return @($script:PreflightChecks | Where-Object { $_.status -eq "fail" })
}

function Assert-NoPreflightFailures {
  param([string]$Context)
  $failedChecks = Get-PreflightFailures
  if ($failedChecks.Count -eq 0) {
    return
  }
  $summary = ($failedChecks | ForEach-Object {
      if ($_.error) {
        "$($_.name): $($_.error)"
      } else {
        "$($_.name)"
      }
    }) -join "; "
  $message = "${Context}: ${summary}"
  Write-PreflightReport -Status "fail" -ErrorMessage $message
  try {
    Invoke-PreflightReportValidationIfAvailable
  } catch {
    throw "${message}; preflight report validation failed: $_"
  }
  throw $message
}

function Get-InvalidPreflightSkips {
  $invalid = @()
  foreach ($Check in $script:PreflightChecks) {
    if ($Check.status -ne "skipped") {
      continue
    }
    $allowedInstalledBrowserSkip = $Check.name -eq "Install isolated Playwright Chromium" -and [bool]$BrowserChannel
    if (-not $allowedInstalledBrowserSkip) {
      $invalid += $Check
    }
  }
  return @($invalid)
}

function Assert-ReleaseReadyPreflight {
  param([string]$Context)
  Assert-NoPreflightFailures -Context $Context
  $invalidSkips = Get-InvalidPreflightSkips
  if ($invalidSkips.Count -eq 0) {
    return
  }
  $summary = ($invalidSkips | ForEach-Object { "$($_.name) is skipped" }) -join "; "
  $message = "${Context}: ${summary}. Only Install isolated Playwright Chromium may be skipped when BrowserChannel uses an installed browser."
  foreach ($Check in $invalidSkips) {
    $Check["status"] = "fail"
    $Check["error"] = "Skipped critical preflight check cannot satisfy release evidence."
  }
  Write-PreflightReport -Status "fail" -ErrorMessage $message
  try {
    Invoke-PreflightReportValidationIfAvailable
  } catch {
    throw "${message}; preflight report validation failed: $_"
  }
  throw $message
}

function Invoke-RecordedPreflight {
  param(
    [Parameter(Mandatory = $true)]
    [string]$Name,
    [Parameter(Mandatory = $true)]
    [string]$FilePath,
    [string[]]$Arguments = @(),
    [hashtable]$Environment = @{},
    [int]$TimeoutSeconds = $PhaseTimeoutSeconds
  )

  try {
    $stdout = Invoke-BoundedNative `
      -Name $Name `
      -FilePath $FilePath `
      -Arguments $Arguments `
      -Environment $Environment `
      -TimeoutSeconds $TimeoutSeconds
    $evidence = @{}
    $output = Limit-PreflightText $stdout
    if ($output) {
      $evidence["output"] = $output
    }
    Add-PreflightCheck -Name $Name -Status "pass" -TimeoutSeconds $TimeoutSeconds -Evidence $evidence
  } catch {
    $message = [string]$_
    Add-PreflightCheck -Name $Name -Status "fail" -TimeoutSeconds $TimeoutSeconds -ErrorMessage $message -NativeCommand (Get-FailedNativeCommandForCheck -Name $Name)
  }
}

function Invoke-RecordedPreflightOutputEquals {
  param(
    [Parameter(Mandatory = $true)]
    [string]$Name,
    [Parameter(Mandatory = $true)]
    [string]$FilePath,
    [string[]]$Arguments = @(),
    [Parameter(Mandatory = $true)]
    [string]$ExpectedOutput,
    [int]$TimeoutSeconds = $PhaseTimeoutSeconds
  )

  $actual = ""
  try {
    $stdout = Invoke-BoundedNative `
      -Name $Name `
      -FilePath $FilePath `
      -Arguments $Arguments `
      -TimeoutSeconds $TimeoutSeconds
    $actual = ($stdout -replace "`r", "").Trim()
    if ($actual -ne $ExpectedOutput) {
      throw "$Name returned '$actual', want '$ExpectedOutput'"
    }
    Add-PreflightCheck -Name $Name -Status "pass" -TimeoutSeconds $TimeoutSeconds -Evidence @{
      output = $actual
      expected = $ExpectedOutput
    }
  } catch {
    $message = [string]$_
    $evidence = @{ expected = $ExpectedOutput }
    if ($actual) {
      $evidence["output"] = $actual
    }
    Add-PreflightCheck -Name $Name -Status "fail" -TimeoutSeconds $TimeoutSeconds -ErrorMessage $message -Evidence $evidence -NativeCommand (Get-FailedNativeCommandForCheck -Name $Name)
  }
}

function Limit-PreflightText {
  param([string]$Text)
  $value = ($Text -replace "`r", "").Trim()
  if ($value.Length -le 1200) {
    return $value
  }
  return $value.Substring(0, 1200)
}

function Invoke-BrowserEvidencePreflight {
  Invoke-RecordedPreflight `
    -Name "Docker Desktop availability" `
    -FilePath "docker" `
    -Arguments @("version") `
    -TimeoutSeconds 60

  Invoke-RecordedPreflightOutputEquals `
    -Name "Docker Desktop context" `
    -FilePath "docker" `
    -Arguments @("context", "show") `
    -ExpectedOutput "desktop-linux" `
    -TimeoutSeconds 60

  if (-not $NoInstallPlaywright) {
    New-Item -ItemType Directory -Force -Path $PlaywrightModuleDir | Out-Null
    Invoke-RecordedPreflight `
      -Name "Install isolated Playwright package" `
      -FilePath "npm" `
      -Arguments @("install", "--prefix", $PlaywrightModuleDir, "--no-save", "--package-lock=false", "playwright") `
      -TimeoutSeconds 180

    if ($BrowserChannel) {
      Add-PreflightCheck -Name "Install isolated Playwright Chromium" -Status "skipped" -TimeoutSeconds 180 -Evidence @{
        reason = "installed-browser-channel"
        browserChannel = $BrowserChannel
      }
    } else {
      Invoke-RecordedPreflight `
        -Name "Install isolated Playwright Chromium" `
        -FilePath "npm" `
        -Arguments @("exec", "--prefix", $PlaywrightModuleDir, "--", "playwright", "install", "chromium") `
        -TimeoutSeconds 180
    }
  } else {
    Add-PreflightCheck -Name "Install isolated Playwright package" -Status "skipped" -TimeoutSeconds 180 -Evidence @{
      reason = "operator-no-install-playwright"
    }
    $SkipBrowserChannelLabel = if ($BrowserChannel) { $BrowserChannel } else { "playwright-chromium" }
    Add-PreflightCheck -Name "Install isolated Playwright Chromium" -Status "skipped" -TimeoutSeconds 180 -Evidence @{
      reason = "operator-no-install-playwright"
      browserChannel = $SkipBrowserChannelLabel
    }
  }

  $toolEnv = @{
    STACKKIT_PLAYWRIGHT_MODULE_DIR = $PlaywrightModuleDir
    STACKKIT_BROWSER_EVIDENCE_RUN_ID = $BrowserEvidenceRunId
  }
  if ($BrowserChannel) {
    $toolEnv["STACKKIT_BROWSER_CHANNEL"] = $BrowserChannel
  }
  Invoke-RecordedPreflight `
    -Name "Playwright package availability" `
    -FilePath "node" `
    -Arguments @("-e", "const { createRequire } = require('module'); const path = require('path'); const req = createRequire(path.join(process.env.STACKKIT_PLAYWRIGHT_MODULE_DIR, 'package.json')); req('playwright'); console.log('playwright=available')") `
    -Environment $toolEnv `
    -TimeoutSeconds 60

  Invoke-RecordedPreflight `
    -Name "Playwright Chromium availability" `
    -FilePath "node" `
    -Arguments @("-e", "const { createRequire } = require('module'); const path = require('path'); const req = createRequire(path.join(process.env.STACKKIT_PLAYWRIGHT_MODULE_DIR, 'package.json')); const { chromium } = req('playwright'); const channel = (process.env.STACKKIT_BROWSER_CHANNEL || '').trim(); const launchOptions = { headless: true }; if (channel) launchOptions.channel = channel; chromium.launch(launchOptions).then(async (browser) => { await browser.close(); console.log(channel ? 'browser-channel=' + channel : 'chromium=available'); }).catch((error) => { console.error(error.message); process.exit(1); })") `
    -Environment $toolEnv `
    -TimeoutSeconds 120

  Assert-ReleaseReadyPreflight -Context "BaseKit browser evidence preflight failed"
  Write-PreflightReport -Status "pass"
  Invoke-PreflightReportValidation
}

function Cleanup-FreshVM {
  if ($KeepVM -or -not $ShouldCleanupFreshVM -or -not (Test-Path -LiteralPath $HomelabPath)) {
    return
  }
  $artifact = Get-Content -Raw -LiteralPath $HomelabPath | ConvertFrom-Json
  $container = [string]$artifact.target.containerName
  $volume = [string]$artifact.target.volumeName
  if ($container -and $container.StartsWith("stackkits-e2e-")) {
    Write-Host "Removing retained Fresh VM container $container"
    Invoke-BoundedNative -Name "Remove retained Fresh VM container" -FilePath "docker" -Arguments @("rm", "-f", "-v", $container) -TimeoutSeconds 120
  }
  if ($volume -and $volume.StartsWith("stackkits-e2e-")) {
    Write-Host "Removing retained Fresh VM volume $volume"
    Invoke-BoundedNative -Name "Remove retained Fresh VM volume" -FilePath "docker" -Arguments @("volume", "rm", "-f", $volume) -TimeoutSeconds 120
  }
}

function Export-SetupRunState {
  if (-not (Test-Path -LiteralPath $HomelabPath)) {
    return
  }
  try {
    $artifact = Get-Content -Raw -LiteralPath $HomelabPath | ConvertFrom-Json
    $container = [string]$artifact.target.containerName
    if (-not $container -or -not $container.StartsWith("stackkits-e2e-")) {
      Write-Host "Skipping SetupRun state export because the SK-S1 artifact has no retained Fresh VM container."
      return
    }
    New-Item -ItemType Directory -Force -Path $ScenarioDir | Out-Null
    $source = "${container}:/root/my-homelab/.stackkit/state.yaml"
    Invoke-BoundedNative `
      -Name "Export SK-S1 SetupRun state" `
      -FilePath "docker" `
      -Arguments @("cp", $source, $SetupStatePath) `
      -TimeoutSeconds 120 | Out-Null
  } catch {
    Write-Host "SetupRun state export failed; browser evidence will record missing setup diagnostics. Details: $_"
  }
}

Push-Location $RepoRoot
try {
  Set-WrapperPhase "command-preflight"
  if ($PreflightOnly -and $SkipBrowserPreflight) {
    throw "-PreflightOnly cannot be combined with -SkipBrowserPreflight"
  }
  foreach ($Command in @("go", "node", "npm", "docker")) {
    Assert-Command $Command | Out-Null
  }
  if (-not $SkipBrowserPreflight) {
    Set-WrapperPhase "browser-preflight"
    Invoke-BrowserEvidencePreflight
  } else {
    Assert-NoPreflightFailures -Context "BaseKit browser evidence command preflight failed"
  }
  if ($PreflightOnly) {
    Write-Host ""
    Write-Host "BaseKit browser evidence preflight is ready:"
    Write-Host "  $PreflightReportPath"
    return
  }

  New-Item -ItemType Directory -Force -Path $ScenarioDir | Out-Null

  if (-not $SkipFreshVM) {
    Set-WrapperPhase "fresh-vm-rollout"
    $script:ShouldCleanupFreshVM = $true
    $freshEnv = @{
      STACKKIT_FRESH_VM_OUTPUT = $HomelabPath
      STACKKIT_FRESH_VM_KEEP = "1"
    }
    if ($DockerConfigJson) {
      $freshEnv["STACKKIT_FRESH_VM_DOCKER_CONFIG_JSON"] = $DockerConfigJson
    }
    if ($DockerConfigPath) {
      $freshEnv["STACKKIT_FRESH_VM_DOCKER_CONFIG"] = $DockerConfigPath
    }
    Invoke-BoundedNative `
      -Name "SK-S1 Fresh Ubuntu rollout" `
      -FilePath "go" `
      -Arguments @("test", "-v", "-tags", "production", "-run", "^TestProductionReadinessLocalHomeLocalhost$", "-timeout", "14m", "./tests/production") `
      -Environment $freshEnv
  }
  Set-WrapperPhase "setup-state-export"
  Export-SetupRunState

  Set-WrapperPhase "homelab-artifact"
  if (-not (Test-Path -LiteralPath $HomelabPath)) {
    throw "Missing SK-S1 homelab artifact: $HomelabPath"
  }

  $captureArgs = @(
    "scripts/evidence/capture-basekit-browser-evidence.mjs",
    "--owner-email", $OwnerEmail,
    "--owner-username", $OwnerUsername,
    "--owner-display-name", $OwnerDisplayName,
    "--output", $BrowserEvidencePath,
    "--screenshot-dir", (Join-Path $ScenarioDir "screenshots"),
    "--evidence-root", $EvidenceRootPath,
    "--setup-state-path", $SetupStatePath,
    "--total-timeout-ms", "840000",
    "--per-check-timeout-ms", "120000"
  )
  if ($BrowserChannel) {
    $captureArgs += @("--browser-channel", $BrowserChannel)
  }
  if ($Headed) {
    $captureArgs += "--headed"
  }
  $captureEnv = @{
    STACKKIT_PLAYWRIGHT_MODULE_DIR = $PlaywrightModuleDir
    STACKKIT_BROWSER_EVIDENCE_RUN_ID = $BrowserEvidenceRunId
  }
  if ($BrowserChannel) {
    $captureEnv["STACKKIT_BROWSER_CHANNEL"] = $BrowserChannel
  }
  Set-WrapperPhase "browser-capture"
  Invoke-BoundedNative `
    -Name "SK-S1 browser screenshot capture" `
    -FilePath "node" `
    -Arguments $captureArgs `
    -Environment $captureEnv
  Set-WrapperPhase "setup-state-export"
  Export-SetupRunState

  Set-WrapperPhase "manifest-validation"
  Invoke-BoundedNative `
    -Name "Validate SK-S1 browser evidence manifest" `
    -FilePath "go" `
    -Arguments @("test", "-v", "-tags", "production", "-run", "^TestBaseKitBetaBrowserEvidenceManifest$", "-timeout", "2m", "./tests/production") `
    -Environment @{
      STACKKIT_BROWSER_EVIDENCE_PATH = $BrowserEvidencePath
      STACKKIT_BROWSER_EVIDENCE_ROOT = $EvidenceRootPath
    } `
    -TimeoutSeconds 120

  Write-Host ""
  Write-Host "BaseKit browser evidence is ready:"
  Write-Host "  $BrowserEvidencePath"
} catch {
  $message = [string]$_
  if (-not $PreflightOnly) {
    Write-BrowserEvidenceFailure -Phase $script:CurrentPhase -ErrorMessage $message
    try {
      Invoke-BrowserEvidenceFailureValidationIfAvailable
    } catch {
      throw "${message}; browser failure evidence validation failed: $_"
    }
  }
  throw
} finally {
  Cleanup-FreshVM
  Pop-Location
}
