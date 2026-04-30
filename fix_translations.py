#!/usr/bin/env python3
"""
Fix translation files: restore from original upstream (commit 52f174fd) 
and re-add the deviceLimit key that we added.

CRITICAL: This script MUST be run with Python 3, NOT PowerShell.
It uses subprocess with binary mode to avoid encoding corruption
that PowerShell's > redirection causes on Chinese Windows (GBK encoding).
"""
import subprocess
import os
import sys

TRANSLATION_DIR = "web/translation"
ORIGINAL_COMMIT = "52f174fd"

# Language-specific deviceLimit translations
DEVICE_LIMIT_TRANSLATIONS = {
    "translate.zh_CN.toml": '"deviceLimit" = "设备限制"',
    "translate.zh_TW.toml": '"deviceLimit" = "設備限制"',
    "translate.ja_JP.toml": '"deviceLimit" = "デバイス制限"',
}

DEFAULT_DEVICE_LINE = '"deviceLimit" = "Device Limit"'


def get_original_content_bytes(filename):
    """
    Get file content from the original upstream commit as raw bytes.
    
    CRITICAL: We use capture_output=True (defaults to bytes mode) and
    NEVER use text=True, because text=True would decode using system
    encoding (GBK on Chinese Windows), corrupting UTF-8 content.
    """
    result = subprocess.run(
        ["git", "show", f"{ORIGINAL_COMMIT}:{TRANSLATION_DIR}/{filename}"],
        capture_output=True,  # Returns bytes, NOT text
        cwd="."
    )
    if result.returncode != 0:
        print(f"ERROR getting original {filename}: {result.stderr.decode('utf-8', errors='replace')}")
        return None
    
    stdout_bytes = result.stdout
    
    # Strip BOM if present (original upstream shouldn't have BOM, but just in case)
    if stdout_bytes[:3] == b'\xef\xbb\xbf':
        print(f"  BOM detected in original {filename}, stripping...")
        stdout_bytes = stdout_bytes[3:]
    
    return stdout_bytes


def add_device_limit_to_bytes(content_bytes, filename):
    """Add deviceLimit key to [pages.inbounds] section, working with bytes."""
    device_line = DEVICE_LIMIT_TRANSLATIONS.get(filename, DEFAULT_DEVICE_LINE)
    device_line_bytes = (device_line + '\n').encode('utf-8')
    
    # Search for allTimeTrafficUsage in the bytes content
    all_time_traffic_bytes = b'allTimeTrafficUsage'
    
    idx = content_bytes.find(all_time_traffic_bytes)
    if idx >= 0:
        # Find the end of this line
        eol_idx = content_bytes.find(b'\n', idx)
        if eol_idx >= 0:
            # Insert deviceLimit after this line
            new_content = content_bytes[:eol_idx+1] + device_line_bytes + content_bytes[eol_idx+1:]
            return new_content
    
    print(f"  WARNING: Could not find allTimeTrafficUsage in {filename}")
    
    # Fallback: find [pages.inbounds] section header
    section_header = b'[pages.inbounds]'
    idx = content_bytes.find(section_header)
    if idx >= 0:
        eol_idx = content_bytes.find(b'\n', idx)
        if eol_idx >= 0:
            new_content = content_bytes[:eol_idx+1] + device_line_bytes + content_bytes[eol_idx+1:]
            return new_content
    
    print(f"  ERROR: Could not find [pages.inbounds] section in {filename}")
    return content_bytes


def verify_utf8(content_bytes, filename):
    """Verify the content is valid UTF-8."""
    try:
        content_bytes.decode('utf-8')
        return True
    except UnicodeDecodeError as e:
        print(f"  ERROR: {filename} is NOT valid UTF-8: {e}")
        return False


def main():
    # Get list of translation files
    files = sorted([f for f in os.listdir(TRANSLATION_DIR) if f.endswith('.toml')])
    
    all_ok = True
    
    for filename in files:
        filepath = os.path.join(TRANSLATION_DIR, filename)
        
        print(f"Processing {filename}...")
        
        # Get original content as raw bytes from upstream commit
        content_bytes = get_original_content_bytes(filename)
        if content_bytes is None:
            all_ok = False
            continue
        
        # Verify it's valid UTF-8
        if not verify_utf8(content_bytes, filename):
            print(f"  Attempting to fix by re-encoding...")
            # Try to decode as UTF-8 with error handling, then re-encode
            try:
                content_bytes = content_bytes.decode('utf-8', errors='replace').encode('utf-8')
            except:
                pass
        
        # Add deviceLimit key
        new_content_bytes = add_device_limit_to_bytes(content_bytes, filename)
        
        # Verify final content is valid UTF-8
        if not verify_utf8(new_content_bytes, filename):
            print(f"  FAILED: {filename} - content is not valid UTF-8")
            all_ok = False
            continue
        
        # Write file as UTF-8 without BOM (binary mode to avoid any encoding conversion)
        with open(filepath, 'wb') as f:
            f.write(new_content_bytes)
        
        # Verify no BOM
        with open(filepath, 'rb') as f:
            first_bytes = f.read(3)
            if first_bytes == b'\xef\xbb\xbf':
                print(f"  ERROR: BOM still present in {filename}")
                all_ok = False
            else:
                # Verify file size
                file_size = os.path.getsize(filepath)
                print(f"  OK: {filename} ({file_size} bytes)")
    
    if all_ok:
        print(f"\nAll files processed successfully!")
    else:
        print(f"\nSome files had errors!")
        sys.exit(1)


if __name__ == "__main__":
    main()
