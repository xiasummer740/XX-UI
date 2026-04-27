#!/bin/bash
# ============================================================
#  XX-UI 版本号自动更新脚本
#  Auto Version Bump Script for XX-UI
# ============================================================
# 用法:
#   ./bump-version.sh              # 显示当前版本和使用帮助
#   ./bump-version.sh 2.10.0       # 更新到指定版本
#   ./bump-version.sh patch        # 自动递增补丁号 (2.9.2 -> 2.9.3)
#   ./bump-version.sh minor        # 自动递增次版本号 (2.9.2 -> 2.10.0)
#   ./bump-version.sh major        # 自动递增主版本号 (2.9.2 -> 3.0.0)
#   ./bump-version.sh push         # 推送代码和标签到 GitHub
# ============================================================

set -e

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

VERSION_FILE="config/version"
NAME_FILE="config/name"

if [ ! -f "$VERSION_FILE" ]; then
    echo -e "${RED}错误: 请在 XX-UI 项目根目录运行此脚本${NC}"
    echo -e "   当前目录: $(pwd)"
    exit 1
fi

CURRENT_VERSION=$(cat "$VERSION_FILE" | tr -d '[:space:]')
CURRENT_NAME=$(cat "$NAME_FILE" | tr -d '[:space:]')

echo -e "${BLUE}========================================${NC}"
echo -e "${BLUE}       XX-UI 版本管理工具               ${NC}"
echo -e "${BLUE}========================================${NC}"
echo ""
echo -e "  项目名称: ${GREEN}$CURRENT_NAME${NC}"
echo -e "  当前版本: ${YELLOW}v$CURRENT_VERSION${NC}"
echo ""

# ========================================
# PUSH 模式
# ========================================
if [ "$1" = "push" ]; then
    echo -e "${BLUE}-- 推送代码和标签到 GitHub --${NC}"
    echo ""

    REMOTE_URL=$(git remote get-url origin 2>/dev/null || echo "")
    if [ -z "$REMOTE_URL" ]; then
        echo -e "${RED}错误: 未配置远程仓库 (git remote origin)${NC}"
        exit 1
    fi
    echo -e "  远程仓库: ${GREEN}$REMOTE_URL${NC}"
    echo ""

    echo -e "  ${YELLOW}-> 推送 main 分支...${NC}"
    git push origin main
    echo -e "  ${GREEN}main 分支推送成功${NC}"
    echo ""

    echo -e "  ${YELLOW}-> 推送标签...${NC}"
    git push origin --tags
    echo -e "  ${GREEN}标签推送成功${NC}"
    echo ""

    LATEST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "")
    if [ -n "$LATEST_TAG" ]; then
        echo -e "  ${YELLOW}-> 在 GitHub 上创建 Release: $LATEST_TAG${NC}"

        RELEASE_NOTES="## XX-UI $LATEST_TAG\n\n"
        RELEASE_NOTES+="### 更新内容\n"
        RELEASE_NOTES+="- 请在此处填写本次更新的具体内容\n\n"
        RELEASE_NOTES+="### 一键安装\n"
        RELEASE_NOTES+="\`\`\`bash\n"
        RELEASE_NOTES+="bash <(curl -Ls https://raw.githubusercontent.com/XiaSummer740/XX-UI/main/install.sh)\n"
        RELEASE_NOTES+="\`\`\`\n\n"
        RELEASE_NOTES+="### 管理命令\n"
        RELEASE_NOTES+="\`\`\`bash\n"
        RELEASE_NOTES+="x-ui\n"
        RELEASE_NOTES+="\`\`\`\n"

        PAYLOAD='{"tag_name":"'$LATEST_TAG'","name":"XX-UI '$LATEST_TAG'","body":"'$(echo -e "$RELEASE_NOTES" | sed 's/"/\\"/g' | tr '\n' '\\n')'","draft":false,"prerelease":false}'

        if [ -n "$GITHUB_TOKEN" ]; then
            RESPONSE=$(curl -s -X POST \
                -H "Authorization: token $GITHUB_TOKEN" \
                -H "Accept: application/vnd.github.v3+json" \
                -d "$PAYLOAD" \
                "https://api.github.com/repos/XiaSummer740/XX-UI/releases" 2>/dev/null || echo "")

            if echo "$RESPONSE" | grep -q '"id"'; then
                echo -e "  ${GREEN}GitHub Release 创建成功: https://github.com/XiaSummer740/XX-UI/releases/tag/$LATEST_TAG${NC}"
            else
                echo -e "  ${YELLOW}GitHub Release 需要在网页端手动创建:${NC}"
                echo -e "     https://github.com/XiaSummer740/XX-UI/releases/new?tag=$LATEST_TAG"
                echo -e "  ${YELLOW}或设置 GITHUB_TOKEN 环境变量后重试${NC}"
            fi
        else
            echo -e "  ${YELLOW}未设置 GITHUB_TOKEN 环境变量${NC}"
            echo -e "    请手动创建 Release:"
            echo -e "     https://github.com/XiaSummer740/XX-UI/releases/new?tag=$LATEST_TAG"
            echo ""
            echo -e "  设置 Token 后可自动创建:"
            echo -e "    ${GREEN}export GITHUB_TOKEN=\"你的GitHubToken\"${NC}"
            echo -e "    ${GREEN}./bump-version.sh push${NC}"
        fi
    fi

    echo ""
    echo -e "${GREEN}推送完成${NC}"
    exit 0
