$repo = "AamindMandragora/pragma"
$installDir = "$env:USERPROFILE\.pragma\bin"

# gets device arch, latest release link
$arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { "386" }
$latest = (Invoke-RestMethod "https://api.github.com/repos/$repo/releases/latest").tag_name
$url = "https://github.com/$repo/releases/download/$latest/pragma-windows-$arch.tar.gz"

Write-Host "Installing pragma $latest..."

# creates new install dir, unzips tarball inside
New-Item -ItemType Directory -Force -Path $installDir | Out-Null
$tmp = "$env:TEMP\pragma.tar.gz"
Invoke-WebRequest -Uri $url -OutFile $tmp
tar xzf $tmp -C $installDir
Remove-Item $tmp

# add to user PATH if not already there
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
if ($userPath -notlike "*$installDir*") {
    [Environment]::SetEnvironmentVariable("Path", "$installDir;$userPath", "User")
    Write-Host "Added $installDir to PATH. Restart your terminal."
}

Write-Host "pragma $latest installed to $installDir\pragma.exe"