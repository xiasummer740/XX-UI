#!/bin/bash

red='\033[0;31m'
green='\033[0;32m'
blue='\033[0;34m'
yellow='\033[0;33m'
plain='\033[0m'

cur_dir=$(pwd)

# Download mirror support (set XUI_DOWNLOAD_BASE to use a mirror, e.g. https://ghproxy.net/https://github.com)
DOWNLOAD_BASE="${XUI_DOWNLOAD_BASE:-https://github.com}"
RAW_BASE="${XUI_RAW_BASE:-https://raw.githubusercontent.com}"

# Curl wrapper with retry, timeout, and automatic IPv4 fallback
_curl() {
    local max_retries=3
    local retry_delay=2
    local retry_count=0

    if ! command -v curl &>/dev/null; then
        echo "Error: curl not found" >&2
        return 1
    fi

    while [ $retry_count -lt $max_retries ]; do
        # Try without address family flag first (supports both IPv4 and IPv6)
        if curl --connect-timeout 10 "$@" 2>/dev/null; then
            return 0
        fi
        # If failed, retry with IPv4-only
        if curl -4 --connect-timeout 10 "$@" 2>/dev/null; then
            return 0
        fi
        retry_count=$((retry_count + 1))
        if [ $retry_count -lt $max_retries ]; then
            echo -e "${yellow}下载失败，${retry_delay}秒后重试 (${retry_count}/${max_retries})...${plain}" >&2
            sleep $retry_delay
            retry_delay=$((retry_delay * 2))
        fi
    done
    return 1
}

xui_folder="${XUI_MAIN_FOLDER:=/usr/local/x-ui}"
xui_service="${XUI_SERVICE:=/etc/systemd/system}"

# check root
[[ $EUID -ne 0 ]] && echo -e "${red}严重错误: ${plain} 请使用 root 权限运行此脚本 \n " && exit 1

# Check OS and set release variable
if [[ -f /etc/os-release ]]; then
    source /etc/os-release
    release=$ID
    elif [[ -f /usr/lib/os-release ]]; then
    source /usr/lib/os-release
    release=$ID
else
    echo "无法检测系统类型，请联系作者！" >&2
    exit 1
fi
echo "系统类型: $release"

arch() {
    case "$(uname -m)" in
        x86_64 | x64 | amd64) echo 'amd64' ;;
        i*86 | x86) echo '386' ;;
        armv8* | armv8 | arm64 | aarch64) echo 'arm64' ;;
        armv7* | armv7 | arm) echo 'armv7' ;;
        armv6* | armv6) echo 'armv6' ;;
        armv5* | armv5) echo 'armv5' ;;
        s390x) echo 's390x' ;;
        *) echo -e "${green}不支持的 CPU 架构！${plain}" && rm -f install.sh && exit 1 ;;
    esac
}

echo "架构: $(arch)"

# Simple helpers
is_ipv4() {
    [[ "$1" =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] && return 0 || return 1
}
is_ipv6() {
    [[ "$1" =~ : ]] && return 0 || return 1
}
is_ip() {
    is_ipv4 "$1" || is_ipv6 "$1"
}
is_domain() {
    [[ "$1" =~ ^([A-Za-z0-9](-*[A-Za-z0-9])*\.)+(xn--[a-z0-9]{2,}|[A-Za-z]{2,})$ ]] && return 0 || return 1
}

# Port helpers
is_port_in_use() {
    local port="$1"
    if command -v ss >/dev/null 2>&1; then
        ss -ltn 2>/dev/null | awk -v p=":${port}$" '$4 ~ p {exit 0} END {exit 1}'
        return
    fi
    if command -v netstat >/dev/null 2>&1; then
        netstat -lnt 2>/dev/null | awk -v p=":${port} " '$4 ~ p {exit 0} END {exit 1}'
        return
    fi
    if command -v lsof >/dev/null 2>&1; then
        lsof -nP -iTCP:${port} -sTCP:LISTEN >/dev/null 2>&1 && return 0
    fi
    return 1
}

install_base() {
    case "${release}" in
        ubuntu | debian | armbian)
            apt-get update && apt-get install -y -q cron curl tar tzdata socat ca-certificates openssl
        ;;
        fedora | amzn | virtuozzo | rhel | almalinux | rocky | ol)
            dnf -y update && dnf install -y -q cronie curl tar tzdata socat ca-certificates openssl
        ;;
        centos)
            if [[ "${VERSION_ID}" =~ ^7 ]]; then
                yum -y update && yum install -y cronie curl tar tzdata socat ca-certificates openssl
            else
                dnf -y update && dnf install -y -q cronie curl tar tzdata socat ca-certificates openssl
            fi
        ;;
        arch | manjaro | parch)
            pacman -Syu && pacman -Syu --noconfirm cronie curl tar tzdata socat ca-certificates openssl
        ;;
        opensuse-tumbleweed | opensuse-leap)
            zypper refresh && zypper -q install -y cron curl tar timezone socat ca-certificates openssl
        ;;
        alpine)
            apk update && apk add dcron curl tar tzdata socat ca-certificates openssl
        ;;
        *)
            apt-get update && apt-get install -y -q cron curl tar tzdata socat ca-certificates openssl
        ;;
    esac
}

gen_random_string() {
    local length="$1"
    openssl rand -base64 $(( length * 2 )) \
        | tr -dc 'a-zA-Z0-9' \
        | head -c "$length"
}

install_acme() {
    echo -e "${green}正在安装 acme.sh SSL 证书管理工具...${plain}"
    cd ~ || return 1
    _curl -s https://get.acme.sh | sh >/dev/null 2>&1
    if [ $? -ne 0 ]; then
        echo -e "${red}安装 acme.sh 失败${plain}"
        return 1
    else
        echo -e "${green}acme.sh 安装成功${plain}"
    fi
    return 0
}

restore_service() {
    local svc="$1"
    if [[ -z "${svc}" ]]; then
        return 0
    fi
    if [[ "${svc}" == pid:* ]]; then
        local pid="${svc#pid:}"
        kill -CONT "${pid}" 2>/dev/null
        echo -e "${green}进程 ${pid} 已恢复。${plain}"
        return 0
    fi
    if systemctl start "${svc}" 2>/dev/null; then
        echo -e "${green}服务 ${svc} 已通过 systemctl 重新启动。${plain}"
        return 0
    fi
    if service "${svc}" start 2>/dev/null; then
        echo -e "${green}服务 ${svc} 已通过 service 重新启动。${plain}"
        return 0
    fi
    if rc-service "${svc}" start 2>/dev/null; then
        echo -e "${green}服务 ${svc} 已通过 rc-service 重新启动。${plain}"
        return 0
    fi
    echo -e "${yellow}无法重新启动服务 ${svc}，您可能需要手动重启。${plain}"
}