fi

# ========================================
# 显示帮助（无参数）
# ========================================
if [ $# -eq 0 ]; then
    PATCH_NEW=$(echo $CURRENT_VERSION | awk -F. '{print $1"."$2"."$3+1}')
    MINOR_NEW=$(echo $CURRENT_VERSION | awk -F. '{print $1"."$2+1".0"}')
    MAJOR_NEW=$(echo $CURRENT_VERSION | awk -F. '{print $1+1".0.0"}')
    echo -e "  使用说明:"
    echo -e "    ${GREEN}./bump-version.sh <version>${NC}  设置指定版本号 (如 2.10.0)"
    echo -e "    ${GREEN}./bump-version.sh patch${NC}      递增补丁号 (当前 -> $PATCH_NEW)"
    echo -e "    ${GREEN}./bump-version.sh minor${NC}      递增次版本号 (当前 -> $MINOR_NEW)"
    echo -e "    ${GREEN}./bump-version.sh major${NC}      递增主版本号 (当前 -> $MAJOR_NEW)"
    echo -e "    ${GREEN}./bump-version.sh push${NC}       推送代码和标签到 GitHub，自动创建 Release"
    echo ""
    exit 0
fi

# ========================================
# 版本更新模式
# ========================================
NEW_VERSION=""
case $1 in
    patch)
        NEW_VERSION=$(echo $CURRENT_VERSION | awk -F. '{print $1"."$2"."$3+1}')
        CHANGE_TYPE="补丁更新 (Patch)"
        ;;
    minor)
        NEW_VERSION=$(echo $CURRENT_VERSION | awk -F. '{print $1"."$2+1".0"}')
        CHANGE_TYPE="次版本更新 (Minor)"
        ;;
    major)
        NEW_VERSION=$(echo $CURRENT_VERSION | awk -F. '{print $1+1".0.0"}')
        CHANGE_TYPE="主版本更新 (Major)"
        ;;
    *)
        if echo "$1" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
            NEW_VERSION="$1"
            CHANGE_TYPE="指定版本更新"
        else
            echo -e "${RED}错误: 未知参数或版本号格式无效${NC}"
            echo -e "   正确格式: ${GREEN}主版本.次版本.补丁${NC} (如 2.9.3)"
            echo -e "   或者使用: ${GREEN}patch | minor | major | push${NC}"
            exit 1
        fi
        ;;
esac

echo -e "  变更类型: ${BLUE}$CHANGE_TYPE${NC}"
echo -e "  旧版本:   ${YELLOW}v$CURRENT_VERSION${NC}"
echo -e "  新版本:   ${GREEN}v$NEW_VERSION${NC}"
echo ""

GIT_REPO=false
if git rev-parse --git-dir > /dev/null 2>&1; then
    GIT_REPO=true
    echo -e "  ${GREEN}Git 仓库已检测到${NC}"
else
    echo -e "  ${YELLOW}未检测到 Git 仓库，跳过 Git 操作${NC}"
fi

echo ""

# 写入新版本
echo -n "$NEW_VERSION" > "$VERSION_FILE"
echo -e "  ${GREEN}版本文件已更新: config/version -> $NEW_VERSION${NC}"

# 更新 README 中的版本引用
for readme in README.md README.zh_CN.md README.ar_EG.md README.es_ES.md README.fa_IR.md README.ru_RU.md; do
    if [ -f "$readme" ]; then
        sed -i "s/v$CURRENT_VERSION/v$NEW_VERSION/g" "$readme" 2>/dev/null || true
    fi
done
echo -e "  ${GREEN}README 文件版本引用已更新${NC}"

# 更新 install.sh
if [ -f "install.sh" ]; then
    sed -i "s/$CURRENT_VERSION/$NEW_VERSION/g" "install.sh" 2>/dev/null || true
    echo -e "  ${GREEN}install.sh 版本引用已更新${NC}"
fi

echo ""

# Git 操作
if [ "$GIT_REPO" = true ]; then
    echo -e "${BLUE}-- Git 操作 --${NC}"

    if [ -n "$(git status --porcelain)" ]; then
        git add -A
        git commit -m "chore: bump version v$CURRENT_VERSION -> v$NEW_VERSION"
        echo -e "  ${GREEN}Git 提交成功: bump version v$CURRENT_VERSION -> v$NEW_VERSION${NC}"
    else
        echo -e "  ${YELLOW}没有需要提交的更改${NC}"
    fi

    TAG_NAME="v$NEW_VERSION"
    if git tag | grep -q "^$TAG_NAME$"; then
        echo -e "  ${YELLOW}标签 $TAG_NAME 已存在，跳过创建${NC}"
    else
        git tag -a "$TAG_NAME" -m "XX-UI v$NEW_VERSION"
        echo -e "  ${GREEN}Git 标签已创建: $TAG_NAME${NC}"
    fi

    echo ""
    echo -e "${YELLOW}-- 后续操作 --${NC}"
    echo -e "  推送代码和标签到 GitHub 并创建 Release:"
    echo -e "    ${GREEN}./bump-version.sh push${NC}"
fi

echo ""
echo -e "${GREEN}版本更新完成: v$CURRENT_VERSION -> v$NEW_VERSION${NC}"
