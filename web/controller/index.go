package controller

import (
	"fmt"
	"net/http"
	"strings"
	"text/template"
	"time"

	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/web/middleware"
	"github.com/XiaSummer740/XX-UI/web/service"
	"github.com/XiaSummer740/XX-UI/web/session"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
)

// Login throttling uses two guards: a strict per-(address, username) policy that
// stops password guessing against a known account, and a looser per-address
// policy that also covers username spraying from a single host. The limits leave
// room for a few honest typos while keeping guessing far too slow to succeed.
var (
	loginGuardUser = middleware.NewLoginGuard(10, 15*time.Minute)
	loginGuardIP   = middleware.NewLoginGuard(30, 15*time.Minute)
)

// LoginForm represents the login request structure.
type LoginForm struct {
	Username      string `json:"username" form:"username"`
	Password      string `json:"password" form:"password"`
	TwoFactorCode string `json:"twoFactorCode" form:"twoFactorCode"`
}

// IndexController handles the main index and login-related routes.
type IndexController struct {
	BaseController

	settingService service.SettingService
	userService    service.UserService
	tgbot          service.Tgbot
}

// NewIndexController creates a new IndexController and initializes its routes.
func NewIndexController(g *gin.RouterGroup) *IndexController {
	a := &IndexController{}
	a.initRouter(g)
	return a
}

// initRouter sets up the routes for index, login, logout, and two-factor authentication.
func (a *IndexController) initRouter(g *gin.RouterGroup) {
	g.GET("/", a.index)
	g.GET("/logout", a.logout)

	g.POST("/login", a.login)
	g.POST("/getTwoFactorEnable", a.getTwoFactorEnable)
}

// index handles the root route, redirecting logged-in users to the panel or showing the login page.
func (a *IndexController) index(c *gin.Context) {
	if session.IsLogin(c) {
		c.Redirect(http.StatusTemporaryRedirect, "panel/")
		return
	}
	html(c, "login.html", "pages.login.title", nil)
}

// login handles user authentication and session creation.
func (a *IndexController) login(c *gin.Context) {
	var form LoginForm

	if err := c.ShouldBind(&form); err != nil {
		pureJsonMsg(c, http.StatusOK, false, I18nWeb(c, "pages.login.toasts.invalidFormData"))
		return
	}
	if form.Username == "" {
		pureJsonMsg(c, http.StatusOK, false, I18nWeb(c, "pages.login.toasts.emptyUsername"))
		return
	}
	if form.Password == "" {
		pureJsonMsg(c, http.StatusOK, false, I18nWeb(c, "pages.login.toasts.emptyPassword"))
		return
	}

	// Throttle repeated failures before touching the database or bcrypt.
	// Logging uses the (forwarded, validated) client address; the throttle keys
	// use the TCP peer address, which cannot be forged: otherwise rotating
	// X-Real-IP would hand an attacker a fresh budget every request.
	remoteIP := getRemoteIp(c)
	throttleIP := getPeerIp(c)
	userKey := throttleIP + "|" + strings.ToLower(form.Username)
	ipKey := throttleIP + "|*"
	if locked, retryAfter := loginGuardUser.Locked(userKey); locked {
		logger.Warningf("login blocked for username %q from %s, retry after %s",
			template.HTMLEscapeString(form.Username), remoteIP, retryAfter.Round(time.Second))
		pureJsonMsg(c, http.StatusOK, false, I18nWeb(c, "pages.login.toasts.tooManyAttempts"))
		return
	}
	if locked, retryAfter := loginGuardIP.Locked(ipKey); locked {
		logger.Warningf("login blocked for address %s, retry after %s", remoteIP, retryAfter.Round(time.Second))
		pureJsonMsg(c, http.StatusOK, false, I18nWeb(c, "pages.login.toasts.tooManyAttempts"))
		return
	}

	user, checkErr := a.userService.CheckUser(form.Username, form.Password, form.TwoFactorCode)
	timeStr := time.Now().Format("2006-01-02 15:04:05")
	safeUser := template.HTMLEscapeString(form.Username)

	if user == nil {
		loginGuardUser.Fail(userKey)
		loginGuardIP.Fail(ipKey)

		// Never log or forward the attempted password: users frequently mistype a
		// real credential by one character, which would leak it permanently.
		logger.Warningf("login failed for username %q from %s", safeUser, remoteIP)

		notifyPass := ""
		if checkErr != nil && checkErr.Error() == "invalid 2fa code" {
			translatedError := a.tgbot.I18nBot("tgbot.messages.2faFailed")
			notifyPass = fmt.Sprintf("*** (%s)", translatedError)
		}

		a.tgbot.UserLoginNotify(safeUser, notifyPass, remoteIP, timeStr, 0)
		pureJsonMsg(c, http.StatusOK, false, I18nWeb(c, "pages.login.toasts.wrongUsernameOrPassword"))
		return
	}

	loginGuardUser.Reset(userKey)
	loginGuardIP.Reset(ipKey)

	logger.Infof("%s logged in successfully, Ip Address: %s\n", safeUser, remoteIP)
	a.tgbot.UserLoginNotify(safeUser, ``, remoteIP, timeStr, 1)

	session.SetLoginUser(c, user)
	if err := sessions.Default(c).Save(); err != nil {
		logger.Warning("Unable to save session: ", err)
		return
	}

	logger.Infof("%s logged in successfully", safeUser)
	jsonMsg(c, I18nWeb(c, "pages.login.toasts.successLogin"), nil)
}

// logout handles user logout by clearing the session and redirecting to the login page.
func (a *IndexController) logout(c *gin.Context) {
	user := session.GetLoginUser(c)
	if user != nil {
		logger.Infof("%s logged out successfully", user.Username)
	}
	session.ClearSession(c)
	if err := sessions.Default(c).Save(); err != nil {
		logger.Warning("Unable to save session after clearing:", err)
	}
	c.Redirect(http.StatusTemporaryRedirect, c.GetString("base_path"))
}

// getTwoFactorEnable retrieves the current status of two-factor authentication.
func (a *IndexController) getTwoFactorEnable(c *gin.Context) {
	status, err := a.settingService.GetTwoFactorEnable()
	if err == nil {
		jsonObj(c, status, nil)
	}
}
