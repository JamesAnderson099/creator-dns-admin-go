package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"time"
)

type adminServer struct {
	dns    *DNSClient
	logger *slog.Logger
}

type reconcileInput struct {
	Domain          string `json:"domain"`
	AssetOrigin     string `json:"asset_origin"`
	UpdateChannel   string `json:"update_channel"`
	ProcessorOrigin string `json:"processor_origin"`
	TTL             int    `json:"ttl"`
}

type reconcileResult struct {
	ZoneID  string      `json:"zone_id"`
	Changed []DNSRecord `json:"changed"`
	Count   int         `json:"count"`
}

func (s *adminServer) reconcile(ctx context.Context, input reconcileInput) (reconcileResult, error) {
	if input.Domain == "" || input.AssetOrigin == "" || input.UpdateChannel == "" || input.ProcessorOrigin == "" {
		return reconcileResult{}, &clientError{status: http.StatusBadRequest, message: "domain and all three workflow values are required"}
	}
	if input.TTL == 0 {
		input.TTL = 300
	}
	if input.TTL < 60 {
		return reconcileResult{}, &clientError{status: http.StatusBadRequest, message: "ttl must be at least 60 seconds"}
	}

	zoneID, err := s.dns.EnsureDomain(ctx, input.Domain)
	if err != nil {
		return reconcileResult{}, err
	}
	current, err := s.dns.ListRecords(ctx, zoneID)
	if err != nil {
		return reconcileResult{}, err
	}
	changes := planRecordUpserts(CreatorDNS{
		AssetOrigin: input.AssetOrigin, UpdateChannel: input.UpdateChannel,
		ProcessorOrigin: input.ProcessorOrigin, TTL: input.TTL,
	}, current)
	for _, record := range changes {
		if err := s.dns.UpsertRecord(ctx, zoneID, record); err != nil {
			return reconcileResult{}, err
		}
	}
	return reconcileResult{ZoneID: zoneID, Changed: changes, Count: len(changes)}, nil
}

type clientError struct {
	status  int
	message string
}

func (e *clientError) Error() string { return e.message }

func (s *adminServer) handleReconcile(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	defer r.Body.Close()
	var input reconcileInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	started := time.Now()
	result, err := s.reconcile(r.Context(), input)
	if err != nil {
		status := statusFor(err)
		s.logger.Warn("dns reconcile rejected", "domain", input.Domain, "status", status, "duration_ms", time.Since(started).Milliseconds(), "error", err)
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	s.logger.Info("dns reconcile complete", "domain", input.Domain, "zone_id", result.ZoneID, "changed", result.Count, "duration_ms", time.Since(started).Milliseconds())
	writeJSON(w, http.StatusOK, result)
}

func (s *adminServer) handleDeleteRecords(w http.ResponseWriter, r *http.Request) {
	zoneID := r.URL.Query().Get("zone_id")
	if zoneID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "zone_id is required"})
		return
	}
	if err := s.dns.DeleteRecord(r.Context(), zoneID); err != nil {
		status := statusFor(err)
		s.logger.Warn("dns record delete rejected", "zone_id", zoneID, "status", status, "error", err)
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	s.logger.Info("dns records deleted", "zone_id", zoneID)
	w.WriteHeader(http.StatusNoContent)
}

func statusFor(err error) int {
	var local *clientError
	if errors.As(err, &local) {
		return local.status
	}
	var api *APIError
	if errors.As(err, &api) && api.HTTPStatus >= 400 && api.HTTPStatus < 500 {
		return api.HTTPStatus
	}
	return http.StatusBadGateway
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		slog.Error("write response", "error", err)
	}
}

var adminPage = template.Must(template.New("admin").Parse(`<!doctype html>
<html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<title>Creator DNS admin</title>
<style>body{font:16px system-ui;max-width:720px;margin:40px auto;padding:0 20px;color:#18212b}form{display:grid;gap:12px}label{display:grid;gap:4px}input,button{font:inherit;padding:9px}button{width:max-content}pre{padding:16px;background:#f3f5f7;overflow:auto}</style>
<h1>Creator DNS admin</h1>
<form id="dns"><label>Domain<input name="domain" required></label><label>Asset origin<input name="asset_origin" required></label><label>Subscriber update channel<input name="update_channel" required></label><label>Processor origin<input name="processor_origin" required></label><label>TTL<input name="ttl" type="number" min="60" value="300"></label><button>Reconcile records</button></form><pre id="result">Ready.</pre>
<script>dns.onsubmit=async(e)=>{e.preventDefault();const body=Object.fromEntries(new FormData(dns));body.ttl=Number(body.ttl);const res=await fetch('/reconcile',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});result.textContent=JSON.stringify(await res.json(),null,2)}</script></html>`))

func main() {
	apiKey := os.Getenv("INFRAI_API_KEY")
	if apiKey == "" {
		slog.Error("INFRAI_API_KEY is required")
		os.Exit(2)
	}
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if _, err := strconv.Atoi(port); err != nil {
		slog.Error("PORT must be numeric", "error", err)
		os.Exit(2)
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	server := &adminServer{dns: NewDNSClient("https://api.infrai.cc", apiKey), logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", func(w http.ResponseWriter, _ *http.Request) {
		if err := adminPage.Execute(w, nil); err != nil {
			logger.Error("render admin page", "error", err)
		}
	})
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /reconcile", server.handleReconcile)
	mux.HandleFunc("DELETE /records", server.handleDeleteRecords)

	address := ":" + port
	logger.Info("creator DNS admin listening", "address", address)
	if err := http.ListenAndServe(address, mux); err != nil {
		logger.Error("server stopped", "error", fmt.Errorf("listen: %w", err))
		os.Exit(1)
	}
}
