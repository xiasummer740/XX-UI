$files = Get-ChildItem "web/translation" -Filter *.toml
foreach ($file in $files) {
    $content = Get-Content $file.FullName -Raw
    # Check for UTF-8 BOM (0xEF 0xBB 0xBF)
    $bytes = [System.IO.File]::ReadAllBytes($file.FullName)
    if ($bytes[0] -eq 0xEF -and $bytes[1] -eq 0xBB -and $bytes[2] -eq 0xBF) {
        # Remove BOM by writing from byte 3 onwards
        $newBytes = $bytes[3..($bytes.Length - 1)]
        $utf8NoBom = New-Object System.Text.UTF8Encoding $false
        [System.IO.File]::WriteAllBytes($file.FullName, $newBytes)
        Write-Host "Fixed BOM: $($file.Name)"
    } else {
        Write-Host "No BOM: $($file.Name)"
    }
}
