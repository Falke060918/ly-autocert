# INHERIT AutoCert 센서 설치·등록 마법사 — 대상: 격리 세그먼트의 센서 호스트(Windows x86_64).
# 실행: 관리자 PowerShell에서  powershell -ExecutionPolicy Bypass -File .\install-sensor.ps1
#
# 하는 일: 사전 점검(권한·시각 동기·패키지/바이너리 무결성·게이트웨이 도달성) → 바이너리 배치 →
#          enroll → 작업 스케줄러 등록(ONSTART/SYSTEM)·기동 → 상태 확인까지 한 번에 끝낸다.
# 절차 원문과 트러블슈팅은 manual/SENSOR.md.
#
# 보안: 등록 토큰은 화면에 에코하지 않고 로그·출력 어디에도 남기지 않는다.
#       개인키는 센서 호스트에서 생성되어 호스트를 떠나지 않는다(이 스크립트도 다루지 않는다).
# 호환: Windows PowerShell 5.1 (PowerShell 7 전용 문법을 쓰지 않는다).

[CmdletBinding()]
param(
    [string]$Server,
    [string]$Token,
    [string]$TokenFile,
    [string]$CaFingerprint,
    [string]$BinaryPath,
    [string]$StateDir,
    [string]$InstallDir = 'C:\Program Files\AutocertSensor',
    [string]$TaskName = 'AutocertSensor',
    [switch]$SkipEnroll,
    [switch]$Status,
    [switch]$Force,
    [switch]$Yes,
    [switch]$NoTui,
    [switch]$Help
)

$ErrorActionPreference = 'Stop'

$DistName = 'autocert-sensor-windows-amd64.exe'
$ExeName = 'autocert-sensor.exe'
$DefaultStateDir = Join-Path $(if ($env:ProgramData) { $env:ProgramData } else { 'C:\ProgramData' }) 'AutocertSensor'
if (-not $StateDir) { $StateDir = $DefaultStateDir }
$ExePath = Join-Path $InstallDir $ExeName
# 실행 래퍼는 상태 디렉터리에 둔다 — 기본 경로(%ProgramData%\AutocertSensor)에 공백이 없어
# 구버전 schtasks 폴백에서 /TR 인용 문제가 생기지 않는다. exe 경로의 공백은 래퍼 안에서 처리한다.
$WrapperPath = Join-Path $StateDir 'run-sensor.cmd'
$LogDir = Join-Path $StateDir 'logs'
$LogFile = Join-Path $LogDir 'sensor.log'

$script:CheckFail = 0
$script:CheckWarn = 0
$script:PackageDir = $null
$script:PackageVerified = $false

# ──────────────────────────────────────────────────────────────────────────────
# 출력 유틸
# ──────────────────────────────────────────────────────────────────────────────
function Write-Head {
    param([string]$Text)
    Write-Host ''
    Write-Host $Text -ForegroundColor White
}

# 한 행은 반드시 Write-Host 한 번으로 낸다 — -NoNewline으로 쪼개면 출력을 파일로
# 리다이렉트했을 때 정보 스트림 레코드가 나뉘어 줄이 깨진다.
function Write-RowOk {
    param([string]$Text)
    Write-Host "  [ OK ]  $Text" -ForegroundColor Green
}

function Write-RowWarn {
    param([string]$Text)
    $script:CheckWarn++
    Write-Host "  [경고]  $Text" -ForegroundColor Yellow
}

function Write-RowFail {
    param([string]$Text)
    $script:CheckFail++
    Write-Host "  [실패]  $Text" -ForegroundColor Red
}

function Write-Hint {
    param([string]$Text)
    Write-Host "        └ $Text" -ForegroundColor DarkGray
}

function Stop-WithError {
    param([string]$Message)
    Write-Host ''
    Write-Host "오류: $Message" -ForegroundColor Red
    exit 1
}

