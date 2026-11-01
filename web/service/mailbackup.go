package service

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/XiaSummer740/XX-UI/config"
	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/logger"
)

// MailBackupService 把面板数据库压缩后发到指定邮箱。
// 目的很单纯：机器哪天真炸了，数据不在同一台机器上。
type MailBackupService struct {
	settingService SettingService
}

// MailBackupConfig 是发信需要的全部参数。
type MailBackupConfig struct {
	Enable   bool
	To       string
	Host     string
	Port     int
	User     string
	Pass     string
	From     string
	Insecure bool // 跳过证书校验（自建邮件服务器才需要）
}

func (s *MailBackupService) config() (MailBackupConfig, error) {
	cfg := MailBackupConfig{}
	if v, err := s.settingService.getBool("mailBackupEnable"); err == nil {
		cfg.Enable = v
	}
	cfg.To = strings.TrimSpace(s.str("mailBackupTo"))
	cfg.Host = strings.TrimSpace(s.str("mailSMTPHost"))
	cfg.User = strings.TrimSpace(s.str("mailSMTPUser"))
	cfg.Pass = s.str("mailSMTPPass")
	cfg.From = strings.TrimSpace(s.str("mailSMTPFrom"))
	if v, err := s.settingService.getBool("mailSMTPInsecure"); err == nil {
		cfg.Insecure = v
	}
	port, err := strconv.Atoi(strings.TrimSpace(s.str("mailSMTPPort")))
	if err != nil || port <= 0 || port > 65535 {
		port = 465
	}
	cfg.Port = port
	if cfg.From == "" {
		cfg.From = cfg.User
	}
	if cfg.Host == "" {
		return cfg, fmt.Errorf("还没填 SMTP 服务器地址")
	}
	if cfg.To == "" {
		return cfg, fmt.Errorf("还没填收件邮箱")
	}
	return cfg, nil
}

// SendBackupByEmail 立刻做一次快照并发送邮件，返回可读的结果说明。
func (s *MailBackupService) SendBackupByEmail() (string, error) {
	cfg, err := s.config()
	if err != nil {
		return "", err
	}
	cfg.Enable = true // 手动触发时不看开关

	data, filename, err := s.buildBackup()
	if err != nil {
		return "", err
	}

	now := time.Now()
	subject := fmt.Sprintf("【XX-UI】数据库备份 %s", now.Format("2006-01-02 15:04"))
	body := fmt.Sprintf(`这是 XX-UI 面板的数据库备份。

时间：%s
文件：%s
大小：%.1f KB
邮件：  发到 %s

恢复方法：
1. 登录面板 → 首页右上角「备份」→「导入数据库」→ 选这个压缩包解压出来的 .db 文件
   （也可以直接解压得到 .db，重命名为 XX-UI.db 覆盖 /etc/x-ui/XX-UI.db，再重启面板）
2. 面板会自动重启，入站、客户端、设置全部回来

⚠️ 这个文件里含面板密码、各种密钥，请勿转发或放到公开位置。
`, now.Format("2006-01-02 15:04:05"), filename, float64(len(data))/1024, cfg.To)

	if err := sendMailWithAttachment(cfg, subject, body, filename, data); err != nil {
		return "", err
	}
	return fmt.Sprintf("已发送到 %s（%s，%.1f KB）", cfg.To, filename, float64(len(data))/1024), nil
}

