$path = Resolve-Path "web/html/inbounds.html"
$content = [System.IO.File]::ReadAllText($path, [System.Text.UTF8Encoding]::new($false))

# Find the comment position
$commentIdx = $content.IndexOf("<!-- SOCKS 协议详细信息 -->")
if ($commentIdx -lt 0) {
    Write-Host "Comment not found!"
    exit 1
}

# Extract the exact old content from the file
$oldLen = "                            <!-- SOCKS 协议详细信息 -->"
$oldLen += "`n                            <template v-if=""dbInbound.isSocks"">"
$oldLen += "`n                              <tr>"
$oldLen += "`n                                <td colspan=""2"" style=""padding: 8px 8px 2px 8px; font-weight: bold; border-bottom: 1px solid #e8e8e8;"">SOCKS 详细信息</td>"
$oldLen += "`n                              </tr>"
$oldLen += "`n                              <tr>"
$oldLen += "`n                                <td style=""padding: 2px 8px;"">Auth</td>"
$oldLen += "`n                                <td style=""padding: 2px 8px;"">"
$oldLen += "`n                                  <a-tag color=""green"">[[ dbInbound.toInbound().settings.auth ]]</a-tag>"
$oldLen += "`n                                </td>"
$oldLen += "`n                              </tr>"
$oldLen += "`n                              <tr>"
$oldLen += "`n                                <td style=""padding: 2px 8px;"">启用 UDP</td>"
$oldLen += "`n                                <td style=""padding: 2px 8px;"">"
$oldLen += "`n                                  <a-tag color=""green"">[[ dbInbound.toInbound().settings.udp ]]</a-tag>"
$oldLen += "`n                                </td>"
$oldLen += "`n                              </tr>"
$oldLen += "`n                              <tr>"
$oldLen += "`n                                <td style=""padding: 2px 8px;"">IP</td>"
$oldLen += "`n                                <td style=""padding: 2px 8px;"">"
$oldLen += "`n                                  <a-tag color=""green"">[[ dbInbound.toInbound().settings.ip ]]</a-tag>"
$oldLen += "`n                                </td>"
$oldLen += "`n                              </tr>"
$oldLen += "`n                              <template v-if=""dbInbound.toInbound().settings.auth == 'password'"">"
$oldLen += "`n                                <tr>"
$oldLen += "`n                                  <td style=""padding: 2px 8px;"">用户名</td>"
$oldLen += "`n                                  <td style=""padding: 2px 8px;"">密码</td>"
$oldLen += "`n                                </tr>"
$oldLen += "`n                                <tr v-for=""(account, index) in dbInbound.toInbound().settings.accounts"">"
$oldLen += "`n                                  <td style=""padding: 2px 8px;"">"
$oldLen += "`n                                    <a-tag color=""green"">[[ account.user ]]</a-tag>"
$oldLen += "`n                                  </td>"
$oldLen += "`n                                  <td style=""padding: 2px 8px;"">"
$oldLen += "`n                                    <a-tag color=""green"">[[ account.pass ]]</a-tag>"
$oldLen += "`n                                  </td>"
$oldLen += "`n                                </tr>"
$oldLen += "`n                              </template>"
$oldLen += "`n                            </template>"

$actualOld = $content.Substring($commentIdx, $oldLen.Length)

# Compare byte by byte
for ($i = 0; $i -lt $oldLen.Length; $i++) {
    if ($oldLen[$i] -ne $actualOld[$i]) {
        $ob = [System.Text.Encoding]::UTF8.GetBytes($oldLen[$i..$i])[0]
        $ab = [System.Text.Encoding]::UTF8.GetBytes($actualOld[$i..$i])[0]
        Write-Host ("Diff at $i old=0x{0:X2}(''{2}'') actual=0x{1:X2}(''{3}'')" -f $ob, $ab, $oldLen[$i], $actualOld[$i])
        exit 1
    }
}

Write-Host "Exact match found at $commentIdx, length $($oldLen.Length)"
