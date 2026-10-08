package aiaccessroutes

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/dujiao-next/internal/app/container"
	aiapp "github.com/dujiao-next/internal/modules/aiaccess/application"
	aidomain "github.com/dujiao-next/internal/modules/aiaccess/domain"
	aistore "github.com/dujiao-next/internal/modules/aiaccess/infrastructure/gormstore"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestLegacyMachineAPIObeysGlobalPause(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:master_legacy_%d?mode=memory&cache=shared", time.Now().UnixNano())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqldb, _ := db.DB()
	sqldb.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqldb.Close() })
	if err = db.AutoMigrate(&aidomain.Key{}, &aidomain.Audit{}, &aidomain.RemoteConfig{}); err != nil {
		t.Fatal(err)
	}
	repo := aistore.New(db)
	keys := aiapp.New(repo)
	remote := aiapp.NewRemote(repo, keys)
	services := &container.Container{AiAccessService: keys, AiRemoteService: remote}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/health", func(c *gin.Context) { c.Status(http.StatusOK) })
	Register(router.Group("/api/v1"), services)
	request := func(path string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer invalid")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	endpoint := "/api/v1/ai/products"
	if result := request(endpoint); result.Code != http.StatusForbidden {
		t.Fatalf("legacy AI endpoint escaped OFF switch: HTTP %d", result.Code)
	}
	if result := request("/health"); result.Code != http.StatusOK {
		t.Fatal("human/public endpoint affected by AI pause")
	}
	if _, err = remote.SetControl(context.Background(), true, false, 1); err != nil {
		t.Fatal(err)
	}
	if result := request(endpoint); result.Code != http.StatusUnauthorized {
		t.Fatalf("AI endpoint not enabled for credential check: HTTP %d", result.Code)
	}
	if _, err = remote.SetControl(context.Background(), false, false, 1); err != nil {
		t.Fatal(err)
	}
	if result := request(endpoint); result.Code != http.StatusForbidden {
		t.Fatalf("legacy AI interface still open after pause: HTTP %d", result.Code)
	}
	if result := request("/health"); result.Code != http.StatusOK {
		t.Fatal("storefront/public routes broken by pause")
	}
}
