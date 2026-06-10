package router

import (
	"net/http"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	apikeyaliascontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/apikeyalias"
	codexinspectioncontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/codexinspection"
	dashboardcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/dashboard"
	healthcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/health"
	managerconfigcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/managerconfig"
	modelpricecontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/modelprice"
	monitoringcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/monitoring"
	panelcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/panel"
	proxycontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/proxy"
	setupcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/setup"
	systemcontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/system"
	usagecontroller "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/controller/usage"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	proxysvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/proxy"
)

func New(appCtx *app.Context) http.Handler {
	healthHandler := &healthcontroller.Handler{ServiceID: appCtx.ServiceID}
	systemHandler := &systemcontroller.Handler{App: appCtx}
	setupHandler := &setupcontroller.Handler{App: appCtx}
	managerConfigHandler := &managerconfigcontroller.Handler{App: appCtx}
	usageHandler := &usagecontroller.Handler{App: appCtx}
	modelPriceHandler := &modelpricecontroller.Handler{App: appCtx}
	apiKeyAliasHandler := &apikeyaliascontroller.Handler{App: appCtx}
	codexInspectionHandler := &codexinspectioncontroller.Handler{App: appCtx}
	dashboardHandler := &dashboardcontroller.Handler{App: appCtx}
	monitoringHandler := &monitoringcontroller.Handler{App: appCtx}
	proxyHandler := &proxycontroller.Handler{App: appCtx}
	panelHandler := &panelcontroller.Handler{App: appCtx}
	jsonHandler := func(next http.HandlerFunc) http.HandlerFunc {
		return middleware.WithGzipJSON(middleware.WithCORS(appCtx.Config, next))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", jsonHandler(healthHandler.Health))
	mux.HandleFunc("/status", jsonHandler(systemHandler.Status))
	mux.HandleFunc("/usage-service/info", jsonHandler(systemHandler.Info))
	mux.HandleFunc("/usage-service/config", jsonHandler(managerConfigHandler.Handle))
	mux.HandleFunc("/setup", jsonHandler(setupHandler.Setup))
	mux.HandleFunc("/management.html", panelHandler.ManagementHTML)
	mux.HandleFunc("/", rootHandler(appCtx, jsonHandler, usageHandler, modelPriceHandler, apiKeyAliasHandler, codexInspectionHandler, dashboardHandler, monitoringHandler, proxyHandler))

	return middleware.Recovery(middleware.RequestLogger(mux))
}

func rootHandler(
	appCtx *app.Context,
	jsonHandler func(http.HandlerFunc) http.HandlerFunc,
	usageHandler *usagecontroller.Handler,
	modelPriceHandler *modelpricecontroller.Handler,
	apiKeyAliasHandler *apikeyaliascontroller.Handler,
	codexInspectionHandler *codexinspectioncontroller.Handler,
	dashboardHandler *dashboardcontroller.Handler,
	monitoringHandler *monitoringcontroller.Handler,
	proxyHandler *proxycontroller.Handler,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			middleware.WriteCORS(appCtx.Config, w, r)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v0/management/model-prices") {
			jsonHandler(modelPriceHandler.Handle)(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v0/management/api-key-aliases") {
			jsonHandler(apiKeyAliasHandler.Handle)(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v0/management/codex-inspection") {
			jsonHandler(codexInspectionHandler.Handle)(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v0/management/dashboard/") {
			jsonHandler(dashboardHandler.Handle)(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v0/management/monitoring/") {
			jsonHandler(monitoringHandler.Handle)(w, r)
			return
		}
		cleanUsagePath := strings.TrimRight(r.URL.Path, "/")
		if cleanUsagePath == "/v0/management/usage" || strings.HasPrefix(cleanUsagePath, "/v0/management/usage/") {
			jsonHandler(usageHandler.Handle)(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/v0/management/") {
			middleware.WithCORS(appCtx.Config, proxyHandler.Management)(w, r)
			return
		}
		if proxysvc.IsModelListPath(r.URL.Path) {
			middleware.WithCORS(appCtx.Config, proxyHandler.ModelList)(w, r)
			return
		}
		if proxysvc.IsCPAProxyPath(r.URL.Path) {
			middleware.WithCORS(appCtx.Config, proxyHandler.CPA)(w, r)
			return
		}
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/management.html", http.StatusTemporaryRedirect)
			return
		}
		http.NotFound(w, r)
	}
}
