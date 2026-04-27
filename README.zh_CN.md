[English](/README.md) | [中文](/README.zh_CN.md)

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="./media/3x-ui-dark.png">
    <img alt="XX-UI" src="./media/3x-ui-light.png">
  </picture>
</p>

<h1 align="center">XX-UI — 轻量级多协议代理管理面板</h1>

<p align="center">
  <b>基于 3X-UI 深度定制优化，更易用、更美观、更稳定</b>
</p>

<p align="center">
  <a href="#-功能特性">✨ 功能</a> •
  <a href="#-快速安装">🚀 安装</a> •
  <a href="#-面板管理命令">📖 命令</a> •
  <a href="#-首次使用">🎯 上手</a> •
  <a href="#-常见问题">❓ FAQ</a>
</p>

---

## ✨ 功能特性

| 特性 | 说明 |
|------|------|
| 🎨 **全新 UI** | 浅蓝紫色流光渐变背景 + 毛玻璃半透明卡片，颜值在线 |
| 📦 **协议齐全** | 支持 VLESS、VMess、Trojan、Shadowsocks、Hysteria2、WireGuard 等 |
| 🌐 **多用户管理** | 支持多用户、多入站、流量统计、到期管理 |
| 🔄 **一键更新** | 面板和内核均可在线更新 |
| 📊 **实时监控** | CPU、内存、流量、在线连接数一目了然 |
| 🌍 **多语言** | 支持中文、英文、波斯语、阿拉伯语等多国语言 |
| 🔒 **安全加固** | 支持 TOTP 两步验证、SSL 证书 |
| 💾 **自动备份** | 支持 Telegram 备份、本地备份 |
| 📱 **响应式** | 桌面端和移动端均可流畅使用 |

---

## 🚀 快速安装

### 📋 系统要求

| 项目 | 要求 |
|------|------|
| 操作系统 | Ubuntu 20.04+ / Debian 11+ / CentOS 8+ |
| 架构 | x86_64 (amd64)、ARM64、ARMv7 |
| 内存 | ≥ 512MB |
| 硬盘 | ≥ 1GB 可用空间 |
| 权限 | **必须使用 root 用户** |

### 一键安装命令

复制下面这行命令，在服务器的终端中粘贴并回车即可：

```bash
bash <(curl -Ls https://raw.githubusercontent.com/XiaSummer740/XX-UI/main/install.sh)
```

> 💡 **安装过程全自动**，安装完成后终端会显示面板的访问地址、端口、用户名和密码，请务必保存好！

### 安装完成后

安装完成后，在浏览器中打开面板地址：
```
http://你的服务器IP:面板端口
```

---

## 📖 面板管理命令

安装完成后，你可以使用 `x-ui` 命令来管理面板：

| 命令 | 功能 | 适合场景 |
|------|------|----------|
| `x-ui` | 显示管理菜单 | 不知道用啥命令时 |
| `x-ui start` | 启动面板 | 面板未运行时 |
| `x-ui stop` | 停止面板 | 需要维护时 |
| `x-ui restart` | 重启面板 | 配置更改后 |
| `x-ui status` | 查看面板状态 | 检查是否运行中 |
| `x-ui enable` | 设置开机自启 | 重启服务器后自动启动 |
| `x-ui disable` | 取消开机自启 | 不需要自动启动了 |
| `x-ui log` | 查看运行日志 | 出问题时排查 |
| `x-ui update` | 更新面板版本 | 升级到最新版 |
| `x-ui install` | 重新安装面板 | 面板损坏时 |

> 💡 如果提示 `x-ui: command not found`，请运行 `source ~/.bashrc` 后再试。

---

## 🎯 首次使用

### 第一步：登录面板

1. 打开浏览器，访问安装完成后显示的地址
2. 输入用户名和密码（默认：`admin` / `admin`）
3. ⚠️ **重要：首次登录后请立即修改默认密码！**

### 第二步：添加入站代理

1. 点击左侧菜单「**入站列表**」
2. 点击右上角「**添加入站**」
3. 填写配置：
   - **协议**：选择 VLESS、VMess、Trojan 等
   - **端口**：选择一个未被占用的端口（如 443、8080、8443 等）
   - **客户**：点击添加客户端，填写邮箱和流量限额
4. 点击「**添加**」保存

### 第三步：客户端连接

1. 在入站列表中，找到刚创建的入站
2. 点击右侧的「**查看配置**」或「**分享**」
3. 复制配置链接，导入到 v2rayN / Shadowrocket / Clash 等客户端即可

### 修改面板设置

1. 点击左侧菜单「**面板设置**」
2. 可修改：端口、用户名密码、WebSocket 路径、SSL 证书等
3. 修改后点击「**保存**」，面板会自动重启

---

## ❓ 常见问题

<details>
<summary><b>❔ 安装后无法访问面板？</b></summary>

1. 检查面板是否运行：`x-ui status`
2. 检查防火墙是否放行端口：
   ```bash
   # Ubuntu/Debian
   ufw allow 面板端口
   # CentOS/RHEL
   firewall-cmd --permanent --add-port=面板端口/tcp && firewall-cmd --reload
   ```
3. 云服务商（阿里云、腾讯云、AWS 等）需要在安全组中放行端口
</details>

<details>
<summary><b>❔ 忘记面板密码了？</b></summary>

运行以下命令重置：
```bash
x-ui reset
```
密码将重置为 `admin`
</details>

<details>
<summary><b>❔ 如何更新面板？</b></summary>

```bash
x-ui update
```
或一键命令：
```bash
bash <(curl -Ls https://raw.githubusercontent.com/XiaSummer740/XX-UI/main/install.sh)
```
</details>

<details>
<summary><b>❔ 如何配置 SSL 证书？</b></summary>

面板设置 → 证书配置，支持：
- **自动申请**：使用 Let's Encrypt 自动申请（需域名解析到服务器）
- **手动上传**：上传已有证书文件
</details>

<details>
<summary><b>❔ 面板端口被防火墙阻止了？</b></summary>

运行以下命令开放端口（将 54321 替换为你的面板端口）：
```bash
ufw allow 54321
```
</details>

---

## 📸 界面预览

| 系统状态 | 入站管理 |
|---------|---------|
| ![status](media/3x-ui-dark.png) | _(截图待补充)_ |

---

## ⚠️ 免责声明

- 本项目仅供个人学习与研究使用
- 请勿用于任何非法用途
- 使用本软件所产生的任何后果由使用者自行承担

---

## ⭐ 支持项目

如果这个项目对你有帮助，请给我们一个 Star ⭐，谢谢支持！

---

## 📄 许可证

本项目基于 [GPL V3](LICENSE) 许可证开源。

---

## 🔗 相关链接

- **GitHub 仓库**：https://github.com/xiasummer740/XX-UI
- **问题反馈**：[提交 Issue](https://github.com/xiasummer740/XX-UI/issues)