setup_ssl_certificate() {
    local domain="$1"
    local server_ip="$2"
    local existing_port="$3"
    local existing_webBasePath="$4"
    
    echo -e "${green}正在设置 SSL 证书...${plain}"
    
    # Check if acme.sh is installed
    if ! command -v ~/.acme.sh/acme.sh &>/dev/null; then
        install_acme
        if [ $? -ne 0 ]; then
            echo -e "${yellow}安装 acme.sh 失败，跳过 SSL 设置${plain}"
            return 1
        fi
    fi
    
    # Clean up stale acme.sh entry if key file is missing
    local acme_ecc_dir="${HOME}/.acme.sh/${domain}_ecc"
    local acme_rsa_dir="${HOME}/.acme.sh/${domain}"
    if ~/.acme.sh/acme.sh --list 2>/dev/null | awk '{print $1}' | grep -Fxq "${domain}"; then
        if [[ ! -f "${acme_ecc_dir}/${domain}.key" ]] && [[ ! -f "${acme_rsa_dir}/${domain}.key" ]]; then
            echo -e "${yellow}检测到 ${domain} 的残留 acme.sh 条目，清理中...${plain}"
            rm -rf "${acme_ecc_dir}" 2>/dev/null
            rm -rf "${acme_rsa_dir}" 2>/dev/null
        fi
    fi

    # Create certificate directory
    local certPath="/root/cert/${domain}"
    mkdir -p "$certPath"

    # Check port 80 availability and offer alternatives
    local WebPort=80
    local use_webroot=0
    local webroot_path=""
    local stopped_service=""

    if is_port_in_use "80"; then
        echo ""
        echo -e "${yellow}端口 80 当前被占用，Let's Encrypt 验证可能失败。${plain}"

        # Detect web server for webroot mode
        local web_server_svc=""
        local detected_webroot=""
        if command -v nginx &>/dev/null; then
            web_server_svc="nginx"
            detected_webroot=$(grep -rh "root " /etc/nginx/conf.d/ /etc/nginx/sites-enabled/ /etc/nginx/sites-available/ 2>/dev/null | grep -v "#" | head -1 | awk '{print $2}' | tr -d ';')
            [[ -z "${detected_webroot}" ]] && detected_webroot=$(grep -rh "root " /etc/nginx/nginx.conf 2>/dev/null | grep -v "#" | head -1 | awk '{print $2}' | tr -d ';')
            [[ -z "${detected_webroot}" ]] && detected_webroot="/usr/share/nginx/html"
        elif command -v apache2 &>/dev/null; then
            web_server_svc="apache2"
            detected_webroot=$(grep -rh "DocumentRoot " /etc/apache2/sites-enabled/ /etc/apache2/sites-available/ 2>/dev/null | grep -v "#" | head -1 | awk '{print $2}')
            [[ -z "${detected_webroot}" ]] && detected_webroot="/var/www/html"
        elif command -v httpd &>/dev/null; then
            web_server_svc="httpd"
            detected_webroot=$(grep -rh "DocumentRoot " /etc/httpd/conf.d/ /etc/httpd/conf/ 2>/dev/null | grep -v "#" | head -1 | awk '{print $2}')
            [[ -z "${detected_webroot}" ]] && detected_webroot="/var/www/html"
        elif command -v caddy &>/dev/null; then
            web_server_svc="caddy"
            detected_webroot="/var/www/html"
        fi

        echo -e "${yellow}请选择解决方案：${plain}"
        echo -e "${green}  1.${plain} Webroot 模式（${green}推荐${plain}）— 通过现有 Web 服务器验证"
        if [[ -n "${web_server_svc}" ]]; then
            echo -e "     → 检测到 ${web_server_svc}，webroot: ${detected_webroot}"
        fi
        echo -e "${green}  2.${plain} 临时停服 — 停止占用端口的服务，签发后自动恢复"
        echo -e "${green}  0.${plain} 跳过 SSL 设置（稍后通过 x-ui 命令配置）"
        read -rp "请选择 [1]: " port_choice
        port_choice="${port_choice:-1}"

        case "${port_choice}" in
        1)
            use_webroot=1
            if [[ -n "${web_server_svc}" && -d "${detected_webroot}" ]]; then
                webroot_path="${detected_webroot}"
            else
                read -rp "请输入 Web 服务器根目录路径: " webroot_path
                webroot_path="${webroot_path// /}"
                if [[ ! -d "${webroot_path}" ]]; then
                    echo -e "${red}路径不存在，跳过 SSL。${plain}"
                    return 1
                fi
            fi
            mkdir -p "${webroot_path}/.well-known/acme-challenge"
            echo -e "${green}Webroot 模式: ${webroot_path}${plain}"
            ;;
        2)
            if [[ -n "${web_server_svc}" ]]; then
                stopped_service="${web_server_svc}"
                echo -e "${yellow}正在临时停止 ${stopped_service}...${plain}"
                systemctl stop "${stopped_service}" 2>/dev/null || service "${stopped_service}" stop 2>/dev/null || rc-service "${stopped_service}" stop 2>/dev/null
                sleep 1
            fi
            ;;
        *)
            echo -e "${yellow}跳过 SSL 证书设置，您可稍后通过 x-ui 命令配置。${plain}"
            return 1
            ;;
        esac
        echo ""
    fi

    # Issue certificate
    echo -e "${green}正在为 ${domain} 签发 SSL 证书...${plain}"

    ~/.acme.sh/acme.sh --set-default-ca --server letsencrypt --force >/dev/null 2>&1
    local issue_rc=0
    if [[ ${use_webroot} -eq 1 ]]; then
        ~/.acme.sh/acme.sh --issue -d ${domain} --webroot "${webroot_path}" --force
        issue_rc=$?
    else
        ~/.acme.sh/acme.sh --issue -d ${domain} --listen-v6 --standalone --httpport ${WebPort} --force
        issue_rc=$?
    fi

    if [[ ${issue_rc} -ne 0 ]]; then
        echo -e "${yellow}为 ${domain} 签发证书失败${plain}"
        echo -e "${yellow}请稍后通过 x-ui 命令重试${plain}"
        rm -rf ~/.acme.sh/${domain} 2>/dev/null
        rm -rf "${acme_ecc_dir}" 2>/dev/null
        rm -rf "$certPath" 2>/dev/null
        [[ -n "${stopped_service}" ]] && restore_service "${stopped_service}"
        return 1
    fi

    # Restore any temporarily stopped service
    [[ -n "${stopped_service}" ]] && restore_service "${stopped_service}"
    
    # Install certificate
    ~/.acme.sh/acme.sh --installcert -d ${domain} \
        --key-file /root/cert/${domain}/privkey.pem \
        --fullchain-file /root/cert/${domain}/fullchain.pem \
        --reloadcmd "systemctl restart x-ui" >/dev/null 2>&1
    
    if [ $? -ne 0 ]; then
        echo -e "${yellow}安装证书失败${plain}"
        [[ -n "${stopped_service}" ]] && restore_service "${stopped_service}"
        return 1
    fi

    # Enable auto-renew
    ~/.acme.sh/acme.sh --upgrade --auto-upgrade >/dev/null 2>&1
    # Secure permissions: private key readable only by owner
    chmod 600 $certPath/privkey.pem 2>/dev/null
    chmod 644 $certPath/fullchain.pem 2>/dev/null

    # Set certificate for panel
    local webCertFile="/root/cert/${domain}/fullchain.pem"
    local webKeyFile="/root/cert/${domain}/privkey.pem"

    if [[ -f "$webCertFile" && -f "$webKeyFile" ]]; then
        ${xui_folder}/x-ui cert -webCert "$webCertFile" -webCertKey "$webKeyFile" >/dev/null 2>&1
        echo -e "${green}SSL 证书安装配置成功！${plain}"
        return 0
    else
        echo -e "${yellow}未找到证书文件${plain}"
        return 1
    fi
}

