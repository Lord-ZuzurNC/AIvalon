# AIvalon local AIO installer (Windows x64): llama.cpp CPU build + Qwen2.5-3B model + launcher.
# Run: powershell -NoProfile -ExecutionPolicy Bypass -File install.ps1
$ErrorActionPreference = 'Stop'
[Net.ServicePointManager]::SecurityProtocol = [Net.ServicePointManager]::SecurityProtocol -bor [Net.SecurityProtocolType]::Tls12

# Not /releases/latest: that is the stable vX.Y.Z tag, which ships no binaries. The b<N> builds
# are prereleases; the list is newest-first, so the first matching asset is the newest build.
$ReleaseApi = 'https://api.github.com/repos/ggml-org/llama.cpp/releases?per_page=30'
$ModelUrl   = 'https://huggingface.co/bartowski/Qwen2.5-3B-Instruct-GGUF/resolve/main/Qwen2.5-3B-Instruct-Q4_K_M.gguf'
$ModelFile  = 'Qwen2.5-3B-Instruct-Q4_K_M.gguf'
$AssetRe    = '^llama-b\d+-bin-win-cpu-x64\.zip$'

function Step([string]$msg) { Write-Host "`n==> $msg" -ForegroundColor Cyan }
function Fail([string]$msg) {
    Write-Host "`nERROR: $msg`nInstall NOT complete." -ForegroundColor Red
    exit 1
}
# Single-quoted PowerShell literal, for writing paths into the launcher.
function Quote([string]$s) { "'" + ($s -replace "'", "''") + "'" }

# 1. Install directory
$defaultDir = Join-Path $env:LOCALAPPDATA 'AIvalon'
$InstallDir = Read-Host "Install directory [$defaultDir]"
if ([string]::IsNullOrWhiteSpace($InstallDir)) { $InstallDir = $defaultDir }
try {
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    $InstallDir = (Resolve-Path $InstallDir).Path
    $probe = Join-Path $InstallDir '.write-test'
    Set-Content -Path $probe -Value 'ok'
    Remove-Item $probe
} catch { Fail "Cannot create or write to '$InstallDir': $($_.Exception.Message)" }
Write-Host "Installing into: $InstallDir"

$TmpDir = Join-Path ([IO.Path]::GetTempPath()) ("aivalon-install-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $TmpDir | Out-Null

try {
    # 2. Resolve latest llama.cpp asset
    Step 'Querying recent llama.cpp releases'
    try { $releases = Invoke-RestMethod -Uri $ReleaseApi -Headers @{ Accept = 'application/vnd.github+json' } }
    catch { Fail "Could not reach the GitHub releases API ($ReleaseApi): $($_.Exception.Message)" }
    $asset = @(foreach ($r in $releases) { $r.assets }) | Where-Object { $_.name -match $AssetRe } | Select-Object -First 1
    if (-not $asset) { Fail "No asset matching $AssetRe in the 30 most recent llama.cpp releases." }
    Write-Host "Found: $($asset.name)"

    # 3. Download + extract (to temp first, so a failure leaves any old install intact)
    Step "Downloading $($asset.name)"
    $zip = Join-Path $TmpDir $asset.name
    try { Invoke-WebRequest -Uri $asset.browser_download_url -OutFile $zip -UseBasicParsing }
    catch { Fail "Download of $($asset.browser_download_url) failed: $($_.Exception.Message)" }

    $llamaDir = Join-Path $InstallDir 'llama.cpp'
    Step "Extracting into $llamaDir"
    try {
        $extract = Join-Path $TmpDir 'extract'
        Expand-Archive -Path $zip -DestinationPath $extract -Force
        if (Test-Path $llamaDir) { Remove-Item -Recurse -Force $llamaDir }
        Move-Item -Path $extract -Destination $llamaDir
    } catch { Fail "Extraction of $($asset.name) failed: $($_.Exception.Message)" }

    $llamaBin = Get-ChildItem -Path $llamaDir -Recurse -File -Filter 'llama-server.exe' | Select-Object -First 1
    if (-not $llamaBin) { Fail "llama-server.exe not found inside $($asset.name) (archive layout changed?)." }
    $LlamaBinPath = $llamaBin.FullName
    Write-Host "llama-server: $LlamaBinPath"

    # 4. Model (kept as .part until complete; no resume in Invoke-WebRequest, re-run restarts it)
    $modelsDir = Join-Path $InstallDir 'models'
    $modelPath = Join-Path $modelsDir $ModelFile
    Step "Downloading model $ModelFile (~2 GB)"
    New-Item -ItemType Directory -Force -Path $modelsDir | Out-Null
    if (Test-Path $modelPath) {
        Write-Host 'Already present, skipping.'
    } else {
        try {
            Invoke-WebRequest -Uri $ModelUrl -OutFile "$modelPath.part" -UseBasicParsing
            Move-Item -Force -Path "$modelPath.part" -Destination $modelPath
        } catch { Fail "Model download failed: $($_.Exception.Message). Re-run the installer to retry." }
    }

    # 5. Launcher
    $launcher = Join-Path $InstallDir 'run-aivalon.ps1'
    Step "Writing launcher $launcher"
    $launcherBody = @"
`$ErrorActionPreference = 'Stop'
`$InstallDir = $(Quote $InstallDir)
`$env:AIVALON_LLAMA_BIN = $(Quote $LlamaBinPath)
`$env:AIVALON_LLAMA_MODELS_DIR = $(Quote $modelsDir)
`$env:AIVALON_ADDR = '127.0.0.1:4140'
`$env:AIVALON_DB = $(Quote (Join-Path $InstallDir 'aivalon.db'))
`$exe = Join-Path `$InstallDir 'aivalon.exe'
if (-not (Test-Path `$exe)) {
    Write-Host "ERROR: AIvalon binary not found at `$exe. Extract the AIvalon release there (aivalon.exe + web\ folder)." -ForegroundColor Red
    Read-Host 'Press Enter to close'
    exit 1
}
Set-Location `$InstallDir  # aivalon resolves web\static relative to the working directory
& `$exe @args
"@
    try { Set-Content -Path $launcher -Value $launcherBody -Encoding UTF8 }
    catch { Fail "Could not write ${launcher}: $($_.Exception.Message)" }

    # 6. Optional desktop shortcut
    $ans = Read-Host 'Create a desktop shortcut? [y/N]'
    if ($ans -match '^[Yy]$') {
        try {
            $desktop = [Environment]::GetFolderPath('Desktop')
            $lnkPath = Join-Path $desktop 'AIvalon.lnk'
            $shell = New-Object -ComObject WScript.Shell
            $lnk = $shell.CreateShortcut($lnkPath)
            $lnk.TargetPath = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
            $lnk.Arguments = "-NoProfile -ExecutionPolicy Bypass -File `"$launcher`""
            $lnk.WorkingDirectory = $InstallDir
            $lnk.Save()
            Write-Host "Shortcut: $lnkPath"
        } catch { Fail "Could not create desktop shortcut: $($_.Exception.Message) (everything else is installed)." }
    }

    Step 'Done'
    if (-not (Test-Path (Join-Path $InstallDir 'aivalon.exe'))) {
        Write-Host "NOTE: place aivalon.exe (and its web\ folder) in $InstallDir before launching." -ForegroundColor Yellow
    }
    Write-Host "Run: powershell -ExecutionPolicy Bypass -File `"$launcher`"  then open http://127.0.0.1:4140"
} finally {
    Remove-Item -Recurse -Force $TmpDir -ErrorAction SilentlyContinue
}
