$content = Get-Content 'web/html/inbounds.html' -Raw

$old = '                          <!-- SOCKS 协议详细信息 -->
                          <table v-if="dbInbound.isSocks" cellpadding="2" style="margin-top: 10px; width: 100%;">
                            <tr>
                              <td colspan="2" style="padding: 8px 8px 2px 8px; font-weight: bold; border-bottom: 1px solid #e8e8e8;">SOCKS 详细信息</td>
                            </tr>
                            <tr>
                              <td style="padding: 2px 8px;">Auth</td>
                              <td style="padding: 2px 8px;">
                                <a-tag color="green">[[ dbInbound.toInbound().settings.auth ]]</a-tag>
                              </td>
                            </tr>
                            <tr>
                              <td style="padding: 2px 8px;">启用 UDP</td>
                              <td style="padding: 2px 8px;">
                                <a-tag color="green">[[ dbInbound.toInbound().settings.udp ]]</a-tag>
                              </td>
                            </tr>
                            <tr>
                              <td style="padding: 2px 8px;">IP</td>
                              <td style="padding: 2px 8px;">
                                <a-tag color="green">[[ dbInbound.toInbound().settings.ip ]]</a-tag>
                              </td>
                            </tr>'

$new = '                          <!-- SOCKS 协议详细信息（与 x-panel-temp 格式一致） -->
                          <table v-if="dbInbound.isSocks" cellpadding="2" style="margin-top: 10px; width: 100%;">
                            <tr>
                              <td style="padding: 2px 8px; white-space: nowrap;">密码Auth</td>
                              <td style="padding: 2px 8px;">
                                <a-tag color="green">[[ dbInbound.toInbound().settings.auth ]]</a-tag>
                              </td>
                            </tr>
                            <tr>
                              <td style="padding: 2px 8px; white-space: nowrap;">启用 udp</td>
                              <td style="padding: 2px 8px;">
                                <a-tag color="green">[[ dbInbound.toInbound().settings.udp ]]</a-tag>
                              </td>
                            </tr>'

$content = $content.Replace($old, $new)

[System.IO.File]::WriteAllText((Resolve-Path 'web/html/inbounds.html'), $content, [System.Text.UTF8Encoding]::new($false))
Write-Host "Done"
