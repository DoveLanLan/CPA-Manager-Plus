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
	basePath            = "/v0/management/clinepass/accounts"
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
	if path == basePath {
		h.handleCollection(w, r)
		return
	}
	if !strings.HasPrefix(path, basePath+"/") {
		response.MethodNotAllowed(w)
		return
	}

	tail := strings.TrimPrefix(path, basePath+"/")
	if strings.HasSuffix(tail, "/usage-limits") {
		id := strings.TrimSuffix(tail, "/usage-limits")
		if id == "" || strings.Contains(id, "/") || r.Method != http.MethodGet {
			response.MethodNotAllowed(w)
			return
		}
		result, err := h.App.ClinePassService.FetchUsageLimits(r.Context(), id)
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
		if err := decodeJSON(w, r, &request); err != nil {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		account, err := h.App.ClinePassService.UpdateAccount(r.Context(), tail, request)
		if err != nil {
			writeServiceError(w, err)
			return
		}
		response.JSON(w, http.StatusOK, account)
	case http.MethodDelete:
		if err := h.App.ClinePassService.DeleteAccount(r.Context(), tail); err != nil {
			writeServiceError(w, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		response.MethodNotAllowed(w)
	}
}

func (h *Handler) handleCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		items, err := h.App.ClinePassService.ListAccounts(r.Context())
		if err != nil {
			response.Error(w, http.StatusInternalServerError, err)
			return
		}
		response.JSON(w, http.StatusOK, map[string]any{"items": items})
	case http.MethodPost:
		var request clinepasssvc.CreateAccountRequest
		if err := decodeJSON(w, r, &request); err != nil {
			response.Error(w, http.StatusBadRequest, err)
			return
		}
		account, err := h.App.ClinePassService.CreateAccount(r.Context(), request)
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
			"code":  "clinepass_account_not_found",
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
			"code":  "clinepass_invalid_request",
		})
		return
	}
	response.Error(w, http.StatusInternalServerError, err)
}
