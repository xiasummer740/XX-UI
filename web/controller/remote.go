package controller

import (
	"net"
	"strconv"

	"github.com/XiaSummer740/XX-UI/database/model"
	"github.com/XiaSummer740/XX-UI/logger"
	"github.com/XiaSummer740/XX-UI/web/service"

	"github.com/gin-gonic/gin"
)

// RemoteController handles remote management API endpoints.
type RemoteController struct {
	BaseController
	inboundService service.InboundService
	xrayService    service.XrayService
}

// NewRemoteController creates a new RemoteController and sets up its routes.
func NewRemoteController(g *gin.RouterGroup) *RemoteController {
	a := &RemoteController{}
	a.initRouter(g)
	return a
}

func (a *RemoteController) initRouter(g *gin.RouterGroup) {
	g.GET("/inbounds", a.listInbounds)
	g.POST("/inbound/:id/client", a.createClient)
	g.GET("/client/:email", a.getClient)
	g.GET("/client/:email/connect", a.getConnectUrl)
	g.POST("/client/:email/traffic", a.setClientTraffic)
	g.POST("/client/:email/delete", a.deleteClient)
}

// listInbounds returns all inbounds with AllowRemote enabled.
func (a *RemoteController) listInbounds(c *gin.Context) {
	all, err := a.inboundService.GetAllInbounds()
	if err != nil {
		jsonMsg(c, "failed to list inbounds", err)
		return
	}
	result := make([]*model.Inbound, 0)
	for _, inbound := range all {
		if inbound.AllowRemote {
			result = append(result, inbound)
		}
	}
	jsonObj(c, result, nil)
}

// createClient adds a new client to the specified inbound.
func (a *RemoteController) createClient(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, "invalid inbound id", err)
		return
	}
	inbound, err := a.inboundService.GetInbound(id)
	if err != nil {
		jsonMsg(c, "inbound not found", err)
		return
	}
	if !inbound.AllowRemote {
		jsonMsg(c, "inbound not accessible", nil)
		return
	}
	data := &model.Inbound{}
	if err := c.ShouldBind(data); err != nil {
		jsonMsg(c, "invalid request", err)
		return
	}
	data.Id = id
	needRestart, err := a.inboundService.AddInboundClient(data)
	if err != nil {
		jsonMsg(c, "failed to create client", err)
		return
	}
	updated, err := a.inboundService.GetInbound(id)
	if err != nil {
		jsonObj(c, gin.H{"needRestart": needRestart}, nil)
		return
	}
	jsonObj(c, gin.H{"inbound": updated, "needRestart": needRestart}, nil)
	if needRestart {
		a.xrayService.SetToNeedRestart()
	}
}

// getClient returns traffic and expiry info for a client by email.
func (a *RemoteController) getClient(c *gin.Context) {
	email := c.Param("email")
	traffic, err := a.inboundService.GetClientTrafficByEmail(email)
	if err != nil {
		jsonMsg(c, "client not found", err)
		return
	}
	jsonObj(c, traffic, nil)
}

// setClientTraffic updates the traffic/expiry for a client by email.
func (a *RemoteController) setClientTraffic(c *gin.Context) {
	email := c.Param("email")
	type trafficUpdate struct {
		TotalGB    int64 `json:"totalGB" form:"totalGB"`
		ExpiryTime int64 `json:"expiryTime" form:"expiryTime"`
		Enable     *bool `json:"enable" form:"enable"`
	}
	var req trafficUpdate
	if err := c.ShouldBind(&req); err != nil {
		jsonMsg(c, "invalid request", err)
		return
	}
	if err := a.inboundService.UpdateClientTraffic(email, req.TotalGB, req.ExpiryTime, req.Enable); err != nil {
		jsonMsg(c, "failed to update client", err)
		return
	}
	logger.Infof("remote updated client %s: totalGB=%d expiry=%d", email, req.TotalGB, req.ExpiryTime)
	jsonMsg(c, "updated", nil)
}

// getConnectUrl returns the full connection URL including all reality/tls params.
func (a *RemoteController) getConnectUrl(c *gin.Context) {
	email := c.Param("email")
	host := c.Request.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	allInbounds, err := a.inboundService.GetAllInbounds()
	if err != nil {
		jsonMsg(c, "failed to find client", err)
		return
	}
	for _, inbound := range allInbounds {
		clients, _ := a.inboundService.GetClients(inbound)
		for _, cl := range clients {
			if cl.Email == email {
				url := a.inboundService.BuildClientConnectUrl(inbound, &cl, host)
				jsonObj(c, gin.H{"url": url, "remark": inbound.Remark, "email": email}, nil)
				return
			}
		}
	}
	jsonMsg(c, "client not found", nil)
}

// deleteClient removes a client by email from its parent inbound.
func (a *RemoteController) deleteClient(c *gin.Context) {
	email := c.Param("email")
	allInbounds, err := a.inboundService.GetAllInbounds()
	if err != nil {
		jsonMsg(c, "failed to find client", err)
		return
	}
	for _, inbound := range allInbounds {
		clients, _ := a.inboundService.GetClients(inbound)
		for _, cl := range clients {
			if cl.Email == email {
				_, err := a.inboundService.DelInboundClientByEmail(inbound.Id, email)
				if err != nil {
					jsonMsg(c, "failed to delete client", err)
					return
				}
				a.xrayService.SetToNeedRestart()
				logger.Infof("remote deleted client %s from inbound %d", email, inbound.Id)
				jsonMsg(c, "deleted", nil)
				return
			}
		}
	}
	jsonMsg(c, "client not found", nil)
}
