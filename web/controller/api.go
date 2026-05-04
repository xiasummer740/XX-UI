package controller

import (
	"net/http"

	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/web/middleware"
	"github.com/XiaSummer740/XX-UI/web/service"
	"github.com/XiaSummer740/XX-UI/web/session"

	"github.com/gin-gonic/gin"
)

// APIController handles the main API routes for the XX-UI panel, including inbounds and server management.
type APIController struct {
	BaseController
	inboundController *InboundController
	serverController  *ServerController
	Tgbot             service.Tgbot
}

// NewAPIController creates a new APIController instance and initializes its routes.
func NewAPIController(g *gin.RouterGroup, customGeo *service.CustomGeoService) *APIController {
	a := &APIController{}
	a.initRouter(g, customGeo)
	return a
}

// checkAPIAuth is a middleware that returns 404 for unauthenticated API requests
// to hide the existence of API endpoints from unauthorized users
func (a *APIController) checkAPIAuth(c *gin.Context) {
	if !session.IsLogin(c) {
		logger.Warning("[checkAPIAuth] Session not logged in, returning 404 for: " + c.Request.Method + " " + c.Request.RequestURI)
		c.AbortWithStatus(http.StatusNotFound)
		return
	}
	logger.Debug("[checkAPIAuth] Session valid for: " + c.Request.Method + " " + c.Request.RequestURI)
	c.Next()
}

// initRouter sets up the API routes for inbounds, server, and other endpoints.
func (a *APIController) initRouter(g *gin.RouterGroup, customGeo *service.CustomGeoService) {
	// Main API group
	api := g.Group("/panel/api")
	api.Use(a.checkAPIAuth)

	// Inbounds API
	inbounds := api.Group("/inbounds")
	a.inboundController = NewInboundController(inbounds)

	// Server API
	server := api.Group("/server")
	a.serverController = NewServerController(server)

	NewCustomGeoController(api.Group("/custom-geo"), customGeo)

	// Remote management API (uses API key auth, not session)
	remote := g.Group("/panel/remote")
	remote.Use(middleware.ApiKeyAuth())
	NewRemoteController(remote)

	// Extra routes
	api.GET("/backuptotgbot", a.BackuptoTgbot)
}

// BackuptoTgbot sends a backup of the panel data to Telegram bot admins.
func (a *APIController) BackuptoTgbot(c *gin.Context) {
	a.Tgbot.SendBackupToAdmins()
}
