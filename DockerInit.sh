#!/bin/sh
# 任何一步失败都要让 docker build 失败：以前 unzip 缺失时脚本仍然返回 0，
# 结果构建出一个没有 xray 二进制的镜像，只有运行时才发现。
set -e

case ${1:-} in
    amd64)
        ARCH="64"
        FNAME="amd64"
        ;;
    i386)
        ARCH="32"
        FNAME="i386"
        ;;
    armv8 | arm64 | aarch64)
        ARCH="arm64-v8a"
        FNAME="arm64"
        ;;
    armv7 | arm | arm32)
        ARCH="arm32-v7a"
        FNAME="arm32"
        ;;
    armv6)
        ARCH="arm32-v6"
        FNAME="armv6"
        ;;
    *)
        ARCH="64"
        FNAME="amd64"
        ;;
esac
mkdir -p build/bin
cd build/bin
curl -sfLRO "https://github.com/XTLS/Xray-core/releases/download/v26.4.17/Xray-linux-${ARCH}.zip"
unzip "Xray-linux-${ARCH}.zip"
rm -f "Xray-linux-${ARCH}.zip" geoip.dat geosite.dat
mv xray "xray-linux-${FNAME}"
# Download geo files in parallel (faster initialization)
curl -sfLRO https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geoip.dat &
curl_pid1=$!
curl -sfLRO https://github.com/Loyalsoldier/v2ray-rules-dat/releases/latest/download/geosite.dat &
curl_pid2=$!
curl -sfLRo geoip_IR.dat https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/geoip.dat &
curl_pid3=$!
curl -sfLRo geosite_IR.dat https://github.com/chocolate4u/Iran-v2ray-rules/releases/latest/download/geosite.dat &
curl_pid4=$!
curl -sfLRo geoip_RU.dat https://github.com/runetfreedom/russia-v2ray-rules-dat/releases/latest/download/geoip.dat &
curl_pid5=$!
curl -sfLRo geosite_RU.dat https://github.com/runetfreedom/russia-v2ray-rules-dat/releases/latest/download/geosite.dat &
curl_pid6=$!
# Wait for all parallel downloads to complete
wait $curl_pid1 $curl_pid2 $curl_pid3 $curl_pid4 $curl_pid5 $curl_pid6

# 收尾检查：xray 与地理数据文件必须真的存在，否则镜像不可用
[ -x "xray-linux-${FNAME}" ] || { echo "xray-linux-${FNAME} missing after download" >&2; exit 1; }
for f in geoip.dat geosite.dat; do
    [ -s "$f" ] || { echo "$f missing after download" >&2; exit 1; }
done

cd ../../