function Show-Usage {
    Write-Host @'
사용법: powershell -ExecutionPolicy Bypass -File .\install-sensor.ps1 [옵션]

옵션 없이 실행하면 방향키 메뉴로 안내한다(권장). 웹 콘솔 [센서] 화면이 보여주는
enroll 명령을 통째로 붙여넣으면 서버 주소·토큰·CA fingerprint를 알아서 뽑아낸다.

비대화 모드 (값 3개를 인자로):
  .\install-sensor.ps1 -Server https://10.20.0.5:8443 `
    -TokenFile C:\temp\enroll-token.txt `
    -CaFingerprint <sha256 hex 64자> -Yes

옵션:
  -Server <URL>             AutoCert 서버 주소 (https://호스트:포트)
  -Token <토큰>             1회용 등록 토큰. 명령 이력에 남으므로 -TokenFile 권장
  -TokenFile <경로>         토큰이 든 파일 (첫 줄만 읽는다)
  -CaFingerprint <hex>      서버 CA 인증서 SHA-256 지문
  -BinaryPath <경로>        센서 바이너리 경로 (기본: 스크립트 주변에서 자동 탐색)
  -InstallDir <경로>        설치 디렉터리 (기본: C:\Program Files\AutocertSensor)
  -StateDir <경로>          상태 디렉터리 (기본: %ProgramData%\AutocertSensor)
  -TaskName <이름>          작업 스케줄러 작업 이름 (기본: AutocertSensor)
  -SkipEnroll               enroll 없이 서비스만 재설치 (이미 등록된 센서용)
  -Status                   설치·등록·작업 상태만 확인하고 종료
  -Force                    사전 점검 경고를 무시하고 진행
  -Yes                      모든 확인을 자동 승인 (비대화)
  -NoTui                    방향키 메뉴 대신 번호 입력만 사용
  -Help                     이 도움말

환경변수 AUTOCERT_ENROLL_TOKEN 으로도 토큰을 넘길 수 있다.
'@
}

if ($Help) { Show-Usage; exit 0 }

# ──────────────────────────────────────────────────────────────────────────────
# 방향키 선택 메뉴 (미지원 콘솔이면 번호 입력으로 자동 폴백)
# ──────────────────────────────────────────────────────────────────────────────
function Test-TuiSupported {
    if ($NoTui -or $Yes) { return $false }
    if ($Host.Name -like '*ISE*') { return $false }
    try {
        if ([Console]::IsInputRedirected -or [Console]::IsOutputRedirected) { return $false }
    } catch {
        return $false
    }
    try {
        $null = $Host.UI.RawUI.KeyAvailable
        $null = $Host.UI.RawUI.CursorPosition
    } catch {
        return $false
    }
    return $true
}
$script:CanTui = Test-TuiSupported

function Show-Menu {
    # 1-기반 인덱스를 돌려준다.
    param(
        [string]$Title,
        [string[]]$Options
    )
    $n = $Options.Count
    if ($n -eq 0) { return 0 }

    Write-Host ''
    Write-Host $Title -ForegroundColor White

    if (-not $script:CanTui) {
        for ($i = 0; $i -lt $n; $i++) {
            Write-Host ("  {0}) {1}" -f ($i + 1), $Options[$i])
        }
        while ($true) {
            $ans = Read-Host "  번호 선택 [1-$n]"
            $num = 0
            if ([int]::TryParse($ans, [ref]$num) -and $num -ge 1 -and $num -le $n) { return $num }
            Write-Host "  1~$n 사이의 번호를 입력한다."
        }
    }

    Write-Host '  ↑/↓ 이동 · Enter 선택' -ForegroundColor DarkGray
    # 먼저 n줄을 확보해 스크롤을 끝낸 뒤 그 시작점을 기억한다(버퍼 하단에서도 좌표가 안 어긋난다).
    for ($i = 0; $i -lt $n; $i++) { Write-Host '' }
    $origin = $Host.UI.RawUI.CursorPosition
    $origin.Y = $origin.Y - $n
    $width = [Math]::Max(20, $Host.UI.RawUI.BufferSize.Width - 1)

    $sel = 0
    while ($true) {
        $Host.UI.RawUI.CursorPosition = $origin
        for ($i = 0; $i -lt $n; $i++) {
            if ($i -eq $sel) {
                $line = ("   > " + $Options[$i])
                Write-Host $line.PadRight($width) -ForegroundColor Black -BackgroundColor Cyan
            } else {
                $line = ("     " + $Options[$i])
                Write-Host $line.PadRight($width)
            }
        }
        $key = $Host.UI.RawUI.ReadKey('NoEcho,IncludeKeyDown')
        switch ($key.VirtualKeyCode) {
            38 { $sel = ($sel - 1 + $n) % $n }          # ↑
            40 { $sel = ($sel + 1) % $n }               # ↓
            75 { $sel = ($sel - 1 + $n) % $n }          # k
            74 { $sel = ($sel + 1) % $n }               # j
            13 { Write-Host ''; return ($sel + 1) }     # Enter
            default {
                $c = $key.Character
                if ($c -ge '1' -and $c -le '9') {
                    $num = [int]::Parse($c)
                    if ($num -le $n) { Write-Host ''; return $num }
                }
            }
        }
    }
}

function Confirm-Step {
    param([string]$Question)
    if ($Yes) { return $true }
    $c = Show-Menu -Title $Question -Options @('예, 계속한다', '아니오, 중단한다')
    return ($c -eq 1)
}

# ──────────────────────────────────────────────────────────────────────────────
# 입력 유틸 — 토큰은 SecureString으로 받아 화면에 에코하지 않는다
# ──────────────────────────────────────────────────────────────────────────────
function ConvertFrom-SecureStringPlain {
    param([System.Security.SecureString]$Secure)
    if ($null -eq $Secure) { return '' }
    $bstr = [System.Runtime.InteropServices.Marshal]::SecureStringToBSTR($Secure)
    try {
        return [System.Runtime.InteropServices.Marshal]::PtrToStringBSTR($bstr)
    } finally {
        [System.Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
    }
}

function Remove-Wrapping {
    param([string]$Value)
    if ($null -eq $Value) { return '' }
    $v = $Value.Trim()
    if ($v.Length -ge 2) {
        if (($v.StartsWith('"') -and $v.EndsWith('"')) -or ($v.StartsWith("'") -and $v.EndsWith("'"))) {
            $v = $v.Substring(1, $v.Length - 2)
        }
    }
    return $v
}

function Read-PastedCommand {
    # 여러 줄 붙여넣기를 에코 없이 읽는다. 줄 끝 '\' 또는 '^'는 이어짐, 빈 줄이면 종료.
    $buf = ''
    while ($true) {
        $secure = Read-Host -AsSecureString
        $line = (ConvertFrom-SecureStringPlain $secure).TrimEnd()
        if ([string]::IsNullOrWhiteSpace($line)) {
            if ($buf.Trim().Length -gt 0) { break }
            continue
        }
        Write-Host '  · 한 줄 입력됨' -ForegroundColor DarkGray
        if ($line.EndsWith('\') -or $line.EndsWith('^') -or $line.EndsWith('`')) {
            $buf += ' ' + $line.Substring(0, $line.Length - 1)
            continue
        }
        $buf += ' ' + $line
        break
    }
    return $buf
}

function ConvertFrom-EnrollCommand {
    # 붙여넣은 enroll 명령에서 서버·토큰·지문을 뽑는다.
    param([string]$Text)
    $result = @{ Server = ''; Token = ''; Fingerprint = '' }
    if ([string]::IsNullOrWhiteSpace($Text)) { return $result }
    $words = $Text -split '\s+' | Where-Object { $_ -ne '' }
    for ($i = 0; $i -lt $words.Count; $i++) {
        $w = $words[$i]
        switch -Regex ($w) {
            '^--server=' { $result.Server = Remove-Wrapping ($w.Substring(9)); continue }
            '^--token=' { $result.Token = Remove-Wrapping ($w.Substring(8)); continue }
            '^--ca-fingerprint=' { $result.Fingerprint = Remove-Wrapping ($w.Substring(17)); continue }
            '^--server$' { if ($i + 1 -lt $words.Count) { $result.Server = Remove-Wrapping $words[$i + 1]; $i++ }; continue }
            '^--token$' { if ($i + 1 -lt $words.Count) { $result.Token = Remove-Wrapping $words[$i + 1]; $i++ }; continue }
            '^--ca-fingerprint$' { if ($i + 1 -lt $words.Count) { $result.Fingerprint = Remove-Wrapping $words[$i + 1]; $i++ }; continue }
        }
    }
    return $result
}

