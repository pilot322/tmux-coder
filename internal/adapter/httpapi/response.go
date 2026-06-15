package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/pilot322/tmux-coder/internal/domain"
	"github.com/pilot322/tmux-coder/internal/usecase"
)

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// writeUsecaseError maps domain/usecase errors to HTTP status codes.
func writeUsecaseError(w http.ResponseWriter, err error) {
	// A StateConflictError is an ErrConflict, so it must be matched before the
	// generic conflict branch to surface its machine-readable code (ADR-0009).
	var sce *usecase.StateConflictError
	if errors.As(err, &sce) {
		writeJSON(w, http.StatusConflict, errorResponse{Error: sce.Error(), Code: sce.Code})
		return
	}
	switch {
	case errors.Is(err, domain.ErrInvalidProjectTitle):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, usecase.ErrValidation):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, usecase.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, usecase.ErrNotImplemented):
		writeError(w, http.StatusNotImplemented, err.Error())
	case errors.Is(err, usecase.ErrProjectNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, usecase.ErrSessionNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, usecase.ErrAgentNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, usecase.ErrGateway):
		writeError(w, http.StatusBadGateway, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