// buildBackup 用 SQLite 的 VACUUM INTO 做一致性快照，再 gzip 压缩。
func (s *MailBackupService) buildBackup() ([]byte, string, error) {
	tmpDir, err := os.MkdirTemp("", "xxui-mailbackup-*")
	if err != nil {
		return nil, "", fmt.Errorf("创建临时目录失败: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	snapshot := filepath.Join(tmpDir, "x-ui.db")
	if err := database.SnapshotTo(snapshot); err != nil {
		return nil, "", fmt.Errorf("数据库快照失败: %w", err)
	}

	raw, err := os.ReadFile(snapshot)
	if err != nil {
		return nil, "", fmt.Errorf("读取快照失败: %w", err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(raw); err != nil {
		gz.Close()
		return nil, "", fmt.Errorf("压缩失败: %w", err)
	}
	if err := gz.Close(); err != nil {
		return nil, "", fmt.Errorf("压缩失败: %w", err)
	}

	filename := fmt.Sprintf("xxui-backup-%s.db.gz", time.Now().Format("20060102-150405"))
	return buf.Bytes(), filename, nil
}

// 每次发送都用当天的备份文件挂在邮件里；失败会写日志（调用方决定要不要告警）。
func sendMailWithAttachment(cfg MailBackupConfig, subject, body, filename string, attachment []byte) error {
	msg := buildMailMessage(cfg, subject, body, filename, attachment)
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))

	tlsCfg := &tls.Config{ServerName: cfg.Host, InsecureSkipVerify: cfg.Insecure} //nolint:gosec

	var client *smtp.Client
	if cfg.Port == 465 {
		conn, err := tls.Dial("tcp", addr, tlsCfg)
		if err != nil {
			return fmt.Errorf("连接 %s 失败: %w", addr, err)
		}
		client, err = smtp.NewClient(conn, cfg.Host)
		if err != nil {
			return fmt.Errorf("建立 SMTP 会话失败: %w", err)
		}
	} else {
		c, err := smtp.Dial(addr)
		if err != nil {
			return fmt.Errorf("连接 %s 失败: %w", addr, err)
		}
		if ok, _ := c.Extension("STARTTLS"); ok {
			if err := c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("STARTTLS 失败: %w", err)
			}
		}
		client = c
	}
	defer client.Close()

	if cfg.User != "" {
		auth := smtp.PlainAuth("", cfg.User, cfg.Pass, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("登录 SMTP 失败（账号或授权码不对）: %w", err)
		}
	}
	if err := client.Mail(cfg.From); err != nil {
		return fmt.Errorf("发件人被拒绝（%s）: %w", cfg.From, err)
	}
	if err := client.Rcpt(cfg.To); err != nil {
		return fmt.Errorf("收件人被拒绝（%s）: %w", cfg.To, err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("开始写邮件失败: %w", err)
	}
	if _, err := w.Write(msg); err != nil {
		w.Close()
		return fmt.Errorf("写入邮件内容失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("提交邮件失败: %w", err)
	}
	return client.Quit()
}

func buildMailMessage(cfg MailBackupConfig, subject, body, filename string, attachment []byte) []byte {
	var b bytes.Buffer
	boundary := fmt.Sprintf("xxui-%d", time.Now().UnixNano())
	b.WriteString("From: " + cfg.From + "\r\n")
	b.WriteString("To: " + cfg.To + "\r\n")
	b.WriteString("Subject: " + mime.BEncoding.Encode("UTF-8", subject) + "\r\n")
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Date: " + time.Now().Format(time.RFC1123Z) + "\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=\"" + boundary + "\"\r\n\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	b.WriteString(wrapBase64([]byte(body)))
	b.WriteString("\r\n")

	b.WriteString("--" + boundary + "\r\n")
	b.WriteString("Content-Type: application/gzip; name=\"" + filename + "\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n")
	b.WriteString("Content-Disposition: attachment; filename=\"" + filename + "\"\r\n\r\n")
	b.WriteString(wrapBase64(attachment))
	b.WriteString("\r\n--" + boundary + "--\r\n")
	return b.Bytes()
}

// wrapBase64 每 76 字符换行，符合 MIME 要求。
func wrapBase64(data []byte) string {
	enc := base64.StdEncoding.EncodeToString(data)
	var sb strings.Builder
	for i := 0; i < len(enc); i += 76 {
		end := i + 76
		if end > len(enc) {
			end = len(enc)
		}
		sb.WriteString(enc[i:end])
		sb.WriteString("\r\n")
	}
	return sb.String()
}

// DescribeTarget 给日志用：只输出主机和收件人，绝不输出密码。
func (c MailBackupConfig) DescribeTarget() string {
	return fmt.Sprintf("%s:%d -> %s", c.Host, c.Port, c.To)
}

var _ = config.GetDBFolderPath
var _ = logger.Info

// str 读一个设置值，读不到就当空串（备份配置缺失时不用报错）。
func (s *MailBackupService) str(key string) string {
	v, err := s.settingService.getString(key)
	if err != nil {
		return ""
	}
	return v
}

// IsEnabled 是否开启了每日邮箱备份。
func (s *MailBackupService) IsEnabled() bool {
	v, err := s.settingService.getBool("mailBackupEnable")
	return err == nil && v
}

// TestConfig 只校验配置是否填全（点「测试发送」前先给个明确提示）。
func (s *MailBackupService) TestConfig() (string, error) {
	cfg, err := s.config()
	if err != nil {
		return "", err
	}
	return cfg.DescribeTarget(), nil
}
