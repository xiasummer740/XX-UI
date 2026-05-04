package middleware

import (
	"net/http"

	"github.com/XiaSummer740/XX-UI/database"
	"github.com/XiaSummer740/XX-UI/database/model"

	"github.com/gin-gonic/gin"
)

// ApiKeyAuth returns a Gin middleware that validates requests using an API key.
// The key is read from the X-API-Key header and compared against the stored setting.
func ApiKeyAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("X-API-Key")
		if key == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"msg":     "missing API key",
			})
			return
		}

		db := database.GetDB()
		setting := &model.Setting{}
		err := db.Model(model.Setting{}).Where("key = ?", "apiKey").First(setting).Error
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success": false,
				"msg":     "API key not configured",
			})
			return
		}

		if setting.Value == "" || setting.Value != key {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"success": false,
				"msg":     "invalid API key",
			})
			return
		}

		c.Next()
	}
}
