package web

import (
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSetupRoutesParsesTemplates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &Server{router: gin.New()}

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("setupRoutes paniği (şablon parse hatası olabilir): %v", r)
		}
	}()

	s.setupRoutes()

	if s.router.HTMLRender == nil {
		t.Fatal("HTML render ayarlanmadı")
	}
}
