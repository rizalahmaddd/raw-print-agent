param(
    [Parameter(Mandatory = $true)][string]$Version,
    [string]$InnoVersion = "7.1.1"
)
$ErrorActionPreference = "Stop"

# Installed from the official release instead of the runner image so every build uses the same compiler.
$dir = Join-Path $env:RUNNER_TEMP "innosetup-$InnoVersion"
$iscc = Join-Path $dir "ISCC.exe"
if (-not (Test-Path $iscc)) {
    $tag = "is-" + ($InnoVersion -replace '\.', '_')
    $url = "https://github.com/jrsoftware/issrc/releases/download/$tag/innosetup-$InnoVersion-x64.exe"
    $setup = Join-Path $env:RUNNER_TEMP "innosetup-$InnoVersion.exe"
    Write-Host "Downloading $url"
    Invoke-WebRequest -Uri $url -OutFile $setup
    $p = Start-Process -FilePath $setup -ArgumentList "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/SP-", "/DIR=`"$dir`"" -Wait -PassThru
    if ($p.ExitCode -ne 0 -or -not (Test-Path $iscc)) { throw "Inno Setup install failed (exit $($p.ExitCode))" }
}

& $iscc "/DAppVersion=$Version" "installer\rawprint.iss"
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