function Test-ServerUrl {
    # 문제가 있으면 사유 문자열, 없으면 $null
    param([string]$Value)
    if ([string]::IsNullOrWhiteSpace($Value)) { return '서버 주소를 읽지 못했다' }
    if ($Value -like 'http://*') { return "서버 주소는 https 여야 한다 (센서 게이트웨이는 mTLS 전용): $Value" }
    if ($Value -notlike 'https://*') { return "서버 주소는 https://호스트:포트 형식이어야 한다: $Value" }
    $rest = $Value.Substring(8).Split('/')[0]
    if ([string]::IsNullOrWhiteSpace($rest)) { return '서버 주소에 호스트가 없다' }
    return $null
}

function ConvertTo-NormalizedFingerprint {
    # 정규화한 hex 64자, 형식이 아니면 $null
    param([string]$Value)
    if ($null -eq $Value) { return $null }
    $v = $Value.Trim().Replace(':', '').Replace(' ', '').ToLowerInvariant()
    if ($v.StartsWith('sha256')) { $v = $v.Substring(6) }
    if ($v -match '^[0-9a-f]{64}$') { return $v }
    return $null
}

function Split-ServerUrl {
    param([string]$Value)
    $rest = $Value.Substring(8).Split('/')[0]
    $port = 443
    $host_ = $rest
    if ($rest.StartsWith('[')) {
        $close = $rest.IndexOf(']')
        $host_ = $rest.Substring(1, $close - 1)
        if ($rest.Length -gt $close + 1) { $port = [int]$rest.Substring($close + 2) }
    } elseif ($rest.Contains(':')) {
        $parts = $rest.Split(':')
        $host_ = $parts[0]
        $port = [int]$parts[1]
    }
    return @{ Host = $host_; Port = $port }
}