# Issue Let's Encrypt IP certificate with shortlived profile (~6 days validity)
# Requires acme.sh and port 80 open for HTTP-01 challenge
setup_ip_certificate() {
    local ipv4="$1"
    local ipv6="$2"  # optional

    echo -e "${green}正在设置 Let's Encrypt IP 证书（短期配置）...${plain}"
    echo -e "${yellow}注意：IP 证书有效期约 6 天，会自动续期。${plain}"
    echo -e "${yellow}默认监听端口 80。如果选择其他端口，请确保外部 80 端口转发到该端口。${plain}"

    # Check for acme.sh
    if ! command -v ~/.acme.sh/acme.sh &>/dev/null; then
        install_acme
        if [ $? -ne 0 ]; then
            echo -e "${red}安装 acme.sh 失败${plain}"
            return 1
        fi
    fi

    # Validate IP address
    if [[ -z "$ipv4" ]]; then
        echo -e "${red}需要提供 IPv4 地址${plain}"
        return 1
    fi

    if ! is_ipv4 "$ipv4"; then
        echo -e "${red}无效的 IPv4 地址: $ipv4${plain}"
        return 1
    fi

    # Create certificate directory
    local certDir="/root/cert/ip"
    mkdir -p "$certDir"

    # Build domain arguments
    local domain_args="-d ${ipv4}"
    if [[ -n "$ipv6" ]] && is_ipv6 "$ipv6"; then
        domain_args="${domain_args} -d ${ipv6}"
        echo -e "${green}包含 IPv6 地址: ${ipv6}${plain}"
    fi

    # Set reload command for auto-renewal (add || true so it doesn't fail during first install)
    local reloadCmd="systemctl restart x-ui 2>/dev/null || rc-service x-ui restart 2>/dev/null || true"

    # Choose port for HTTP-01 listener (default 80, prompt override)
    local WebPort=""
    read -rp "请输入 ACME HTTP-01 验证端口（默认 80）: " WebPort
    WebPort="${WebPort:-80}"
    if ! [[ "${WebPort}" =~ ^[0-9]+$ ]] || ((WebPort < 1 || WebPort > 65535)); then
        echo -e "${red}端口无效，将使用默认端口 80。${plain}"
        WebPort=80
    fi
    echo -e "${green}使用端口 ${WebPort} 进行独立验证。${plain}"
    if [[ "${WebPort}" -ne 80 ]]; then
        echo -e "${yellow}提示：Let's Encrypt 仍连接端口 80；请将外部端口 80 转发到 ${WebPort}。${plain}"
    fi

    # Ensure chosen port is available
    while true; do
        if is_port_in_use "${WebPort}"; then
            echo -e "${yellow}端口 ${WebPort} 已被占用。${plain}"

            local alt_port=""
            read -rp "请输入 acme.sh 的另一个端口（留空取消）: " alt_port
            alt_port="${alt_port// /}"
            if [[ -z "${alt_port}" ]]; then
                echo -e "${red}端口 ${WebPort} 被占用，无法继续。${plain}"
                return 1
            fi
            if ! [[ "${alt_port}" =~ ^[0-9]+$ ]] || ((alt_port < 1 || alt_port > 65535)); then
                echo -e "${red}提供的端口无效。${plain}"
                return 1
            fi
            WebPort="${alt_port}"
            continue
        else
            echo -e "${green}端口 ${WebPort} 可用，准备进行独立验证。${plain}"
            break
        fi
    done

    # Issue certificate with shortlived profile
    echo -e "${green}正在为 ${ipv4} 签发 IP 证书...${plain}"
    ~/.acme.sh/acme.sh --set-default-ca --server letsencrypt --force >/dev/null 2>&1
    
    ~/.acme.sh/acme.sh --issue \
        ${domain_args} \
        --standalone \
        --server letsencrypt \
        --certificate-profile shortlived \
        --days 6 \
        --httpport ${WebPort} \
        --force

    if [ $? -ne 0 ]; then
        echo -e "${red}签发 IP 证书失败${plain}"
        echo -e "${yellow}请确保端口 ${WebPort} 可访问（或从外部端口 80 转发）${plain}"
        # Cleanup acme.sh data for both IPv4 and IPv6 if specified
        rm -rf ~/.acme.sh/${ipv4} 2>/dev/null
        [[ -n "$ipv6" ]] && rm -rf ~/.acme.sh/${ipv6} 2>/dev/null
        rm -rf ${certDir} 2>/dev/null
        return 1
    fi

    echo -e "${green}证书签发成功，正在安装...${plain}"

    # Install certificate
    # Note: acme.sh may report "Reload error" and exit non-zero if reloadcmd fails,
    # but the cert files are still installed. We check for files instead of exit code.
    ~/.acme.sh/acme.sh --installcert -d ${ipv4} \
        --key-file "${certDir}/privkey.pem" \
        --fullchain-file "${certDir}/fullchain.pem" \
        --reloadcmd "${reloadCmd}" 2>&1 || true

    # Verify certificate files exist (don't rely on exit code - reloadcmd failure causes non-zero)
    if [[ ! -f "${certDir}/fullchain.pem" || ! -f "${certDir}/privkey.pem" ]]; then
        echo -e "${red}安装后未找到证书文件${plain}"
        # Cleanup acme.sh data for both IPv4 and IPv6 if specified
        rm -rf ~/.acme.sh/${ipv4} 2>/dev/null
        [[ -n "$ipv6" ]] && rm -rf ~/.acme.sh/${ipv6} 2>/dev/null
        rm -rf ${certDir} 2>/dev/null
        return 1
    fi
    
    echo -e "${green}证书文件安装成功${plain}"

    # Enable auto-upgrade for acme.sh (ensures cron job runs)
    ~/.acme.sh/acme.sh --upgrade --auto-upgrade >/dev/null 2>&1

    # Secure permissions: private key readable only by owner
    chmod 600 ${certDir}/privkey.pem 2>/dev/null
    chmod 644 ${certDir}/fullchain.pem 2>/dev/null

    # Configure panel to use the certificate
    echo -e "${green}正在为面板设置证书路径...${plain}"
    ${xui_folder}/x-ui cert -webCert "${certDir}/fullchain.pem" -webCertKey "${certDir}/privkey.pem"
    
    if [ $? -ne 0 ]; then
        echo -e "${yellow}警告：无法自动设置证书路径${plain}"
        echo -e "${yellow}证书文件位置：${plain}"
        echo -e "  证书: ${certDir}/fullchain.pem"
        echo -e "  密钥: ${certDir}/privkey.pem"
    else
        echo -e "${green}证书路径配置成功${plain}"
    fi

    echo -e "${green}IP 证书安装配置成功！${plain}"
    echo -e "${green}证书有效期约 6 天，由 acme.sh 定时任务自动续期。${plain}"
    echo -e "${yellow}acme.sh 会在到期前自动续期并重载 x-ui。${plain}"
    return 0
}

