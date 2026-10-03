package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	domainAddPath    = "/v1/dns/domain/add"
	domainGetPath    = "/v1/dns/domain/get"
	recordListPath   = "/v1/dns/record/list"
	recordUpsertPath = "/v1/dns/record/upsert"
	recordDeletePath = "/v1/dns/record/delete"
)

var errDomainNotFound = errors.New("domain not found")

type APIError struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus int    `json:"-"`
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Code + ": " + e.Message
	}
	return e.Code
}

type envelope struct {
	OK       bool            `json:"ok"`
	Data     json.RawMessage `json:"data"`
	Error    *APIError       `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

type DNSClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
	sleep   func(context.Context, time.Duration) error
}

func NewDNSClient(baseURL, apiKey string) *DNSClient {
	return &DNSClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 10 * time.Second},
		sleep: func(ctx context.Context, delay time.Duration) error {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		},
	}
}

func (c *DNSClient) request(ctx context.Context, method, path string, query url.Values, body any, idempotencyKey string, out any) error {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
	}

	for attempt := 0; attempt < 4; attempt++ {
		endpoint := c.baseURL + path
		if len(query) > 0 {
			endpoint += "?" + query.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
		if err != nil {
			return fmt.Errorf("create request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}

		response, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("send request: %w", err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, 1<<20))
		response.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read response: %w", readErr)
		}

		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("decode response envelope: %w", err)
		}
		if response.StatusCode == http.StatusTooManyRequests && attempt < 3 {
			delay := retryDelay(response.Header.Get("Retry-After"), attempt)
			if err := c.sleep(ctx, delay); err != nil {
				return err
			}
			continue
		}
		if !env.OK {
			if env.Error == nil {
				return errors.New("Infrai rejected the request")
			}
			env.Error.HTTPStatus = response.StatusCode
			return env.Error
		}
		if response.StatusCode >= 500 {
			return fmt.Errorf("Infrai transport status %d", response.StatusCode)
		}
		if out != nil && len(env.Data) > 0 {
			if err := json.Unmarshal(env.Data, out); err != nil {
				return fmt.Errorf("decode response data: %w", err)
			}
		}
		return nil
	}
	return errors.New("request retry budget exhausted")
}

func retryDelay(value string, attempt int) time.Duration {
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	return time.Duration(1<<attempt) * 250 * time.Millisecond
}

func stableKey(parts ...string) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%q", parts)))
	return hex.EncodeToString(digest[:16])
}

func (c *DNSClient) AddDomain(ctx context.Context, domain string) (string, error) {
	var data struct {
		ZoneID string `json:"zone_id"`
	}
	err := c.request(ctx, http.MethodPost, domainAddPath, nil, map[string]string{"domain": domain}, "domain-"+stableKey(domain), &data)
	if err != nil {
		return "", err
	}
	if data.ZoneID == "" {
		return "", errors.New("domain response omitted zone_id")
	}
	return data.ZoneID, nil
}

func (c *DNSClient) GetDomain(ctx context.Context, domain string) (string, error) {
	var data struct {
		ZoneID string `json:"zone_id"`
	}
	err := c.request(ctx, http.MethodGet, domainGetPath, url.Values{"domain": {domain}}, nil, "", &data)
	if err != nil {
		return "", err
	}
	if data.ZoneID == "" {
		return "", errDomainNotFound
	}
	return data.ZoneID, nil
}

func (c *DNSClient) EnsureDomain(ctx context.Context, domain string) (string, error) {
	zoneID, err := c.GetDomain(ctx, domain)
	if err == nil {
		return zoneID, nil
	}
	if errors.Is(err, errDomainNotFound) {
		return c.AddDomain(ctx, domain)
	}
	var api *APIError
	if !errors.As(err, &api) || api.HTTPStatus != http.StatusNotFound {
		return "", err
	}
	return c.AddDomain(ctx, domain)
}

func (c *DNSClient) ListRecords(ctx context.Context, zoneID string) ([]DNSRecord, error) {
	var data struct {
		Records []DNSRecord `json:"records"`
	}
	err := c.request(ctx, http.MethodGet, recordListPath, url.Values{"zone_id": {zoneID}}, nil, "", &data)
	return data.Records, err
}

func (c *DNSClient) UpsertRecord(ctx context.Context, zoneID string, record DNSRecord) error {
	record.ZoneID = zoneID
	return c.request(ctx, http.MethodPut, recordUpsertPath, nil, record, "record-"+stableKey(zoneID, record.Type, record.Name, record.Content), nil)
}

func (c *DNSClient) DeleteRecord(ctx context.Context, zoneID string) error {
	query := url.Values{"zone_id": {zoneID}}
	return c.request(ctx, http.MethodDelete, recordDeletePath, query, nil, "delete-"+stableKey(zoneID), nil)
}
