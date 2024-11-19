package server

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/lima-vm/lima/pkg/hostagent"
	"github.com/lima-vm/lima/pkg/httputil"
)

type Backend struct {
	Agent *hostagent.HostAgent
}

func (b *Backend) onError(w http.ResponseWriter, err error, ec int) {
	w.WriteHeader(ec)
	w.Header().Set("Content-Type", "application/json")
	// err may potentially contain credential info (in a future version),
	// but it is safe to return the err to the client, because we do not expose the socket to the internet
	e := httputil.ErrorJSON{
		Message: err.Error(),
	}
	_ = json.NewEncoder(w).Encode(e)
}

// GetInfo is the handler for GET /v1/info.
func (b *Backend) GetInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	info, err := b.Agent.Info(ctx)
	if err != nil {
		b.onError(w, err, http.StatusInternalServerError)
		return
	}
	m, err := json.Marshal(info)
	if err != nil {
		b.onError(w, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(m)
}

// DriverConfig is the handler for GET /v1/driver/config and PATCH /v1/driver/config.
func (b *Backend) DriverConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var config interface{}
	if r.Method == http.MethodPatch {
		defer r.Body.Close()
		if err := json.NewDecoder(r.Body).Decode(&config); err != nil {
			b.onError(w, err, http.StatusBadRequest)
			return
		}
	}
	config, err := b.Agent.DriverRuntimeConfig(ctx, config)
	if err != nil {
		b.onError(w, err, http.StatusInternalServerError)
		return
	}
	m, err := json.Marshal(config)
	if err != nil {
		b.onError(w, err, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(m)
}

func AddRoutes(r *http.ServeMux, b *Backend) {
	r.Handle("/v1/info", http.HandlerFunc(b.GetInfo))
	r.Handle("GET /v1/driver/config", http.HandlerFunc(b.DriverConfig))
	r.Handle("PATCH /v1/driver/config", http.HandlerFunc(b.DriverConfig))
}