# Comprehensive manual SSL certificate issuance via acme.sh
ssl_cert_issue() {
    local existing_webBasePath=$(${xui_folder}/x-ui setting -show true | grep 'webBasePath:' | awk -F': ' '{print $2}' | tr -d '[:space:]' | sed 's#^/##')
    local existing_port=$(${xui_folder}/x-ui setting -show true | grep 'port:' | awk -F': ' '{print $2}' | tr -d '[:space:]')
    
    # check for acme.sh first
    if ! command -v ~/.acme.sh/acme.sh &>/dev/null; then
        echo "未找到 acme.sh，正在安装..."
        cd ~ || return 1
        _curl -s https://get.acme.sh | sh
        if [ $? -ne 0 ]; then
            echo -e "${red}安装 acme.sh 失败${plain}"
            return 1
        else
            echo -e "${green}acme.sh 安装成功${plain}"
        fi
    fi

    # get the domain here, and we need to verify it
    local domain=""
    while true; do
        read -rp "请输入您的域名: " domain
        domain="${domain// /}"  # Trim whitespace
        
        if [[ -z "$domain" ]]; then
            echo -e "${red}域名不能为空，请重新输入。${plain}"
            continue
        fi
        
        if ! is_domain "$domain"; then
            echo -e "${red}域名格式无效: ${domain}。请输入有效的域名。${plain}"
            continue
        fi
        
        break
    done
    echo -e "${green}您的域名: ${domain}，正在检测...${plain}"
    SSL_ISSUED_DOMAIN="${domain}"

    # detect existing certificate and reuse it if present
    # Also verify the actual key file exists — acme.sh may have a stale entry
    # from a previous failed attempt where the .key file was never created.
    local cert_exists=0
    local acme_ecc_dir="${HOME}/.acme.sh/${domain}_ecc"
    local acme_rsa_dir="${HOME}/.acme.sh/${domain}"
    if ~/.acme.sh/acme.sh --list 2>/dev/null | awk '{print $1}' | grep -Fxq "${domain}"; then
        if [[ -f "${acme_ecc_dir}/${domain}.key" ]] || [[ -f "${acme_rsa_dir}/${domain}.key" ]]; then
            cert_exists=1
            local certInfo=$(~/.acme.sh/acme.sh --list 2>/dev/null | grep -F "${domain}")
            echo -e "${yellow}发现 ${domain} 的现有证书，将复用。${plain}"
            [[ -n "${certInfo}" ]] && echo "$certInfo"
        else
            echo -e "${yellow}检测到 ${domain} 的残留 acme.sh 条目（密钥文件丢失），将重新签发。${plain}"
            rm -rf "${acme_ecc_dir}" 2>/dev/null
            rm -rf "${acme_rsa_dir}" 2>/dev/null
        fi
    else
        echo -e "${green}您的域名已准备好签发证书...${plain}"
    fi

    # create a directory for the certificate
    certPath="/root/cert/${domain}"
    if [ ! -d "$certPath" ]; then
        mkdir -p "$certPath"
    else
        rm -rf "$certPath"
        mkdir -p "$certPath"
    fi

    # get the port number for the standalone server
    local WebPort=80
    local use_webroot=0
    local webroot_path=""
    local stopped_service=""

    read -rp "请选择要使用的端口（默认 80）: " WebPort
    if [[ ${WebPort} -gt 65535 || ${WebPort} -lt 1 ]]; then
        echo -e "${yellow}您输入的 ${WebPort} 无效，将使用默认端口 80。${plain}"
        WebPort=80
    fi
    echo -e "${green}将使用端口 ${WebPort} 签发证书。${plain}"

    # ——— Port conflict detection & resolution ———
    if is_port_in_use "${WebPort}"; then
        echo ""
        echo -e "${yellow}端口 ${WebPort} 当前被其他进程占用。${plain}"

        # Detect which web server (if any) is running
        local web_server_svc=""
        local detected_webroot=""
        if command -v nginx &>/dev/null; then
            web_server_svc="nginx"
            detected_webroot=$(grep -rh "root " /etc/nginx/conf.d/ /etc/nginx/sites-enabled/ /etc/nginx/sites-available/ 2>/dev/null | grep -v "#" | head -1 | awk '{print $2}' | tr -d ';')
            [[ -z "${detected_webroot}" ]] && detected_webroot=$(grep -rh "root " /etc/nginx/nginx.conf 2>/dev/null | grep -v "#" | head -1 | awk '{print $2}' | tr -d ';')
            [[ -z "${detected_webroot}" ]] && detected_webroot="/usr/share/nginx/html"
        elif command -v apache2 &>/dev/null; then
            web_server_svc="apache2"
            detected_webroot=$(grep -rh "DocumentRoot " /etc/apache2/sites-enabled/ /etc/apache2/sites-available/ 2>/dev/null | grep -v "#" | head -1 | awk '{print $2}')
            [[ -z "${detected_webroot}" ]] && detected_webroot="/var/www/html"
        elif command -v httpd &>/dev/null; then
            web_server_svc="httpd"
            detected_webroot=$(grep -rh "DocumentRoot " /etc/httpd/conf.d/ /etc/httpd/conf/ 2>/dev/null | grep -v "#" | head -1 | awk '{print $2}')
            [[ -z "${detected_webroot}" ]] && detected_webroot="/var/www/html"
        elif command -v caddy &>/dev/null; then
            web_server_svc="caddy"
            detected_webroot="/var/www/html"
        fi

        echo -e "${yellow}端口 ${WebPort} 已被占用，请选择解决方案：${plain}"
        echo -e "${green}  1.${plain} Webroot 模式 — 通过现有 Web 服务器验证（${green}推荐，不占用端口${plain}）"
        if [[ -n "${web_server_svc}" ]]; then
            echo -e "     → 检测到 ${web_server_svc}，自动 webroot: ${detected_webroot}"
        fi
        echo -e "${green}  2.${plain} 临时停服模式 — 停止占用端口的服务 → 签发证书 → 自动恢复"
        echo -e "${green}  3.${plain} 更换端口 — 使用其他端口（需外部端口转发 80→新端口）"
        echo -e "${green}  0.${plain} 放弃"
        read -rp "请选择 [1]: " port_choice
        port_choice="${port_choice:-1}"

        case "${port_choice}" in
        1)
            use_webroot=1
            if [[ -n "${web_server_svc}" && -d "${detected_webroot}" ]]; then
                webroot_path="${detected_webroot}"
                echo -e "${green}使用 Webroot 模式，自动检测路径: ${webroot_path} (${web_server_svc})${plain}"
            else
                read -rp "请输入 Web 服务器的根目录路径: " webroot_path
                webroot_path="${webroot_path// /}"
                if [[ ! -d "${webroot_path}" ]]; then
                    echo -e "${red}路径 ${webroot_path} 不存在，尝试创建...${plain}"
                    mkdir -p "${webroot_path}" 2>/dev/null || {
                        echo -e "${red}无法创建目录，放弃。${plain}"
                        return 1
                    }
                fi
            fi
            mkdir -p "${webroot_path}/.well-known/acme-challenge"
            echo -e "${green}Webroot ACME 验证目录已准备: ${webroot_path}/.well-known/acme-challenge${plain}"
            ;;
        2)
            if [[ -n "${web_server_svc}" ]]; then
                stopped_service="${web_server_svc}"
                echo -e "${yellow}正在临时停止 ${stopped_service}...${plain}"
                if systemctl stop "${stopped_service}" 2>/dev/null; then
                    echo -e "${green}${stopped_service} 已通过 systemctl 停止。${plain}"
                elif service "${stopped_service}" stop 2>/dev/null; then
                    echo -e "${green}${stopped_service} 已通过 service 停止。${plain}"
                elif rc-service "${stopped_service}" stop 2>/dev/null; then
                    echo -e "${green}${stopped_service} 已通过 rc-service 停止。${plain}"
                else
                    echo -e "${red}无法停止 ${stopped_service}，请手动停止后重试。${plain}"
                    return 1
                fi
                sleep 1
            else
                local pid_holder=""
                if command -v ss &>/dev/null; then
                    pid_holder=$(ss -tlnp 2>/dev/null | grep ":${WebPort} " | grep -oP 'pid=\K[0-9]+' | head -1)
                fi
                if [[ -n "${pid_holder}" ]]; then
                    stopped_service="pid:${pid_holder}"
                    kill -STOP "${pid_holder}" 2>/dev/null
                    echo -e "${yellow}进程 ${pid_holder} 已暂停（证书安装完成后将恢复）。${plain}"
                else
                    echo -e "${red}无法确定占用端口 ${WebPort} 的进程，放弃。${plain}"
                    return 1
                fi
            fi
            ;;
        3)
            read -rp "请输入替代端口: " WebPort
            if ! [[ "${WebPort}" =~ ^[0-9]+$ ]] || ((WebPort < 1 || WebPort > 65535)); then
                echo -e "${red}无效端口，放弃。${plain}"
                return 1
            fi
            echo -e "${green}将使用端口 ${WebPort}。${plain}"
            echo -e "${yellow}注意：Let's Encrypt 始终从外部访问 80 端口！${plain}"
            echo -e "${yellow}请确保已将外部 80 端口转发到本机 ${WebPort} 端口。${plain}"
            read -rp "确认继续？ [y/N]: " confirm_forward
            if [[ "${confirm_forward}" != "y" && "${confirm_forward}" != "Y" ]]; then
                echo -e "${red}用户取消。${plain}"
                return 1
            fi
            ;;
        *)
            echo -e "${red}用户取消。${plain}"
            return 1
            ;;
        esac
        echo ""
    fi
    # ——— End port conflict resolution ———

    # Stop panel temporarily (free up panel port if needed)
    echo -e "${yellow}正在临时停止面板...${plain}"
    systemctl stop x-ui 2>/dev/null || rc-service x-ui stop 2>/dev/null

    if [[ ${cert_exists} -eq 0 ]]; then
        # issue the certificate
        ~/.acme.sh/acme.sh --set-default-ca --server letsencrypt --force
        local issue_rc=0
        if [[ ${use_webroot} -eq 1 ]]; then
            ~/.acme.sh/acme.sh --issue -d ${domain} --webroot "${webroot_path}" --force
            issue_rc=$?
        else
            ~/.acme.sh/acme.sh --issue -d ${domain} --listen-v6 --standalone --httpport ${WebPort} --force
            issue_rc=$?
        fi
        if [[ ${issue_rc} -ne 0 ]]; then
            echo -e "${red}签发证书失败，请检查日志。${plain}"
            rm -rf ~/.acme.sh/${domain}
            rm -rf "${acme_ecc_dir}" 2>/dev/null
            systemctl start x-ui 2>/dev/null || rc-service x-ui start 2>/dev/null
            [[ -n "${stopped_service}" ]] && restore_service "${stopped_service}"
            return 1
        else
            echo -e "${green}签发证书成功，正在安装证书...${plain}"
        fi
    else
        echo -e "${green}使用现有证书，正在安装...${plain}"
    fi

    # Setup reload command (use default, no prompt)
    reloadCmd="systemctl restart x-ui || rc-service x-ui restart"
    echo -e "${green}ACME 重载命令已设置为默认值。${plain}"

    # install the certificate
    local installOutput=""
    installOutput=$(~/.acme.sh/acme.sh --installcert -d ${domain} \
        --key-file /root/cert/${domain}/privkey.pem \
        --fullchain-file /root/cert/${domain}/fullchain.pem --reloadcmd "${reloadCmd}" 2>&1)
    local installRc=$?
    echo "${installOutput}"

    local installWroteFiles=0
    if echo "${installOutput}" | grep -q "Installing key to:" && echo "${installOutput}" | grep -q "Installing full chain to:"; then
        installWroteFiles=1
    fi

    if [[ -f "/root/cert/${domain}/privkey.pem" && -f "/root/cert/${domain}/fullchain.pem" && ( ${installRc} -eq 0 || ${installWroteFiles} -eq 1 ) ]]; then
        echo -e "${green}安装证书成功，正在启用自动续期...${plain}"
    else
        echo -e "${red}安装证书失败，退出。${plain}"
        if [[ ${cert_exists} -eq 0 ]]; then
            rm -rf ~/.acme.sh/${domain}
        fi
        systemctl start x-ui 2>/dev/null || rc-service x-ui start 2>/dev/null
        [[ -n "${stopped_service}" ]] && restore_service "${stopped_service}"
        return 1
    fi

    # Restore any service we temporarily stopped — cert is installed now
    [[ -n "${stopped_service}" ]] && restore_service "${stopped_service}"

    # enable auto-renew
    ~/.acme.sh/acme.sh --upgrade --auto-upgrade
    if [ $? -ne 0 ]; then
        echo -e "${yellow}自动续期设置有误，证书详情:${plain}"
        ls -lah /root/cert/${domain}/
        # Secure permissions: private key readable only by owner
        chmod 600 $certPath/privkey.pem 2>/dev/null
        chmod 644 $certPath/fullchain.pem 2>/dev/null
    else
        echo -e "${green}自动续期设置成功，证书详情:${plain}"
        ls -lah /root/cert/${domain}/
        # Secure permissions: private key readable only by owner
        chmod 600 $certPath/privkey.pem 2>/dev/null
        chmod 644 $certPath/fullchain.pem 2>/dev/null
    fi

    # start panel
    systemctl start x-ui 2>/dev/null || rc-service x-ui start 2>/dev/null

    # Automatically set certificate as panel certificate (no prompt)
    echo -e "${green}正在将证书设置为面板证书...${plain}"
    local webCertFile="/root/cert/${domain}/fullchain.pem"
    local webKeyFile="/root/cert/${domain}/privkey.pem"

    if [[ -f "$webCertFile" && -f "$webKeyFile" ]]; then
        ${xui_folder}/x-ui cert -webCert "$webCertFile" -webCertKey "$webKeyFile"
        echo -e "${green}面板证书路径已设置${plain}"
        echo -e "${green}证书文件: $webCertFile${plain}"
        echo -e "${green}密钥文件: $webKeyFile${plain}"
        echo ""
        echo -e "${green}访问地址: https://${domain}:${existing_port}/${existing_webBasePath}${plain}"
        echo -e "${yellow}面板将重启以应用 SSL 证书...${plain}"
        systemctl restart x-ui 2>/dev/null || rc-service x-ui restart 2>/dev/null
    else
        echo -e "${red}错误：未找到域名 $domain 的证书或密钥文件。${plain}"
    fi
    
    return 0
}

