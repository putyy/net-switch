param(
    [ValidateSet("amd64", "arm64")]
    [string]$Architecture = "amd64"
)

$ErrorActionPreference = "Stop"
$scriptDirectory = Split-Path -Parent $MyInvocation.MyCommand.Path
$projectRoot = Split-Path -Parent $scriptDirectory
$version = (Get-Content -Raw (Join-Path $projectRoot "VERSION")).Trim()

if ($version -notmatch '^\d+\.\d+\.\d+$') {
    throw "VERSION 必须是三段数字版本号，例如 0.1.0"
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "未找到 go"
}
$makeNSISCommand = Get-Command makensis.exe -ErrorAction SilentlyContinue
if (-not $makeNSISCommand) {
    $makeNSISCommand = Get-Command makensis -ErrorAction SilentlyContinue
}
$makeNSISPath = if ($makeNSISCommand) { $makeNSISCommand.Source } else { $null }
if (-not $makeNSISPath) {
    $makeNSISCandidates = @()
    if (${env:ProgramFiles(x86)}) {
        $makeNSISCandidates += Join-Path ${env:ProgramFiles(x86)} "NSIS\makensis.exe"
    }
    if ($env:ProgramFiles) {
        $makeNSISCandidates += Join-Path $env:ProgramFiles "NSIS\makensis.exe"
    }
    if ($env:ChocolateyInstall) {
        $makeNSISCandidates += Join-Path $env:ChocolateyInstall "bin\makensis.exe"
    }
    $makeNSISCandidates += "C:\ProgramData\chocolatey\bin\makensis.exe"
    foreach ($candidate in $makeNSISCandidates) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            $makeNSISPath = $candidate
            break
        }
    }
}
if (-not $makeNSISPath) {
    throw "未找到 makensis，请先安装 NSIS"
}

$distDirectory = Join-Path $scriptDirectory "dist"
$archiveName = "Net-Switch-$version-windows-$Architecture"
$archivePath = Join-Path $distDirectory "$archiveName.zip"
$installerPath = Join-Path $distDirectory "$archiveName-setup.exe"
$workDirectory = Join-Path ([IO.Path]::GetTempPath()) ("net-switch-" + [Guid]::NewGuid().ToString("N"))
$portableDirectory = Join-Path $workDirectory $archiveName
$binaryPath = Join-Path $portableDirectory "net-switch.exe"

try {
    New-Item -ItemType Directory -Force $distDirectory, $portableDirectory | Out-Null
    $commit = (& git -C $projectRoot rev-parse --short HEAD 2>$null)
    if (-not $commit) { $commit = "unknown" }
    $builtAt = [DateTime]::UtcNow.ToString("yyyy-MM-ddTHH:mm:ssZ")
    $linkerFlags = "-s -w -H=windowsgui -X main.version=$version -X main.commit=$commit -X main.builtAt=$builtAt"

    $previousGOOS = $env:GOOS
    $previousGOARCH = $env:GOARCH
    $previousCGO = $env:CGO_ENABLED
    try {
        $env:GOOS = "windows"
        $env:GOARCH = $Architecture
        $env:CGO_ENABLED = "0"
        Push-Location $projectRoot
        try {
            & go build -trimpath -ldflags $linkerFlags -o $binaryPath ./cmd/net-switch
            if ($LASTEXITCODE -ne 0) { throw "Go 构建失败" }
        } finally {
            Pop-Location
        }
    } finally {
        $env:GOOS = $previousGOOS
        $env:GOARCH = $previousGOARCH
        $env:CGO_ENABLED = $previousCGO
    }

    Copy-Item (Join-Path $scriptDirectory "windows\README.txt") (Join-Path $portableDirectory "README.txt")
    Copy-Item (Join-Path $scriptDirectory "icons\net-switch.ico") (Join-Path $portableDirectory "net-switch.ico")
    Remove-Item -Force -ErrorAction SilentlyContinue $archivePath, $installerPath
    Compress-Archive -Path $portableDirectory -DestinationPath $archivePath -CompressionLevel Optimal

    $nsiPath = Join-Path $scriptDirectory "windows\net-switch.nsi"
    $iconPath = Join-Path $scriptDirectory "icons\net-switch.ico"
    & $makeNSISPath "/DVERSION=$version" "/DARCHITECTURE=$Architecture" "/DBINARY_PATH=$binaryPath" "/DICON_PATH=$iconPath" "/DOUTPUT_PATH=$installerPath" $nsiPath
    if ($LASTEXITCODE -ne 0) { throw "NSIS 打包失败" }

    Write-Host "Windows 便携包已生成: $archivePath"
    Write-Host "Windows 安装包已生成: $installerPath"
} finally {
    if (Test-Path $workDirectory) {
        Remove-Item -Recurse -Force $workDirectory
    }
}