# ──────────────────────────────────────────────────────────────────────────────
# 사전 점검
# ──────────────────────────────────────────────────────────────────────────────
function Test-IsAdmin {
    $identity = [System.Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object System.Security.Principal.WindowsPrincipal($identity)
    return $principal.IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Invoke-CheckAdmin {
    if (Test-IsAdmin) {
        Write-RowOk '관리자 권한 - 관리자로 실행 중'
    } else {
        Write-RowFail '관리자 권한 - 관리자가 아니다'
        Write-Hint '고치는 법: PowerShell을 [관리자 권한으로 실행]한 뒤 다시 실행한다'
    }
}

function Invoke-CheckTimeSync {
    # mTLS 인증서 유효기간 검증 때문에 시계가 어긋나면 enroll 자체가 실패한다 (SENSOR.md 2장).
    $now = (Get-Date).ToString('yyyy-MM-dd HH:mm:ss')
    $utc = (Get-Date).ToUniversalTime().ToString('yyyy-MM-dd HH:mm:ss')

    $svc = Get-Service -Name 'W32Time' -ErrorAction SilentlyContinue
    if ($null -eq $svc) {
        Write-RowWarn "시각 동기 - Windows Time 서비스(W32Time)를 찾지 못했다 · 로컬 $now (UTC $utc)"
        Write-Hint '서버 시계와 수 분 이내인지 눈으로 대조한다 - 어긋나면 enroll이 인증서 유효기간 오류로 실패한다'
        return
    }
    if ($svc.Status -ne 'Running') {
        Write-RowFail "시각 동기 - Windows Time 서비스가 꺼져 있다 (상태 $($svc.Status)) · 로컬 $now (UTC $utc)"
        Write-Hint 'mTLS 인증서 유효기간 검증 때문에 시계가 어긋나면 enroll이 실패한다'
        Write-Hint '고치는 법 (관리자 cmd):'
        Write-Hint '  sc config w32time start= auto && net start w32time'
        Write-Hint '  w32tm /config /manualpeerlist:"<내부 NTP 서버>" /syncfromflags:manual /update && w32tm /resync'
        return
    }

    $source = ''
    try {
        $source = (& w32tm.exe /query /source 2>&1 | Out-String).Trim()
    } catch {
        $source = ''
    }
    if ([string]::IsNullOrWhiteSpace($source)) {
        Write-RowWarn "시각 동기 - 동기 원본을 확인하지 못했다 · 로컬 $now (UTC $utc)"
        Write-Hint '서버 시계와 수 분 이내인지 눈으로 대조한다 - 어긋나면 enroll이 실패한다'
        return
    }

    # 로컬 시계로 자유 구동 중이면 실질적으로 미동기다 (표기는 로케일마다 다르므로 패턴으로 판별).
    if ($source -match '(?i)cmos|free.?running|로컬|local') {
        Write-RowFail "시각 동기 - NTP에 동기되지 않았다 (원본: $source) · 로컬 $now (UTC $utc)"
        Write-Hint 'mTLS 인증서 유효기간 검증 때문에 시계가 어긋나면 enroll이 실패한다'
        Write-Hint '고치는 법 (관리자 cmd) - 폐쇄망 내부 NTP 서버를 지정한다:'
        Write-Hint '  w32tm /config /manualpeerlist:"<내부 NTP 서버>" /syncfromflags:manual /update'
        Write-Hint '  w32tm /resync   (그 뒤 w32tm /query /source 로 다시 확인)'
        return
    }

    Write-RowOk "시각 동기 - 동기됨 (원본: $source) · 로컬 $now (UTC $utc)"
}

$script:BinarySrc = ''
$script:BinarySha = ''

function Find-SensorBinary {
    if ($BinaryPath) {
        if (Test-Path -LiteralPath $BinaryPath -PathType Leaf) { return (Resolve-Path -LiteralPath $BinaryPath).Path }
        return $null
    }
    $scriptDir = Split-Path -Parent $PSCommandPath
    $candidates = @(
        (Join-Path (Get-Location).Path $DistName),
        (Join-Path (Get-Location).Path $ExeName),
        (Join-Path $scriptDir $DistName),
        (Join-Path $scriptDir $ExeName),
        (Join-Path $scriptDir "..\sensor\$DistName"),
        (Join-Path $scriptDir "..\backend\dist\$DistName"),
        (Join-Path $scriptDir "sensor\$DistName"),
        (Join-Path $scriptDir "..\$DistName")
    )
    foreach ($c in $candidates) {
        if (Test-Path -LiteralPath $c -PathType Leaf) { return (Resolve-Path -LiteralPath $c).Path }
    }
    return $null
}

# 반입 패키지(sensor_windows_<버전>\)의 패키지 레벨 SHA256SUMS. 설치 스크립트 자신과
# 매뉴얼까지 포함하므로 바이너리 단위 대조보다 넓다. build-sensor-dist.sh 가 만드는
# backend\dist\SHA256SUMS 는 바이너리 2줄뿐이고 MANIFEST.txt 가 없다 - 그 차이로 두
# 경로를 가른다(패키지 레벨이 있으면 우선).
function Find-PackageDir {
    $candidates = @()
    if ($PSCommandPath) { $candidates += (Split-Path -Parent $PSCommandPath) }
    $candidates += (Get-Location).Path
    foreach ($c in $candidates) {
        if (-not $c) { continue }
        if ((Test-Path -LiteralPath (Join-Path $c 'SHA256SUMS') -PathType Leaf) -and
            (Test-Path -LiteralPath (Join-Path $c 'MANIFEST.txt') -PathType Leaf)) {
            return (Resolve-Path -LiteralPath $c).Path
        }
    }
    return $null
}

function Invoke-CheckPackage {
    $dir = Find-PackageDir
    if (-not $dir) {
        # 패키지가 아니라 바이너리만 반입한 경우다 - Invoke-CheckBinary 가 단건 대조한다.
        return
    }
    $sumsFile = Join-Path $dir 'SHA256SUMS'

    $okCount = 0
    $mismatch = New-Object System.Collections.ArrayList
    $missing = New-Object System.Collections.ArrayList

    foreach ($line in (Get-Content -LiteralPath $sumsFile)) {
        if (-not $line -or -not $line.Trim()) { continue }
        $parts = $line.Trim() -split '\s+', 2
        if ($parts.Count -lt 2) { continue }
        $want = $parts[0].ToLowerInvariant()
        # "*name" (바이너리 모드) · "./name" (패키지) 두 형태를 다 받는다.
        $rel = $parts[1].TrimStart('*').Replace('/', '\')
        if ($rel.StartsWith('.\')) { $rel = $rel.Substring(2) }
        $target = Join-Path $dir $rel
        if (-not (Test-Path -LiteralPath $target -PathType Leaf)) {
            [void]$missing.Add($rel)
            continue
        }
        $got = (Get-FileHash -LiteralPath $target -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($got -eq $want) { $okCount++ } else { [void]$mismatch.Add($rel) }
    }

    if ($mismatch.Count -gt 0) {
        # 내용이 다르다 = 매체 손상 또는 변조. 바이너리를 시스템 경로에 배치하기 전에 끊는다.
        # -Force 로도 넘기지 않는다 - 여기서 넘기면 검증이 있으나 마나다.
        Write-RowFail "패키지 무결성 - 파일 내용이 SHA256SUMS와 다르다 · $dir"
        foreach ($m in $mismatch) { Write-Hint "불일치: $m" }
        Write-Hint '매체 손상 또는 변조다. 패키지를 다시 반입해 재시도한다 (SENSOR.md 4.2)'
        Stop-WithError '패키지 체크섬 불일치 - 바이너리를 배치하지 않고 중단했다'
    }
    if ($missing.Count -gt 0) {
        Write-RowFail "패키지 무결성 - SHA256SUMS에 적힌 파일이 없다 · $dir"
        foreach ($m in $missing) { Write-Hint "없음: $m" }
        Write-Hint '고치는 법: 압축을 다시 풀어 패키지를 온전한 상태로 만든 뒤 실행한다'
        return
    }
    if ($okCount -eq 0) {
        Write-RowWarn "패키지 무결성 - SHA256SUMS에서 대조할 항목을 찾지 못했다 · $dir"
        return
    }

    $script:PackageDir = $dir
    $script:PackageVerified = $true
    Write-RowOk "패키지 무결성 - 확인됨 (SHA256SUMS $okCount 건) · $dir"
}

function Invoke-CheckBinary {
    $found = Find-SensorBinary
    if (-not $found) {
        Write-RowFail "센서 바이너리 - 찾지 못했다 ($DistName)"
        Write-Hint '고치는 법: 반입한 바이너리와 같은 디렉터리에서 실행하거나 -BinaryPath <경로> 로 지정한다'
        Write-Hint '번들을 쓴다면 번들의 sensor\ 디렉터리에 있다 (SENSOR.md 4장)'
        return
    }
    $script:BinarySrc = $found
    $script:BinarySha = (Get-FileHash -LiteralPath $found -Algorithm SHA256).Hash.ToLowerInvariant()

    # 패키지 레벨 대조가 이미 이 바이너리를 포함해 통과했으면 다시 보지 않는다.
    if ($script:PackageVerified -and $script:PackageDir -and
        $found.StartsWith(($script:PackageDir.TrimEnd('\') + '\'), [System.StringComparison]::OrdinalIgnoreCase)) {
        Write-RowOk "센서 바이너리 - 무결성 확인됨 (패키지 SHA256SUMS 대조) · $found"
        return
    }

    $sumsFile = Join-Path (Split-Path -Parent $found) 'SHA256SUMS'
    if (-not (Test-Path -LiteralPath $sumsFile -PathType Leaf)) {
        Write-RowWarn "센서 바이너리 - $found (SHA256SUMS가 옆에 없어 대조 생략)"
        Write-Hint "실측 SHA256: $script:BinarySha - 배포처 값과 눈으로 대조한다"
        return
    }

    $name = Split-Path -Leaf $found
    $want = ''
    foreach ($line in (Get-Content -LiteralPath $sumsFile)) {
        $parts = $line.Trim() -split '\s+', 2
        if ($parts.Count -lt 2) { continue }
        # 항목 경로는 "name" · "*name" · "./name" 세 형태가 다 나온다.
        $entry = Split-Path -Leaf ($parts[1].TrimStart('*').Replace('/', '\'))
        if ($entry -eq $name) { $want = $parts[0].ToLowerInvariant(); break }
    }
    if (-not $want) {
        Write-RowWarn "센서 바이너리 - $found (SHA256SUMS에 이 파일 항목이 없다)"
        return
    }
    if ($want -eq $script:BinarySha) {
        Write-RowOk "센서 바이너리 - 무결성 확인됨 (SHA256SUMS 대조) · $found"
    } else {
        Write-RowFail "센서 바이너리 - SHA256 불일치 · $found"
        Write-Hint '매체 손상 또는 변조다. 설치를 중단하고 바이너리를 다시 반입한다 (SENSOR.md 4.2)'
    }
}

function Test-TcpReachable {
    param([string]$TargetHost, [int]$Port, [int]$TimeoutMs = 6000)
    $client = New-Object System.Net.Sockets.TcpClient
    try {
        $async = $client.BeginConnect($TargetHost, $Port, $null, $null)
        if (-not $async.AsyncWaitHandle.WaitOne($TimeoutMs, $false)) { return $false }
        $client.EndConnect($async)
        return $true
    } catch {
        return $false
    } finally {
        $client.Close()
    }
}

function Invoke-CheckReachable {
    param([string]$Url)
    $parts = Split-ServerUrl $Url
    if (Test-TcpReachable -TargetHost $parts.Host -Port $parts.Port) {
        Write-RowOk "서버 게이트웨이 도달성 - $($parts.Host):$($parts.Port) TCP 연결됨"
    } else {
        Write-RowFail "서버 게이트웨이 도달성 - $($parts.Host):$($parts.Port) 에 연결하지 못했다"
        Write-Hint "고치는 법 ①: 센서 호스트 → 서버 $($parts.Port)/TCP 아웃바운드 방화벽을 연다 (SENSOR.md 3.2)"
        Write-Hint "고치는 법 ②: 서버에서 게이트웨이가 떴는지 확인 - backend 로그의 'sensor gateway listening'"
        Write-Hint '  없으면 .env의 SENSOR_CA_ENC_KEY·SENSOR_ADVERTISED_HOSTS를 세트로 확인 후 재기동 (SENSOR.md 3.1)'
    }
}

# ──────────────────────────────────────────────────────────────────────────────
# 현재 설치 상태
# ──────────────────────────────────────────────────────────────────────────────
function Get-InstalledSensorId {
    $f = Join-Path $StateDir 'sensor_id'
    if (-not (Test-Path -LiteralPath $f -PathType Leaf)) { return $null }
    $v = (Get-Content -LiteralPath $f -TotalCount 1)
    if ($null -eq $v) { return $null }
    $v = $v.Trim()
    if ($v -eq '') { return $null }
    return $v
}

function Get-SensorTask {
    try {
        return Get-ScheduledTask -TaskName $TaskName -ErrorAction Stop
    } catch {
        return $null
    }
}

function Get-TaskStateText {
    $t = Get-SensorTask
    if ($null -eq $t) { return '미등록' }
    return [string]$t.State
}

function Get-WrapperServerUrl {
    if (-not (Test-Path -LiteralPath $WrapperPath -PathType Leaf)) { return $null }
    $text = Get-Content -LiteralPath $WrapperPath -Raw
    $m = [regex]::Match($text, '--server\s+(\S+)')
    if ($m.Success) { return $m.Groups[1].Value }
    return $null
}

function Show-Status {
    Write-Head '현재 상태'
    $sid = Get-InstalledSensorId
    if ($sid) {
        Write-RowOk "등록 - 완료 · 센서 ID $sid"
    } else {
        Write-RowWarn "등록 - 아직 등록되지 않았다 ($StateDir 에 신원 없음)"
    }
    if (Test-Path -LiteralPath $ExePath -PathType Leaf) {
        Write-RowOk "바이너리 - $ExePath"
    } else {
        Write-RowWarn "바이너리 - $ExePath 없음"
    }
    $t = Get-SensorTask
    if ($null -eq $t) {
        Write-RowWarn "작업 - 스케줄러 작업 '$TaskName' 없음 (상시 실행 미등록)"
    } else {
        $proc = Get-Process -Name 'autocert-sensor' -ErrorAction SilentlyContinue
        if ($t.State -eq 'Running' -and $proc) {
            Write-RowOk "작업 - '$TaskName': Running · 센서 프로세스 확인됨"
        } elseif ($t.State -eq 'Running') {
            Write-RowWarn "작업 - '$TaskName': Running 이지만 센서 프로세스를 찾지 못했다"
        } else {
            Write-RowWarn "작업 - '$TaskName': $($t.State)"
        }
        Write-Host ''
        Write-Host "  로그 확인: Get-Content -Path `"$LogFile`" -Tail 50 -Wait"
    }
}

# ──────────────────────────────────────────────────────────────────────────────
# 입력 수집
# ──────────────────────────────────────────────────────────────────────────────
$script:InServer = ''
$script:InToken = ''
$script:InFp = ''

function Read-InputPaste {
    Write-Head 'enroll 명령 붙여넣기'
    Write-Host '  웹 콘솔 [센서] 화면의 "enroll 명령 예시"를 통째로 붙여넣고 Enter.'
    Write-Host '  여러 줄이면 그대로 붙여넣는다. 마지막에 빈 줄 Enter로 끝낸다.' -ForegroundColor DarkGray
    Write-Host '  토큰이 섞여 있으므로 입력은 화면에 표시하지 않는다.' -ForegroundColor DarkGray
    Write-Host ''
    $parsed = ConvertFrom-EnrollCommand (Read-PastedCommand)
    $script:InServer = $parsed.Server
    $script:InToken = $parsed.Token
    $script:InFp = $parsed.Fingerprint
}

function Read-InputFields {
    Write-Head '값 3개 입력'
    $script:InServer = Remove-Wrapping (Read-Host '  서버 주소 (예: https://10.20.0.5:8443)')
    $script:InToken = Remove-Wrapping (ConvertFrom-SecureStringPlain (Read-Host '  등록 토큰 (화면에 표시되지 않는다)' -AsSecureString))
    $script:InFp = Remove-Wrapping (Read-Host '  CA fingerprint (sha256 hex 64자)')
}

function Resolve-Input {
    while ($true) {
        $err = Test-ServerUrl $script:InServer
        if (-not $err) {
            if ([string]::IsNullOrWhiteSpace($script:InToken)) {
                $err = '등록 토큰을 읽지 못했다'
            } else {
                $norm = ConvertTo-NormalizedFingerprint $script:InFp
                if ($norm) { $script:InFp = $norm } else { $err = 'CA fingerprint 형식이 아니다 (hex 64자여야 한다)' }
            }
        }
        if (-not $err) { return }

        Write-Host ''
        Write-Host "  입력을 확인해야 한다: $err" -ForegroundColor Yellow
        if ($Yes) { Stop-WithError $err }
        $c = Show-Menu -Title '어떻게 할까' -Options @(
            '값 3개를 다시 입력한다',
            'enroll 명령을 다시 붙여넣는다',
            '중단한다')
        switch ($c) {
            1 { Read-InputFields }
            2 { Read-InputPaste }
            default { Stop-WithError '사용자가 중단했다' }
        }
    }
}

function Read-EnrollInput {
    $script:InServer = $Server
    $script:InToken = $Token
    $script:InFp = $CaFingerprint

    if ($script:InServer -and $script:InToken -and $script:InFp) {
        Resolve-Input
        return
    }
    if ($Yes) {
        Stop-WithError '비대화 모드에는 -Server, -Token(또는 -TokenFile), -CaFingerprint 가 모두 필요하다'
    }
    $c = Show-Menu -Title '등록 정보를 어떻게 넣을까' -Options @(
        'enroll 명령을 통째로 붙여넣는다 (권장 · 값 3개를 자동 추출)',
        '서버 주소 / 토큰 / fingerprint 를 따로 입력한다')
    if ($c -eq 1) { Read-InputPaste } else { Read-InputFields }
    Resolve-Input
}

# ──────────────────────────────────────────────────────────────────────────────
# 설치 단계
# ──────────────────────────────────────────────────────────────────────────────
function Install-SensorBinary {
    Write-Head '[1/5] 바이너리 배치'
    if (-not $script:BinarySrc) {
        if (Test-Path -LiteralPath $ExePath -PathType Leaf) {
            Write-Host "  반입 바이너리를 못 찾았지만 $ExePath 가 이미 있다 - 그대로 쓴다."
            return
        }
        Stop-WithError '설치할 센서 바이너리가 없다 (-BinaryPath 로 지정한다)'
    }
    if (-not (Test-Path -LiteralPath $InstallDir -PathType Container)) {
        New-Item -ItemType Directory -Path $InstallDir -Force | Out-Null
        Write-Host "  생성: $InstallDir"
    }
    if ($script:BinarySrc -eq $ExePath) {
        Write-Host "  이미 $ExePath 에 있다 - 복사를 건너뛴다."
        return
    }
    if (Test-Path -LiteralPath $ExePath -PathType Leaf) {
        $cur = (Get-FileHash -LiteralPath $ExePath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($cur -eq $script:BinarySha) {
            Write-Host "  $ExePath 가 같은 바이너리다 - 복사를 건너뛴다."
            return
        }
        # 실행 중이면 덮어쓸 수 없으므로 먼저 멈춘다.
        Stop-SensorTask
    }
    Copy-Item -LiteralPath $script:BinarySrc -Destination $ExePath -Force
    Write-Host "  $script:BinarySrc → $ExePath"
}

function Initialize-StateDir {
    Write-Head '[2/5] 상태 디렉터리'
    if (Test-Path -LiteralPath $StateDir -PathType Container) {
        Write-Host "  이미 있다: $StateDir (내용은 건드리지 않는다)"
    } else {
        New-Item -ItemType Directory -Path $StateDir -Force | Out-Null
        Write-Host "  생성: $StateDir"
    }
    if (-not (Test-Path -LiteralPath $LogDir -PathType Container)) {
        New-Item -ItemType Directory -Path $LogDir -Force | Out-Null
    }
    # 상속을 끊고 SYSTEM·Administrators만 남긴다 (SID로 지정 — 로케일 무관).
    # 여기에 센서 개인키(key.pem)가 저장된다.
    $icaclsOk = $true
    try {
        & icacls.exe $StateDir /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F' | Out-Null
        if ($LASTEXITCODE -ne 0) { $icaclsOk = $false }
    } catch {
        $icaclsOk = $false
    }
    if ($icaclsOk) {
        Write-Host '  접근 권한: SYSTEM·Administrators 전용 (상속 해제) - 개인키가 여기 저장된다.'
    } else {
        Write-RowWarn '상태 디렉터리 ACL 설정에 실패했다 - 접근 권한을 수동으로 좁힌다'
        Write-Hint "icacls `"$StateDir`" /inheritance:r /grant:r `"*S-1-5-18:(OI)(CI)F`" `"*S-1-5-32-544:(OI)(CI)F`""
    }
}

function Invoke-Enroll {
    Write-Head '[3/5] 서버에 등록 (enroll)'
    $existing = Get-InstalledSensorId
    if ($existing) { Write-Host "  기존 신원을 덮어쓴다 (센서 ID $existing)." }

    # 토큰은 인자로만 전달하고 어디에도 출력하지 않는다.
    Write-Host "  실행: $ExePath enroll --server $script:InServer --token *** --ca-fingerprint $script:InFp"
    & $ExePath enroll --server $script:InServer --token $script:InToken --ca-fingerprint $script:InFp --state-dir $StateDir
    $code = $LASTEXITCODE
    $script:InToken = ''
    if ($code -eq 0) {
        Write-Host '  등록 성공' -ForegroundColor Green
        return
    }
    Write-Host ''
    Write-Host '  등록 실패' -ForegroundColor Red
    Write-Host '  자주 나오는 원인:'
    Write-Host '    · 401 invalid_token   → 토큰이 이미 쓰였거나 24시간이 지났다. 콘솔에서 새로 발급받는다'
    Write-Host '    · fingerprint 불일치  → 서버를 재구축했다면 CA가 바뀌었다. 새로 등록한다'
    Write-Host '    · 인증서 유효기간 오류 → 이 호스트의 시계 드리프트. 시각 동기 후 재시도한다'
    Stop-WithError 'enroll 실패 - 위 원인과 manual/SENSOR.md 9장 참고'
}

function Write-RunWrapper {
    # 콘솔 앱의 표준출력을 파일로 남기려면 래퍼가 필요하다. 10MB를 넘으면 1세대만 보관한다.
    # 이 .cmd는 cmd.exe가 읽으므로 ASCII로만 쓴다 (한글 주석 금지 — 코드페이지 문제).
    $extra = ''
    if ($StateDir -ne $DefaultStateDir) { $extra = " --state-dir `"$StateDir`"" }
    $lines = @(
        '@echo off',
        'rem INHERIT AutoCert sensor runner - generated by install-sensor.ps1. Do not edit.',
        'setlocal',
        "set `"LOGDIR=$LogDir`"",
        "set `"LOG=$LogFile`"",
        'if not exist "%LOGDIR%" mkdir "%LOGDIR%"',
        'if exist "%LOG%" for %%F in ("%LOG%") do if %%~zF GTR 10485760 move /Y "%LOG%" "%LOG%.1" >nul 2>&1',
        "`"$ExePath`" run --server $script:InServer$extra >> `"%LOG%`" 2>&1"
    )
    [System.IO.File]::WriteAllText($WrapperPath, ($lines -join "`r`n") + "`r`n", [System.Text.Encoding]::ASCII)
}

function Stop-SensorTask {
    try {
        $t = Get-SensorTask
        if ($null -ne $t) { Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue }
    } catch {
        # 스케줄러 모듈이 없으면 schtasks로
        & schtasks.exe /End /TN $TaskName 2>&1 | Out-Null
    }
    Get-Process -Name 'autocert-sensor' -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 500
}

function Register-SensorTask {
    Write-Head '[4/5] 상시 실행 등록 (작업 스케줄러)'
    Write-RunWrapper
    Write-Host "  실행 래퍼: $WrapperPath (로그를 $LogFile 로 남긴다)"

    $useModule = $null -ne (Get-Command -Name 'Register-ScheduledTask' -ErrorAction SilentlyContinue)
    if ($useModule) {
        try {
            $action = New-ScheduledTaskAction -Execute $WrapperPath
            $trigger = New-ScheduledTaskTrigger -AtStartup
            $principal = New-ScheduledTaskPrincipal -UserId 'SYSTEM' -LogonType ServiceAccount -RunLevel Highest
            $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -MultipleInstances IgnoreNew -RestartInterval (New-TimeSpan -Minutes 1) -RestartCount 999
            $settings.ExecutionTimeLimit = 'PT0S'   # 무제한 - 센서는 상주 프로세스다
            Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Principal $principal -Settings $settings -Force | Out-Null
            Write-Host "  등록: '$TaskName' (부팅 시 시작 · SYSTEM · 1분 간격 자동 재시작)"
            return $true
        } catch {
            Write-RowWarn "ScheduledTasks 모듈 등록 실패 - schtasks로 재시도한다 ($($_.Exception.Message))"
        }
    }

    # 폴백 — ScheduledTasks 모듈이 없는 구버전(2008 R2 등).
    # /TR 값에 공백이 있으면 PowerShell 5.1의 네이티브 인자 전달이 따옴표를 제대로 못 넘기므로,
    # 기본 래퍼 경로(%ProgramData%\AutocertSensor\run-sensor.cmd)는 공백이 없게 잡아 두었다.
    # -StateDir로 공백이 있는 경로를 준 경우에만 8.3 단축 경로로 우회한다.
    $tr = $WrapperPath
    if ($tr.Contains(' ')) {
        try {
            $fso = New-Object -ComObject Scripting.FileSystemObject
            $short = $fso.GetFile($WrapperPath).ShortPath
            if ($short -and -not $short.Contains(' ')) { $tr = $short }
        } catch {
            # 8.3 이름이 꺼진 볼륨 — 아래 이스케이프에 의존한다
        }
    }
    if ($tr.Contains(' ')) { $tr = '\"{0}\"' -f $tr }

    & schtasks.exe /Create /TN $TaskName /SC ONSTART /RU SYSTEM /RL HIGHEST /F /TR $tr | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-RowFail "작업 스케줄러 등록 실패 (schtasks exit $LASTEXITCODE)"
        Write-Hint "수동 등록: schtasks /Create /TN `"$TaskName`" /SC ONSTART /RU SYSTEM /TR `"$WrapperPath`""
        return $false
    }
    Write-Host "  등록: '$TaskName' (schtasks · 부팅 시 시작 · SYSTEM)"
    Write-Host '  구버전 스케줄러라 자동 재시작 설정은 들어가지 않았다 - 필요하면 서비스 래퍼(NSSM 등)를 쓴다.' -ForegroundColor DarkGray
    return $true
}

function Start-SensorTask {
    Write-Head '[5/5] 기동과 상태 확인'
    $started = $false
    try {
        Start-ScheduledTask -TaskName $TaskName -ErrorAction Stop
        $started = $true
    } catch {
        & schtasks.exe /Run /TN $TaskName | Out-Null
        $started = ($LASTEXITCODE -eq 0)
    }
    if ($started) { Write-Host "  작업 '$TaskName' 실행 요청됨" }

    $ok = $false
    for ($i = 0; $i -lt 8; $i++) {
        Start-Sleep -Seconds 1
        $proc = Get-Process -Name 'autocert-sensor' -ErrorAction SilentlyContinue
        if ($proc) { $ok = $true; break }
    }

    $state = Get-TaskStateText
    if ($ok) {
        Write-RowOk "실행 상태 - 작업 $state · 센서 프로세스 가동 중"
        return $true
    }
    Write-RowFail "실행 상태 - 작업 $state · 센서 프로세스를 찾지 못했다"
    if (Test-Path -LiteralPath $LogFile -PathType Leaf) {
        Write-Host ''
        Write-Host '  최근 로그:'
        Get-Content -LiteralPath $LogFile -Tail 15 | ForEach-Object { Write-Host "    $_" }
    }
    return $false
}

function Show-Summary {
    param([bool]$Ok)
    $sid = Get-InstalledSensorId
    if (-not $sid) { $sid = '(확인 불가)' }
    Write-Host ''
    if ($Ok) {
        Write-Host '설치 완료' -ForegroundColor Green
    } else {
        Write-Host '설치는 됐지만 확인이 필요하다' -ForegroundColor Yellow
    }
    Write-Host "  센서 호스트   $env:COMPUTERNAME"
    Write-Host "  센서 ID       $sid"
    Write-Host "  서버 주소     $script:InServer"
    Write-Host "  상태 디렉터리 $StateDir (SYSTEM·Administrators 전용)"
    Write-Host "  작업          $TaskName - $(Get-TaskStateText)"
    Write-Host ''
    Write-Host "  로그 확인:  Get-Content -Path `"$LogFile`" -Tail 50 -Wait"
    Write-Host ''
    Write-Host "  웹 콘솔 [센서] 화면에서 상태가 '활성'으로, 마지막 접속이 수 분 이내로 갱신되면 정상이다." -ForegroundColor DarkGray
    Write-Host '  안 붙으면: 위 로그 → manual\SENSOR.md 9장 트러블슈팅 표.' -ForegroundColor DarkGray
}

# ──────────────────────────────────────────────────────────────────────────────
# 메인
# ──────────────────────────────────────────────────────────────────────────────
Write-Host 'INHERIT AutoCert - 센서 설치·등록' -ForegroundColor Cyan

if ($TokenFile) {
    if (-not (Test-Path -LiteralPath $TokenFile -PathType Leaf)) { Stop-WithError "토큰 파일을 읽을 수 없다: $TokenFile" }
    $Token = ((Get-Content -LiteralPath $TokenFile -TotalCount 1) -join '').Trim()
}
if (-not $Token -and $env:AUTOCERT_ENROLL_TOKEN) { $Token = $env:AUTOCERT_ENROLL_TOKEN }

if ($Status) {
    Show-Status
    exit 0
}

# 재실행 안전 — 이미 등록된 센서를 감지하면 무엇을 할지 먼저 묻는다.
$mode = 'install'
if ($SkipEnroll) { $mode = 'service-only' }
if (-not $SkipEnroll) {
    $existingId = Get-InstalledSensorId
    if ($existingId) {
        Write-Head '이미 등록된 센서가 있다'
        Write-Host "  센서 ID       $existingId"
        Write-Host "  상태 디렉터리 $StateDir"
        Write-Host "  작업          $TaskName - $(Get-TaskStateText)"
        Write-Host '  상태 디렉터리는 이 스크립트가 지우지 않는다.' -ForegroundColor DarkGray
        if ($Yes) {
            $mode = 'service-only'
            Write-Host '  비대화 모드: 서비스만 재설치한다.'
        } else {
            $c = Show-Menu -Title '무엇을 할까' -Options @(
                '재등록 - 새 토큰으로 다시 등록한다 (기존 신원을 덮어쓴다)',
                '서비스만 재설치 - 등록은 그대로 두고 작업·기동만 다시 한다',
                '상태만 확인',
                '취소')
            switch ($c) {
                1 { $mode = 'install' }
                2 { $mode = 'service-only' }
                3 { Show-Status; exit 0 }
                default { Write-Host '취소했다.'; exit 0 }
            }
        }
    }
}

# --- 사전 점검 (1/2) ---
Write-Head '사전 점검'
Invoke-CheckAdmin
Invoke-CheckTimeSync
# 패키지 대조가 먼저다 - 통과하면 Invoke-CheckBinary 가 그 결과를 그대로 쓴다.
Invoke-CheckPackage
Invoke-CheckBinary

if ($script:CheckFail -gt 0) {
    if ($Force) {
        Write-Host ''
        Write-Host "  -Force: 실패 $script:CheckFail 건을 무시하고 진행한다." -ForegroundColor Yellow
    } else {
        Write-Host ''
        Write-Host "  실패 $script:CheckFail 건. 위의 '고치는 법'을 처리하고 다시 실행한다." -ForegroundColor Red
        Write-Host '  알면서 강행하려면 -Force.' -ForegroundColor DarkGray
        exit 1
    }
}

# --- 등록 정보 ---
if ($mode -eq 'service-only') {
    $script:InServer = $Server
    if (-not $script:InServer) { $script:InServer = Get-WrapperServerUrl }
    if (-not $script:InServer) {
        if ($Yes) { Stop-WithError '기존 래퍼에서 서버 주소를 찾지 못했다 - -Server 로 지정한다' }
        Write-Head '서버 주소'
        Write-Host '  기존 설정에서 서버 주소를 찾지 못했다.'
        $script:InServer = Remove-Wrapping (Read-Host '  서버 주소 (예: https://10.20.0.5:8443)')
    } else {
        Write-Host ''
        Write-Host "  서버 주소는 기존 설정을 그대로 쓴다: $script:InServer"
    }
    $err = Test-ServerUrl $script:InServer
    if ($err) { Stop-WithError $err }
} else {
    Read-EnrollInput
}

# --- 사전 점검 (2/2) — 도달성은 주소를 알아야 볼 수 있다 ---
Write-Head '사전 점검 - 서버 도달성'
$script:CheckFail = 0
Invoke-CheckReachable $script:InServer
if ($script:CheckFail -gt 0) {
    if ($Force -or $Yes) {
        Write-Host ''
        Write-Host '  도달하지 못했지만 계속 진행한다.' -ForegroundColor Yellow
    } else {
        if (-not (Confirm-Step '서버에 닿지 않는다. 그래도 계속할까? (enroll이 거의 확실히 실패한다)')) {
            $script:InToken = ''
            Stop-WithError '중단했다 - 방화벽·게이트웨이 확인 후 다시 실행한다'
        }
    }
}

# --- 설치 ---
Install-SensorBinary
Initialize-StateDir
if ($mode -eq 'service-only') {
    Write-Head '[3/5] 서버에 등록 (enroll)'
    Write-Host '  건너뛴다 - 기존 등록을 그대로 쓴다.'
} else {
    Invoke-Enroll
}

$result = $true
if (Register-SensorTask) {
    if (-not (Start-SensorTask)) { $result = $false }
} else {
    $result = $false
}

Show-Summary -Ok $result
if ($result) { exit 0 } else { exit 1 }