# Reusable interactive SSL setup (domain or IP)
# Sets global `SSL_HOST` to the chosen domain/IP for Access URL usage
prompt_and_setup_ssl() {
    local panel_port="$1"
    local web_base_path="$2"   # expected without leading slash
    local server_ip="$3"

    local ssl_choice=""

    echo -e "${yellow}请选择 SSL 证书设置方式:${plain}"
    echo -e "${green}1.${plain} Let's Encrypt 域名证书（90天有效期，自动续期）"
    echo -e "${green}2.${plain} Let's Encrypt IP 地址证书（6天有效期，自动续期）"
    echo -e "${green}3.${plain} 自定义 SSL 证书（指定现有证书文件路径）"
    echo -e "${blue}注意：${plain} 选项 1 和 2 需要开放端口 80。选项 3 需要手动提供证书路径。"
    read -rp "请选择（默认 2，IP 证书）: " ssl_choice
    ssl_choice="${ssl_choice// /}"  # Trim whitespace
    
    # Default to 2 (IP cert) if input is empty or invalid (not 1 or 3)
    if [[ "$ssl_choice" != "1" && "$ssl_choice" != "3" ]]; then
        ssl_choice="2"
    fi

    case "$ssl_choice" in
    1)
        # User chose Let's Encrypt domain option
        echo -e "${green}使用 Let's Encrypt 域名证书...${plain}"
        if ssl_cert_issue; then
            local cert_domain="${SSL_ISSUED_DOMAIN}"
            if [[ -z "${cert_domain}" ]]; then
                cert_domain=$(~/.acme.sh/acme.sh --list 2>/dev/null | tail -1 | awk '{print $1}')
            fi

            if [[ -n "${cert_domain}" ]]; then
                SSL_HOST="${cert_domain}"
                echo -e "${green}✓ SSL 证书配置成功，域名: ${cert_domain}${plain}"
            else
                echo -e "${yellow}SSL 设置可能已完成，但域名提取失败${plain}"
                SSL_HOST="${server_ip}"
            fi
        else
            echo -e "${red}域名 SSL 证书设置失败。${plain}"
            SSL_HOST="${server_ip}"
        fi
        ;;
    2)
        # User chose Let's Encrypt IP certificate option
        echo -e "${green}使用 Let's Encrypt IP 证书（短期配置）...${plain}"
        
        # Ask for optional IPv6
        local ipv6_addr=""
        read -rp "是否有 IPv6 地址需要包含？（留空跳过）: " ipv6_addr
        ipv6_addr="${ipv6_addr// /}"  # Trim whitespace
        
        # Stop panel if running (port 80 needed)
        if [[ $release == "alpine" ]]; then
            rc-service x-ui stop >/dev/null 2>&1
        else
            systemctl stop x-ui >/dev/null 2>&1
        fi
        
        setup_ip_certificate "${server_ip}" "${ipv6_addr}"
        if [ $? -eq 0 ]; then
            SSL_HOST="${server_ip}"
            echo -e "${green}✓ Let's Encrypt IP 证书配置成功${plain}"
        else
            echo -e "${red}✗ IP 证书设置失败。请检查端口 80 是否已开放。${plain}"
            SSL_HOST="${server_ip}"
        fi
        ;;
    3)
        # User chose Custom Paths (User Provided) option
        echo -e "${green}使用自定义现有证书...${plain}"
        local custom_cert=""
        local custom_key=""
        local custom_domain=""

        # 3.1 Request Domain to compose Panel URL later
        read -rp "请输入证书签发的域名: " custom_domain
        custom_domain="${custom_domain// /}" # Remove spaces

        # 3.2 Loop for Certificate Path
        while true; do
            read -rp "请输入证书文件路径（如 fullchain.pem）: " custom_cert
            # Strip quotes if present
            custom_cert=$(echo "$custom_cert" | tr -d '"' | tr -d "'")

            if [[ -f "$custom_cert" && -r "$custom_cert" && -s "$custom_cert" ]]; then
                break
            elif [[ ! -f "$custom_cert" ]]; then
                echo -e "${red}错误：文件不存在！请重试。${plain}"
            elif [[ ! -r "$custom_cert" ]]; then
                echo -e "${red}错误：文件存在但不可读（请检查权限）！${plain}"
            else
                echo -e "${red}错误：文件为空！${plain}"
            fi
        done

        # 3.3 Loop for Private Key Path
        while true; do
            read -rp "请输入私钥文件路径（如 privkey.pem）: " custom_key
            # Strip quotes if present
            custom_key=$(echo "$custom_key" | tr -d '"' | tr -d "'")

            if [[ -f "$custom_key" && -r "$custom_key" && -s "$custom_key" ]]; then
                break
            elif [[ ! -f "$custom_key" ]]; then
                echo -e "${red}错误：文件不存在！请重试。${plain}"
            elif [[ ! -r "$custom_key" ]]; then
                echo -e "${red}错误：文件存在但不可读（请检查权限）！${plain}"
            else
                echo -e "${red}错误：文件为空！${plain}"
            fi
        done

        # 3.4 Apply Settings via x-ui binary
        ${xui_folder}/x-ui cert -webCert "$custom_cert" -webCertKey "$custom_key" >/dev/null 2>&1
        
        # Set SSL_HOST for composing Panel URL
        if [[ -n "$custom_domain" ]]; then
            SSL_HOST="$custom_domain"
        else
            SSL_HOST="${server_ip}"
        fi

        echo -e "${green}✓ 自定义证书路径已应用。${plain}"
        echo -e "${yellow}注意：您需要自行负责这些证书文件的续期。${plain}"

        systemctl restart x-ui >/dev/null 2>&1 || rc-service x-ui restart >/dev/null 2>&1
        ;;
    *)
        echo -e "${red}无效选项，跳过 SSL 设置。${plain}"
        SSL_HOST="${server_ip}"
        ;;
    esac
}

