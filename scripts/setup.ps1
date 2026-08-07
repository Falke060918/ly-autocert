# claude-starter setup — rtk 설치 및 Claude Code 훅 등록 (Windows, 머신당 1회)
# 실행: powershell -ExecutionPolicy Bypass -File scripts\setup.ps1
$ErrorActionPreference = "Stop"

Write-Host "=== claude-starter setup ===" -ForegroundColor Cyan

# 1. rtk 확인/설치
$rtkCmd = Get-Command rtk -ErrorAction SilentlyContinue
if ($null -eq $rtkCmd) {
    Write-Host "[1/3] rtk가 없어 설치합니다..." -ForegroundColor Yellow
    $binDir = Join-Path $env:USERPROFILE ".local\bin"
    New-Item -ItemType Directory -Force -Path $binDir | Out-Null

    $api = Invoke-RestMethod "https://api.github.com/repos/rtk-ai/rtk/releases/latest"
    # x86_64/amd64 자산을 명시 우선 (First 1만으로는 aarch64 등이 먼저 걸릴 수 있음)
    $asset = $api.assets | Where-Object { $_.name -match "(x86_64|amd64).*windows-msvc\.zip$" } | Select-Object -First 1
    if ($null -eq $asset) {
        $asset = $api.assets | Where-Object { $_.name -like "*windows-msvc.zip" } | Select-Object -First 1
    }
    if ($null -eq $asset) { throw "Windows용 rtk 릴리스를 찾지 못했습니다." }

    $zip = Join-Path $env:TEMP $asset.name
    Invoke-WebRequest $asset.browser_download_url -OutFile $zip
    Expand-Archive $zip -DestinationPath $binDir -Force
    Remove-Item $zip

    # zip 내부에 하위 폴더가 있는 경우 rtk.exe를 binDir 최상위로 이동
    if (-not (Test-Path (Join-Path $binDir "rtk.exe"))) {
        $found = Get-ChildItem $binDir -Recurse -Filter "rtk.exe" | Select-Object -First 1
        if ($null -ne $found) { Move-Item $found.FullName (Join-Path $binDir "rtk.exe") -Force }
    }
    $rtkExe = Join-Path $binDir "rtk.exe"
    if (-not (Test-Path $rtkExe)) { throw "rtk.exe 추출에 실패했습니다." }

    # 사용자 PATH 등록
    $userPath = [Environment]::GetEnvironmentVariable("Path", "User")
    if ($userPath -notlike "*$binDir*") {
        [Environment]::SetEnvironmentVariable("Path", "$userPath;$binDir", "User")
        Write-Host "  PATH에 $binDir 추가됨 (새 터미널부터 적용)"
    }
    $env:Path = "$env:Path;$binDir"
    Write-Host "  rtk 설치 완료: $rtkExe" -ForegroundColor Green
} else {
    $rtkExe = $rtkCmd.Source
    Write-Host "[1/3] rtk 이미 설치됨: $rtkExe" -ForegroundColor Green
}

# 2. ripgrep 확인 (rtk 일부 필터가 사용)
$rg = Get-Command rg -ErrorAction SilentlyContinue
if ($null -eq $rg) {
    Write-Host "[2/3] ripgrep(rg)이 PATH에 없습니다. 설치를 권장합니다:" -ForegroundColor Yellow
    Write-Host "      winget install BurntSushi.ripgrep.MSVC"
} else {
    Write-Host "[2/3] ripgrep 확인됨: $($rg.Source)" -ForegroundColor Green
}

# 3. Claude Code 전역 훅 등록 (bash 명령 자동 압축)
Write-Host "[3/3] rtk 훅 등록 (rtk init -g)..."
& $rtkExe init -g --auto-patch
# 네이티브 명령 실패는 $ErrorActionPreference에 안 걸리므로 exit code를 직접 검사
if ($LASTEXITCODE -ne 0) { throw "rtk init 실패 (exit code $LASTEXITCODE) — 훅이 등록되지 않았습니다." }
Write-Host ""
Write-Host "완료. 대부분 즉시 적용되어 bash 명령 출력이 자동 압축됩니다." -ForegroundColor Cyan
Write-Host "  적용이 안 되면 Claude Code를 재시작하세요." -ForegroundColor Cyan
Write-Host "  확인: rtk init --show    절약량: rtk gain    제거: rtk init -g --uninstall"
