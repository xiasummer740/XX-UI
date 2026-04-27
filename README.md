[English](/README.md) | [中文](/README.zh_CN.md)

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="./media/3x-ui-dark.png">
    <img alt="XX-UI" src="./media/3x-ui-light.png">
  </picture>
</p>

<h1 align="center">XX-UI — Lightweight Multi-Protocol Proxy Management Panel</h1>

<p align="center">
  <b>Deeply customized and optimized from 3X-UI — more beautiful, easier to use, more stable</b>
</p>

<p align="center">
  <a href="#-features">✨ Features</a> •
  <a href="#-quick-installation">🚀 Install</a> •
  <a href="#-management-commands">📖 Commands</a> •
  <a href="#-first-steps">🎯 Get Started</a> •
  <a href="#-faq">❓ FAQ</a>
</p>

---

## ✨ Features

| Feature | Description |
|---------|-------------|
| 🎨 **Beautiful UI** | Light blue-purple flowing gradient background + glassmorphism cards |
| 📦 **Multiple Protocols** | VLESS, VMess, Trojan, Shadowsocks, Hysteria2, WireGuard and more |
| 🌐 **Multi-User** | Supports multiple inbounds, clients, traffic stats, and expiration management |
| 🔄 **One-Click Update** | Update panel and Xray-core online |
| 📊 **Real-Time Monitor** | CPU, memory, traffic, online connections at a glance |
| 🌍 **Multi-Language** | English, Chinese, Persian, Arabic and more |
| 🔒 **Security** | TOTP two-factor authentication, SSL certificate support |
| 💾 **Auto Backup** | Telegram backup, local backup support |
| 📱 **Responsive** | Works perfectly on both desktop and mobile |

---

## 🚀 Quick Installation

### System Requirements

| Item | Requirement |
|------|-------------|
| OS | Ubuntu 20.04+ / Debian 11+ / CentOS 8+ |
| Architecture | x86_64 (amd64), ARM64, ARMv7 |
| RAM | ≥ 512MB |
| Disk | ≥ 1GB free space |
| User | **Must run as root** |

### One-Line Install

Copy and paste this command into your server's terminal:

```bash
bash <(curl -Ls https://raw.githubusercontent.com/XiaSummer740/XX-UI/main/install.sh)
```

> 💡 The installation is fully automatic. After completion, the panel's address, port, username, and password will be displayed — **please save them!**

### Access the Panel

Open your browser and visit:
```
http://your-server-ip:panel-port
```

---

## 📖 Management Commands

After installation, use the `x-ui` command to manage the panel:

| Command | Function | When to Use |
|---------|----------|-------------|
| `x-ui` | Show management menu | Not sure what command to use |
| `x-ui start` | Start the panel | Panel is not running |
| `x-ui stop` | Stop the panel | Need to do maintenance |
| `x-ui restart` | Restart the panel | After config changes |
| `x-ui status` | Check panel status | Check if panel is running |
| `x-ui enable` | Enable auto-start | Want panel to start on boot |
| `x-ui disable` | Disable auto-start | No longer need auto-start |
| `x-ui log` | View logs | Troubleshooting issues |
| `x-ui update` | Update panel | Upgrade to latest version |
| `x-ui install` | Reinstall panel | Panel is corrupted |

> 💡 If you get `x-ui: command not found`, run `source ~/.bashrc` first.

---

## 🎯 First Steps

### Step 1: Login

1. Open the address shown after installation in your browser
2. Enter username and password (default: `admin` / `admin`)
3. ⚠️ **Change the default password immediately after first login!**

### Step 2: Add an Inbound

1. Click "**Inbounds**" in the left menu
2. Click "**Add Inbound**" at the top right
3. Fill in the configuration:
   - **Protocol**: Select VLESS, VMess, Trojan, etc.
   - **Port**: Choose an unused port (e.g., 443, 8080, 8443)
   - **Clients**: Add clients with email and traffic limit
4. Click "**Add**" to save

### Step 3: Connect Clients

1. Find your inbound in the list
2. Click "**View Config**" or "**Share**"
3. Copy the config link and import it into v2rayN, Shadowrocket, Clash, etc.

---

## ❓ FAQ

<details>
<summary><b>❔ Can't access the panel after installation?</b></summary>

1. Check if panel is running: `x-ui status`
2. Check firewall:
   ```bash
   # Ubuntu/Debian
   ufw allow panel-port
   # CentOS/RHEL
   firewall-cmd --permanent --add-port=panel-port/tcp && firewall-cmd --reload
   ```
3. Cloud providers (AWS, Alibaba Cloud, etc.) need to open the port in security group
</details>

<details>
<summary><b>❔ Forgot the panel password?</b></summary>

Run this to reset:
```bash
x-ui reset
```
Password will be reset to `admin`
</details>

<details>
<summary><b>❔ How to update the panel?</b></summary>

```bash
x-ui update
```
Or one-command install:
```bash
bash <(curl -Ls https://raw.githubusercontent.com/XiaSummer740/XX-UI/main/install.sh)
```
</details>

<details>
<summary><b>❔ How to configure SSL?</b></summary>

Go to Panel Settings → Certificate Config:
- **Auto**: Use Let's Encrypt (requires domain pointing to your server)
- **Manual**: Upload certificate files
</details>

---

## ⚠️ Disclaimer

- This project is for personal learning and research only
- Do not use it for any illegal purposes
- Use at your own risk

---

## ⭐ Support

If this project helps you, please give it a Star ⭐!

---

## 📄 License

[GPL V3](LICENSE)

---

## 🔗 Links

- **GitHub**: https://github.com/xiasummer740/XX-UI
- **Issues**: [Report a problem](https://github.com/xiasummer740/XX-UI/issues)
