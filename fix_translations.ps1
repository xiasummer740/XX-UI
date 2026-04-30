# Fix translation files: restore from original upstream and re-add deviceLimit key
$originalCommit = "52f174fd"
$translationDir = "web/translation"

# Language-specific deviceLimit translations
$deviceLimitMap = @{
    "translate.zh_CN.toml" = '"deviceLimit" = "设备限制"'
    "translate.zh_TW.toml" = '"deviceLimit" = "設備限制"'
    "translate.ja_JP.toml" = '"deviceLimit" = "デバイス制限"'
}

$defaultDeviceLine = '"deviceLimit" = "Device Limit"'

# Get all translation files
$files = Get-ChildItem "$translationDir\*.toml" | Sort-Object Name

foreach ($file in $files) {
    $filename = $file.Name
    Write-Host "Processing $filename..."
    
    # Get original content from upstream commit using raw bytes via git hash-object
    # First get the blob hash from the original commit
    $blobHash = git rev-parse "$originalCommit`:$translationDir/$filename"
    if ($LASTEXITCODE -ne 0) {
        Write-Host "  ERROR: Could not get blob hash for $filename"
        continue
    }
    
    # Use git cat-file to get raw bytes, pipe to a temp file
    $tempFile = [System.IO.Path]::GetTempFileName()
    git cat-file -p $blobHash > $tempFile
    
    # Read back as bytes to verify no BOM
    $bytes = [System.IO.File]::ReadAllBytes($tempFile)
    if ($bytes.Length -ge 3 -and $bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) {
        Write-Host "  BOM detected in original, stripping..."
        $bytes = $bytes[3..($bytes.Length - 1)]
        [System.IO.File]::WriteAllBytes($tempFile, $bytes)
    }
    
    # Read content as UTF-8
    $content = [System.IO.File]::ReadAllText($tempFile, [System.Text.Encoding]::UTF8)
    
    # Determine deviceLimit line for this file
    $deviceLine = $defaultDeviceLine
    if ($deviceLimitMap.ContainsKey($filename)) {
        $deviceLine = $deviceLimitMap[$filename]
    }
    
    # Add deviceLimit after allTimeTrafficUsage line
    $lines = $content -split "`n"
    $newLines = @()
    $added = $false
    
    foreach ($line in $lines) {
        $newLines += $line
        if ($line -match 'allTimeTrafficUsage' -and -not $added) {
            $newLines += $deviceLine
            $added = $true
        }
    }
    
    if (-not $added) {
        Write-Host "  WARNING: Could not find allTimeTrafficUsage in $filename"
        # Try to find [pages.inbounds] section and add after it
        for ($i = 0; $i -lt $lines.Length; $i++) {
            if ($lines[$i] -match '^\[pages\.inbounds\]' -and -not $added) {
                # Add deviceLimit after the section header
                $newLines = @()
                for ($j = 0; $j -le $i; $j++) {
                    $newLines += $lines[$j]
                }
                $newLines += $deviceLine
                for ($j = $i+1; $j -lt $lines.Length; $j++) {
                    $newLines += $lines[$j]
                }
                $added = $true
                break
            }
        }
    }
    
    $newContent = $newLines -join "`n"
    
    # Write as UTF-8 without BOM
    $utf8NoBom = New-Object System.Text.UTF8Encoding $false
    [System.IO.File]::WriteAllText($file.FullName, $newContent, $utf8NoBom)
    
    # Verify no BOM
    $verifyBytes = [System.IO.File]::ReadAllBytes($file.FullName)
    if ($verifyBytes.Length -ge 3 -and $verifyBytes[0] -eq 0xEF -and $verifyBytes[1] -eq 0xBB -and $verifyBytes[2] -eq 0xBF) {
        Write-Host "  ERROR: BOM still present!"
    } else {
        Write-Host "  OK - No BOM, deviceLimit added"
    }
    
    # Clean up temp file
    Remove-Item $tempFile -Force -ErrorAction SilentlyContinue
}

Write-Host "`nAll files processed."
