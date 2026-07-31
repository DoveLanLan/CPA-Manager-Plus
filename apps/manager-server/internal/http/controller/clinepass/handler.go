package clinepass

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/app"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/middleware"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/http/response"
	clinepasssvc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/clinepass"
)

const (
	basePath            = "/v0/management/third-party-quota/accounts"
	legacyBasePath      = "/v0/management/clinepass/accounts"
	maxAccountBodyBytes = 32 * 1024
)

type Handler struct {
	App *app.Context
}

func (h *Handler) Handle(w http.ResponseWriter, r *http.Request) {
	if !middleware.AuthorizePanel(w, r, h.App.AdminAuthService) {
		return
	}

	path := strings.TrimRight(r.URL.Path, "/")
	requestBasePath := basePath
	if path == legacyBasePath || strings.HasPrefix(path, legacyBasePath+"/") {
		requestBasePath = legacyBasePath
	}
	legacyRequest := requestBasePath == legacyBasePath
	if path == requestBasePath {
		h.handleCollection(w, r, legacyRequest)
		return
	}
	if !strings.HasPrefix(path, requestBasePath+"/") {
		response.MethodNotAllowed(w)
		return
	}

	tail := strings.TrimPrefix(path, requestBasePath+"/")
	if strings.HasSuffix(tail, "/usage-limits") {
		id := strings.TrimSuffix(tail, "/usage-limits")
		if id == "" || strings.Contains(id, "/") || r.Method != http.MethodGet {
			response.MethodNotAllowed(w)
			return
		}
		result, err := h.App.ThirdPartyQuotaService.FetchUsageLimits(r.Context(), id)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		response.JSON(w, http.StatusOK, result)
		return
	}

	if tail == "" || strings.Contains(tail, "/") {
		response.MethodNotAllowed(w)
		return
	}
	switch r.Method {
	case http.MethodPatch:
		var request clinepasssvc.UpdateAccountRequest
		var err error
		if legacyRequest {
			var legacy struct {
				Label     *string `json:"label,omitempty"`
				SessionID *string `json:"sessionId,omitempty"`
			}
			err = decodeJSON(w, r, &legacy)
			request.Label = legacy.Label
			request.Credential = legacy.SessionID
		} else {
			err = decodeJSON(w, r, &request)
		}
		if err != nil {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		account, err := h.App.ThirdPartyQuotaService.UpdateAccount(r.Context(), tail, request)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		response.JSON(w, http.StatusOK, account)
	case http.MethodDelete:
		if err := h.App.ThirdPartyQuotaService.DeleteAccount(r.Context(), tail); err != nil {
			writeServiceError(w, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		response.MethodNotAllowed(w)
	}
}

func (h *Handler) handleCollection(w http.ResponseWriter, r *http.Request, legacyRequest bool) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.App.ThirdPartyQuotaService.ListAccounts(r.Context())
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var request clinepasssvc.CreateAccountRequest
		var err error
		if legacyRequest {
			var legacy struct {
				Label     string `json:"label"`
				SessionID string `json:"sessionId"`
			}
			err = decodeJSON(w, r, &legacy)
			request = clinepasssvc.CreateAccountRequest{
				Provider:   clinepasssvc.ProviderClinePass,
				Label:      legacy.Label,
				Credential: legacy.SessionID,
			}
		} else {
			err = decodeJSON(w, r, &request)
		}
		if err != nil {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		account, err := h.App.ThirdPartyQuotaService.CreateAccount(r.Context(), request)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		response.JSON(w, http.StatusCreated, account)
	default:
		response.MethodNotAllowed(w)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	body := http.MaxBytesReader(w, r.Body, maxAccountBodyBytes)
	decoder := json.NewDecoder(body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("request body must contain one JSON object")
		}
		return err
	}
	return nil
}

func writeServiceError(w http.ResponseWriter, err error) {
	if errors.Is(err, clinepasssvc.ErrAccountNotFound) {
		response.JSON(w, http.StatusNotFound, map[string]any{
			"error": err.Error(),
			"code":  "third_party_quota_account_not_found",
		})
		return
	}
	var usageError *clinepasssvc.UsageError
	if errors.As(err, &usageError) {
		response.JSON(w, usageError.StatusCode, map[string]any{
			"error": usageError.Message,
			"code":  usageError.Code,
		})
		return
	}
	message := err.Error()
	if strings.Contains(message, "required") || strings.Contains(message, "at most") || strings.Contains(message, "invalid") {
		response.JSON(w, http.StatusBadRequest, map[string]any{
			"error": message,
			"code":  "third_party_quota_invalid_request",
		})
		return
	}
	response.Error(w, http.StatusInternalServerError, err)
}