config_after_install() {
    local existing_hasDefaultCredential=$(${xui_folder}/x-ui setting -show true | grep -Eo 'hasDefaultCredential: .+' | awk '{print $2}')
    local existing_webBasePath=$(${xui_folder}/x-ui setting -show true | grep -Eo 'webBasePath: .+' | awk '{print $2}' | sed 's#^/##')
    local existing_port=$(${xui_folder}/x-ui setting -show true | grep -Eo 'port: .+' | awk '{print $2}')
    # Properly detect empty cert by checking if cert: line exists and has content after it
    local existing_cert=$(${xui_folder}/x-ui setting -getCert true | grep 'cert:' | awk -F': ' '{print $2}' | tr -d '[:space:]')
    local URL_lists=(
        "https://api4.ipify.org"
        "https://ipv4.icanhazip.com"
        "https://v4.api.ipinfo.io/ip"
        "https://ipv4.myexternalip.com/raw"
        "https://4.ident.me"
        "https://check-host.net/ip"
    )
    local server_ip=""
    for ip_address in "${URL_lists[@]}"; do
        local response=$(_curl -s -w "\n%{http_code}" --max-time 3 "${ip_address}" 2>/dev/null)
        local http_code=$(echo "$response" | tail -n1)
        local ip_result=$(echo "$response" | head -n-1 | tr -d '[:space:]')
        if [[ "${http_code}" == "200" && -n "${ip_result}" ]]; then
            server_ip="${ip_result}"
            break
        fi
    done
    
    if [[ ${#existing_webBasePath} -lt 4 ]]; then
        if [[ "$existing_hasDefaultCredential" == "true" ]]; then
            local config_webBasePath=""
            local config_username=""
            local config_password=""
            
            read -rp "请设置面板用户名（默认 admin）: " config_username
            config_username=${config_username:-admin}
            read -rp "请设置面板密码（默认 admin）: " config_password
            config_password=${config_password:-admin}
            read -rp "是否自定义面板端口？（否则将自动生成随机端口）[y/n]: " config_confirm
            if [[ "${config_confirm}" == "y" || "${config_confirm}" == "Y" ]]; then
                read -rp "请设置面板端口: " config_port
                echo -e "${yellow}面板端口: ${config_port}${plain}"
            else
                local config_port=$(shuf -i 1024-62000 -n 1)
                echo -e "${yellow}已生成随机端口: ${config_port}${plain}"
            fi
            read -rp "请设置面板访问路径（留空自动生成随机路径）: " config_webBasePath
            if [[ -z "$config_webBasePath" ]]; then
                config_webBasePath=$(gen_random_string 18)
                echo -e "${yellow}已生成随机访问路径: ${config_webBasePath}${plain}"
            else
                echo -e "${yellow}面板访问路径: ${config_webBasePath}${plain}"
            fi
            
            ${xui_folder}/x-ui setting -username "${config_username}" -password "${config_password}" -port "${config_port}" -webBasePath "${config_webBasePath}"
            
            echo ""
            echo -e "${green}═══════════════════════════════════════════${plain}"
            echo -e "${green}        SSL 证书设置（必选）              ${plain}"
            echo -e "${green}═══════════════════════════════════════════${plain}"
            echo -e "${yellow}为确保安全，所有面板都需要 SSL 证书。${plain}"
            echo -e "${yellow}Let's Encrypt 现已支持域名和 IP 地址！${plain}"
            echo ""

            prompt_and_setup_ssl "${config_port}" "${config_webBasePath}" "${server_ip}"
            
            # Display final credentials and access information
            echo ""
            echo -e "${green}═══════════════════════════════════════════${plain}"
            echo -e "${green}       面板安装完成！                    ${plain}"
            echo -e "${green}═══════════════════════════════════════════${plain}"
            echo -e "${green}用户名:      ${config_username}${plain}"
            echo -e "${green}密码:        ${config_password}${plain}"
            echo -e "${green}端口:        ${config_port}${plain}"
            echo -e "${green}WebBasePath: ${config_webBasePath}${plain}"
            echo -e "${green}访问地址:    https://${SSL_HOST}:${config_port}/${config_webBasePath}${plain}"
            echo -e "${green}═══════════════════════════════════════════${plain}"
            echo -e "${yellow}⚠ 重要：请妥善保存以上凭据！${plain}"
            echo -e "${yellow}⚠ SSL 证书：已启用并配置${plain}"
        else
            local config_webBasePath=$(gen_random_string 18)
            echo -e "${yellow}WebBasePath 缺失或太短，正在生成新的...${plain}"
            ${xui_folder}/x-ui setting -webBasePath "${config_webBasePath}"
            echo -e "${green}新的 WebBasePath: ${config_webBasePath}${plain}"

            # If the panel is already installed but no certificate is configured, prompt for SSL now
            if [[ -z "${existing_cert}" ]]; then
                echo ""
                echo -e "${green}═══════════════════════════════════════════${plain}"
                echo -e "${green}     SSL 证书设置（推荐）              ${plain}"
                echo -e "${green}═══════════════════════════════════════════${plain}"
                echo -e "${yellow}Let's Encrypt 现已支持域名和 IP 地址！${plain}"
                echo ""
                prompt_and_setup_ssl "${existing_port}" "${config_webBasePath}" "${server_ip}"
                echo -e "${green}访问地址:  https://${SSL_HOST}:${existing_port}/${config_webBasePath}${plain}"
            else
                # If a cert already exists, just show the access URL
                echo -e "${green}访问地址: https://${server_ip}:${existing_port}/${config_webBasePath}${plain}"
            fi
        fi
    else
        if [[ "$existing_hasDefaultCredential" == "true" ]]; then
            local config_username=$(gen_random_string 10)
            local config_password=$(gen_random_string 10)
            
            echo -e "${yellow}检测到默认凭据，出于安全考虑将更新...${plain}"
            ${xui_folder}/x-ui setting -username "${config_username}" -password "${config_password}"
            echo -e "已生成新的随机登录凭据："
            echo -e "###############################################"
            echo -e "${green}用户名: ${config_username}${plain}"
            echo -e "${green}密码:   ${config_password}${plain}"
            echo -e "###############################################"
        else
            echo -e "${green}用户名、密码和 WebBasePath 已正确设置。${plain}"
        fi

        # Existing install: if no cert configured, prompt user for SSL setup
        # Properly detect empty cert by checking if cert: line exists and has content after it
        existing_cert=$(${xui_folder}/x-ui setting -getCert true | grep 'cert:' | awk -F': ' '{print $2}' | tr -d '[:space:]')
        if [[ -z "$existing_cert" ]]; then
            echo ""
            echo -e "${green}═══════════════════════════════════════════${plain}"
            echo -e "${green}     SSL 证书设置（推荐）              ${plain}"
            echo -e "${green}═══════════════════════════════════════════${plain}"
            echo -e "${yellow}Let's Encrypt 现已支持域名和 IP 地址！${plain}"
            echo ""
            prompt_and_setup_ssl "${existing_port}" "${existing_webBasePath}" "${server_ip}"
            echo -e "${green}访问地址:  https://${SSL_HOST}:${existing_port}/${existing_webBasePath}${plain}"
        else
            echo -e "${green}SSL 证书已配置，无需操作。${plain}"
        fi
    fi
    
    ${xui_folder}/x-ui migrate
}

install_x-ui() {
    cd ${xui_folder%/x-ui}/
    
    # Download resources
    if [ $# == 0 ]; then
        tag_version=$(_curl -Ls "https://api.github.com/repos/xiasummer740/XX-UI/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')
        if [[ ! -n "$tag_version" ]]; then
            echo -e "${red}获取版本号失败，可能是 GitHub API 限制，请稍后重试${plain}"
            exit 1
        fi
        echo -e "获取到最新版本: ${tag_version}，开始安装..."
    else
        tag_version=$1
        tag_version_numeric=${tag_version#v}
        min_version="2.3.5"
        
        if [[ "$(printf '%s\n' "$min_version" "$tag_version_numeric" | sort -V | head -n1)" != "$min_version" ]]; then
            echo -e "${red}请使用更新的版本（至少 v2.3.5），退出安装。${plain}"
            exit 1
        fi
        echo -e "开始安装 xx-ui $1"
    fi

    # Build download URL with mirror support
    local download_url="${DOWNLOAD_BASE}/xiasummer740/XX-UI/releases/download/${tag_version}/x-ui-linux-$(arch).tar.gz"
    echo -e "${green}下载地址: ${download_url}${plain}"
    echo -e "${yellow}提示: 如果下载失败，可设置环境变量 XUI_DOWNLOAD_BASE 使用镜像${plain}"
    echo -e "${yellow}示例: export XUI_DOWNLOAD_BASE=https://ghproxy.net/https://github.com${plain}"
    
    _curl -fLRo ${xui_folder}-linux-$(arch).tar.gz "${download_url}"
    if [[ $? -ne 0 ]]; then
        echo -e "${red}下载 x-ui 失败，请确保服务器可以访问 GitHub${plain}"
        echo -e "${yellow}您可以尝试设置镜像源后重试:${plain}"
        echo -e "${yellow}  export XUI_DOWNLOAD_BASE=https://ghproxy.net/https://github.com${plain}"
        echo -e "${yellow}  export XUI_RAW_BASE=https://ghproxy.net/https://raw.githubusercontent.com${plain}"
        exit 1
    fi
    
    _curl -fLRo /usr/bin/x-ui-temp "${RAW_BASE}/xiasummer740/XX-UI/main/x-ui.sh"
    if [[ $? -ne 0 ]]; then
        echo -e "${red}下载 x-ui.sh 失败${plain}"
        exit 1
    fi
    
    # Stop x-ui service and remove old resources
    if [[ -e ${xui_folder}/ ]]; then
        if [[ $release == "alpine" ]]; then
            rc-service x-ui stop
        else
            systemctl stop x-ui
        fi
        rm ${xui_folder}/ -rf
    fi
    
    # Extract resources and set permissions
    tar zxvf x-ui-linux-$(arch).tar.gz
    rm x-ui-linux-$(arch).tar.gz -f
    
    cd x-ui
    chmod +x x-ui
    chmod +x x-ui.sh
    
    # Check the system's architecture and rename the file accordingly
    if [[ $(arch) == "armv5" || $(arch) == "armv6" || $(arch) == "armv7" ]]; then
        mv bin/xray-linux-$(arch) bin/xray-linux-arm
        chmod +x bin/xray-linux-arm
    fi
    chmod +x x-ui bin/xray-linux-$(arch)
    
    # Update x-ui cli and se set permission
    mv -f /usr/bin/x-ui-temp /usr/bin/x-ui
    chmod +x /usr/bin/x-ui
    mkdir -p /var/log/x-ui
    config_after_install

    # Etckeeper compatibility
    if [ -d "/etc/.git" ]; then
        if [ -f "/etc/.gitignore" ]; then
            if ! grep -q "x-ui/x-ui.db" "/etc/.gitignore"; then
                echo "" >> "/etc/.gitignore"
                echo "x-ui/x-ui.db" >> "/etc/.gitignore"
                echo -e "${green}已将 x-ui.db 添加到 /etc/.gitignore（etckeeper 兼容性）${plain}"
            fi
        else
            echo "x-ui/x-ui.db" > "/etc/.gitignore"
            echo -e "${green}已创建 /etc/.gitignore 并添加 x-ui.db（etckeeper 兼容性）${plain}"
        fi
    fi
    
    if [[ $release == "alpine" ]]; then
        _curl -fLRo /etc/init.d/x-ui https://raw.githubusercontent.com/xiasummer740/XX-UI/main/x-ui.rc
        if [[ $? -ne 0 ]]; then
            echo -e "${red}下载 x-ui.rc 失败${plain}"
            exit 1
        fi
        chmod +x /etc/init.d/x-ui
        rc-update add x-ui
        rc-service x-ui start
    else
        # Install systemd service file
        service_installed=false
        
        if [ -f "x-ui.service" ]; then
            echo -e "${green}在解压文件中找到 x-ui.service，正在安装...${plain}"
            cp -f x-ui.service ${xui_service}/ >/dev/null 2>&1
            if [[ $? -eq 0 ]]; then
                service_installed=true
            fi
        fi
        
        if [ "$service_installed" = false ]; then
            case "${release}" in
                ubuntu | debian | armbian)
                    if [ -f "x-ui.service.debian" ]; then
                        echo -e "${green}在解压文件中找到 x-ui.service.debian，正在安装...${plain}"
                        cp -f x-ui.service.debian ${xui_service}/x-ui.service >/dev/null 2>&1
                        if [[ $? -eq 0 ]]; then
                            service_installed=true
                        fi
                    fi
                ;;
                arch | manjaro | parch)
                    if [ -f "x-ui.service.arch" ]; then
                        echo -e "${green}在解压文件中找到 x-ui.service.arch，正在安装...${plain}"
                        cp -f x-ui.service.arch ${xui_service}/x-ui.service >/dev/null 2>&1
                        if [[ $? -eq 0 ]]; then
                            service_installed=true
                        fi
                    fi
                ;;
                *)
                    if [ -f "x-ui.service.rhel" ]; then
                        echo -e "${green}在解压文件中找到 x-ui.service.rhel，正在安装...${plain}"
                        cp -f x-ui.service.rhel ${xui_service}/x-ui.service >/dev/null 2>&1
                        if [[ $? -eq 0 ]]; then
                            service_installed=true
                        fi
                    fi
                ;;
            esac
        fi
        
        # If service file not found in tar.gz, download from GitHub
        if [ "$service_installed" = false ]; then
            echo -e "${yellow}在 tar.gz 中未找到服务文件，正在从 GitHub 下载...${plain}"
            case "${release}" in
                ubuntu | debian | armbian)
                    _curl -fLRo ${xui_service}/x-ui.service https://raw.githubusercontent.com/xiasummer740/XX-UI/main/x-ui.service.debian >/dev/null 2>&1
                ;;
                arch | manjaro | parch)
                    _curl -fLRo ${xui_service}/x-ui.service https://raw.githubusercontent.com/xiasummer740/XX-UI/main/x-ui.service.arch >/dev/null 2>&1
                ;;
                *)
                    _curl -fLRo ${xui_service}/x-ui.service https://raw.githubusercontent.com/xiasummer740/XX-UI/main/x-ui.service.rhel >/dev/null 2>&1
                ;;
            esac
            
            if [[ $? -ne 0 ]]; then
                echo -e "${red}从 GitHub 安装 x-ui.service 失败${plain}"
                exit 1
            fi
            service_installed=true
        fi
        
        if [ "$service_installed" = true ]; then
            echo -e "${green}正在设置 systemd 服务...${plain}"
            chown root:root ${xui_service}/x-ui.service >/dev/null 2>&1
            chmod 644 ${xui_service}/x-ui.service >/dev/null 2>&1
            systemctl daemon-reload
            systemctl enable x-ui
            systemctl start x-ui
        else
            echo -e "${red}安装 x-ui.service 文件失败${plain}"
            exit 1
        fi
    fi
    
    echo -e "${green}XX-UI ${tag_version}${plain} 安装完成，面板正在运行..."
    echo -e ""
    echo -e "┌───────────────────────────────────────────────────────┐"
    echo -e "│  ${blue}x-ui 管理命令帮助（子命令）:${plain}                    │"
    echo -e "│                                                       │"
    echo -e "│  ${blue}x-ui${plain}              - 打开管理脚本菜单            │"
    echo -e "│  ${blue}x-ui start${plain}        - 启动面板                   │"
    echo -e "│  ${blue}x-ui stop${plain}         - 停止面板                   │"
    echo -e "│  ${blue}x-ui restart${plain}      - 重启面板                   │"
    echo -e "│  ${blue}x-ui status${plain}       - 查看面板状态               │"
    echo -e "│  ${blue}x-ui settings${plain}     - 查看当前设置               │"
    echo -e "│  ${blue}x-ui enable${plain}       - 设置开机自启               │"
    echo -e "│  ${blue}x-ui disable${plain}      - 取消开机自启               │"
    echo -e "│  ${blue}x-ui log${plain}          - 查看面板日志               │"
    echo -e "│  ${blue}x-ui banlog${plain}       - 查看 Fail2ban 封禁日志     │"
    echo -e "│  ${blue}x-ui update${plain}       - 更新面板                   │"
    echo -e "│  ${blue}x-ui legacy${plain}       - 切换旧版                   │"
    echo -e "│  ${blue}x-ui install${plain}      - 安装面板                   │"
    echo -e "│  ${blue}x-ui uninstall${plain}    - 卸载面板                   │"
    echo -e "└───────────────────────────────────────────────────────┘"
    echo -e ""
    echo -e "${green}输入 x-ui 查看管理菜单页面${plain}"
}

echo -e "${green}正在运行...${plain}"
install_base
install_x-ui $1
